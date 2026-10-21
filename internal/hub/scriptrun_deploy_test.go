package hub

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

func runScript(t *testing.T, s *Server, r *ScriptRunner, content string) store.Job {
	t.Helper()
	ctx := context.Background()
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindScriptRun, Title: "t", Queue: "script:1", Steps: 1, Author: "admin",
		Params: ScriptRunParams{ScriptID: 1, Name: "t", Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		job, _ := s.db.JobByID(ctx, id)
		if job.Done() || time.Now().After(deadline) {
			return job
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// «pipeline … end» сохраняет конвейер как окно конвейера: значения блока
// env — в секреты (в описании «@secret»), повтор с тем же текстом —
// новая версия без ошибки (generate: у заданного пропускается), другое
// значение уже заданного ключа — ошибка, а не тихая замена.
func TestScriptPipelineSave(t *testing.T) {
	ctx := context.Background()
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	r := NewScriptRunner(s)
	s.jobs.Register(KindScriptRun, r)
	text := func(domain string) string {
		return strings.Join([]string{
			"pipeline hello",
			"repo: https://example.com/r.git",
			"ref: main",
			"action: compose",
			"compose:",
			"  file: docker-compose.yml",
			"  project: hello",
			"  hosts: [web1]",
			"  env:",
			"    DB_PASSWORD: generate:password",
			"    DOMAIN: " + domain,
			"end",
		}, "\n")
	}
	if job := runScript(t, s, r, text("a.example.com")); job.Status != store.JobSucceeded {
		t.Fatalf("%s: %s", job.Status, job.Error)
	}
	pl, ok := r.pipelineByName(ctx, "hello")
	if !ok || !strings.Contains(pl.Content, "@secret") || strings.Contains(pl.Content, "a.example.com") {
		t.Fatalf("описание: %q", pl.Content)
	}
	env, err := s.pipelineEnv(pl)
	if err != nil || env == nil || !strings.Contains(*env, "DOMAIN=a.example.com") || !strings.Contains(*env, "DB_PASSWORD=") {
		t.Fatalf(".env: %v %v", env, err)
	}
	if job := runScript(t, s, r, text("a.example.com")); job.Status != store.JobSucceeded {
		t.Fatalf("повтор: %s: %s", job.Status, job.Error)
	}
	job := runScript(t, s, r, text("b.example.com"))
	if job.Status != store.JobFailed || !strings.Contains(job.Error, "DOMAIN") {
		t.Fatalf("другое значение: %s: %s", job.Status, job.Error)
	}
	if list, _ := db.ListPipelines(ctx); len(list) != 1 {
		t.Fatalf("конвейеров %d", len(list))
	}
}

// «wait url» — запросом с хаба: ждёт нужный код, перенаправление не
// считается ответом адреса.
func TestScriptWaitURL(t *testing.T) {
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	r := NewScriptRunner(s)
	s.jobs.Register(KindScriptRun, r)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits++
		switch req.URL.Path {
		case "/ok":
			if hits < 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/redirect":
			http.Redirect(w, req, "/ok", http.StatusFound)
		}
	}))
	defer srv.Close()
	if job := runScript(t, s, r, "wait url "+srv.URL+"/ok 200 30s"); job.Status != store.JobSucceeded {
		t.Fatalf("%s: %s", job.Status, job.Error)
	}
	if job := runScript(t, s, r, "wait url "+srv.URL+"/redirect 200 1s"); job.Status != store.JobFailed {
		t.Fatalf("перенаправление засчитано: %s", job.Status)
	}
}
