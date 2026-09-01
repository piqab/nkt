package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

// TestHandleConfigListAttachesSiteNames exercises handleConfigList's own
// cross-reference step against real fixture data (fixtures/host/etc/nginx),
// not just model.AttachSiteNames in isolation — ConfigManager.List (walks
// disk directly) and the inventory scanner (parses server_name into
// Endpoint.Names) are two independent code paths that happen to agree on
// file paths; this is what proves the handler actually joins them, not just
// that the join function itself works given already-matching inputs.
func TestHandleConfigListAttachesSiteNames(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Mode:            config.ModeFixtures,
		FixturesRoot:    root,
		DataDir:         t.TempDir(),
		NginxRoot:       "/etc/nginx",
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyRoot:     "/etc/haproxy",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		CommandTimeout:  5 * time.Second,
	}
	c := collect.NewFixtures(root)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	configs := control.NewConfigManager(cfg, c, db, scanner, control.NewServiceManager(cfg, c, db))
	s := &Server{cfg: cfg, scanner: scanner, configs: configs}

	rec := httptest.NewRecorder()
	s.handleConfigList(rec, httptest.NewRequest(http.MethodGet, "/api/configs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Files []model.ManagedFile `json:"files"`
	}
	decodeJSONBody(t, rec, &body)

	const wantPath = "/etc/nginx/sites-enabled/app.example.com.conf"
	var got *model.ManagedFile
	for i := range body.Files {
		if body.Files[i].Path == wantPath {
			got = &body.Files[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("%s not found in files list: %+v", wantPath, body.Files)
	}

	// app.example.com.conf declares "app.example.com api.example.com" on
	// one server block and "app.example.com" again on another (see the
	// fixture file itself) — proves both the join (Sites present at all)
	// and the dedup (app.example.com listed once, not twice).
	var names []string
	for _, site := range got.Sites {
		names = append(names, site.Name)
	}
	want := []string{"app.example.com", "api.example.com"}
	if len(names) != len(want) {
		t.Fatalf("Sites = %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("Sites[%d] = %q, want %q (full: %v)", i, names[i], w, names)
		}
	}
}
