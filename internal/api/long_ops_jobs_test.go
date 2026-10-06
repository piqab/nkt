package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Долгие операции с ?job=1 заводят задание с проверенными командами, а
// неверный ввод отвергают до задания.
func TestLongOpsAsJobs(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	release := make(chan struct{})
	defer close(release)
	jm.Register(cmdjob.Kind, blockRunner{release})
	dir := t.TempDir()
	s := &Server{db: db, jobs: jm, cfg: &config.Config{}, images: control.NewImageManager(nil, nil, dir)}

	call := func(h http.HandlerFunc, target string, params map[string]string, body any) (int, []string) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req := httptest.NewRequest("POST", target, bytes.NewReader(b))
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
		if rec.Code != 200 || out.JobID == 0 {
			return rec.Code, nil
		}
		j, err := db.JobByID(t.Context(), out.JobID)
		if err != nil {
			t.Fatal(err)
		}
		var p cmdjob.Params
		_ = json.Unmarshal([]byte(j.Params), &p)
		var cmds []string
		for _, c := range p.Commands {
			argv := append([]string{}, c.Argv...)
			if len(argv) > 0 {
				argv[0] = filepath.Base(argv[0])
			}
			cmds = append(cmds, strings.Join(argv, " "))
		}
		return rec.Code, cmds
	}

	if _, cmds := call(s.handleLXDInstanceAction, "/lxd/instances/web/stop?job=1", map[string]string{"name": "web", "action": "stop"}, nil); len(cmds) != 1 || cmds[0] != "lxc stop web" {
		t.Errorf("остановка LXD: %v", cmds)
	}
	if code, _ := call(s.handleLXDInstanceAction, "/x?job=1", map[string]string{"name": "web;rm", "action": "stop"}, nil); code != 400 {
		t.Errorf("плохое имя инстанса: %d", code)
	}
	if code, _ := call(s.handleLXDInstanceAction, "/x?job=1", map[string]string{"name": "web", "action": "delete"}, nil); code != 400 {
		t.Errorf("чужое действие: %d", code)
	}
	if _, cmds := call(s.handleLXDSnapshotCreate, "/x?job=1", map[string]string{"name": "web"}, map[string]any{"name": "snap1", "stateful": true}); len(cmds) != 1 || cmds[0] != "lxc snapshot web snap1 --stateful" {
		t.Errorf("снимок: %v", cmds)
	}
	if _, cmds := call(s.handleLXDSnapshotRestore, "/x?job=1", map[string]string{"name": "web", "snap": "snap1"}, nil); len(cmds) != 1 || cmds[0] != "lxc restore web snap1" {
		t.Errorf("откат: %v", cmds)
	}
	if code, _ := call(s.handleLXDSnapshotRestore, "/x?job=1", map[string]string{"name": "web", "snap": "../x"}, nil); code != 400 {
		t.Errorf("плохое имя снимка: %d", code)
	}

	_, cmds := call(s.handleImagesSave, "/images/save?job=1", nil, map[string]any{"refs": []string{"nginx:1.27", "ghcr.io/acme/app:2"}})
	if len(cmds) != 2 || !strings.HasPrefix(cmds[0], "docker save -o "+filepath.Join(dir, "docker__nginx_1.27__")) || !strings.HasSuffix(cmds[1], " ghcr.io/acme/app:2") {
		t.Errorf("сохранение образов: %v", cmds)
	}
	if code, _ := call(s.handleImagesSave, "/images/save?job=1", nil, map[string]any{"refs": []string{"$(reboot)"}}); code != 400 {
		t.Errorf("плохая ссылка образа: %d", code)
	}
}
