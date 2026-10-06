package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// brokenBody отдаёт часть файла и обрывается — как закрытая вкладка.
type brokenBody struct{ left int }

func (b *brokenBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errors.New("connection reset")
	}
	n := min(len(p), b.left)
	b.left -= n
	return n, nil
}

// Загрузка с компьютера: задание есть с самого начала, ждёт файл, по
// токену принимает его один раз; оборванная передача — ошибка задания, а
// не тишина, и недописанный файл не остаётся.
func TestUploadAsJob(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	dir := t.TempDir()
	s := &Server{db: db, jobs: jm, cfg: &config.Config{}, images: control.NewImageManager(nil, nil, dir)}
	jm.Register(KindUpload, &uploadRunner{s})

	begin := func(name string, size int) (int64, string) {
		b, _ := json.Marshal(map[string]any{"target": "archive", "name": name, "size": size})
		rec := httptest.NewRecorder()
		s.handleUploadBegin(rec, httptest.NewRequest("POST", "/uploads/begin", bytes.NewReader(b)))
		var out struct {
			JobID int64  `json:"job_id"`
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 200 || out.JobID == 0 || len(out.Token) != 48 {
			t.Fatalf("начало %s: %d %s", name, rec.Code, rec.Body.String())
		}
		return out.JobID, out.Token
	}
	put := func(token string, body io.Reader) int {
		rec := httptest.NewRecorder()
		s.handleImageArchiveUpload(rec, httptest.NewRequest("PUT", "/images/archives/upload?upload="+token, body))
		return rec.Code
	}
	wait := func(id int64) store.Job {
		for i := 0; i < 200; i++ {
			j, err := db.JobByID(t.Context(), id)
			if err == nil && (j.Status == store.JobSucceeded || j.Status == store.JobFailed) {
				return j
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("задание %d не закончилось", id)
		return store.Job{}
	}

	data := bytes.Repeat([]byte("layer "), 100_000)
	id, token := begin("docker__app.tar", len(data))
	if j, _ := db.JobByID(t.Context(), id); j.Status != store.JobRunning && j.Status != store.JobQueued {
		t.Fatalf("задание до передачи: %s", j.Status)
	}
	if code := put(token, bytes.NewReader(data)); code != 200 {
		t.Fatalf("передача: %d", code)
	}
	if j := wait(id); j.Status != store.JobSucceeded {
		t.Fatalf("задание: %s %s", j.Status, j.Error)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "docker__app.tar")); !bytes.Equal(got, data) {
		t.Errorf("архив: %d байт", len(got))
	}
	if code := put(token, bytes.NewReader(data)); code != 404 {
		t.Errorf("повтор токена: %d", code)
	}
	if code := put(strings.Repeat("0", 48), bytes.NewReader(data)); code != 404 {
		t.Errorf("чужой токен: %d", code)
	}

	id, token = begin("docker__half.tar", len(data))
	put(token, &brokenBody{left: len(data) / 2})
	j := wait(id)
	if j.Status != store.JobFailed || !strings.Contains(j.Error, "50%") {
		t.Errorf("оборванная передача: %s %q", j.Status, j.Error)
	}
	for _, f := range []string{"docker__half.tar", "docker__half.tar.part"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("остался %s", f)
		}
	}

	for _, bad := range []map[string]any{
		{"target": "archive", "name": "../x.tar", "size": 1},
		{"target": "archive", "name": "docker__x.exe", "size": 1},
		{"target": "archive", "name": "docker__x.tar", "size": int64(20 << 30)},
		{"target": "nowhere", "name": "x.tar", "size": 1},
	} {
		b, _ := json.Marshal(bad)
		rec := httptest.NewRecorder()
		s.handleUploadBegin(rec, httptest.NewRequest("POST", "/uploads/begin", bytes.NewReader(b)))
		if rec.Code != 400 {
			t.Errorf("%v: %d", bad, rec.Code)
		}
	}
}
