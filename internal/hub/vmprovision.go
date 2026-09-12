package hub

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/profile"
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

// addressPoll — сколько хаб сам ждёт адреса после того, как задание на
// хосте закончилось.
const addressPoll = 5 * time.Minute

// installTimeout — сколько ждать установки nkt на новую машину. Первый
// запуск ещё доделывает cloud-init, поэтому запас больше обычного.
const installTimeout = 30 * time.Minute

// addressRe достаёт адрес из строки журнала хоста.
var addressRe = regexp.MustCompile(`Адрес машины: ([0-9.]+)`)

// VMProvisionParams — вход задания.
type VMProvisionParams struct {
	// HostID — на какой машине создаётся виртуальная.
	HostID int64 `json:"host_id"`
	// Spec — что за машину создаём. Ключ хаба сюда дописывается уже
	// здесь, оператор его не вводит.
	Spec vmcreate.Spec `json:"spec"`
	// InstallNKT — поставить на новую машину nkt сразу после её
	// появления. Без этого она остаётся обычным записанным хостом, на
	// который установку запускают кнопкой.
	InstallNKT bool `json:"install_nkt"`
	// ProfileID — профиль, который применить к новой машине. Требует
	// установленного nkt: применяет его сама машина.
	ProfileID int64 `json:"profile_id,omitempty"`
}

