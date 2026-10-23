package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// Контейнеры и службы из результата инструмента: целый JSON и обрезанный
// (предел результата для модели) — имена верхнего уровня, без имён сетей.
func TestSeenObjects(t *testing.T) {
	whole := `{"containers":[{"id":"a1","name":"/shop-web-1","state":"running","ports":[{"name":"http"}]},{"id":"b2","name":"shop-db-1","state":"exited"}],"networks":[{"name":"bridge"}]}`
	got := seenObjects("host_containers", 3, "web-1", whole)
	if len(got) != 2 || got[0].Name != "shop-web-1" || got[0].Status != "running" || got[1].Name != "shop-db-1" || got[0].Host != "web-1" || got[0].HostID != 3 {
		t.Fatalf("whole: %+v", got)
	}
	cut := whole[:len(whole)-40]
	if got := seenObjects("host_containers", 3, "web-1", cut); len(got) != 2 || got[1].Name != "shop-db-1" {
		t.Fatalf("truncated: %+v", got)
	}
	svc := `{"services":[{"name":"nginx","unit":"nginx.service","active_state":"active"},{"name":"ssh","unit":"ssh.service","active_state":"active"}],"allow_mutations":true}`
	if got := seenObjects("host_services", 1, "db-1", svc); len(got) != 2 || got[0].Kind != "service" || got[0].Status != "active" {
		t.Fatalf("services: %+v", got)
	}
	if got := seenObjects("list_hosts", 1, "x", whole); got != nil {
		t.Fatalf("other tool: %+v", got)
	}
}

// Объекты для ссылок и подсказок; избранное — у каждого своё, проверки
// размеров и id; без включённого ассистента — 403; токену — нет.
func TestAssistantObjectsAndFavorites(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	_, _ = db.CreateHost(ctx, "web1", "203.0.113.10", 22, "root", store.HostAuthPassword, []byte("x"))
	hash, _ := auth.HashPassword("other-password-1234")
	if _, err := db.CreateUser(ctx, "ops", hash, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	login := func(user, pass string) *http.Cookie {
		rec := do("POST", "/api/auth/login", `{"username":"`+user+`","password":"`+pass+`"}`, nil)
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.SessionCookie {
				return c
			}
		}
		t.Fatalf("login %s: %d %s", user, rec.Code, rec.Body)
		return nil
	}
	admin, ops := login("admin", "admin-password-1234"), login("ops", "other-password-1234")
	if rec := do("GET", "/api/hub/assistant/objects", "", admin); rec.Code != http.StatusForbidden {
		t.Fatalf("assistant off: %d", rec.Code)
	}
	if rec := do("PUT", "/api/hub/experimental", `{"map":false,"clusters":false,"assistant":true,"mcp":false}`, admin); rec.Code != 200 {
		t.Fatalf("turn on: %d %s", rec.Code, rec.Body)
	}
	rec := do("GET", "/api/hub/assistant/objects", "", admin)
	var objs struct {
		Objects []assistantObject `json:"objects"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &objs)
	found := false
	for _, o := range objs.Objects {
		found = found || (o.Kind == "host" && o.Name == "web1" && o.ID > 0)
	}
	if rec.Code != 200 || !found {
		t.Fatalf("objects: %d %s", rec.Code, rec.Body)
	}

	fav := `{"sections":[{"id":"s1","title":"Общее","items":[{"id":"i1","text":"Проверь хост web1"}]}]}`
	if rec := do("PUT", "/api/hub/assistant/favorites", fav, admin); rec.Code != 200 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if rec := do("GET", "/api/hub/assistant/favorites", "", admin); !strings.Contains(rec.Body.String(), "Проверь хост web1") {
		t.Fatalf("get own: %s", rec.Body)
	}
	if rec := do("GET", "/api/hub/assistant/favorites", "", ops); strings.Contains(rec.Body.String(), "Проверь") || !strings.Contains(rec.Body.String(), `"sections":[]`) {
		t.Fatalf("other user sees favorites: %s", rec.Body)
	}
	for _, bad := range []string{
		`{"sections":[{"id":"../x","title":"a","items":[]}]}`,
		`{"sections":[{"id":"s1","title":"","items":[]}]}`,
		`{"sections":[{"id":"s1","title":"a","items":[{"id":"s1","text":"dup id"}]}]}`,
		`{"sections":[{"id":"s1","title":"a","items":[{"id":"i1","text":""}]}]}`,
		`{"sections":[{"id":"s1","title":"a","items":[{"id":"i1","text":"` + strings.Repeat("x", 5000) + `"}]}]}`,
	} {
		if rec := do("PUT", "/api/hub/assistant/favorites", bad, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("accepted %.60s: %d", bad, rec.Code)
		}
	}
	// Токену избранное и объекты недоступны: ассистент — только в интерфейсе.
	tok := do("POST", "/api/hub/tokens", `{"name":"t","role":"admin"}`, admin)
	var c struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(tok.Body.Bytes(), &c)
	req := httptest.NewRequest("GET", "/api/hub/assistant/favorites", nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("token: %d", rec2.Code)
	}
}
