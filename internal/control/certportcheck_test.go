package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/store"
)

// TestCheckPortFreeIdentifiesBlocker covers "если сервисы не определены —
// сообщение, какой сервис мешает": fixtures/host/.commands/ss.txt shows
// nginx still listening on :80, so checkPortFree must name exactly that
// process rather than reporting a generic "occupied".
func TestCheckPortFreeIdentifiesBlocker(t *testing.T) {
	m, _ := renewSetup(t)
	holder, free := m.checkPortFree(context.Background(), 80)
	if free {
		t.Fatal("free = true, ожидалось false — порт 80 занят в фикстурах")
	}
	if !strings.Contains(holder, "nginx") {
		t.Errorf("holder = %q, ожидалось упоминание nginx", holder)
	}
}

func TestCheckPortFreePortNotInUse(t *testing.T) {
	m, _ := renewSetup(t)
	if _, free := m.checkPortFree(context.Background(), 65000); !free {
		t.Error("free = false для порта, которого нет среди слушателей")
	}
}

// TestCheckPortFreeForStandaloneSkippedInFixtures covers why the gate must
// not run against a canned snapshot: ss.txt always reports nginx on :80
// regardless of what stopForStandalone just "stopped" moments earlier —
// fixtures commands don't simulate state changes between calls — so
// enforcing the check there would reject every renewal in fixtures mode.
func TestCheckPortFreeForStandaloneSkippedInFixtures(t *testing.T) {
	m, _ := renewSetup(t)
	var reported []string
	err := m.checkPortFreeForStandalone(context.Background(), func(s string) { reported = append(reported, s) })
	if err != nil {
		t.Errorf("ожидался nil в режиме fixtures, получено: %v", err)
	}
	if len(reported) != 0 {
		t.Errorf("проверка порта не должна ничего сообщать в режиме fixtures, получено: %v", reported)
	}
}

// TestCheckPortFreeForStandaloneBlocksOutsideFixtures covers the actual
// gate: outside fixtures mode, a port still held after stopForStandalone
// must refuse the operation with the blocking process named, instead of
// letting certbot fail later with its own opaque bind error.
func TestCheckPortFreeForStandaloneBlocksOutsideFixtures(t *testing.T) {
	root := copyFixturesRoot(t)
	cfg := &config.Config{
		// Only cfg.Mode gates checkPortFreeForStandalone; commands still go
		// through the Fixtures collector constructed below, so this never
		// touches the real filesystem or runs real commands.
		Mode:            config.ModeLocal,
		FixturesRoot:    root,
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		ComposeFiles:    []string{"/srv/docker/docker-compose.yml"},
		CommandTimeout:  5 * time.Second,
		CertbotTimeout:  20 * time.Second,
	}
	c := collect.NewFixtures(root)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	services := NewServiceManager(cfg, c, db)
	m := NewCertManager(cfg, c, db, services, scanner)

	var reported []string
	err = m.checkPortFreeForStandalone(context.Background(), func(s string) { reported = append(reported, s) })
	if err == nil {
		t.Fatal("ожидалась ошибка — порт 80 занят nginx в фикстурах ss.txt")
	}
	if !strings.Contains(err.Error(), "nginx") {
		t.Errorf("ошибка не называет блокирующий процесс: %v", err)
	}
	if len(reported) == 0 {
		t.Error("ожидалось сообщение о проверке порта")
	}
}
