package api

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Журнал задания переводится при показе: задание, запущенное из
// русского интерфейса, английский читатель видит по-английски —
// заголовок, шаг, строки и ошибку. Строки без ключа (сырой вывод
// инструментов) остаются как записаны.
func TestJobLogLocalizedForReader(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := jobs.New(db, slog.New(slog.DiscardHandler))
	defer m.Close()
	m.Register("demo", i18nRunner{fn: func(_ context.Context, jc *jobs.Context) error {
		jc.StepKey(1, 1, "hub.clusterImageOnHost", "disk.qcow2", "hv1")
		jc.Log("hub.clusterImageOnHost", "disk.qcow2", "hv1")
		jc.Logf("raw: E: apt failed")
		return msgs.Errorf("hub.clusterImageMissing", "disk.qcow2")
	}})
	ctx := msgs.WithLang(context.Background(), msgs.RU)
	id, err := m.Start(ctx, jobs.Spec{Kind: "demo", TitleKey: "hub.clusterImageOnHost", TitleArgs: []any{"disk.qcow2", "hv1"}, Queue: "q", Steps: 1})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var job store.Job
	for {
		job, _ = db.JobByID(ctx, id)
		if job.Done() || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != store.JobFailed {
		t.Fatalf("статус %s", job.Status)
	}
	// Записано по-русски (язык автора).
	if job.Title != msgs.T(msgs.RU, "hub.clusterImageOnHost", "disk.qcow2", "hv1") {
		t.Errorf("заголовок записан не по-русски: %q", job.Title)
	}
	lines, _ := db.JobLog(ctx, id, 0, 100)
	if len(lines) != 3 {
		t.Fatalf("строк %d: %+v", len(lines), lines)
	}

	en := job
	localizeJob(msgs.EN, &en)
	if want := msgs.T(msgs.EN, "hub.clusterImageOnHost", "disk.qcow2", "hv1"); en.Title != want || en.StepName != want {
		t.Errorf("заголовок/шаг по-английски = %q / %q, ждали %q", en.Title, en.StepName, want)
	}
	if want := msgs.T(msgs.EN, "hub.clusterImageMissing", "disk.qcow2"); en.Error != want {
		t.Errorf("ошибка по-английски = %q, ждали %q", en.Error, want)
	}
	enLines := append([]store.JobLogLine(nil), lines...)
	localizeLines(msgs.EN, enLines)
	if want := msgs.T(msgs.EN, "hub.clusterImageOnHost", "disk.qcow2", "hv1"); enLines[0].Text != want {
		t.Errorf("строка по-английски = %q, ждали %q", enLines[0].Text, want)
	}
	if enLines[1].Text != "raw: E: apt failed" {
		t.Errorf("сырая строка изменилась: %q", enLines[1].Text)
	}
	if want := msgs.T(msgs.EN, "jobs.errorLine", msgs.T(msgs.EN, "hub.clusterImageMissing", "disk.qcow2")); enLines[2].Text != want {
		t.Errorf("строка ошибки по-английски = %q, ждали %q", enLines[2].Text, want)
	}
	// Русский читатель видит русское.
	ruLines := append([]store.JobLogLine(nil), lines...)
	localizeLines(msgs.RU, ruLines)
	if ruLines[0].Text != lines[0].Text {
		t.Errorf("русская строка изменилась: %q", ruLines[0].Text)
	}
}

type i18nRunner struct {
	fn func(ctx context.Context, jc *jobs.Context) error
}

func (r i18nRunner) Run(ctx context.Context, jc *jobs.Context) error { return r.fn(ctx, jc) }
