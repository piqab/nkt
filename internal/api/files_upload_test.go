package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/files"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Загрузка под номером: защищённый файл без force — отказ; прежние версии
// перезаписанных — в истории; откат заданием возвращает их и убирает
// добавленное.
func TestFilesUploadHistoryAndRollback(t *testing.T) {
	root := t.TempDir()
	run := func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		switch argv[0] {
		case "install":
			data, _ := os.ReadFile(argv[len(argv)-2])
			_ = os.WriteFile(argv[len(argv)-1], data, 0o644)
		case "mkdir":
			_ = os.MkdirAll(argv[len(argv)-1], 0o755)
		case "rm":
			_ = os.Remove(argv[len(argv)-1])
		}
		return collect.CommandResult{}, nil
	}
	local := collect.NewLocal("", "", 0)
	fm := files.NewManager([]string{root}, local, run, nil, filepath.Join(t.TempDir(), "tmp"))
	cfg := &config.Config{Mode: config.ModeLocal, DataDir: t.TempDir(), NginxRoot: "/etc/nginx", HAProxyRoot: "/etc/haproxy", CommandTimeout: 5 * time.Second}
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, local, db)
	cm := control.NewConfigManager(cfg, local, db, scanner, control.NewServiceManager(cfg, local, db))
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	jm.Register(KindUploadRollback, &UploadRollbackRunner{DB: db, Configs: cm, Files: fm})
	s := &Server{cfg: cfg, db: db, files: fm, configs: cm, jobs: jm}
	r := chi.NewRouter()
	r.Put("/files/upload", s.handleFilesUpload)
	r.Post("/files/upload/begin", s.handleFilesUploadBegin)
	r.Post("/files/upload/{id}/finish", s.handleFilesUploadFinish)
	r.Get("/files/uploads/{id}", s.handleFilesUploadItems)
	r.Post("/files/uploads/{id}/rollback", s.handleFilesUploadRollback)
	call := func(method, u string, body []byte) (int, map[string]any) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, u, bytes.NewReader(body)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	site := filepath.Join(root, "site")
	_ = os.MkdirAll(site, 0o755)
	_ = os.WriteFile(filepath.Join(site, "index.html"), []byte("v1"), 0o644)
	_ = os.WriteFile(filepath.Join(site, ".env"), []byte("S=1"), 0o644)

	b, _ := json.Marshal(map[string]string{"dir": site, "note": "релиз"})
	code, out := call("POST", "/files/upload/begin", b)
	if code != 200 {
		t.Fatalf("begin: %d %v", code, out)
	}
	id := strconv.Itoa(int(out["id"].(float64)))
	put := func(name, body string, force bool) int {
		q := url.Values{"dir": {site}, "name": {name}, "upload": {id}}
		if force {
			q.Set("force", "1")
		}
		c, _ := call("PUT", "/files/upload?"+q.Encode(), []byte(body))
		return c
	}
	if c := put("index.html", "v2", false); c != 200 {
		t.Fatalf("index: %d", c)
	}
	if c := put(".env", "S=2", false); c == 200 {
		t.Error(".env перезаписан без force")
	}
	if c := put("new/page.html", "p", false); c != 200 {
		t.Fatalf("new: %d", c)
	}
	if c, _ := call("POST", "/files/upload/"+id+"/finish", nil); c != 200 {
		t.Fatal("finish")
	}
	_, out = call("GET", "/files/uploads/"+id, nil)
	if items, _ := out["items"].([]any); len(items) != 2 {
		t.Fatalf("items: %v", out)
	}
	if got, _ := os.ReadFile(filepath.Join(site, ".env")); string(got) != "S=1" {
		t.Errorf(".env: %q", got)
	}

	code, out = call("POST", "/files/uploads/"+id+"/rollback", nil)
	if code != 200 {
		t.Fatalf("rollback: %d %v", code, out)
	}
	jobID := int64(out["job_id"].(float64))
	deadline := time.Now().Add(10 * time.Second)
	var job store.Job
	for {
		job, _ = db.JobByID(context.Background(), jobID)
		if job.Done() || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != store.JobSucceeded {
		lines, _ := db.JobLog(context.Background(), jobID, 0, 50)
		t.Fatalf("задание: %s %s %+v", job.Status, job.Error, lines)
	}
	if got, _ := os.ReadFile(filepath.Join(site, "index.html")); string(got) != "v1" {
		t.Errorf("index после отката: %q", got)
	}
	if _, err := os.Stat(filepath.Join(site, "new", "page.html")); !os.IsNotExist(err) {
		t.Error("добавленный файл не убран")
	}
}
