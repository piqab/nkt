package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/inventory"
)

// TestHandleFirewallReportsManagers is the regression test for a bug shipped
// in 9d5c757: model.FirewallState.UFWInstalled was computed correctly by
// the parser but never serialised into /api/firewall's JSON, so the
// Firewall page always read it as the zero value (false) — showing "ufw не
// установлен" and the install button on every host, including ones where
// ufw is installed and active. Now generalised to the "managers" list
// (ufw + firewalld) that replaced the ufw-only fields.
func TestHandleFirewallReportsManagers(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Mode: config.ModeFixtures}
	scanner := inventory.New(cfg, collect.NewFixtures(root), nil)
	s := &Server{cfg: cfg, scanner: scanner}

	req := httptest.NewRequest(http.MethodGet, "/api/firewall", nil)
	rec := httptest.NewRecorder()
	s.handleFirewall(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Managers []struct {
			Name      string `json:"name"`
			Installed bool   `json:"installed"`
			Active    bool   `json:"active"`
		} `json:"managers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	found := false
	for _, m := range body.Managers {
		if m.Name == "ufw" {
			found = true
			if !m.Installed {
				t.Errorf(`ufw.installed = false, want true (the fixtures host stubs "command -v ufw")`)
			}
		}
	}
	if !found {
		t.Errorf("managers = %+v, want a \"ufw\" entry", body.Managers)
	}
}
