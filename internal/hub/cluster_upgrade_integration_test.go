package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Обновление кластера из одного узла: хаб проверяет версию по control
// plane, задание запускает обновление узла на хосте (сценарий в fixtures
// имитируется), ждёт Ready и пишет итог.
func TestClusterUpgradeRoundTrip(t *testing.T) {
	db, key, _, clusterID, ctx := fixtureClusterHub(t, "lab-cp-1")
	m := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	s.jobs.Register(KindClusterUpgrade, NewClusterUpgradeRunner(m))

	call := func(body string) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(body)))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", strconv.FormatInt(clusterID, 10))
		req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		s.handleClusterUpgrade(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, _ := call(`{"version":"1.30"}`); code != 400 {
		t.Fatalf("понижение: %d", code)
	}
	code, out := call(`{"version":"1.32"}`)
	if code != 200 {
		t.Fatalf("старт: %d %v", code, out)
	}
	id := int64(out["job_id"].(float64))
	var job store.Job
	for {
		job, _ = db.JobByID(ctx, id)
		if job.Done() || ctx.Err() != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	log := jobLogText(t, ctx, db, id)
	if job.Status != store.JobSucceeded {
		t.Fatalf("статус %s: %s\n%s", job.Status, job.Error, log)
	}
	if !strings.Contains(log, "lab-cp-1") || strings.Contains(log, "drain") {
		t.Errorf("журнал:\n%s", log)
	}
}
