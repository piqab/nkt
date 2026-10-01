package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// Исходящие вебхуки: подпись, выбор событий и пределы, повтор после
// отказа, пробное событие, выкладка.
func TestOutgoingWebhooks(t *testing.T) {
	old := outRetries
	outRetries = []time.Duration{10 * time.Millisecond}
	defer func() { outRetries = old }()

	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	webID, _ := db.CreateHost(ctx, "web1", "203.0.113.10", 22, "root", store.HostAuthPassword, []byte("x"))
	_ = db.SetHostGroup(ctx, webID, "prod")
	dbID, _ := db.CreateHost(ctx, "db1", "203.0.113.11", 22, "root", store.HostAuthPassword, []byte("x"))

	type got struct {
		kind, delivery, ts, sig string
		body                    map[string]any
		raw                     []byte
	}
	var mu sync.Mutex
	var received []got
	fail := 1 // первый запрос — 500, повтор проходит
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if fail > 0 {
			fail--
			w.WriteHeader(500)
			return
		}
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		received = append(received, got{r.Header.Get("X-NKT-Event"), r.Header.Get("X-NKT-Delivery"), r.Header.Get("X-NKT-Timestamp"), r.Header.Get("X-NKT-Signature"), body, raw})
	}))
	defer recv.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"admin-password-1234"}`))
	h.ServeHTTP(rec, req)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	do := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, _ := do("POST", "/api/hub/webhooks", `{"name":"x","url":"ftp://a"}`); code != 400 {
		t.Fatalf("bad url accepted: %d", code)
	}
	if code, _ := do("POST", "/api/hub/webhooks", `{"name":"x","url":"https://a.example","kinds":["nope"]}`); code != 400 {
		t.Fatalf("bad kind accepted: %d", code)
	}
	code, created := do("POST", "/api/hub/webhooks", `{"name":"n8n","url":"`+recv.URL+`/hook","kinds":["unreachable","deploy-failed"],"groups":["prod"],"lang":"en","enabled":true}`)
	if code != 200 || created["secret"] == nil {
		t.Fatalf("create: %d %v", code, created)
	}
	secret := created["secret"].(string)

	host, _ := db.HostByID(ctx, webID)
	other, _ := db.HostByID(ctx, dbID)
	srv.hub.recordEventMsg(ctx, host, store.EventUnreachable, "warning", "hub.unreachableMin", 3) // доставится (после повтора)
	srv.hub.recordEventMsg(ctx, host, store.EventRecovered, "", "hub.unreachableMin", 1)          // не тот вид
	srv.hub.recordEventMsg(ctx, other, store.EventUnreachable, "warning", "hub.unreachableMin", 2) // вне пределов
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	if len(received) != 1 {
		mu.Unlock()
		t.Fatalf("received %d: %+v", len(received), received)
	}
	g := received[0]
	mu.Unlock()
	if g.kind != "unreachable" || g.sig != OutSignature(secret, g.ts, g.raw) || g.delivery == "" {
		t.Fatalf("headers: %+v", g)
	}
	if hostObj, _ := g.body["host"].(map[string]any); hostObj["name"] != "web1" || g.body["event_id"] == nil {
		t.Fatalf("body: %s", g.raw)
	}
	if txt, _ := g.body["text"].(string); !strings.Contains(txt, "3") || strings.ContainsAny(txt, "абв") {
		t.Fatalf("english text: %q", txt)
	}

	// Пробное событие — сразу, ответ — итог доставки.
	id := int(created["id"].(float64))
	code, res := do("POST", "/api/hub/webhooks/"+strconv.Itoa(id)+"/test", "")
	if code != 200 || res["code"].(float64) != 200 {
		t.Fatalf("test: %d %v", code, res)
	}
	// Состояние доставки — в списке, секрета там нет.
	code, list := do("GET", "/api/hub/webhooks", "")
	hooks := list["hooks"].([]any)
	first := hooks[0].(map[string]any)
	if code != 200 || first["last_kind"] != "test" || first["secret_enc"] != nil || first["has_secret"] != true {
		t.Fatalf("list: %v", first)
	}
	// Новый секрет — старая подпись больше не годится.
	_, rot := do("POST", "/api/hub/webhooks/"+strconv.Itoa(id)+"/rotate", "")
	if rot["secret"] == nil || rot["secret"] == secret {
		t.Fatalf("rotate: %v", rot)
	}
	if code, _ := do("DELETE", "/api/hub/webhooks/"+strconv.Itoa(id), ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}

	// Выкладка не прошла — адресату без пределов.
	_, c2 := do("POST", "/api/hub/webhooks", `{"name":"chat","url":"`+recv.URL+`/d","kinds":["deploy-failed"],"enabled":true}`)
	mu.Lock()
	received = nil
	mu.Unlock()
	srv.emitDeploy(ctx, store.Pipeline{ID: 7, Name: "shop", Content: "action: script"}, store.Deployment{Tag: "v1.2.0", Trigger: "webhook"}, 42, "0123456789abcdef", errors.New("boom"))
	srv.emitDeploy(ctx, store.Pipeline{ID: 7, Name: "shop"}, store.Deployment{Tag: "v1.2.1"}, 43, "", nil) // успех — не выбран
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0].kind != "deploy-failed" || received[0].body["error"] != "boom" || received[0].body["job_id"].(float64) != 42 {
		t.Fatalf("deploy event: %+v (hook %v)", received, c2)
	}
	if pl, _ := received[0].body["pipeline"].(map[string]any); pl["name"] != "shop" || received[0].body["tag"] != "v1.2.0" {
		t.Fatalf("deploy body: %s", received[0].raw)
	}
}

// Тело без HTML-экранирования: JSON.stringify(JSON.parse(тело)) в JS
// даёт те же байты — подпись сходится.
func TestOutPayloadNoHTMLEscape(t *testing.T) {
	raw := outPayload(OutHook{Lang: "en"}, OutEvent{Kind: "problems", Text: "<b> & co", TS: time.Unix(0, 0)}, "d1", "nkt test")
	if strings.Contains(string(raw), "\\u003c") || !strings.Contains(string(raw), "<b> & co") || strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("%s", raw)
	}
}
