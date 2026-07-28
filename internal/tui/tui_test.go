package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/monitor"
	"github.com/althq/netknownsthat/internal/store"
)

// fixtureDeps wires the whole application against the canned host snapshot and
// a throwaway database, so the interface can be driven without a real host.
func fixtureDeps(t *testing.T, screen tcell.Screen) Deps {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("путь к снапшоту: %v", err)
	}
	cfg := &config.Config{
		Mode:              config.ModeFixtures,
		FixturesRoot:      root,
		DataDir:           t.TempDir(),
		NginxMainConfig:   "/etc/nginx/nginx.conf",
		HAProxyMainConf:   "/etc/haproxy/haproxy.cfg",
		ComposeFiles:      []string{"/srv/docker/docker-compose.yml"},
		CommandTimeout:    5 * time.Second,
		ProbeTimeout:      time.Second,
		ProbeInterval:     time.Minute,
		MetricsInterval:   time.Minute,
		InventoryInterval: time.Hour,
		AllowMutations:    true,
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	collector := collect.NewFixtures(root)
	scanner := inventory.New(cfg, collector, db)
	services := control.NewServiceManager(cfg, collector, db)

	return Deps{
		Cfg:       cfg,
		DB:        db,
		Collector: collector,
		Scanner:   scanner,
		Services:  services,
		Configs:   control.NewConfigManager(cfg, collector, db, scanner, services),
		Firewall:  control.NewFirewallManager(cfg, collector, db),
		Prober:    monitor.NewProber(db, cfg),
		Screen:    screen,
	}
}

// newWideScreen builds a simulated terminal. It starts at the tcell default
// size; callers widen it once the application has taken the screen over.
func newWideScreen(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("инициализация симулятора: %v", err)
	}
	return sim
}

// screenText flattens the simulated terminal into a searchable string.
func screenText(sim tcell.SimulationScreen) string {
	cells, width, height := sim.GetContents()
	var sb strings.Builder
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			runes := cells[y*width+x].Runes
			if len(runes) == 0 {
				sb.WriteRune(' ')
				continue
			}
			sb.WriteRune(runes[0])
		}
		sb.WriteRune('\n')
	}
	return sb.String()
}

// waitFor polls the simulated screen until it contains the needle.
func waitFor(t *testing.T, sim tcell.SimulationScreen, needle string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = screenText(sim)
		if strings.Contains(last, needle) {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Errorf("на экране так и не появилось %q. Последний кадр:\n%s", needle, last)
	return last
}

// TestScreensRenderAgainstFixtures drives every screen through a simulated
// terminal. It is a smoke test with teeth: each screen loads real parsed data,
// so a panic or a wrong field in any refresh path fails here.
func TestScreensRenderAgainstFixtures(t *testing.T) {
	sim := newWideScreen(t)
	deps := fixtureDeps(t, sim)
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), deps) }()

	// The header appears as soon as the first scan lands.
	waitFor(t, sim, "NetKnownsThat")
	// tview re-initialises the screen when it takes it over, which resets the
	// simulated size; widen it once the application is actually drawing.
	sim.SetSize(220, 60)
	waitFor(t, sim, "edge-01")

	cases := []struct {
		key    rune
		screen string
		expect []string
	}{
		{'1', "Обзор", []string{"Требуют внимания", "Слушателей объявлено", "nginx", "ufw"}},
		{'2', "Проблемы", []string{"Серьёзность", "docker-bypasses-firewall"}},
		{'3', "Карта", []string{"Карта сетевых ресурсов", "Доступно из внешней сети"}},
		{'4', "Доступность", []string{"Ресурсы под наблюдением", "Недоступность по часам"}},
		{'5', "Нагрузка", []string{"Кто нагружает больше всех", "Расписание использования"}},
		{'6', "Конфигурации", []string{"Файлы конфигурации", "nginx.conf"}},
		{'7', "Сервисы", []string{"Сервисы и контейнеры", "acme-redis", "systemd"}},
		{'8', "Firewall", []string{"Правила", "DNAT"}},
		{'9', "Журнал", []string{"Журнал действий", "Фоновые задачи"}},
	}

	for _, c := range cases {
		sim.InjectKey(tcell.KeyRune, c.key, tcell.ModNone)
		for _, needle := range c.expect {
			frame := waitFor(t, sim, needle)
			if t.Failed() {
				t.Fatalf("экран %q: не отрисовался %q\n%s", c.screen, needle, frame)
			}
		}
	}

	// The help overlay and Escape must work from any screen.
	sim.InjectKey(tcell.KeyRune, '?', tcell.ModNone)
	waitFor(t, sim, "Навигация")
	sim.InjectKey(tcell.KeyEsc, 0, tcell.ModNone)

	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("приложение завершилось с ошибкой: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("приложение не завершилось по нажатию q")
	}
}

// TestFindingsFilterCycles checks the one piece of screen state that is not
// derived from the snapshot.
func TestFindingsFilterCycles(t *testing.T) {
	sim := newWideScreen(t)
	deps := fixtureDeps(t, sim)
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), deps) }()

	waitFor(t, sim, "NetKnownsThat")
	sim.SetSize(220, 60)
	waitFor(t, sim, "edge-01")
	sim.InjectKey(tcell.KeyRune, '2', tcell.ModNone)
	waitFor(t, sim, "Серьёзность")

	// First press narrows to critical only; both planted critical findings stay.
	sim.InjectKey(tcell.KeyRune, 'f', tcell.ModNone)
	frame := waitFor(t, sim, "показано 2 из 25")
	if strings.Contains(frame, "container-undeclared") {
		t.Error("фильтр «критично» не должен показывать находки низкой серьёзности")
	}

	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	<-done
}
