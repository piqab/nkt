package hub

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

func eventTestManager(t *testing.T) (*Manager, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := secretbox.Encrypt(key, []byte("секрет"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	id, err := db.CreateHost(context.Background(), "web-1", "10.0.0.7", 22, "root", store.HostAuthKey, enc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	return NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler)), id
}

// Оповещение должно называть хост: во всплывающем было только имя, а в
// журнале не было ничего — узнать, о какой машине речь, было нельзя.
func TestEventCarriesHostNameAndAddress(t *testing.T) {
	m, id := eventTestManager(t)
	ctx := context.Background()

	// Отвечающий хост при первом наблюдении молчит: это не событие, а
	// начало наблюдения.
	m.noteReachability(ctx, id, true, "")
	if events, _, _ := m.Events(ctx, 10); len(events) != 0 {
		t.Fatalf("первое наблюдение отвечающего хоста записано как событие: %+v", events)
	}

	m.overviewMu.Lock()
	m.overview[id] = hostOverview{reachable: true, findings: map[string]int{"high": 1}}
	m.overviewMu.Unlock()

	m.noteReachability(ctx, id, false, "таймаут подключения")
	events, _, err := m.Events(ctx, 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("событий %d, ожидалось одно: %+v", len(events), events)
	}
	e := events[0]
	if e.Kind != store.EventUnreachable {
		t.Errorf("вид события = %q", e.Kind)
	}
	if e.HostName != "web-1" {
		t.Errorf("имя хоста = %q", e.HostName)
	}
	if !strings.Contains(e.HostAddr, "10.0.0.7") || !strings.Contains(e.HostAddr, "root") {
		t.Errorf("адрес хоста = %q, а по нему и узнают, о какой машине речь", e.HostAddr)
	}
	if !strings.Contains(e.Detail, "таймаут подключения") {
		t.Errorf("причина потеряна: %q", e.Detail)
	}

	// Хост, лежащий третьи сутки, не должен писать строку каждые полминуты.
	m.overviewMu.Lock()
	m.overview[id] = hostOverview{reachable: false}
	m.overviewMu.Unlock()
	m.noteReachability(ctx, id, false, "таймаут подключения")
	if events, _, _ := m.Events(ctx, 10); len(events) != 1 {
		t.Errorf("повторная недоступность записана ещё раз: %d событий", len(events))
	}

	// Возврат — отдельное событие.
	m.noteReachability(ctx, id, true, "")
	events, _, _ = m.Events(ctx, 10)
	if len(events) != 2 || events[0].Kind != store.EventRecovered {
		t.Errorf("возврат хоста не записан: %+v", events)
	}
}

// Находки: пишется рост и полное исчезновение, а не каждый опрос.
func TestEventFindingsTransitions(t *testing.T) {
	m, id := eventTestManager(t)
	ctx := context.Background()

	m.overviewMu.Lock()
	m.overview[id] = hostOverview{reachable: true, findings: map[string]int{"critical": 0, "high": 1}}
	m.overviewMu.Unlock()

	// Столько же — молчим.
	m.noteFindings(ctx, id, map[string]int{"high": 1})
	if events, _, _ := m.Events(ctx, 10); len(events) != 0 {
		t.Fatalf("неизменившиеся находки записаны: %+v", events)
	}

	m.noteFindings(ctx, id, map[string]int{"critical": 2, "high": 1})
	events, _, _ := m.Events(ctx, 10)
	if len(events) != 1 || events[0].Kind != store.EventProblems {
		t.Fatalf("рост числа находок не записан: %+v", events)
	}
	if !strings.Contains(events[0].Detail, "3") || !strings.Contains(events[0].Detail, "1") {
		t.Errorf("в подробностях нет «было → стало»: %q", events[0].Detail)
	}

	m.overviewMu.Lock()
	m.overview[id] = hostOverview{reachable: true, findings: map[string]int{"critical": 2, "high": 1}}
	m.overviewMu.Unlock()
	m.noteFindings(ctx, id, map[string]int{})
	events, _, _ = m.Events(ctx, 10)
	if len(events) != 2 || events[0].Kind != store.EventResolved {
		t.Errorf("исчезновение находок не записано: %+v", events)
	}
}

// Хост, не отвечающий с самого начала наблюдения, тоже попадает в
// журнал: иначе перезапуск хаба стирал бы саму память о том, что он лежит.
func TestEventUnreachableOnFirstSight(t *testing.T) {
	m, id := eventTestManager(t)
	ctx := context.Background()
	m.noteReachability(ctx, id, false, "нет маршрута")
	events, _, _ := m.Events(ctx, 10)
	if len(events) != 1 || events[0].Kind != store.EventUnreachable {
		t.Fatalf("первая недоступность не записана: %+v", events)
	}
}

// Счётчик непоказанных гаснет, когда журнал прочитан.
func TestEventsUnreadCount(t *testing.T) {
	m, id := eventTestManager(t)
	ctx := context.Background()
	m.overviewMu.Lock()
	m.overview[id] = hostOverview{reachable: true}
	m.overviewMu.Unlock()

	m.noteReachability(ctx, id, false, "нет связи")
	if _, unread, _ := m.Events(ctx, 10); unread != 1 {
		t.Fatalf("непоказанных = %d, ожидалось 1", unread)
	}
	if err := m.MarkEventsSeen(ctx); err != nil {
		t.Fatalf("MarkEventsSeen: %v", err)
	}
	if _, unread, _ := m.Events(ctx, 10); unread != 0 {
		t.Errorf("после прочтения непоказанных = %d", unread)
	}
}
