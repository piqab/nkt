package hub

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Машина, созданная на управляемом хосте, — это ещё одна машина, которой
// потом придётся управлять. Заводить её в хабе руками, переписывая адрес
// и раздавая ключи, — работа ровно того рода, ради избавления от которой
// хаб и существует.
//
// Поэтому хаб делает это сам: заранее выдаёт себе ключ, просит хост
// создать машину с этим ключом внутри, дожидается её адреса и записывает
// машину в список хостов. Установка nkt на неё остаётся отдельным
// шагом — как и для любого другого добавленного хоста.

// KindVMProvision — вид задания.
const KindVMProvision = "vm.provision"

// vmJobPoll — как часто спрашивать хост, чем кончилось создание.
const vmJobPoll = 3 * time.Second

// vmJobTimeout — сколько ждать создания машины. Копирование образа на
// медленном диске бывает долгим, но не бесконечным.
const vmJobTimeout = 40 * time.Minute

// addressRe достаёт адрес из строки журнала хоста.
var addressRe = regexp.MustCompile(`Адрес машины: ([0-9.]+)`)

// VMProvisionParams — вход задания.
type VMProvisionParams struct {
	// HostID — на какой машине создаётся виртуальная.
	HostID int64 `json:"host_id"`
	// Spec — что за машину создаём. Ключ хаба сюда дописывается уже
	// здесь, оператор его не вводит.
	Spec vmcreate.Spec `json:"spec"`
}

type vmProvisionResume struct {
	// NewHostID — запись хоста уже заведена: повторять её нельзя, иначе
	// после перезапуска службы в списке окажется два одинаковых хоста.
	NewHostID int64 `json:"new_host_id"`
	// RemoteJobID — задание на хосте уже запущено: у него свой журнал, и
	// начинать создание заново значило бы делать вторую машину.
	RemoteJobID int64  `json:"remote_job_id"`
	Address     string `json:"address"`
}

// VMProvisionRunner создаёт машину на хосте и берёт её под управление.
type VMProvisionRunner struct {
	m *Manager
}

// NewVMProvisionRunner строит исполнителя.
func NewVMProvisionRunner(m *Manager) *VMProvisionRunner { return &VMProvisionRunner{m: m} }

// Resumable — да, и здесь это важнее обычного: между шагами заводится
// запись хоста и запускается создание машины, а повторять такое вслепую
// нельзя. Что уже сделано, помнит состояние продолжения.
func (r *VMProvisionRunner) Resumable() bool { return true }

