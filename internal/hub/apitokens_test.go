package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// API-токены через полный роутер хаба: Bearer и подпись, роли, пределы по
// хостам и группам, адреса, срок, закрытые вызовы.
func TestAPITokens(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()

	webID, err := db.CreateHost(ctx, "web1", "203.0.113.10", 22, "root", store.HostAuthPassword, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.SetHostGroup(ctx, webID, "prod")

	do := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		if set != nil {
			set(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("POST", "/api/auth/login", `{"username":"admin","password":"admin-password-1234"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	session := func(r *http.Request) { r.AddCookie(cookie) }

	type created struct {
		ID     int64  `json:"id"`
		KeyID  string `json:"key_id"`
		Secret string `json:"secret"`
		Token  string `json:"token"`
	}
	create := func(body string) created {
		t.Helper()
		rec := do("POST", "/api/hub/tokens", body, session)
		if rec.Code != 200 {
			t.Fatalf("create %s: %d %s", body, rec.Code, rec.Body)
		}
		var c created
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return c
	}
	bearer := func(c created) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	}
	expect := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: want %d, got %d %s", what, code, rec.Code, rec.Body)
		}
	}

	// Проверка полей при создании.
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"root"}`, session), 400, "bad role")
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"read","groups":["nope"]}`, session), 400, "bad group")
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"read","ips":["not-ip"]}`, session), 400, "bad ip")

	read := create(`{"name":"reader","role":"read"}`)
	if !strings.HasPrefix(read.Token, "nkt_"+read.KeyID+"_") {
		t.Fatalf("token form: %q", read.Token)
	}
	expect(do("GET", "/api/hub/hosts", "", bearer(read)), 200, "read: hosts")
	expect(do("GET", "/api/hosts/local/overview", "", bearer(read)), 200, "read: local overview")
	expect(do("GET", "/api/auth/me", "", bearer(read)), 200, "read: me")
	expect(do("POST", "/api/hub/pipelines/dryrun", `{"pipeline_id":1}`, bearer(read)), 403, "read: dry run")
	expect(do("GET", "/api/hub/tokens", "", bearer(read)), 403, "read: tokens")
	expect(do("GET", "/api/hub/export", "", bearer(read)), 403, "read: export")
	expect(do("GET", "/api/hosts/local/files/download?path=/etc/passwd", "", bearer(read)), 403, "read: files")
	expect(do("GET", "/api/hosts/local/configs", "", bearer(read)), 403, "read: configs")
	expect(do("GET", "/api/hosts/local/jobs/1/ws", "", func(r *http.Request) {
		bearer(read)(r)
		r.Header.Set("Upgrade", "websocket")
	}), 403, "read: websocket")
	expect(do("GET", "/api/hub/hosts", "", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+read.Token+"x")
	}), 401, "wrong secret")

	// Подписанный запрос: проходит один раз.
	admin := create(`{"name":"automation","role":"admin"}`)
	sign := func(c created, method, uri, body string, ts time.Time, nonce string) func(*http.Request) {
		return func(r *http.Request) {
			sec := strconv.FormatInt(ts.Unix(), 10)
			r.Header.Set("X-NKT-API-Key", c.KeyID)
			r.Header.Set("X-NKT-API-Timestamp", sec)
			r.Header.Set("X-NKT-API-Nonce", nonce)
			r.Header.Set("X-NKT-API-Signature", TokenSignature(c.Secret, sec, nonce, method, uri, []byte(body)))
		}
	}
	now := time.Now()
	expect(do("GET", "/api/hub/pipelines", "", sign(admin, "GET", "/api/hub/pipelines", "", now, "nonce-0000000001")), 200, "signed")
	expect(do("GET", "/api/hub/pipelines", "", sign(admin, "GET", "/api/hub/pipelines", "", now, "nonce-0000000001")), 401, "replay")
	expect(do("GET", "/api/hub/pipelines", "", sign(admin, "GET", "/api/hub/pipelines", "", now.Add(-10*time.Minute), "nonce-0000000002")), 401, "stale")
	expect(do("GET", "/api/hub/pipelines", "", sign(admin, "GET", "/api/hub/hosts", "", now, "nonce-0000000003")), 401, "other path")
	body := `{"action":"ban","ips":["198.51.100.7"],"host_ids":[999]}`
	expect(do("POST", "/api/hub/fail2ban/fleet", body, sign(admin, "POST", "/api/hub/fail2ban/fleet", body+" ", now, "nonce-0000000004")), 401, "tampered body")

	// Пределы: только группа prod — машины хаба не видно.
	scoped := create(`{"name":"prod-only","role":"admin","groups":["prod"]}`)
	rec = do("GET", "/api/hub/hosts", "", bearer(scoped))
	expect(rec, 200, "scoped: hosts")
	var hosts []struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &hosts)
	if len(hosts) != 1 || hosts[0].ID != webID {
		t.Fatalf("scoped hosts: %s", rec.Body)
	}
	expect(do("GET", "/api/hosts/local/overview", "", bearer(scoped)), 403, "scoped: local")
	expect(do("POST", "/api/hub/pipelines/dryrun", `{"content":"action: compose"}`, bearer(scoped)), 403, "scoped: dry content")
	expect(do("POST", "/api/hub/fail2ban/fleet", `{"action":"ban","ips":["198.51.100.7"],"host_ids":[-1]}`, bearer(scoped)), 403, "scoped: fleet")

	// Переименование группы — токен остаётся при ней.
	if err := db.RenameHostGroup(ctx, "prod", "production"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := db.APITokenByID(ctx, scoped.ID); len(tok.Groups) != 1 || tok.Groups[0] != "production" {
		t.Fatalf("groups after rename: %v", tok.Groups)
	}

	// Адреса: заголовкам прокси верим только с loopback.
	ipOnly := create(`{"name":"from-office","role":"read","ips":["198.51.100.0/24"]}`)
	expect(do("GET", "/api/hub/hosts", "", bearer(ipOnly)), 403, "ip: direct")
	expect(do("GET", "/api/hub/hosts", "", func(r *http.Request) {
		bearer(ipOnly)(r)
		r.Header.Set("X-Forwarded-For", "198.51.100.5")
	}), 403, "ip: spoofed header")
	expect(do("GET", "/api/hub/hosts", "", func(r *http.Request) {
		bearer(ipOnly)(r)
		r.RemoteAddr = "127.0.0.1:5555"
		r.Header.Set("X-Forwarded-For", "198.51.100.5")
	}), 200, "ip: via local proxy")

	// Срок, новый секрет, отзыв.
	tok, _ := db.APITokenByID(ctx, read.ID)
	tok.ExpiresAt = store.FormatTime(time.Now().Add(-time.Hour))
	_ = db.UpdateAPIToken(ctx, tok)
	expect(do("GET", "/api/hub/hosts", "", bearer(read)), 401, "expired")
	rec = do("POST", "/api/hub/tokens/"+strconv.FormatInt(admin.ID, 10)+"/rotate", "", session)
	expect(rec, 200, "rotate")
	var rotated created
	_ = json.Unmarshal(rec.Body.Bytes(), &rotated)
	rotated.ID = admin.ID
	expect(do("GET", "/api/hub/hosts", "", bearer(admin)), 401, "old secret after rotate")
	expect(do("GET", "/api/hub/hosts", "", bearer(rotated)), 200, "new secret")
	expect(do("DELETE", "/api/hub/tokens/"+strconv.FormatInt(admin.ID, 10), "", session), 200, "delete")
	expect(do("GET", "/api/hub/hosts", "", bearer(rotated)), 401, "revoked")
	if tok, _ := db.APITokenByID(ctx, scoped.ID); tok.LastUsed == "" {
		t.Fatal("last use not recorded")
	}
}

func TestTokenClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.1:999"
	r = r.WithContext(context.WithValue(r.Context(), peerCtxKey{}, "203.0.113.1:999"))
	if got := tokenClientIP(r); got != "203.0.113.1" {
		t.Fatal(got)
	}
	if !ipAllowed([]string{"10.0.0.0/8"}, "10.1.2.3") || ipAllowed([]string{"10.0.0.0/8"}, "11.0.0.1") || !ipAllowed(nil, "1.2.3.4") {
		t.Fatal("ipAllowed")
	}
	if !ipAllowed([]string{"2001:db8::1"}, "2001:db8::1") {
		t.Fatal("ipAllowed v6")
	}
}
