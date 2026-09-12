package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

func standaloneSetup(t *testing.T) *CertManager {
	t.Helper()
	root := copyFixturesRoot(t)
	cfg := &config.Config{
		Mode: config.ModeFixtures, FixturesRoot: root,
		NginxMainConfig: "/etc/nginx/nginx.conf", HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		CommandTimeout: 5 * time.Second, CertbotTimeout: 20 * time.Second,
	}
	c := collect.NewFixtures(root)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	return NewCertManager(cfg, c, db, NewServiceManager(cfg, c, db), scanner, nil)
}

// Кто держит 80/443 — из живого списка сокетов, а не из списка по памяти:
// в снимке это nginx (pid 812) на обоих портах, и это один держатель,
// служба nginx.service, которую можно остановить и вернуть.
func TestStandalonePlanFindsServiceHolder(t *testing.T) {
	m := standaloneSetup(t)
	plan, err := m.StandalonePlan(context.Background())
	if err != nil {
		t.Fatalf("StandalonePlan: %v", err)
	}
	if len(plan.Holders) != 1 {
		t.Fatalf("держателей %d, ожидался один (nginx на 80 и 443): %+v", len(plan.Holders), plan.Holders)
	}
	h := plan.Holders[0]
	if h.Kind != HolderService || h.Unit != "nginx.service" || !h.Restartable {
		t.Errorf("держатель = %+v, ожидалась служба nginx.service", h)
	}
	if len(h.Ports) != 2 {
		t.Errorf("порты держателя = %v, ожидались оба: 80 и 443", h.Ports)
	}
	if plan.Blocked {
		t.Error("план заблокирован, хотя единственный держатель — служба")
	}
}

// Ручной процесс без разрешения на перезапуск — отказ с именем, а не
// убитый чужой сервис; контейнер — отказ всегда.
func TestFreeStandalonePortsRespectsPermissions(t *testing.T) {
	m := standaloneSetup(t)
	flask := PortHolder{Ports: []int{80}, PID: 4242, Process: "python3", User: "alex",
		Command: "python3 app.py", Kind: HolderProcess, Restartable: true, argv: []string{"python3", "app.py"}}

	_, err := m.freeStandalonePorts(context.Background(), "test", StandalonePlan{Holders: []PortHolder{flask}}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "python3") || !strings.Contains(err.Error(), "4242") {
		t.Errorf("процесс без разрешения: err=%v, ожидался отказ с его именем и pid", err)
	}

	box := PortHolder{Ports: []int{443}, PID: 77, Process: "nginx", ContainerID: "abcdef123456789", Kind: HolderContainer}
	_, err = m.freeStandalonePorts(context.Background(), "test", StandalonePlan{Holders: []PortHolder{box}},
		map[int]bool{77: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "контейнер") {
		t.Errorf("контейнер: err=%v, ожидался отказ «освободите сами»", err)
	}
}

// Окружение старого запуска переезжает в новый без служебных переменных
// systemd — они принадлежат прошлому юниту и новому только помешают.
func TestSplitNUL(t *testing.T) {
	got := splitNUL([]byte("python3\x00app.py\x00\x00"))
	if len(got) != 2 || got[0] != "python3" || got[1] != "app.py" {
		t.Errorf("splitNUL = %q", got)
	}
}