// Run проходит все шаги.
func (r *VMProvisionRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p VMProvisionParams
	if err := jc.Params(&p); err != nil {
		return fmt.Errorf("разбор задания: %w", err)
	}
	if err := p.Spec.Validate(); err != nil {
		return err
	}
	var done vmProvisionResume
	if err := jc.LoadResume(&done); err != nil {
		return fmt.Errorf("разбор состояния продолжения: %w", err)
	}

	host, err := r.m.db.HostByID(ctx, p.HostID)
	if err != nil {
		return fmt.Errorf("хост, на котором создаём машину: %w", err)
	}

	// 1. Запись нового хоста и ключ для него.
	jc.Step(1, 4, "ключ")
	if done.NewHostID == 0 {
		// Адрес пока неизвестен — машины ещё нет. Ставится заглушка, а
		// настоящий адрес запишется на шаге 3: без записи негде взять
		// ключ, который должен попасть в машину при первом запуске.
		id, key, err := r.m.AddHostGenerated(ctx, p.Spec.Name, "0.0.0.0", 22, p.Spec.User, false)
		if err != nil {
			return fmt.Errorf("запись нового хоста: %w", err)
		}
		done.NewHostID = id
		jc.SaveResume(done)
		p.Spec.ExtraKeys = append(p.Spec.ExtraKeys, key)
		// Машина привязывается к хосту, на котором создана: в списке она
		// показывается внутри него и переезжает между группами только
		// вместе с ним. Своей группы у неё нет — «база в проде, а сервер
		// под ней в резерве» ничего не описывает.
		if err := r.m.db.SetHostParent(ctx, id, host.ID); err != nil {
			return fmt.Errorf("привязка машины к хосту: %w", err)
		}
		jc.Logf("Хост %s заведён в списке под %s, ключ для входа выдан.", p.Spec.Name, host.Name)
	} else {
		jc.Logf("Хост уже заведён (продолжение), пропускаю.")
	}

	// 2. Создание машины на хосте.
	jc.Step(2, 4, "создание на "+host.Name)
	if done.RemoteJobID == 0 {
		var started struct {
			JobID int64 `json:"job_id"`
		}
		if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/vm/create", p.Spec, &started); err != nil {
			// На хосте ещё ничего не создано, а запись хоста уже есть —
			// и останется в списке пустышкой с адресом-заглушкой. Она
			// там ни к чему: ключ не пригодился, машины нет.
			if delErr := r.m.db.DeleteHost(ctx, done.NewHostID); delErr != nil {
				jc.Logf("Заготовку хоста убрать не удалось: %v", delErr)
			} else {
				jc.Logf("Заготовка хоста %s убрана из списка — создавать было нечего.", p.Spec.Name)
			}
			return fmt.Errorf("запуск создания на %s: %w", host.Name, err)
		}
		done.RemoteJobID = started.JobID
		jc.SaveResume(done)
	}

	// 3. Ожидание конца и адреса.
	jc.Step(3, 4, "ожидание")
	addr, err := r.waitVMJob(ctx, jc, host, done.RemoteJobID)
	if err != nil {
		return err
	}
	if addr != "" {
		done.Address = addr
		jc.SaveResume(done)
	}

	// 4. Запись адреса.
	jc.Step(4, 4, "адрес")
	if done.Address == "" {
		jc.Logf("Машина создана, но её адрес не определился — впишите его в карточке хоста вручную.")
		return nil
	}
	if err := r.m.UpdateHost(ctx, done.NewHostID, p.Spec.Name, done.Address, 22, p.Spec.User,
		store.HostAuthKey, "", false); err != nil {
		return fmt.Errorf("запись адреса: %w", err)
	}
	jc.Logf("Готово: машина %s доступна по адресу %s, хост заведён в списке.", p.Spec.Name, done.Address)
	jc.Logf("Установку nkt на неё запустите обычной кнопкой «установить» — как для любого другого хоста.")
	return nil
}

// waitVMJob следит за заданием на хосте и вылавливает адрес машины.
func (r *VMProvisionRunner) waitVMJob(ctx context.Context, jc *jobs.Context,
	host store.Host, jobID int64) (string, error) {

	deadline := time.Now().Add(vmJobTimeout)
	var after int64
	var addr string
	for {
		if err := ctx.Err(); err != nil {
			return addr, err
		}
		if time.Now().After(deadline) {
			return addr, fmt.Errorf("создание машины не завершилось за %s", vmJobTimeout)
		}

		var res struct {
			Job   store.Job          `json:"job"`
			Lines []store.JobLogLine `json:"lines"`
		}
		path := fmt.Sprintf("/api/jobs/%d/log?after=%d", jobID, after)
		if _, err := r.m.HostAPI(ctx, host.ID, "GET", path, nil, &res); err != nil {
			jc.Logf("      связь с %s прервалась (%v), пробую снова", host.Name, err)
			if !sleepCtx(ctx, vmJobPoll) {
				return addr, ctx.Err()
			}
			continue
		}
		for _, line := range res.Lines {
			after = line.Seq
			jc.Logf("      %s", line.Text)
			if m := addressRe.FindStringSubmatch(line.Text); m != nil {
				addr = m[1]
			}
		}
		switch res.Job.Status {
		case store.JobSucceeded:
			return addr, nil
		case store.JobFailed, store.JobCanceled, store.JobInterrupted:
			return addr, fmt.Errorf("создание машины на %s: %s (%s)",
				host.Name, res.Job.Status, strings.TrimSpace(res.Job.Error))
		}
		if !sleepCtx(ctx, vmJobPoll) {
			return addr, ctx.Err()
		}
	}
}
