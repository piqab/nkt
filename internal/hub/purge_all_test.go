package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// «Удалить nkt везде»: без свежего полного экспорта — отказ; хост, где
// очистка прошла, убирается из хаба, недоступный — остаётся с ошибкой.
func TestPurgeAll(t *testing.T) {
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	t.Cleanup(s.jobs.Close)
	s.jobs.Register(KindPurgeAll, NewPurgeAllRunner(m))
	ctx := context.Background()
	gone, _ := m.AddHost(ctx, "gone", "192.0.2.10", 22, "root", store.HostAuthPassword, "pw", false)
	dead, _ := m.AddHost(ctx, "dead", "127.0.0.1", 1, "root", store.HostAuthPassword, "pw", false)

	post := func(body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		s.handlePurgeAll(w, httptest.NewRequest(http.MethodPost, "/hub/purge-all", bytes.NewReader(raw)))
		return w
	}
	fullExportMu.Lock()
	fullExportAt = time.Time{}
	fullExportMu.Unlock()
	if w := post(PurgeAllParams{HostIDs: []int64{gone}}); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("without export: %d %s", w.Code, w.Body)
	}
	m.noteFullExport()
	if w := post(PurgeAllParams{HostIDs: []int64{LocalHostID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("localhost only: %d", w.Code)
	}
	// gone — без шагов на самом хосте (очистка «прошла»), dead — со
	// службой (подключиться нельзя).
	w := post(PurgeAllParams{HostIDs: []int64{gone}})
	var res struct {
		JobID int64 `json:"job_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	waitJob(t, db, res.JobID)
	if _, err := db.HostByID(ctx, gone); err == nil {
		t.Fatal("gone not removed")
	}
	w = post(PurgeAllParams{HostIDs: []int64{dead}, Purge: PurgeOptions{Service: true}})
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	j := waitJob(t, db, res.JobID)
	if j.Status != store.JobFailed {
		t.Fatalf("job status %s", j.Status)
	}
	if _, err := db.HostByID(ctx, dead); err != nil {
		t.Fatal("unreachable host removed")
	}
}

func waitJob(t *testing.T, db *store.DB, id int64) store.Job {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		j, _ := db.JobByID(context.Background(), id)
		if j.Done() || time.Now().After(deadline) {
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
}
