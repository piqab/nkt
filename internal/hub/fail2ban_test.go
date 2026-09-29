package hub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// Новые баны — оповещение с адресами; первый опрос только запоминает,
// повтор тех же банов молчит.
func TestNoteBans(t *testing.T) {
	m, id := eventTestManager(t)
	ctx := context.Background()
	first := &f2bSummary{Installed: true, Running: true, Banned: 1, Bans: []f2bBan{{IP: "203.0.113.5", Jail: "sshd"}}}
	m.noteBans(ctx, id, first)
	m.overviewMu.Lock()
	m.overview[id] = hostOverview{reachable: true, f2b: first}
	m.overviewMu.Unlock()
	m.noteBans(ctx, id, first)
	if events, _, _ := m.Events(ctx, 10); len(events) != 0 {
		t.Fatalf("no new bans, yet events: %+v", events)
	}
	next := &f2bSummary{Installed: true, Running: true, Banned: 2, Bans: []f2bBan{{IP: "203.0.113.5", Jail: "sshd"}, {IP: "198.51.100.9", Jail: "sshd"}}}
	m.noteBans(ctx, id, next)
	events, _, _ := m.Events(ctx, 10)
	if len(events) != 1 || events[0].Kind != store.EventBans || !strings.Contains(events[0].Detail, "198.51.100.9 (sshd)") || strings.Contains(events[0].Detail, "203.0.113.5") {
		t.Fatalf("events: %+v", events)
	}
}

// Сводка: адрес, забаненный на двух хостах, — сверху, с хостами и
// джейлами.
func TestF2BBannedSummary(t *testing.T) {
	m, id := eventTestManager(t)
	ctx := context.Background()
	id2, err := m.db.CreateHost(ctx, "web-2", "10.0.0.8", 22, "root", store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []int64{id, id2} {
		if err := m.db.SetHostStatus(ctx, h, store.HostStatusOnline, ""); err != nil {
			t.Fatal(err)
		}
	}
	m.overviewMu.Lock()
	m.overview[id] = hostOverview{f2b: &f2bSummary{Installed: true, Running: true, Banned: 2, Bans: []f2bBan{{IP: "203.0.113.5", Jail: "sshd"}, {IP: "192.0.2.1", Jail: "nginx-http-auth"}}}}
	m.overview[id2] = hostOverview{f2b: &f2bSummary{Installed: true, Running: true, Banned: 1, Bans: []f2bBan{{IP: "203.0.113.5", Jail: "nkt-manual"}}}}
	m.overviewMu.Unlock()
	s := &Server{db: m.db, hub: m}
	rec := httptest.NewRecorder()
	s.handleF2BBanned(rec, httptest.NewRequest("GET", "/hub/fail2ban/banned", nil))
	var out struct {
		IPs []F2BBannedIP `json:"ips"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.IPs) != 2 || out.IPs[0].IP != "203.0.113.5" || len(out.IPs[0].Hosts) != 2 {
		t.Fatalf("%+v", out.IPs)
	}
	targets, err := s.f2bTargets(ctx, nil)
	if err != nil || len(targets) != 2 {
		t.Fatalf("targets: %v %+v", err, targets)
	}
}
