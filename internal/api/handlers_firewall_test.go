package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
)

// TestHandleFirewallReportsUFWInstalled is the regression test for a bug
// shipped in 9d5c757: model.FirewallState.UFWInstalled was computed
// correctly by the parser but never serialised into /api/firewall's JSON,
// so the Firewall page always read it as the zero value (false) — showing
// "ufw не установлен" and the install button on every host, including ones
// where ufw is installed and active.
func TestHandleFirewallReportsUFWInstalled(t *testing.T) {
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
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if got, ok := body["ufw_installed"].(bool); !ok || !got {
		t.Errorf(`ufw_installed = %v, want true (the fixtures host stubs "command -v ufw")`, body["ufw_installed"])
	}
}
