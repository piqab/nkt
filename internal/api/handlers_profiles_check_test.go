package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/profile"
)

// «Проверить» в окне правки профиля: разбор тем же кодом, что при
// сохранении; ошибка — со строкой YAML, успех — что профиль задаёт.
func TestProfileCheck(t *testing.T) {
	s := &Server{}
	check := func(content string) profileCheckResult {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"content": content})
		req := httptest.NewRequest(http.MethodPost, "/api/profiles/check", strings.NewReader(string(body)))
		req = req.WithContext(msgs.WithLang(req.Context(), msgs.RU))
		rec := httptest.NewRecorder()
		s.handleProfileCheck(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("check: %d %s", rec.Code, rec.Body)
		}
		var res profileCheckResult
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		return res
	}
	for _, e := range profile.Examples {
		if res := check(e.Content); !res.OK || res.Name == "" || len(res.Counts) == 0 {
			t.Errorf("example %s: %+v", e.Key, res)
		}
	}
	res := check("version: 1\nname: web\npackages:\n  - nginx\nservice:\n  nginx: {}\n")
	if res.OK || res.Line != 5 || !strings.Contains(res.Error, "service") {
		t.Errorf("unknown field: %+v", res)
	}
	if res := check("version: 1\npackages:\n  - nginx\n"); res.OK || res.Error == "" {
		t.Errorf("no name: %+v", res)
	}
	if res := check("version: 1\nname: web\npackages:\n  - \"nginx; rm -rf /\"\n"); res.OK {
		t.Errorf("bad package: %+v", res)
	}
}

// Встроенная справка формата — на языке запроса, с готовыми профилями.
func TestProfileHelp(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/profiles/help", nil)
	req = req.WithContext(msgs.WithLang(req.Context(), msgs.EN))
	rec := httptest.NewRecorder()
	s.handleProfileHelp(rec, req)
	var h profile.Help
	_ = json.Unmarshal(rec.Body.Bytes(), &h)
	if len(h.Sections) != len(profile.Sections) || len(h.Examples) != len(profile.Examples) || h.Sections[0].Title == "" || strings.ContainsAny(h.Intro[0], "абвгдеж") {
		t.Errorf("help: %+v", h)
	}
}
