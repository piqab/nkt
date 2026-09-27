package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	h := SameOrigin([]string{"http://localhost:5173"}, func(p string) bool { return strings.HasPrefix(p, "/api/hooks/") })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	cases := []struct {
		method, path, origin, sfs string
		want                      int
	}{
		{"POST", "/api/x", "", "", 200},                                 // не браузер
		{"POST", "/api/x", "http://hub:8077", "same-origin", 200},       // свой интерфейс
		{"POST", "/api/x", "http://hub:8446", "same-site", 403},         // проброс на соседнем порту
		{"POST", "/api/x", "http://hub:8446", "", 403},                  // старый браузер: Origin ≠ Host
		{"POST", "/api/x", "http://hub:8077", "", 200},                  // старый браузер, свой
		{"DELETE", "/api/x", "https://evil.example", "cross-site", 403}, // чужой сайт
		{"GET", "/api/x", "http://hub:8446", "same-site", 200},          // чтение — CORS и так не даст ответ
		{"POST", "/api/hooks/a", "", "cross-site", 200},                 // вебхук
		{"POST", "/api/x", "http://localhost:5173", "same-site", 200},   // разрешённый источник
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "http://hub:8077"+c.path, nil)
		req.Host = "hub:8077"
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if c.sfs != "" {
			req.Header.Set("Sec-Fetch-Site", c.sfs)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%+v → %d", c, rec.Code)
		}
	}
}
