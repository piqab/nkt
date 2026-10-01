package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
)

// Эталон из документации Slack («Verifying requests from Slack»).
func TestSlackVerifyVector(t *testing.T) {
	body := "token=xyzz0WbapA4vBCDEFasx0q6G&team_id=T1DC2JH3J&team_domain=testteamnow&channel_id=G8PSS9T3V&channel_name=foobar&user_id=U2CERLKJA&user_name=roadrunner&command=%2Fwebhook-collect&text=&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2FT1DC2JH3J%2F397700885554%2F96rGlfmibIGlgcZRskXaIFfN&trigger_id=398738663015.47445629121.803a0bc887a14d10d2c447fce8b6703c"
	sig := "v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503"
	now := time.Unix(1531420618, 0)
	if !slackVerify("8f742231b10e8888abcd99yyyzzz85a5", "1531420618", sig, []byte(body), now) {
		t.Fatal("documented vector rejected")
	}
	if slackVerify("8f742231b10e8888abcd99yyyzzz85a5", "1531420618", sig, []byte(body), now.Add(10*time.Minute)) {
		t.Fatal("stale request accepted")
	}
	if slackVerify("8f742231b10e8888abcd99yyyzzz85a5", "1531420618", sig, []byte(body+"x"), now) {
		t.Fatal("tampered body accepted")
	}
}

func TestSlackBot(t *testing.T) {
	var mu sync.Mutex
	var posted []map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var in map[string]any
		_ = json.Unmarshal(raw, &in)
		switch r.URL.Path {
		case "/auth.test":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "team": "acme", "user": "nkt"})
		case "/chat.postMessage":
			mu.Lock()
			posted = append(posted, in)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "unknown_method"})
		}
	}))
	defer api.Close()
	old := slackAPIBase
	slackAPIBase = api.URL
	defer func() { slackAPIBase = old }()

	srv, _, _ := localFixtureHub(t)
	h := srv.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"admin-password-1234"}`)))
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	const signing = "0123456789abcdef0123456789abcdef"
	req := httptest.NewRequest("PUT", "/api/hub/slack", strings.NewReader(`{"enabled":true,"token":"xoxb-1111-2222-aaaaaaaaaaaaaaaa","signing_secret":"`+signing+`","lang":"en",
		"channels":[{"id":"C0ADMIN01","role":"admin","notify":true},{"id":"C0READ001","role":"read"}],"users":["U0OPS0001"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}

	sign := func(r *http.Request, body string, secret string) {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte("v0:" + ts + ":" + body))
		r.Header.Set("X-Slack-Request-Timestamp", ts)
		r.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	call := func(handler http.Handler, path string, form url.Values, secret string) *httptest.ResponseRecorder {
		body := form.Encode()
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		sign(req, body, secret)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	cmd := func(channel, user, text string) url.Values {
		return url.Values{"channel_id": {channel}, "user_id": {user}, "user_name": {"ops"}, "command": {"/nkt"}, "text": {text}}
	}
	const base = "/api/hub/callbacks/slack/commands"
	if rec := call(h, base, cmd("C0ADMIN01", "U0OPS0001", "status"), "ffffffffffffffffffffffffffffffff"); rec.Code != 401 {
		t.Fatalf("bad signature: %d", rec.Code)
	}
	rec = call(h, base, cmd("C0ADMIN01", "U0OPS0001", "status"), signing)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Hosts:") {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, base, cmd("C0OTHER01", "U0OPS0001", "status"), signing); !strings.Contains(rec.Body.String(), "C0OTHER01") || strings.Contains(rec.Body.String(), "Hosts:") {
		t.Fatalf("unknown channel: %s", rec.Body)
	}
	if rec := call(h, base, cmd("C0READ001", "U0OPS0001", "ban 198.51.100.7"), signing); !strings.Contains(rec.Body.String(), "not available here") {
		t.Fatalf("read channel acted: %s", rec.Body)
	}
	rec = call(h, base, cmd("C0ADMIN01", "U0OPS0001", "ban 198.51.100.7"), signing)
	var resp struct {
		Blocks []struct {
			Elements []struct {
				Value string `json:"value"`
			} `json:"elements"`
		} `json:"blocks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Blocks) != 2 || len(resp.Blocks[1].Elements) != 2 {
		t.Fatalf("confirm buttons: %s", rec.Body)
	}
	confirm := resp.Blocks[1].Elements[0].Value
	press := func(handler http.Handler, path, user string) {
		payload, _ := json.Marshal(map[string]any{"type": "block_actions", "user": map[string]any{"id": user, "username": "ops"},
			"channel": map[string]any{"id": "C0ADMIN01"}, "actions": []map[string]any{{"value": confirm}}})
		if rec := call(handler, path, url.Values{"payload": {string(payload)}}, signing); rec.Code != 200 {
			t.Fatalf("press: %d", rec.Code)
		}
	}
	// Через edge с ролью «колбэки» — тот же обработчик; без роли — нет.
	edgeCB := srv.edgeHandler(EdgeSettings{Roles: []string{EdgeRoleCallbacks}})
	if rec := call(srv.edgeHandler(EdgeSettings{}), "/callbacks/slack/interactive", url.Values{}, signing); rec.Code != 404 && rec.Code != 405 {
		t.Fatalf("hooks-only edge served callbacks: %d", rec.Code)
	}
	press(edgeCB, "/callbacks/slack/interactive", "U0STRANGER") // чужой — подтверждение не его
	press(edgeCB, "/callbacks/slack/interactive", "U0OPS0001")
	deadline := time.Now().Add(5 * time.Second)
	var texts []string
	for time.Now().Before(deadline) {
		mu.Lock()
		texts = texts[:0]
		for _, p := range posted {
			texts = append(texts, p["text"].(string))
		}
		mu.Unlock()
		joined := strings.Join(texts, "\n")
		if strings.Contains(joined, "expired or is not yours") && (strings.Contains(joined, "Job ") || strings.Contains(joined, "fail2ban")) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "expired or is not yours") || !(strings.Contains(joined, "Job ") || strings.Contains(joined, "fail2ban")) {
		t.Fatalf("posted: %v", texts)
	}

	// Оповещение — в канал с notify.
	srv.slackNotify(context.Background(), OutEvent{Kind: OutDeploySucceeded, PipelineID: 3, PipelineName: "shop", JobID: 9, Text: "ok"})
	mu.Lock()
	last := posted[len(posted)-1]
	mu.Unlock()
	if last["channel"] != "C0ADMIN01" || !strings.Contains(last["text"].(string), "deployment succeeded") {
		t.Fatalf("notify: %v", last)
	}
}
