package api

import (
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"os/exec"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
)

func (s *Server) handleLXDInstances(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": snap.LXD})
}

type lxdCreateRequest struct {
	Image string `json:"image"`
	Name  string `json:"name"`
	// VM — образ виртуальной машины (lxc launch --vm).
	VM bool `json:"vm"`
}

func (s *Server) handleLXDInstanceCreate(w http.ResponseWriter, r *http.Request) {
	var req lxdCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	if err := s.lxd.CreateInstance(r.Context(), user, req.Image, req.Name, req.VM); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLXDInstanceAction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	action := chi.URLParam(r, "action")
	user := auth.Username(r.Context())
	if err := s.lxd.InstanceAction(r.Context(), user, name, action); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLXDInstanceDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	force := r.URL.Query().Get("force") == "true"
	user := auth.Username(r.Context())
	if err := s.lxd.DeleteInstance(r.Context(), user, name, force); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLXDImages — GET /lxd/images?remote=local|images|ubuntu: образы для
// «Новый инстанс LXD».
func (s *Server) handleLXDImages(w http.ResponseWriter, r *http.Request) {
	remote := r.URL.Query().Get("remote")
	if remote == "" {
		remote = "local"
	}
	list, err := s.lxd.ListImages(r.Context(), remote)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if list == nil {
		list = []control.LXDImage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"remote": remote, "images": list})
}

// handleLXDAutostart — POST /lxd/instances/{name}/autostart {on}.
func (s *Server) handleLXDAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.lxd.SetAutostart(r.Context(), auth.Username(r.Context()), chi.URLParam(r, "name"), req.On); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// lxdLogsArgv — просмотр логов инстанса: source=journal — журнал внутри
// (journalctl, при его отсутствии — syslog/messages), source=lxd — лог
// самого LXD об инстансе (запуск, ошибки). Без ${…}: уходит через
// systemd-run.
func lxdLogsArgv(name, source string, tail int, follow bool) ([]string, bool) {
	if !containerNameRe.MatchString(name) {
		return nil, false
	}
	lxc := "lxc"
	if p, err := exec.LookPath("lxc"); err == nil {
		lxc = p
	}
	if source == "lxd" {
		return []string{lxc, "info", name, "--show-log"}, true
	}
	f := ""
	if follow {
		f = " -f"
	}
	n := strconv.Itoa(tail)
	script := "journalctl --no-pager -n " + n + f + " 2>/dev/null || tail -n " + n + f + " /var/log/syslog /var/log/messages 2>/dev/null"
	return []string{lxc, "exec", name, "--", "sh", "-c", script}, true
}

// handleLXDLogsWS — GET /lxd/instances/{name}/logs/ws?source=journal|lxd&tail=200&follow=1.
func (s *Server) handleLXDLogsWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return
	}
	q := r.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	if tail <= 0 || tail > 10000 {
		tail = 200
	}
	name := chi.URLParam(r, "name")
	argv, ok := lxdLogsArgv(name, q.Get("source"), tail, q.Get("follow") == "1")
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "api.consoleBadTarget", "lxd", name))
		return
	}
	cmd := unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, argv...)
	s.runPTYSession(w, r, cmd, "lxd-logs", name, s.cfg.TerminalIdleTimeout)
}
