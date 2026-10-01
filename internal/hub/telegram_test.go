package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// fakeTelegram — Bot API для теста: очередь обновлений и отправленные
// сообщения.
type fakeTelegram struct {
	mu      sync.Mutex
	updates []map[string]any
	nextID  int64
	sent    []map[string]any
}

func (f *fakeTelegram) push(u map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	u["update_id"] = f.nextID
	f.updates = append(f.updates, u)
}

func (f *fakeTelegram) messages() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.sent...)
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	raw, _ := io.ReadAll(r.Body)
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	reply := func(result any) { _ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result}) }
	switch method {
	case "getMe":
		reply(map[string]any{"id": 1, "username": "nkt_test_bot"})
	case "getUpdates":
		f.mu.Lock()
		offset, _ := in["offset"].(float64)
		var out []map[string]any
		for _, u := range f.updates {
			if float64(u["update_id"].(int64)) >= offset {
				out = append(out, u)
			}
		}
		f.mu.Unlock()
		if len(out) == 0 {
			time.Sleep(50 * time.Millisecond)
		}
		reply(out)
	case "sendMessage":
		f.mu.Lock()
		f.sent = append(f.sent, in)
		f.mu.Unlock()
		reply(map[string]any{"message_id": 1})
	default:
		reply(true)
	}
}

func TestTelegramBot(t *testing.T) {
	fake := &fakeTelegram{}
	api := httptest.NewServer(fake)
	defer api.Close()
	old := tgAPIBase
	tgAPIBase = api.URL
	defer func() { tgAPIBase = old }()

	srv, _, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.StartTelegram(ctx)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"admin-password-1234"}`)))
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	put := func(body string) int {
		req := httptest.NewRequest("PUT", "/api/hub/telegram", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := put(`{"enabled":true,"token":"not-a-token"}`); code != 400 {
		t.Fatalf("bad token accepted: %d", code)
	}
	if code := put(`{"enabled":true,"token":"123456789:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","lang":"en",
		"chats":[{"id":100,"role":"admin","notify":true},{"id":200,"role":"read"}],"users":[7]}`); code != 200 {
		t.Fatalf("save: %d", code)
	}
	if st := srv.tgSettings(ctx); st.BotName != "nkt_test_bot" || len(st.Chats) != 2 {
		t.Fatalf("settings: %+v", st)
	}

	msg := func(chat, user int64, text string) map[string]any {
		return map[string]any{"message": map[string]any{"message_id": 1, "chat": map[string]any{"id": chat}, "from": map[string]any{"id": user, "username": "ops"}, "text": text}}
	}
	waitFor := func(chat int64, contains string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			for _, m := range fake.messages() {
				if int64(m["chat_id"].(float64)) == chat && strings.Contains(m["text"].(string), contains) {
					return m
				}
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("no message to %d with %q; sent: %v", chat, contains, fake.messages())
		return nil
	}

	fake.push(msg(999, 7, "/start"))
	waitFor(999, "Chat number: 999")
	fake.push(msg(999, 7, "/status"))
	fake.push(msg(100, 7, "/status"))
	waitFor(100, "Hosts:")
	fake.push(msg(200, 7, "/deploy x"))
	waitFor(200, "not available here")
	fake.push(msg(100, 8, "/ban 198.51.100.7"))
	waitFor(100, "not available here") // человек не из списка
	fake.push(msg(100, 7, "/ban 198.51.100.7"))
	confirm := waitFor(100, "Ban 198.51.100.7")
	rows := confirm["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	data := rows[0].([]any)[0].(map[string]any)["callback_data"].(string)
	// Чужая кнопка не срабатывает, своя — запускает.
	cb := func(user int64) map[string]any {
		return map[string]any{"callback_query": map[string]any{"id": "c1", "from": map[string]any{"id": user}, "data": data,
			"message": map[string]any{"message_id": 2, "chat": map[string]any{"id": 100}}}}
	}
	fake.push(cb(8))
	waitFor(100, "expired or is not yours")
	fake.push(cb(7))
	deadline := time.Now().Add(5 * time.Second)
	acted := false
	for time.Now().Before(deadline) && !acted {
		for _, m := range fake.messages() {
			text := m["text"].(string)
			if int64(m["chat_id"].(float64)) == 100 && (strings.Contains(text, "Job ") || strings.Contains(text, "fail2ban")) {
				acted = true
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !acted {
		t.Fatalf("ban not acted: %v", fake.messages())
	}

	// Оповещение — в чат с notify, с кнопками журнала и повтора.
	srv.hub.emitOut(OutEvent{Kind: OutDeployFailed, PipelineID: 5, PipelineName: "shop", JobID: 42, Key: "hub.outDeployFailed", Args: msgs.EncodeArgs([]any{"shop", "v1", "boom"})})
	n := waitFor(100, "deployment failed")
	if !strings.Contains(n["text"].(string), "shop") {
		t.Fatalf("notify text: %v", n["text"])
	}
	btns := n["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)
	if len(btns) != 2 || btns[0].(map[string]any)["callback_data"] != "jl:42" || btns[1].(map[string]any)["callback_data"] != "rd:5" {
		t.Fatalf("buttons: %v", btns)
	}
	for _, m := range fake.messages() {
		if int64(m["chat_id"].(float64)) == 200 && strings.Contains(m["text"].(string), "deployment failed") {
			t.Fatal("notification went to a chat without notify")
		}
		if int64(m["chat_id"].(float64)) == 999 && strings.Contains(m["text"].(string), "Hosts:") {
			t.Fatal("unknown chat got an answer")
		}
	}
	_ = store.JobQueued
}