type vmProvisionResume struct {
	// HostUpdated — версия nkt на хосте уже приведена в порядок. Шаг
	// нулевой, но пропускать его при продолжении надо так же, как
	// остальные: повторная установка перезапишет работающий nkt без
	// нужды.
	HostUpdated bool `json:"host_updated"`
	// Installed и ProfileDone — доведённые до конца поздние шаги. Как и
	// раньше, повторять их вслепую нельзя: установка перезапишет уже
	// работающий nkt, а применение профиля во второй раз хоть и
	// безвредно, но занимает машину и путает журнал.
	Installed   bool `json:"installed"`
	ProfileDone bool `json:"profile_done"`
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
		return msgs.Errorf("hub.parsingJob", err)
	}
	if err := p.Spec.Validate(); err != nil {
		return err
	}
	var done vmProvisionResume
	if err := jc.LoadResume(&done); err != nil {
		return msgs.Errorf("hub.parsingResumeState", err)
	}

	host, err := r.m.db.HostByID(ctx, p.HostID)
	if err != nil {
		return msgs.Errorf("hub.hostCreateMachine", err)
	}
	// Сколько всего шагов, известно сразу: без установки nkt их четыре,
	// с ней пять, с профилем шесть. Считать это по ходу значило бы
	// показывать «шаг 2 из 4», а потом вдруг «5 из 6».
	steps := 4
	if p.InstallNKT {
		steps = 5
	}
	if p.ProfileID != 0 {
		steps = 6
	}

	// 0. Версия nkt на самом хосте.
	//
	// Создание машины опирается на его API: каталог образов, создание,
	// адрес. На хосте со старой версией этих запросов просто нет, и
	// задание провалилось бы посреди работы с невнятным «код 404».
	// Поэтому сначала — обновление, и только потом всё остальное.
	if !done.HostUpdated {
		if err := r.ensureHostVersion(ctx, jc, host); err != nil {
			return err
		}
		done.HostUpdated = true
		jc.SaveResume(done)
		// Хост мог перезапуститься при обновлении — перечитываем запись.
		if fresh, err := r.m.db.HostByID(ctx, host.ID); err == nil {
			host = fresh
		}
	}

	// 1. Запись нового хоста и ключ для него.
	jc.Step(1, steps, msgs.T(jc.Lang(), "hub.vmStepKey"))
	if done.NewHostID == 0 {
		// Адрес пока неизвестен — машины ещё нет. Ставится заглушка, а
		// настоящий адрес запишется на шаге 3: без записи негде взять
		// ключ, который должен попасть в машину при первом запуске.
		id, key, err := r.m.AddHostGenerated(ctx, p.Spec.Name, "0.0.0.0", 22, p.Spec.User, false)
		if err != nil {
			return msgs.Errorf("hub.savingNewHost", err)
		}
		done.NewHostID = id
		jc.SaveResume(done)
		p.Spec.ExtraKeys = append(p.Spec.ExtraKeys, key)
		// Машина привязывается к хосту, на котором создана: в списке она
		// показывается внутри него и переезжает между группами только
		// вместе с ним. Своей группы у неё нет — «база в проде, а сервер
		// под ней в резерве» ничего не описывает.
		if err := r.m.db.SetHostParent(ctx, id, host.ID); err != nil {
			return msgs.Errorf("hub.bindingMachineHost", err)
		}
		jc.Log("hub.hostListedAsLoginKey", p.Spec.Name, host.Name)
	} else {
		jc.Log("hub.hostAlreadyListedResumeSkipping")
	}

	// 2. Создание машины на хосте.
	jc.Step(2, steps, msgs.T(jc.Lang(), "hub.vmStepCreateOn", host.Name))
	if done.RemoteJobID == 0 {
		var started struct {
			JobID int64 `json:"job_id"`
		}
		if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/vm/create", p.Spec, &started); err != nil {
			// На хосте ещё ничего не создано, а запись хоста уже есть —
			// и останется в списке пустышкой с адресом-заглушкой. Она
			// там ни к чему: ключ не пригодился, машины нет.
			if delErr := r.m.db.DeleteHost(ctx, done.NewHostID); delErr != nil {
				jc.Log("hub.couldRemovePlaceholderHost", delErr)
			} else {
				jc.Log("hub.placeholderHostRemovedListThere", p.Spec.Name)
			}
			return msgs.Errorf("hub.startingCreation", host.Name, err)
		}
		done.RemoteJobID = started.JobID
		jc.SaveResume(done)
	}

	// 3. Ожидание конца и адреса.
	jc.Step(3, steps, msgs.T(jc.Lang(), "hub.vmStepWait"))
	addr, err := r.waitVMJob(ctx, jc, host, done.RemoteJobID)
	if err != nil {
		return err
	}
	if addr == "" {
		// Задание на хосте закончилось раньше, чем машина ответила.
		// Спрашиваем сами: адрес появляется от одной до нескольких
		// минут, и оставить запись с заглушкой значит обречь установку
		// на невнятный отказ рукопожатия.
		addr = r.pollAddress(ctx, jc, host, p.Spec.Name)
	}
	if addr != "" {
		done.Address = addr
		jc.SaveResume(done)
	}

	// 4. Запись адреса.
	jc.Step(4, steps, msgs.T(jc.Lang(), "hub.vmStepAddress"))
	if done.Address == "" {
		jc.Log("hub.machineCreatedButAddressDetected")
		return nil
	}
	if err := r.m.UpdateHost(ctx, done.NewHostID, p.Spec.Name, done.Address, 22, p.Spec.User,
		store.HostAuthKey, "", false); err != nil {
		return msgs.Errorf("hub.savingAddress", err)
	}
	jc.Log("hub.machineReachableHostListed", p.Spec.Name, done.Address)

	if !p.InstallNKT {
		jc.Log("hub.startNktInstallationUsualInstall")
		return nil
	}

	// 5. Установка nkt.
	jc.Step(5, steps, msgs.T(jc.Lang(), "hub.vmStepInstall"))
	if !done.Installed {
		if err := r.installNKT(ctx, jc, done.NewHostID); err != nil {
			return err
		}
		done.Installed = true
		jc.SaveResume(done)
	}

	if p.ProfileID == 0 {
		jc.Log("hub.doneMachineCreatedListedManaged")
		return nil
	}

	// 6. Применение профиля.
	jc.Step(6, steps, msgs.T(jc.Lang(), "hub.vmStepProfile"))
	if !done.ProfileDone {
		if err := r.applyProfile(ctx, jc, done.NewHostID, p.ProfileID); err != nil {
			return err
		}
		done.ProfileDone = true
		jc.SaveResume(done)
	}
	jc.Log("hub.doneMachineCreatedManagedHub")
	return nil
}

// ensureHostVersion сверяет версию nkt на хосте с версией хаба и
// обновляет её, если хост отстал.
//
// Сравнение — та же функция, что решает «устарел ли хост» в списке:
// два разных ответа на один вопрос в одной программе хуже, чем один
// неточный. Хост новее хаба не трогается: понижать версию, потому что
// хаб старее, — не то, чего от кнопки «создать машину» ждут.
func (r *VMProvisionRunner) ensureHostVersion(ctx context.Context, jc *jobs.Context, host store.Host) error {
	hubVersion := r.m.Version()
	current := host.NktVersion

	var health struct {
		Version string `json:"version"`
	}
	if _, err := r.m.HostAPI(ctx, host.ID, "GET", "/api/health", nil, &health); err == nil && health.Version != "" {
		// То, что действительно работает на хосте, вернее того, что
		// записано при последней установке.
		current = health.Version
	}

	if current != "" && !isNewerVersion(hubVersion, current) {
		jc.Log("hub.nktVersionUpdateNeeded", host.Name, current)
		return nil
	}
	jc.Log("hub.runsNktHubHasUpdating",
		host.Name, orUnknown(ctx, current), hubVersion)
	if err := r.runInstall(ctx, jc, host.ID); err != nil {
		return msgs.Errorf("hub.updatingNkt", host.Name, err)
	}
	jc.Log("hub.nktUpdated", host.Name)
	return nil
}

