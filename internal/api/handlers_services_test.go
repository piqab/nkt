package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

// servicesTestServer wires a Server with a real ServiceManager against the
// repo's fixtures/host — enough to exercise handleServiceLogs/
// handleKillProcess's own HTTP-layer plumbing (route param extraction,
// status codes, response shape), which internal/control's own tests for
// the underlying ServiceManager methods don't touch at all.
func servicesTestServer(t *testing.T) *Server {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Mode: config.ModeFixtures, FixturesRoot: root, CommandTimeout: 5 * time.Second}
	c := collect.NewFixtures(root)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Server{cfg: cfg, services: control.NewServiceManager(cfg, c, db), scanner: inventory.New(cfg, c, db)}
}

// withChiParams stands in for chi's own router dispatch — these handlers
// are tested directly (not through a mounted router), so URL params chi
// would normally have parsed out of the path have to be set by hand.
func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestHandleServiceLogs(t *testing.T) {
	s := servicesTestServer(t)

	t.Run("known service: 200 with output", func(t *testing.T) {
		req := withChiParams(httptest.NewRequest(http.MethodGet, "/api/services/nginx/logs", nil), map[string]string{"name": "nginx"})
		rec := httptest.NewRecorder()
		s.handleServiceLogs(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		var body struct {
			Output string `json:"output"`
		}
		decodeJSONBody(t, rec, &body)
		if !strings.Contains(body.Output, "nginx") {
			t.Errorf("output = %q, want it to mention nginx", body.Output)
		}
	})

	t.Run("unknown service: 400", func(t *testing.T) {
		req := withChiParams(httptest.NewRequest(http.MethodGet, "/api/services/not-a-service/logs", nil), map[string]string{"name": "not-a-service"})
		rec := httptest.NewRecorder()
		s.handleServiceLogs(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("lines query param is honored (no error at least)", func(t *testing.T) {
		req := withChiParams(httptest.NewRequest(http.MethodGet, "/api/services/nginx/logs?lines=10", nil), map[string]string{"name": "nginx"})
		rec := httptest.NewRecorder()
		s.handleServiceLogs(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleKillProcess(t *testing.T) {
	s := servicesTestServer(t)

	t.Run("matching command: 200", func(t *testing.T) {
		body := `{"pid":1400,"command":"python3 -m http.server 8082 --directory /srv/tmp-share","signal":"TERM"}`
		req := httptest.NewRequest(http.MethodPost, "/api/misc/kill", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleKillProcess(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("mismatched command (stale PID): 400", func(t *testing.T) {
		body := `{"pid":1400,"command":"something else entirely","signal":"TERM"}`
		req := httptest.NewRequest(http.MethodPost, "/api/misc/kill", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleKillProcess(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("invalid signal: 400", func(t *testing.T) {
		body := `{"pid":1400,"command":"x","signal":"HUP"}`
		req := httptest.NewRequest(http.MethodPost, "/api/misc/kill", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleKillProcess(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed body: 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/misc/kill", strings.NewReader("not json"))
		rec := httptest.NewRecorder()
		s.handleKillProcess(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
		}
	})
}
