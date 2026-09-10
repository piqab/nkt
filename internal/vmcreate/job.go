package vmcreate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/vmimage"
)

// KindCreate — вид задания «создать машину».
const KindCreate = "vm.create"

// imagesRoot — каталог дисков qemu/KVM. Тот же, что уже использует
// создание пустого диска.
const imagesRoot = "/var/lib/libvirt/images"

// CreateParams — вход задания.
type CreateParams struct {
	Spec Spec `json:"spec"`
}

// createResume — что уже сделано. Шаги перечислены явно, а не номером:
// продолжение должно понимать, чего именно не хватает, если между
// запусками что-то удалили руками.
type createResume struct {
	ImageReady bool `json:"image_ready"`
	DiskReady  bool `json:"disk_ready"`
	SeedReady  bool `json:"seed_ready"`
	Defined    bool `json:"defined"`
}

// CreateRunner создаёт машину из облачного образа.
type CreateRunner struct {
	store *vmimage.Store
	c     collect.Collector
	// escape — запуск команд вне песочницы юнита: qemu-img и virsh
	// пишут в /var/lib/libvirt, куда изнутри юнита хода нет.
	escape control.PrivilegedRunner
	// addressWait — сколько ждать адреса от libvirt. Поле, а не
	// константа: тестам ждать полторы минуты незачем.
	addressWait time.Duration
}

// NewCreateRunner строит исполнителя.
func NewCreateRunner(store *vmimage.Store, c collect.Collector, escape control.PrivilegedRunner) *CreateRunner {
	return &CreateRunner{store: store, c: c, escape: escape, addressWait: defaultAddressWait}
}

// SetAddressWait меняет, сколько ждать адреса машины.
func (r *CreateRunner) SetAddressWait(d time.Duration) { r.addressWait = d }

// Resumable — да: каждый шаг проверяет, не сделан ли он уже, и не
// повторяет сделанного.
func (r *CreateRunner) Resumable() bool { return true }

// Run проходит все шаги создания машины.
func (r *CreateRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p CreateParams
	if err := jc.Params(&p); err != nil {
		return fmt.Errorf("разбор задания: %w", err)
	}
	spec := p.Spec
	if err := spec.Validate(); err != nil {
		return err
	}
	if r.escape == nil {
		return fmt.Errorf("создание машин недоступно в этом режиме")
	}
	img, ok := vmimage.ByID(spec.ImageID)
	if !ok {
		return fmt.Errorf("нет такого образа в каталоге: %q", spec.ImageID)
	}
	var done createResume
	if err := jc.LoadResume(&done); err != nil {
		return fmt.Errorf("разбор состояния продолжения: %w", err)
	}

	diskPath := filepath.Join(imagesRoot, spec.Name+".qcow2")
	seedPath := filepath.Join(imagesRoot, spec.Name+"-seed.iso")

	// 1. Образ.
	jc.Step(1, 5, "образ")
	// Образ, уже лежащий в кэше, берётся как есть: заново спрашивать у
	// зеркала контрольную сумму значило бы ставить создание машины в
	// зависимость от сети, которой здесь не нужно.
	if r.store.Have(img) {
		done.ImageReady = true
	}
	if !done.ImageReady {
		base, err := r.store.Download(ctx, img, func(pr vmimage.Progress) {
			if pr.Total > 0 {
				jc.Logf("скачивание образа: %d%%", pr.Done*100/pr.Total)
			}
		})
		if err != nil {
			return fmt.Errorf("образ: %w", err)
		}
		jc.Logf("образ на месте: %s", base)
		done.ImageReady = true
		jc.SaveResume(done)
	}

	// 2. Диск машины: копия образа нужного размера.
	jc.Step(2, 5, "диск")
	if !done.DiskReady {
		if err := r.makeDisk(ctx, jc, r.store.Path(img), diskPath, spec.DiskGB); err != nil {
			return err
		}
		done.DiskReady = true
		jc.SaveResume(done)
	}

	// 3. Настройки первого запуска.
	jc.Step(3, 5, "cloud-init")
	if !done.SeedReady {
		if err := r.makeSeed(ctx, jc, spec, seedPath); err != nil {
			return err
		}
		done.SeedReady = true
		jc.SaveResume(done)
	}

	// 4. Определение домена.
	jc.Step(4, 5, "домен")
	if !done.Defined {
		if err := r.defineDomain(ctx, jc, spec, diskPath, seedPath); err != nil {
			return err
		}
		done.Defined = true
		jc.SaveResume(done)
	}

	// 5. Запуск.
	jc.Step(5, 5, "запуск")
	if res, err := r.run(ctx, "virsh", "start", spec.Name); err != nil {
		return err
	} else if res.ExitCode != 0 && !strings.Contains(res.Output(), "already active") {
		return fmt.Errorf("virsh start: %s", firstLine(res.Output()))
	}
	if spec.Autostart {
		if _, err := r.run(ctx, "virsh", "autostart", spec.Name); err != nil {
			jc.Logf("автозапуск включить не удалось: %v", err)
		}
	}
	jc.Logf("Машина %s создана и запущена.", spec.Name)
	jc.Logf("Первый запуск занимает до минуты: cloud-init заводит пользователя %s и растит файловую систему.", spec.User)

	// Адрес нужен тому, кто будет к машине подключаться, — прежде всего
	// хабу, если машину создают для него. Ждать до конца задания
	// незачем: строка журнала с адресом видна сразу.
	if addr := r.waitAddress(ctx, jc, spec.Name); addr != "" {
		jc.Logf("Адрес машины: %s", addr)
	} else {
		jc.Logf("Адрес пока не известен — машина ещё поднимается или сеть без DHCP-аренды.")
	}
	return nil
}

