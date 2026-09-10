package vmcreate

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmimage"
)

// fakeEscape изображает запуск команд вне песочницы, запоминая их.
type fakeEscape struct {
	calls []string
	// missing — команды, которых «нет на хосте»: они отвечают ненулевым
	// кодом, как это делает shell.
	missing map[string]bool
	fail    map[string]bool
	// installs — что происходит с хостом после apt-get install. nil
	// означает «пакеты не помогли»: так проверяется и этот случай.
	installs func()
}

func (f *fakeEscape) run(_ context.Context, argv ...string) (collect.CommandResult, error) {
	f.calls = append(f.calls, strings.Join(argv, " "))
	res := collect.CommandResult{Argv: argv}
	// Проверка наличия программ идёт тем же путём, что и остальные
	// команды: отвечаем на неё так же, как настоящая оболочка.
	if len(argv) == 3 && argv[0] == "sh" && strings.HasPrefix(argv[2], "command -v ") {
		cmd := strings.TrimPrefix(argv[2], "command -v ")
		if f.missing[cmd] {
			res.ExitCode = 1
			return res, nil
		}
		res.Stdout = "/usr/bin/" + cmd + "\n"
		return res, nil
	}
	if len(argv) > 2 && argv[0] == "apt-get" && argv[1] == "install" && f.installs != nil {
		f.installs()
	}
	switch {
	case f.missing[argv[0]]:
		res.ExitCode = 127
		res.Stderr = argv[0] + ": command not found"
	case f.fail[argv[0]]:
		res.ExitCode = 1
		res.Stderr = argv[0] + ": не вышло"
	case argv[0] == "test":
		// «файла ещё нет» — иначе создание откажет из-за занятого имени.
		res.ExitCode = 1
	}
	return res, nil
}

func newManager(t *testing.T) (*jobs.Manager, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(m.Close)
	return m, db
}

// readyStore — кэш, в котором нужный образ уже лежит: скачивание здесь не
// проверяется, у него свои тесты.
func readyStore(t *testing.T) *vmimage.Store {
	t.Helper()
	dir := t.TempDir()
	img, _ := vmimage.ByID("ubuntu-22.04")
	if err := os.WriteFile(filepath.Join(dir, img.FileName), []byte("qcow2"), 0o644); err != nil {
		t.Fatalf("подготовка образа: %v", err)
	}
	return vmimage.NewStore(dir)
}

func waitJob(t *testing.T, db *store.DB, id int64, want string) store.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := db.JobByID(context.Background(), id)
		if err == nil && job.Status == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := db.JobByID(context.Background(), id)
	t.Fatalf("задание в состоянии %q, ожидалось %q: %s", job.Status, want, job.Error)
	return store.Job{}
}

// testRunner — исполнитель, который не ждёт адреса машины: в тесте его
// всё равно неоткуда взять, а полторы минуты ожидания превратили бы
// проверку в таймаут.
func testRunner(t *testing.T, esc *fakeEscape) *CreateRunner {
	t.Helper()
	r := NewCreateRunner(readyStore(t), nil, esc.run)
	r.SetAddressWait(0)
	return r
}

func createSpec() Spec {
	return Spec{
		Name: "web-01", ImageID: "ubuntu-22.04", DiskGB: 20, MemoryMB: 2048, VCPUs: 2,
		User: "deploy", SSHKey: "ssh-ed25519 AAAAKEY deploy@laptop",
	}
}

