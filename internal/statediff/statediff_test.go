package statediff

import (
	"testing"
	"time"

	"github.com/piqab/nkt/internal/model"
)

func find(changes []Change, kind, key string) (Change, bool) {
	for _, c := range changes {
		if c.Kind == kind && c.Key == key {
			return c, true
		}
	}
	return Change{}, false
}

// То, ради чего сравнение и делается: порт закрылся, служба остановилась,
// появился новый файл конфигурации.
func TestDiffFindsRealChanges(t *testing.T) {
	prev := model.Snapshot{
		Services: []model.ServiceUnit{
			{Name: "nginx", Installed: true, ActiveState: "active", Enabled: "enabled"},
			{Name: "fail2ban", Installed: true, ActiveState: "active", Enabled: "enabled"},
		},
		Listeners: []model.Listener{
			{Protocol: "tcp", Port: 443, Process: "nginx"},
			{Protocol: "tcp", Port: 5432, Process: "postgres"},
		},
		Files: []model.ManagedFile{{Path: "/etc/nginx/nginx.conf", SHA256: "aaaaaaaaaaaabbbb"}},
	}
	cur := model.Snapshot{
		Services: []model.ServiceUnit{
			{Name: "nginx", Installed: true, ActiveState: "inactive", Enabled: "enabled"},
			{Name: "fail2ban", Installed: true, ActiveState: "active", Enabled: "enabled"},
			{Name: "docker", Installed: true, ActiveState: "active", Enabled: "enabled"},
		},
		Listeners: []model.Listener{
			{Protocol: "tcp", Port: 443, Process: "nginx"},
		},
		Files: []model.ManagedFile{
			{Path: "/etc/nginx/nginx.conf", SHA256: "ccccccccccccdddd"},
			{Path: "/etc/nginx/conf.d/api.conf", SHA256: "eeeeeeeeeeeeffff"},
		},
	}

	changes := Diff(prev, cur)

	if c, ok := find(changes, KindService, "nginx"); !ok || c.Action != Changed ||
		c.Was != "active/enabled" || c.Now != "inactive/enabled" {
		t.Errorf("остановленная служба = %+v", c)
	}
	if c, ok := find(changes, KindService, "docker"); !ok || c.Action != Appeared {
		t.Errorf("новая служба = %+v", c)
	}
	if _, ok := find(changes, KindService, "fail2ban"); ok {
		t.Errorf("неизменившаяся служба попала в список изменений")
	}
	if c, ok := find(changes, KindPort, "tcp/5432"); !ok || c.Action != Disappeared || c.Was != "postgres" {
		t.Errorf("закрывшийся порт = %+v", c)
	}
	if c, ok := find(changes, KindFile, "/etc/nginx/nginx.conf"); !ok || c.Action != Changed {
		t.Errorf("правленый файл = %+v", c)
	}
	if c, ok := find(changes, KindFile, "/etc/nginx/conf.d/api.conf"); !ok || c.Action != Appeared {
		t.Errorf("новый файл = %+v", c)
	}
}

// Изменчивое не считается изменением: иначе список был бы полон шума, а
// настоящая правка тонула бы в нём.
func TestDiffIgnoresNoise(t *testing.T) {
	base := model.Snapshot{
		TS: "2026-09-11T10:00:00Z",
		Services: []model.ServiceUnit{
			{Name: "nginx", Installed: true, ActiveState: "active", Enabled: "enabled",
				MemoryBytes: 100 << 20, MainPID: 111, Restarts: 1, SinceText: "вчера"},
			// Неустановленная служба — пустое место в списке известных
			// nkt служб, а не состояние сервера.
			{Name: "haproxy", Installed: false},
		},
		Listeners: []model.Listener{{Protocol: "tcp", Port: 443, Process: "nginx", PID: 111}},
		Container: []model.Container{{Name: "web", Image: "nginx:1.27", State: "running", Status: "Up 8 days"}},
		Files:     []model.ManagedFile{{Path: "/etc/nginx/nginx.conf", SHA256: "aaaaaaaaaaaabbbb", ModTime: time.Now()}},
	}
	noisy := model.Snapshot{
		TS: "2026-09-11T10:05:00Z",
		Services: []model.ServiceUnit{
			{Name: "nginx", Installed: true, ActiveState: "active", Enabled: "enabled",
				MemoryBytes: 140 << 20, MainPID: 222, Restarts: 2, SinceText: "сегодня"},
			{Name: "haproxy", Installed: false},
		},
		Listeners: []model.Listener{{Protocol: "tcp", Port: 443, Process: "nginx", PID: 222}},
		Container: []model.Container{{Name: "web", Image: "nginx:1.27", State: "running", Status: "Up 9 days"}},
		Files: []model.ManagedFile{{Path: "/etc/nginx/nginx.conf", SHA256: "aaaaaaaaaaaabbbb",
			ModTime: time.Now().Add(time.Hour)}},
	}

	if changes := Diff(base, noisy); len(changes) != 0 {
		t.Errorf("шум попал в изменения: %+v", changes)
	}
}

// Контейнер, которому подменили образ, — изменение, а не новый контейнер.
func TestDiffContainerImage(t *testing.T) {
	prev := model.Snapshot{Container: []model.Container{{Name: "web", Image: "nginx:1.25", State: "running"}}}
	cur := model.Snapshot{Container: []model.Container{{Name: "web", Image: "nginx:1.27", State: "running"}}}
	c, ok := find(Diff(prev, cur), KindContainer, "web")
	if !ok || c.Action != Changed || c.Was != "nginx:1.25 running" || c.Now != "nginx:1.27 running" {
		t.Errorf("смена образа = %+v", c)
	}
}

// Перестановка правил фаервола — не изменение: правило то же самое, лишь
// с другим порядковым номером.
func TestDiffFirewallIgnoresOrder(t *testing.T) {
	rule := func(id string, order int, port string) model.FirewallRule {
		return model.FirewallRule{ID: id, Order: order, Backend: "ufw", Chain: "INPUT",
			Protocol: "tcp", PortSpec: port, Action: "ACCEPT"}
	}
	prev := model.Snapshot{Firewall: model.FirewallState{Rules: []model.FirewallRule{rule("1", 1, "22"), rule("2", 2, "443")}}}
	cur := model.Snapshot{Firewall: model.FirewallState{Rules: []model.FirewallRule{rule("7", 1, "443"), rule("8", 2, "22")}}}
	if changes := Diff(prev, cur); len(changes) != 0 {
		t.Errorf("перестановка правил показана как изменение: %+v", changes)
	}

	// А вот исчезнувшее правило — настоящее изменение.
	only22 := model.Snapshot{Firewall: model.FirewallState{Rules: []model.FirewallRule{rule("1", 1, "22")}}}
	changes := Diff(prev, only22)
	if len(changes) != 1 || changes[0].Action != Disappeared {
		t.Errorf("удалённое правило = %+v", changes)
	}
}
