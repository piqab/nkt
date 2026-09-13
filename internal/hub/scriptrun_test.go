package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Сценарий заводит группу и хосты (с паролем из текста и спрошенным при
// запуске), продолжается после «перезапуска» с той же строки, не заводя
// хосты второй раз, и останавливается на первом шаге, который требует
// связи с хостом.
func TestScriptRunnerHostsAndResume(t *testing.T) {
	ctx := context.Background()
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	r := NewScriptRunner(s)
	s.jobs.Register(KindScriptRun, r)

	pid, err := db.CreateProfile(ctx, store.Profile{Name: "web", Content: "version: 1\nname: web"})
	if err != nil {
		t.Fatal(err)
	}
	_ = pid
	content := strings.Join([]string{
		"group farm profile web",
		`host web1 192.0.2.10 user root password "secret" group farm`,
		"host web2 192.0.2.11:2222 user deploy password ask",
		"install web1",
	}, "\n")
	ticket := r.keep(map[string]string{"host:web2": "asked"})
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindScriptRun, Title: "t", Queue: "script:1", Steps: 4,
		Params: ScriptRunParams{ScriptID: 1, Name: "t", Content: content, Ticket: ticket}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var job store.Job
	for {
		job, _ = db.JobByID(ctx, id)
		if job.Done() || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if job.Status != store.JobFailed {
		t.Fatalf("ожидался провал на install (SSH к 192.0.2.10 недоступен), статус %s: %s", job.Status, job.Error)
	}
	if !strings.Contains(job.Error, "строка 4") {
		t.Errorf("ошибка не указывает на строку install: %s", job.Error)
	}
	hosts, _ := db.ListHosts(ctx)
	if len(hosts) != 2 {
		t.Fatalf("хостов %d, ожидалось 2: %+v", len(hosts), hosts)
	}
	byName := map[string]store.Host{}
	for _, h := range hosts {
		byName[h.Name] = h
	}
	if byName["web1"].Group != "farm" || byName["web2"].SSHPort != 2222 {
		t.Errorf("хосты: %+v", byName)
	}
	if gp, _ := db.HostGroupProfile(ctx, "farm"); gp != pid {
		t.Errorf("профиль группы = %d", gp)
	}
	if _, ok := r.value(ticket, "host:web2"); ok {
		t.Error("пароль запуска остался в памяти после задания")
	}

	// Продолжение: три шага сделаны, install снова падает, а хосты не
	// дублируются.
	var done scriptRunResume
	if err := json.Unmarshal([]byte(job.Resume), &done); err != nil {
		t.Fatalf("resume: %v (%q)", err, job.Resume)
	}
	if done.Done != 3 || len(done.HostIDs) != 2 {
		t.Errorf("resume: %+v", done)
	}
}
