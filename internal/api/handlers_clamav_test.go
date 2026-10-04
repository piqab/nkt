package api

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Отмена задания ClamAV останавливает всё дерево процессов, а не только
// sh: раньше отмена снимала ожидание, а clamscan доживал до конца.
func TestClamStreamCancelKillsChildren(t *testing.T) {
	if os.Getenv("INVOCATION_ID") != "" {
		t.Skip("под systemd путь другой (свой юнит)")
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := jobs.New(db, slog.New(slog.DiscardHandler))
	defer m.Close()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := &Server{}
	m.Register("clam-test", i18nRunner{fn: func(ctx context.Context, jc *jobs.Context) error {
		// Дочерний процесс — как clamscan под sh -c.
		return s.clamStream(ctx, jc, "sleep 60 & echo $! > "+pidFile+"; echo started; wait", nil)
	}})
	ctx := context.Background()
	id, err := m.Start(ctx, jobs.Spec{Kind: "clam-test", Title: "t", Queue: clamQueue})
	if err != nil {
		t.Fatal(err)
	}
	var pid string
	for i := 0; i < 200 && pid == ""; i++ {
		raw, _ := os.ReadFile(pidFile)
		pid = strings.TrimSpace(string(raw))
		time.Sleep(20 * time.Millisecond)
	}
	if pid == "" {
		t.Fatal("дочерний процесс не запустился")
	}
	if err := m.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var job store.Job
	for time.Now().Before(deadline) {
		job, _ = db.JobByID(ctx, id)
		if job.Done() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != store.JobCanceled {
		t.Fatalf("статус %s", job.Status)
	}
	// Процесс-зомби до wait родителя ещё виден в /proc — смотрим состояние.
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile("/proc/" + pid + "/stat")
		if err != nil || strings.Contains(string(raw), ") Z ") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("дочерний процесс %s пережил отмену", pid)
}
