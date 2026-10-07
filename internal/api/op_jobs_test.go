package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Распаковка, генерация локалей и удаление пользователя с домашним
// каталогом с ?job=1 заводят задание операции хоста с её параметрами; без
// домашнего каталога и без генерации — как раньше, без задания.
func TestHostOpJobs(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	release := make(chan struct{})
	defer close(release)
	jm.Register(KindHostOp, blockRunner{release})
	s := &Server{db: db, jobs: jm, cfg: &config.Config{}}

	op := func(h http.HandlerFunc, method, target string, params map[string]string, body any) (int, HostOpParams) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, target, bytes.NewReader(b))
		rc := chi.NewRouteContext()
		for k, v := range params {
			rc.URLParams.Add(k, v)
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
		rec := httptest.NewRecorder()
		h(rec, req)
		var out struct {
			JobID int64 `json:"job_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		var p HostOpParams
		if out.JobID > 0 {
			j, _ := db.JobByID(t.Context(), out.JobID)
			_ = json.Unmarshal([]byte(j.Params), &p)
		}
		return rec.Code, p
	}

	if _, p := op(s.handleLocalesUpdate, "POST", "/system/locales?job=1", nil, map[string]any{"generate": []string{"ru_RU.UTF-8"}}); p.Op != "locales" {
		t.Errorf("локали: %+v", p)
	}
	if _, p := op(s.handleOSUserDelete, "DELETE", "/os-users/bob?home=1&job=1", map[string]string{"name": "bob"}, nil); p.Op != "osuser.delete" || string(p.Args) != `{"name":"bob","home":true}` {
		t.Errorf("удаление пользователя: %+v %s", p, p.Args)
	}
	// Без файлового менеджера (s.files) распаковки нет — отказ, а не задание.
	if code, p := op(s.handleFilesExtract, "POST", "/files/extract?job=1", nil, map[string]any{"path": "/srv/a.tar"}); code == 200 || p.Op != "" {
		t.Errorf("распаковка без проводника: %d %+v", code, p)
	}
}

// Выпуск и продление certbot с ?job=1 — задание хоста; неверный ввод
// отклоняется до задания.
func TestCertbotJobs(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	release := make(chan struct{})
	defer close(release)
	jm.Register(KindCertbot, blockRunner{release})
	s := &Server{db: db, jobs: jm, cfg: &config.Config{}}

	post := func(h http.HandlerFunc, body any) (int, CertbotParams) {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("POST", "/x?job=1", bytes.NewReader(b)))
		var out struct {
			JobID int64 `json:"job_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		var p CertbotParams
		if out.JobID > 0 {
			j, _ := db.JobByID(t.Context(), out.JobID)
			_ = json.Unmarshal([]byte(j.Params), &p)
		}
		return rec.Code, p
	}
	if code, p := post(s.handleRenewCertbot, map[string]any{"lineage": "example.com", "restart_pids": []int{42}}); code != 200 || p.Op != "renew" || p.Lineage != "example.com" || len(p.RestartPIDs) != 1 {
		t.Errorf("продление: %d %+v", code, p)
	}
	if code, _ := post(s.handleRenewCertbot, map[string]any{"lineage": "../etc"}); code != 400 {
		t.Errorf("плохой lineage: %d", code)
	}
	if code, p := post(s.handleIssueCertbot, map[string]any{"domains": []string{"new.example.com"}}); code != 200 || p.Op != "issue" || len(p.Domains) != 1 {
		t.Errorf("выпуск: %d %+v", code, p)
	}
	if code, _ := post(s.handleIssueCertbot, map[string]any{"domains": []string{"*.example.com"}}); code != 400 {
		t.Errorf("wildcard без DNS-проверки: %d", code)
	}
}
