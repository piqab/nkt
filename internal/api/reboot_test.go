package api

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

func TestRebootPreviewAndConfirm(t *testing.T) {
	s, _, _ := f2bServer(t)
	ctx := auth.WithUser(context.Background(), store.User{Username: "admin", Role: store.RoleAdmin})
	if _, err := s.scanner.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleRebootPreview(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	var out struct {
		Running     map[string]int `json:"running"`
		NoAutostart []struct {
			Kind, Name, Reason string
		} `json:"no_autostart"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Running["service"] == 0 || out.Running["docker"] == 0 {
		t.Fatalf("running: %+v", out.Running)
	}
	for _, n := range out.NoAutostart {
		if n.Kind == "docker" && (n.Reason == "restart: always" || n.Reason == "restart: unless-stopped") {
			t.Fatalf("autostarting container listed: %+v", n)
		}
	}
	call := func(body string) (int, string) {
		w := httptest.NewRecorder()
		s.handleReboot(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)).WithContext(ctx))
		return w.Code, w.Body.String()
	}
	if code, _ := call(`{}`); code != http.StatusBadRequest {
		t.Fatalf("unconfirmed: %d", code)
	}
	if code, body := call(`{"confirm":true}`); code != http.StatusOK || !strings.Contains(body, `"simulated":true`) {
		t.Fatalf("confirmed: %d %s", code, body)
	}
}
