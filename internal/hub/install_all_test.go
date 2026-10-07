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
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// «Установить на хосты»: машину хаба выбрать нельзя; задание запускает
// обычную установку на каждом хосте и пересказывает её итог — здесь
// хост недоступен, установка падает, задание это честно говорит.
func TestInstallAll(t *testing.T) {
	// Подключение «есть» — дальше падает сама установка.
	oldProbe := installAllProbe
	installAllProbe = func(*Manager, context.Context, store.Host) error { return nil }
	t.Cleanup(func() { installAllProbe = oldProbe })
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

// Нет SSH-подключения — хост пропускается: установка не запускается,
// задание не падает, в журнале — «нет подключения» и итог.
func TestInstallAllSkipsUnreachable(t *testing.T) {
	oldA, oldT, oldG := installAllProbeAttempts, installAllProbeTimeout, installAllProbeGap
	installAllProbeAttempts, installAllProbeTimeout, installAllProbeGap = 2, 2*time.Second, 10*time.Millisecond
	t.Cleanup(func() { installAllProbeAttempts, installAllProbeTimeout, installAllProbeGap = oldA, oldT, oldG })
	m, db := newTestManager(t)
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	m.SetJobs(jm)
	jm.Register(KindHostInstall, NewHostInstallRunner(m))
	jm.Register(KindInstallAll, NewInstallAllRunner(m))
	s := &Server{hub: m, db: db, jobs: jm}
	ctx := auth.WithUser(context.Background(), store.User{Username: "admin", Role: store.RoleAdmin})
	secret, _ := secretbox.Encrypt(m.key, []byte("x"))
	id, err := db.CreateHost(ctx, "gone", "127.0.0.1", 1, "root", store.HostAuthPassword, secret)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleInstallAll(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"host_ids":[`+itoa(int(id))+`]}`)).WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	jobID := mustJobs(t, db, KindInstallAll)[0].ID
	var j store.Job
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if j, _ = db.JobByID(ctx, jobID); j.Status == store.JobSucceeded || j.Status == store.JobFailed {
			break
		}
	}
	log := jobLogText(t, ctx, db, jobID)
	if j.Status != store.JobSucceeded || !strings.Contains(log, "gone") {
		t.Fatalf("job %s:\n%s", j.Status, log)
	}
	if n := len(mustJobs(t, db, KindHostInstall)); n != 0 {
		t.Fatalf("установка запущена без подключения: %d", n)
	}
}