func TestCreateRunsExpectedCommands(t *testing.T) {
	m, db := newManager(t)
	esc := &fakeEscape{}
	m.Register(KindCreate, testRunner(t, esc))

	id, err := m.Start(context.Background(), jobs.Spec{
		Kind: KindCreate, Queue: "host", Steps: 5,
		Params: CreateParams{Spec: createSpec()},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitJob(t, db, id, store2Succeeded)

	joined := strings.Join(esc.calls, "\n")
	for _, want := range []string{
		// Диск — полная копия образа, а не ссылка на него: удаление кэша
		// не должно ломать созданные машины.
		"qemu-img convert -f qcow2 -O qcow2",
		"qemu-img resize",
		"20G",
		// Настройки первого запуска и определение домена.
		"cloud-localds",
		"virsh define",
		"virsh start web-01",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("не выполнено %q. Команды:\n%s", want, joined)
		}
	}
	// Автозапуск не просили — включать его самовольно нельзя.
	if strings.Contains(joined, "virsh autostart") {
		t.Errorf("включён автозапуск, которого не просили:\n%s", joined)
	}
}

// Когда cloud-localds на хосте нет, тот же образ настроек собирается
// genisoimage — иначе создание машины упиралось бы в один пакет.
func TestCreateFallsBackToGenisoimage(t *testing.T) {
	m, db := newManager(t)
	esc := &fakeEscape{missing: map[string]bool{"cloud-localds": true}}
	m.Register(KindCreate, testRunner(t, esc))

	id, _ := m.Start(context.Background(), jobs.Spec{
		Kind: KindCreate, Queue: "host", Params: CreateParams{Spec: createSpec()},
	})
	waitJob(t, db, id, store2Succeeded)

	joined := strings.Join(esc.calls, "\n")
	if !strings.Contains(joined, "genisoimage -output") || !strings.Contains(joined, "-volid cidata") {
		t.Errorf("запасной путь не сработал:\n%s", joined)
	}
}

// Недостающие программы ставятся сами, нулевым шагом: отправлять
// оператора делать руками то, что nkt умеет, — лишняя работа.
func TestCreateInstallsMissingTools(t *testing.T) {
	m, db := newManager(t)
	// «Нет» только до установки: apt-get install делает их доступными,
	// как и на настоящем хосте.
	esc := &fakeEscape{missing: map[string]bool{"cloud-localds": true, "genisoimage": true}}
	esc.installs = func() {
		delete(esc.missing, "cloud-localds")
		delete(esc.missing, "genisoimage")
	}
	m.Register(KindCreate, testRunner(t, esc))

	id, _ := m.Start(context.Background(), jobs.Spec{
		Kind: KindCreate, Queue: "host", Params: CreateParams{Spec: createSpec()},
	})
	waitJob(t, db, id, store2Succeeded)

	joined := strings.Join(esc.calls, "\n")
	if !strings.Contains(joined, "apt-get install -y cloud-image-utils") {
		t.Errorf("недостающее не поставлено:\n%s", joined)
	}
	// Из пары взаимозаменяемых ставится одна.
	if strings.Contains(joined, "genisoimage") && strings.Contains(joined, "install -y cloud-image-utils genisoimage") {
		t.Errorf("поставлены обе замены разом:\n%s", joined)
	}
	if !strings.Contains(joined, "virsh define") {
		t.Errorf("после установки создание не продолжилось:\n%s", joined)
	}
}

// Если и после установки нужного нет, задание честно отказывает — и до
// того, как скопирует диск: минуты и гигабайты впустую.
func TestCreateFailsWhenToolsStillMissing(t *testing.T) {
	m, db := newManager(t)
	esc := &fakeEscape{missing: map[string]bool{"cloud-localds": true, "genisoimage": true}}
	m.Register(KindCreate, testRunner(t, esc))

	id, _ := m.Start(context.Background(), jobs.Spec{
		Kind: KindCreate, Queue: "host", Params: CreateParams{Spec: createSpec()},
	})
	job := waitJob(t, db, id, store2Failed)
	if !strings.Contains(job.Error, "cloud-localds") {
		t.Errorf("причина отказа = %q", job.Error)
	}
	joined := strings.Join(esc.calls, "\n")
	if strings.Contains(joined, "qemu-img convert") {
		t.Errorf("диск копировался, хотя собрать настройки всё равно нечем:\n%s", joined)
	}
}

// Продолжение после перезапуска не переделывает сделанное: диск уже
// скопирован, домен определён — остаётся запустить.
func TestCreateResumesWithoutRedoing(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	params := `{"spec":{"name":"web-01","image_id":"ubuntu-22.04","disk_gb":20,` +
		`"memory_mb":2048,"vcpus":2,"user":"deploy","ssh_key":"ssh-ed25519 AAAAKEY x"}}`
	id, err := db.CreateJob(ctx, store.Job{
		Kind: KindCreate, Status: store.JobRunning, Queue: "host",
		Params: params,
		Resume: `{"image_ready":true,"disk_ready":true,"seed_ready":true,"defined":true}`,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	m := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(m.Close)
	esc := &fakeEscape{}
	m.Register(KindCreate, testRunner(t, esc))
	if err := m.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	waitJob(t, db, id, store2Succeeded)

	joined := strings.Join(esc.calls, "\n")
	// Именно работа, а не проверка наличия программ: «command -v
	// qemu-img» тоже содержит это имя, но ничего не делает.
	if strings.Contains(joined, "qemu-img convert") || strings.Contains(joined, "virsh define") {
		t.Errorf("продолжение переделало уже сделанное:\n%s", joined)
	}
	if !strings.Contains(joined, "virsh start web-01") {
		t.Errorf("машина не запущена:\n%s", joined)
	}
}

// Занятое имя — отказ до всякой работы: перезаписать чужой диск было бы
// потерей данных.
func TestCreateRefusesExistingDisk(t *testing.T) {
	m, db := newManager(t)
	esc := &fakeEscape{}
	// «test -e» отвечает нулём — файл на месте.
	esc.fail = map[string]bool{}
	runner := NewCreateRunner(readyStore(t), nil, func(ctx context.Context, argv ...string) (collect.CommandResult, error) {
		if argv[0] == "test" {
			return collect.CommandResult{Argv: argv}, nil
		}
		return esc.run(ctx, argv...)
	})
	runner.SetAddressWait(0)
	m.Register(KindCreate, runner)

	id, _ := m.Start(context.Background(), jobs.Spec{
		Kind: KindCreate, Queue: "host", Params: CreateParams{Spec: createSpec()},
	})
	job := waitJob(t, db, id, store2Failed)
	if !strings.Contains(job.Error, "уже существует") {
		t.Errorf("причина отказа = %q", job.Error)
	}
}

// Имена статусов вынесены, чтобы не тянуть store в каждую строку.
const (
	store2Succeeded = store.JobSucceeded
	store2Failed    = store.JobFailed
)