// installNKT ставит nkt на новую машину и ждёт конца установки.
//
// Тем же путём, что и обычная кнопка «установить»: у установки свой
// механизм заданий, старше системы фоновых заданий, и заводить для неё
// второй способ значило бы получить два разных поведения там, где нужно
// одно.
func (r *VMProvisionRunner) installNKT(ctx context.Context, jc *jobs.Context, hostID int64) error {
	return r.runInstall(ctx, jc, hostID)
}

// runInstall запускает установку (или обновление) nkt на хосте и ждёт
// её конца, пересказывая ход дела в свой журнал.
func (r *VMProvisionRunner) runInstall(ctx context.Context, jc *jobs.Context, hostID int64) error {
	jobID, err := r.m.StartInstall(ctx, hostID, false, nil)
	if err != nil {
		return msgs.Errorf("hub.startingNktInstallation", err)
	}
	deadline := time.Now().Add(installTimeout)
	seen := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.nktInstallationDidFinishWithin", installTimeout)
		}
		events, done, errMsg, ok := r.m.InstallJobStatus(jobID)
		if !ok {
			return msgs.Errorf("hub.installationJobLost")
		}
		for _, e := range events[seen:] {
			jc.Logf("      %s", e.Text)
		}
		seen = len(events)
		if done {
			if errMsg != "" {
				return msgs.Errorf("hub.installingNkt", errMsg)
			}
			return nil
		}
		if !sleepCtx(ctx, vmJobPoll) {
			return ctx.Err()
		}
	}
}

// applyProfile строит план на новой машине и применяет его целиком.
//
// Целиком — потому что машина только что создана: расхождение с профилем
// здесь и есть весь смысл, отмечать в нём нечего.
func (r *VMProvisionRunner) applyProfile(ctx context.Context, jc *jobs.Context,
	hostID, profileID int64) error {

	prof, err := r.m.db.ProfileByID(ctx, profileID)
	if err != nil {
		return msgs.Errorf("hub.profile", err)
	}
	host, err := r.m.db.HostByID(ctx, hostID)
	if err != nil {
		return err
	}
	jc.Log("hub.applyingProfile", prof.Name)

	var plan profile.Plan
	if _, err := r.m.HostAPI(ctx, hostID, "POST", "/api/profiles/plan",
		map[string]string{"content": prof.Content}, &plan); err != nil {
		return msgs.Errorf("hub.buildingPlan", err)
	}
	if len(plan.Changes) == 0 {
		jc.Log("hub.drift")
		return nil
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := r.m.HostAPI(ctx, hostID, "POST", "/api/profiles/apply",
		map[string]any{"name": prof.Name, "changes": plan.Changes}, &started); err != nil {
		return msgs.Errorf("hub.startingApply", err)
	}
	_, err = r.waitVMJob(ctx, jc, host, started.JobID)
	return err
}

// pollAddress доспрашивает адрес машины у хоста.
func (r *VMProvisionRunner) pollAddress(ctx context.Context, jc *jobs.Context,
	host store.Host, name string) string {

	jc.Log("hub.waitingMachineSAddressStill")
	deadline := time.Now().Add(addressPoll)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ""
		}
		var res struct {
			Address string `json:"address"`
		}
		path := "/api/vm/address?name=" + url.QueryEscape(name)
		if _, err := r.m.HostAPI(ctx, host.ID, "GET", path, nil, &res); err == nil && res.Address != "" {
			return res.Address
		}
		if !sleepCtx(ctx, vmJobPoll) {
			return ""
		}
	}
	return ""
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
			return addr, msgs.Errorf("hub.machineCreationDidFinishWithin", vmJobTimeout)
		}

		var res struct {
			Job   store.Job          `json:"job"`
			Lines []store.JobLogLine `json:"lines"`
		}
		path := fmt.Sprintf("/api/jobs/%d/log?after=%d", jobID, after)
		if _, err := r.m.HostAPI(ctx, host.ID, "GET", path, nil, &res); err != nil {
			jc.Log("hub.connectionLostRetrying2", host.Name, err)
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
			return addr, msgs.Errorf("hub.creatingMachine",
				host.Name, res.Job.Status, strings.TrimSpace(res.Job.Error))
		}
		if !sleepCtx(ctx, vmJobPoll) {
			return addr, ctx.Err()
		}
	}
}
