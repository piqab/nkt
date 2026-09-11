package hub

import (
	"context"
	"log/slog"
	osuser "os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Машина внутри хоста живёт в его виртуальной сети, и с хаба туда нет
// маршрута: прямое подключение кончалось «dial tcp 192.168.123.168:22:
// i/o timeout». Проверяется, что подключение к такой записи идёт через её
// хост и что поверх пробитого канала работает обычная SSH-сессия.
//
// Полной изоляции сети в одном тесте не изобразить — здесь один настоящий
// sshd на loopback, — поэтому доказательства два: цепочка соединений
// (переход открыт) и живая команда, выполненная через неё.
func TestDialHostGoesThroughParentForMachine(t *testing.T) {
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)
	me, err := osuser.Current()
	if err != nil {
		t.Fatalf("os/user.Current: %v", err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open hub store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	secretEnc, err := secretbox.Encrypt(key, clientKeyPEM)
	if err != nil {
		t.Fatalf("encrypt ssh key: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hostID, err := db.CreateHost(ctx, "host", sshAddr, sshPort, me.Username, store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	vmID, err := db.CreateHost(ctx, "machine", sshAddr, sshPort, me.Username, store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost (машина): %v", err)
	}
	if err := db.SetHostParent(ctx, vmID, hostID); err != nil {
		t.Fatalf("SetHostParent: %v", err)
	}

	m := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))

	host, err := db.HostByID(ctx, hostID)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	direct, err := m.dialHost(ctx, host)
	if err != nil {
		t.Fatalf("подключение к обычному хосту: %v", err)
	}
	defer direct.Close()
	if direct.under != nil {
		t.Errorf("к обычному хосту пошли через переход, хотя он доступен напрямую")
	}

	vm, err := db.HostByID(ctx, vmID)
	if err != nil {
		t.Fatalf("HostByID (машина): %v", err)
	}
	link, err := m.dialHost(ctx, vm)
	if err != nil {
		t.Fatalf("подключение к машине через её хост: %v", err)
	}
	defer link.Close()
	if link.under == nil {
		t.Fatalf("к машине пошли напрямую — с хаба в её сеть маршрута нет, так соединение и обрывалось таймаутом")
	}

	out, err := runRemote(link.client, "echo через-хост")
	if err != nil {
		t.Fatalf("команда через переход: %v: %s", err, out)
	}
	if !strings.Contains(out, "через-хост") {
		t.Errorf("вывод через переход = %q", out)
	}
}

// Машина без адреса — та, что ещё не получила его от DHCP. Стучаться туда
// бессмысленно, и объяснять это должно сообщение, а не «ssh: handshake
// failed» с адресом 0.0.0.0.
func TestDialHostRefusesPlaceholderAddress(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open hub store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	secretEnc, err := secretbox.Encrypt(key, []byte("не ключ, сюда не дойдёт"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	ctx := context.Background()
	id, err := db.CreateHost(ctx, "machine", PlaceholderAddr, 22, "root", store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	host, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	m := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	_, err = m.dialHost(ctx, host)
	if err == nil {
		t.Fatal("подключение по адресу-заглушке принято")
	}
	if !strings.Contains(err.Error(), "адрес машины") {
		t.Errorf("ошибка = %v, а должна объяснять, что адреса ещё нет", err)
	}
}
