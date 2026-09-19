package hub

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Установка хоста — задание хаба: StartInstall возвращает его номер,
// журнал пишется ключами (читается на любом языке), повторный запуск на
// тот же хост, пока идёт первый, возвращает тот же номер, а провал
// (хост не в сети) — статус failed и ошибка хоста.
func TestStartInstallIsHubJob(t *testing.T) {
	ctx := context.Background()
	m, db := newTestManager(t)
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	m.SetJobs(jm)
	jm.Register(KindHostInstall, NewHostInstallRunner(m))

	hostID, err := m.AddHost(ctx, "h1", "192.0.2.10", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.StartInstall(msgs.WithLang(ctx, msgs.RU), hostID, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := m.StartInstall(ctx, hostID, true, nil); err != nil || again != id {
		t.Errorf("повторный запуск = %d, %v; ждали тот же номер %d", again, err, id)
	}
	if latest, ok := m.LatestJobID(hostID); !ok || latest != id {
		t.Errorf("LatestJobID = %d, %v", latest, ok)
	}
	deadline := time.Now().Add(90 * time.Second)
	var job store.Job
	for {
		job, _ = db.JobByID(ctx, id)
		if job.Done() || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job.Status != store.JobFailed {
		t.Fatalf("статус %s (%s)", job.Status, job.Error)
	}
	if job.Kind != KindHostInstall || job.TitleKey != "hub.installJobTitle" {
		t.Errorf("задание = %+v", job)
	}
	lines, _ := db.JobLog(ctx, id, 0, 100)
	if len(lines) == 0 || lines[0].Key != "hub.startingInstall" {
		t.Errorf("журнал без ключей: %+v", lines)
	}
	host, _ := db.HostByID(ctx, hostID)
	if host.Status != store.HostStatusError {
		t.Errorf("статус хоста %q, ждали error; ошибка задания %q; журнал %+v", host.Status, job.Error, lines)
	}
}
