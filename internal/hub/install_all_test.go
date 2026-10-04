package hub

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// «Установить на хосты»: машину хаба выбрать нельзя; задание запускает
// обычную установку на каждом хосте и пересказывает её итог — здесь
// хост недоступен, установка падает, задание это честно говорит.
func TestInstallAll(t *testing.T) {
	m, db := newTestManager(t)
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	m.SetJobs(jm)
	jm.Register(KindHostInstall, NewHostInstallRunner(m))
	jm.Register(KindInstallAll, NewInstallAllRunner(m))
	s := &Server{hub: m, db: db, jobs: jm}
	ctx := auth.WithUser(context.Background(), store.User{Username: "admin", Role: store.RoleAdmin})
	id, err := db.CreateHost(ctx, "nowhere", "127.0.0.1", 1, "root", store.HostAuthPassword, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	call := func(body string) (int, string) {
		w := httptest.NewRecorder()
		s.handleInstallAll(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)).WithContext(ctx))
		return w.Code, w.Body.String()
	}
	if code, _ := call(`{"host_ids":[-1]}`); code != http.StatusBadRequest {
		t.Fatalf("hub machine accepted: %d", code)
	}
	if code, _ := call(`{"host_ids":[]}`); code != http.StatusBadRequest {
		t.Fatalf("empty accepted: %d", code)
	}
	code, body := call(`{"host_ids":[` + itoa(int(id)) + `]}`)
	if code != http.StatusOK {
		t.Fatalf("start: %d %s", code, body)
	}
	var jobID int64
	for _, j := range mustJobs(t, db, KindInstallAll) {
		jobID = j.ID
	}
	deadline := time.Now().Add(2 * time.Minute)
	var j store.Job
	for time.Now().Before(deadline) {
		if j, _ = db.JobByID(ctx, jobID); j.Status == store.JobSucceeded || j.Status == store.JobFailed {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	log := jobLogText(t, ctx, db, jobID)
	if j.Status != store.JobFailed || !strings.Contains(log, "nowhere") {
		t.Fatalf("job %s:\n%s", j.Status, log)
	}
	if len(mustJobs(t, db, KindHostInstall)) != 1 {
		t.Fatal("host install job not started")
	}
}

func mustJobs(t *testing.T, db *store.DB, kind string) []store.Job {
	t.Helper()
	list, err := db.ListJobs(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Job
	for _, j := range list {
		if j.Kind == kind {
			out = append(out, j)
		}
	}
	return out
}
