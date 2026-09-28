package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

type blockRunner struct{ release chan struct{} }

func (b blockRunner) Run(ctx context.Context, _ *jobs.Context) error {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil
}

// Объект, который уже удаляется, не заводит второго задания: ответ — то же
// задание; GET /deletions видит его, чтобы интерфейс заблокировал строку.
func TestDeleteDedup(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	release := make(chan struct{})
	defer close(release)
	jm.Register(KindDelete, blockRunner{release})
	s := &Server{db: db, jobs: jm}
	post := func(items ...DeleteItem) map[string]any {
		b, _ := json.Marshal(DeleteParams{Items: items})
		rec := httptest.NewRecorder()
		s.handleDelete(rec, httptest.NewRequest("POST", "/deletions", bytes.NewReader(b)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		out["code"] = float64(rec.Code)
		return out
	}
	vm := DeleteItem{Kind: delVM, Name: "db1", RemoveStorage: true}
	first := post(vm, DeleteItem{Kind: delLXD, Name: "web"})
	if first["code"].(float64) != 200 || first["job_id"] == nil {
		t.Fatalf("первое: %v", first)
	}
	again := post(vm)
	if again["job_id"] != first["job_id"] || again["already"] != true {
		t.Errorf("повтор: %v (первое %v)", again, first)
	}
	if bad := post(DeleteItem{Kind: "rm -rf", Name: "/"}); bad["code"].(float64) != 400 {
		t.Errorf("чужой вид: %v", bad)
	}
	rec := httptest.NewRecorder()
	s.handleDeletions(rec, httptest.NewRequest("GET", "/deletions", nil))
	var active struct {
		Active map[string]int64 `json:"active"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &active)
	if len(active.Active) != 2 || active.Active["vm:db1"] == 0 || active.Active["lxd:web"] == 0 {
		t.Errorf("идут: %v", active.Active)
	}
}
