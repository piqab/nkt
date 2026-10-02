package hub

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

func TestBotTime(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Yekaterinburg")
	now := time.Date(2026, 10, 2, 13, 30, 0, 0, loc)
	for ts, want := range map[time.Time]string{
		time.Date(2026, 10, 2, 8, 5, 44, 0, time.UTC):  "13:05",
		time.Date(2026, 10, 1, 17, 40, 0, 0, time.UTC): "вчера 22:40",
		time.Date(2026, 9, 30, 3, 15, 0, 0, time.UTC):  "30.09 08:15",
	} {
		if got := botTime(ts, loc, msgs.RU, now); got != want {
			t.Errorf("%v: %q, want %q", ts, got, want)
		}
	}
	if !validZone("") || !validZone("Europe/Moscow") || validZone("Mars/Base") {
		t.Fatal("validZone")
	}
}

func TestEventBodyFindings(t *testing.T) {
	args := msgs.EncodeArgs([]any{25, 11, "A: 0 из 1; B: 0 из 1; C: ImagePullBackOff; D; E; …"})
	got := eventBody(msgs.RU, "hub.seriousFindingsNowNamed", args, "")
	want := "Серьёзных находок: 25 (было 11)\n• A: 0 из 1\n• B: 0 из 1\n• C: ImagePullBackOff\n…и ещё 11"
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestBotReplyMarkup(t *testing.T) {
	r := botReply{Parts: []botPart{{Head: "⚠️ web<1>", Body: "a & b"}, {Body: "c"}}}
	if got := r.telegramHTML(); got != "<b>⚠️ web&lt;1&gt;</b>\na &amp; b\n\nc" {
		t.Fatalf("telegram: %q", got)
	}
	if got := r.slackMrkdwn(); got != "*⚠️ web&lt;1&gt;*\na &amp; b\n\nc" {
		t.Fatalf("slack: %q", got)
	}
}

func TestHostStateColors(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		h    hostWithOverview
		icon string
	}{
		{hostWithOverview{Host: store.Host{Status: store.HostStatusError, ErrorMsg: "расшифровка SSH-секрета"}}, "🔴"},
		{hostWithOverview{Host: store.Host{Status: store.HostStatusOnline}, Reachable: &no}, "🔴"},
		{hostWithOverview{Host: store.Host{Status: store.HostStatusOnline}, Reachable: &yes, Findings: map[string]int{"high": 22}}, "🟠"},
		{hostWithOverview{Host: store.Host{Status: store.HostStatusOnline}, Reachable: &yes, Findings: map[string]int{"medium": 2}}, "🟡"},
		{hostWithOverview{Host: store.Host{Status: store.HostStatusOnline}, Reachable: &yes}, "🟢"},
		{hostWithOverview{Host: store.Host{Status: store.HostStatusNew}}, "⚪"},
	} {
		if icon, _, _ := hostState(msgs.RU, c.h); icon != c.icon {
			t.Errorf("%+v: %s, want %s", c.h.Host.Status, icon, c.icon)
		}
	}
}

// /alerts: одинаковые события разных хостов рядом по времени — один блок.
func TestBotAlertsGrouping(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	text := msgs.EncodeArgs([]any{18, 13, "StatefulSet keeper-0: доступно 0 из 1 реплик"})
	for _, h := range []string{"cg221", "crem1"} {
		if _, err := db.AddHostEvent(ctx, store.HostEvent{HostID: 7, HostName: h, HostAddr: "x", Kind: store.EventProblems,
			Severity: "critical+high", Detail: "x", DetailKey: "hub.seriousFindingsNowNamed", DetailArgs: text}); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = db.AddHostEvent(ctx, store.HostEvent{HostID: 8, HostName: "ns1", HostAddr: "y", Kind: store.EventUnreachable, Detail: "timeout"})
	loc, _ := time.LoadLocation("Europe/Moscow")
	r := srv.botCore().alerts(ctx, botTurn{Lang: msgs.RU, Loc: loc})
	if len(r.Parts) != 2 {
		t.Fatalf("parts: %+v", r.Parts)
	}
	if !strings.Contains(r.Parts[1].Head, "crem1, cg221") && !strings.Contains(r.Parts[1].Head, "cg221, crem1") {
		t.Fatalf("grouped head: %q", r.Parts[1].Head)
	}
	if !strings.Contains(r.Parts[1].Body, "• StatefulSet keeper-0") || strings.Contains(r.Parts[0].Head, "#") {
		t.Fatalf("body: %+v", r.Parts)
	}
	if len(r.Buttons) == 0 {
		t.Fatal("no findings buttons")
	}
}