// defaultAddressWait — сколько ждать адреса. Первый запуск облачного
// образа занимает до минуты сам по себе, потом машина ещё берёт адрес у
// DHCP, поэтому полутора минут не хватало: задание успевало закончиться
// раньше, чем машина отвечала.
const defaultAddressWait = 5 * time.Minute

// waitAddress спрашивает у libvirt адрес машины, пока тот не появится.
func (r *CreateRunner) waitAddress(ctx context.Context, jc *jobs.Context, name string) string {
	deadline := time.Now().Add(r.addressWait)
	// Молчащий журнал в этом месте выглядит как зависшее задание, хотя
	// ожидание тут нормальное: машина грузится.
	told := time.Now()
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ""
		}
		if addr := r.Address(ctx, name); addr != "" {
			return addr
		}
		if time.Since(told) > 30*time.Second {
			jc.Logf("      адрес пока не появился, жду (машина ещё поднимается)")
			told = time.Now()
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(3 * time.Second):
		}
	}
	return ""
}

// addressSources — откуда libvirt берёт адрес машины, в порядке
// надёжности.
//
// lease знает только про сети самого libvirt (NAT). Машина в мосту берёт
// адрес у DHCP-сервера сети, и там его знает либо гостевой агент (канал
// для него есть в описании домена), либо таблица ARP хоста — поэтому
// спрашиваем все три, а не одну.
var addressSources = []string{"lease", "agent", "arp"}

// Address спрашивает у libvirt адрес машины. Пустая строка — «пока не
// знаю», а не «нет».
func (r *CreateRunner) Address(ctx context.Context, name string) string {
	for _, source := range addressSources {
		res, err := r.run(ctx, "virsh", "domifaddr", name, "--source", source)
		if err != nil || res.ExitCode != 0 {
			continue
		}
		if addr := parseDomifaddr(res.Output()); addr != "" {
			return addr
		}
	}
	return ""
}

