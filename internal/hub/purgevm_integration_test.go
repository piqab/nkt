package hub

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Удаление машины из хаба оставляло её работать на хосте: хаб знал только
// про запись и пытался зайти по SSH в саму машину (а её адрес мог быть и
// вовсе неизвестен — «SSH-рукопожатие с 0.0.0.0»). Убирать машину должен
// её хост, а не она сама.
//
// Здесь настоящий nkt в режиме фикстур играет хост с двумя машинами:
// «web-vm» запущена, «db-vm» выключена. Проверяются оба пути — с
// выключением перед удалением и без него.
func TestPurgeVMRemovesMachineThroughItsHost(t *testing.T) {
	manager, db, key, hostID := startFixturesHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	secretEnc, err := secretbox.Encrypt(key, []byte("ключ машины сюда не понадобится"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Машина без адреса — именно тот случай, на котором очистка раньше
	// спотыкалась рукопожатием с 0.0.0.0.
	vmID, err := db.CreateHost(ctx, "db-vm", PlaceholderAddr, 22, "root", store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost (машина): %v", err)
	}
	if err := db.SetHostParent(ctx, vmID, hostID); err != nil {
		t.Fatalf("SetHostParent: %v", err)
	}

	res := manager.PurgeHost(ctx, vmID, PurgeOptions{VM: true, VMDisks: true})
	if !res.OK {
		t.Fatalf("удаление выключенной машины: %s (шаги: %v)", res.Error, res.Steps)
	}
	if !strings.Contains(strings.Join(res.Steps, "; "), "диски удалены") {
		t.Errorf("шаги = %v, в них не видно удаления с дисками", res.Steps)
	}

	// Запущенную сначала гасят: virsh undefine отказывается удалять
	// работающий домен, и без этого шага удаление возвращало отказ.
	runningID, err := db.CreateHost(ctx, "web-vm", PlaceholderAddr, 22, "root", store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost (запущенная машина): %v", err)
	}
	if err := db.SetHostParent(ctx, runningID, hostID); err != nil {
		t.Fatalf("SetHostParent: %v", err)
	}
	res = manager.PurgeHost(ctx, runningID, PurgeOptions{VM: true, VMDisks: false})
	if !res.OK {
		t.Fatalf("удаление запущенной машины: %s (шаги: %v)", res.Error, res.Steps)
	}
	steps := strings.Join(res.Steps, "; ")
	if !strings.Contains(steps, "диски оставлены") {
		t.Errorf("шаги = %v, без галочки диски трогать нельзя", res.Steps)
	}

	// Машины с таким именем на хосте нет — это не ошибка, а «уже нет».
	goneID, err := db.CreateHost(ctx, "нет-такой", PlaceholderAddr, 22, "root", store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	if err := db.SetHostParent(ctx, goneID, hostID); err != nil {
		t.Fatalf("SetHostParent: %v", err)
	}
	res = manager.PurgeHost(ctx, goneID, PurgeOptions{VM: true, VMDisks: true})
	if !res.OK {
		t.Fatalf("удаление несуществующей машины: %s", res.Error)
	}
	if !strings.Contains(strings.Join(res.Steps, "; "), "уже нет") {
		t.Errorf("шаги = %v", res.Steps)
	}
}

// Обычный хост «вместе с дисками» удалить не у кого — у него нет хозяина.
func TestPurgeVMRefusedForPlainHost(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open hub store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	secretEnc, err := secretbox.Encrypt(key, []byte("ключ"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ctx := context.Background()
	id, err := db.CreateHost(ctx, "обычный", "192.0.2.10", 22, "root", store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	m := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	if res := m.PurgeHost(ctx, id, PurgeOptions{VM: true}); res.OK || res.Error == "" {
		t.Errorf("обычный хост удалён как машина: %+v", res)
	}
}

// startFixturesHost поднимает настоящий nkt в режиме фикстур за настоящим
// sshd и заводит на него запись хоста — ровно то состояние, в котором хаб
// работает с управляемым хостом.
func startFixturesHost(t *testing.T) (*Manager, *store.DB, []byte, int64) {
	t.Helper()
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)

	repoRoot := findRepoRoot(t)
	nktBin := filepath.Join(t.TempDir(), "nkt")
	buildCmd := exec.Command("go", "build", "-o", nktBin, "./cmd/nkt")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build nkt for the test: %v\n%s", err, out)
	}

	const adminPassword = "integration-test-password-1234"
	remoteCmd := exec.Command(nktBin)
	remoteCmd.Dir = repoRoot
	remoteCmd.Env = append(os.Environ(),
		"NKT_MODE=fixtures",
		"NKT_ADDR="+remoteAPIAddr,
		"NKT_DATA_DIR="+t.TempDir(),
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD="+adminPassword,
		"NKT_COOKIE_SECURE=false",
		"NKT_SCHEDULER_ENABLED=false",
	)
	if err := remoteCmd.Start(); err != nil {
		t.Fatalf("start remote nkt: %v", err)
	}
	t.Cleanup(func() {
		_ = remoteCmd.Process.Kill()
		_, _ = remoteCmd.Process.Wait()
	})
	waitForLocalHTTP(t, "http://"+remoteAPIAddr+"/api/health")

	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open hub store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	me, err := osuser.Current()
	if err != nil {
		t.Fatalf("os/user.Current: %v", err)
	}
	secretEnc, err := secretbox.Encrypt(key, clientKeyPEM)
	if err != nil {
		t.Fatalf("encrypt ssh key: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hostID, err := db.CreateHost(ctx, "test-host", sshAddr, sshPort, me.Username, store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	adminPasswordEnc, err := secretbox.Encrypt(key, []byte(adminPassword))
	if err != nil {
		t.Fatalf("encrypt admin password: %v", err)
	}
	if err := db.SetHostAdmin(ctx, hostID, "admin", adminPasswordEnc); err != nil {
		t.Fatalf("SetHostAdmin: %v", err)
	}
	if err := db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}

	manager := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	return manager, db, key, hostID
}