// parseDomifaddr достаёт адрес из вывода virsh domifaddr.
//
// Формат: имя интерфейса, MAC, протокол и адрес с маской в последней
// колонке. Берётся первый IPv4: у машины с одним интерфейсом он и есть
// ответ, а разбирать несколько сетей здесь незачем — это работа
// оператора, а не догадка.
func parseDomifaddr(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.EqualFold(fields[2], "ipv4") {
			continue
		}
		addr, _, _ := strings.Cut(fields[3], "/")
		if addr != "" && addr != "0.0.0.0" {
			return addr
		}
	}
	return ""
}

// makeDisk делает диск машины из образа.
//
// Полная копия, а не ссылка на образ (backing file): ссылка экономит
// место, но удаление кэша образов ломает все машины, которые на него
// смотрят, — цена молчаливая и слишком высокая.
func (r *CreateRunner) makeDisk(ctx context.Context, jc *jobs.Context, base, disk string, sizeGB int) error {
	if res, err := r.run(ctx, "test", "-e", disk); err == nil && res.ExitCode == 0 {
		return fmt.Errorf("файл диска %s уже существует — выберите другое имя машины", disk)
	}
	jc.Logf("готовлю диск %s (%d ГБ)", disk, sizeGB)
	res, err := r.run(ctx, "qemu-img", "convert", "-f", "qcow2", "-O", "qcow2", base, disk)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("qemu-img convert: %s", firstLine(res.Output()))
	}
	res, err = r.run(ctx, "qemu-img", "resize", disk, fmt.Sprintf("%dG", sizeGB))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("qemu-img resize: %s", firstLine(res.Output()))
	}
	return nil
}

// makeSeed собирает образ с настройками cloud-init.
//
// Файлы кладутся во временный каталог nkt (туда писать можно), а
// собирается образ уже вне песочницы: cloud-localds или genisoimage —
// что найдётся на хосте.
func (r *CreateRunner) makeSeed(ctx context.Context, jc *jobs.Context, spec Spec, seedPath string) error {
	dir, err := os.MkdirTemp(r.store.Dir(), "seed-")
	if err != nil {
		return fmt.Errorf("временный каталог: %w", err)
	}
	defer os.RemoveAll(dir)

	userData := filepath.Join(dir, "user-data")
	metaData := filepath.Join(dir, "meta-data")
	if err := os.WriteFile(userData, []byte(UserData(spec)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(metaData, []byte(MetaData(spec)), 0o644); err != nil {
		return err
	}

	if res, err := r.run(ctx, "cloud-localds", seedPath, userData, metaData); err == nil && res.ExitCode == 0 {
		jc.Logf("настройки первого запуска: %s (cloud-localds)", seedPath)
		return nil
	}
	// Запасной путь: genisoimage делает то же самое вручную — метка тома
	// cidata и есть то, по чему cloud-init находит настройки.
	res, err := r.run(ctx, "genisoimage", "-output", seedPath, "-volid", "cidata",
		"-joliet", "-rock", userData, metaData)
	if err != nil {
		return fmt.Errorf("сборка настроек первого запуска: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("на хосте нет ни cloud-localds, ни genisoimage: %s", firstLine(res.Output()))
	}
	jc.Logf("настройки первого запуска: %s (genisoimage)", seedPath)
	return nil
}

// defineDomain отдаёт libvirt описание машины.
func (r *CreateRunner) defineDomain(ctx context.Context, jc *jobs.Context, spec Spec, disk, seed string) error {
	xmlPath := filepath.Join(r.store.Dir(), spec.Name+".xml")
	if err := os.WriteFile(xmlPath, []byte(DomainXML(spec, disk, seed)), 0o644); err != nil {
		return err
	}
	defer os.Remove(xmlPath)

	res, err := r.run(ctx, "virsh", "define", xmlPath)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("virsh define: %s", firstLine(res.Output()))
	}
	jc.Logf("домен %s определён", spec.Name)
	return nil
}

func (r *CreateRunner) run(ctx context.Context, argv ...string) (collect.CommandResult, error) {
	return r.escape(ctx, argv...)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if s == "" {
		return "команда завершилась с ошибкой"
	}
	return s
}
