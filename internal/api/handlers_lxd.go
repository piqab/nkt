package api

import (
	"errors"
	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/guestcred"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

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
	// User/Password/Generate — необязательный вход на экран и в консоль:
	// пароль сохраняется в хранилище гостей и ставится шагом задания.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Generate bool   `json:"generate,omitempty"`
}

func (s *Server) handleLXDInstanceCreate(w http.ResponseWriter, r *http.Request) {
	var req lxdCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if wantsJob(r) {
		args, err := control.LXDLaunchArgs(req.Image, req.Name, req.VM)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		cmds := []cmdjob.Command{{Argv: append([]string{hostTool("lxc")}, args...), StepKey: "lxd.stepLaunch", StepArgs: []any{req.Image}}}
		if req.Generate || req.Password != "" {
			user := strings.TrimSpace(req.User)
			if user == "" {
				user = "root"
			}
			pass := req.Password
			if req.Generate {
				pass = guestcred.Generate()
			}
			if s.guestCreds == nil {
				writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "cmdjob.noSecrets"))
				return
			}
			if err := s.guestCreds.Put(r.Context(), "lxd", req.Name, user, pass, auth.Username(r.Context())); err != nil {
				writeErr(w, r, http.StatusBadRequest, err)
				return
			}
			cmds = append(cmds, guestPasswordCommand("lxd", req.Name, user))
		}
		s.startCmdJob(w, r, "lxd.jobCreate", []any{req.Name}, "lxd:create:"+req.Name, cmdjob.Params{Commands: cmds, Refresh: true}, "lxd.create", req.Name)
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
	if s.guestCreds != nil {
		_ = s.guestCreds.Delete(r.Context(), "lxd", name)
	}
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

// handleLXDSnapshots — GET /lxd/instances/{name}/snapshots.
func (s *Server) handleLXDSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.lxd.ListSnapshots(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list})
}

// handleLXDSnapshotCreate — POST /lxd/instances/{name}/snapshots {name, stateful}.
func (s *Server) handleLXDSnapshotCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Stateful bool   `json:"stateful"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.lxdSnapshotAction(w, r, req.Name, "create", req.Stateful)
}

// handleLXDSnapshotRestore — POST /lxd/instances/{name}/snapshots/{snap}/restore.
func (s *Server) handleLXDSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	s.lxdSnapshotAction(w, r, chi.URLParam(r, "snap"), "restore", false)
}

// handleLXDSnapshotDelete — DELETE /lxd/instances/{name}/snapshots/{snap}.
func (s *Server) handleLXDSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	s.lxdSnapshotAction(w, r, chi.URLParam(r, "snap"), "delete", false)
}

func (s *Server) lxdSnapshotAction(w http.ResponseWriter, r *http.Request, snap, action string, stateful bool) {
	if err := s.lxd.SnapshotAction(r.Context(), auth.Username(r.Context()), chi.URLParam(r, "name"), snap, action, stateful); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLXDConfig — GET /lxd/instances/{name}/config: lxc config show
// (правится) и --expanded (с профилями, для справки).
func (s *Server) handleLXDConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.lxd.ReadConfig(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": cfg, "history_path": control.LXDConfigPath(chi.URLParam(r, "name"))})
}

// handleLXDConfigWrite — PUT /lxd/instances/{name}/config {content, note, expected_sha256}.
func (s *Server) handleLXDConfigWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content  string `json:"content"`
		Note     string `json:"note"`
		Expected string `json:"expected_sha256"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	id, err := s.lxd.WriteConfig(r.Context(), s.configs, auth.Username(r.Context()), chi.URLParam(r, "name"), req.Content, req.Note, req.Expected)
	if errors.Is(err, control.ErrLXDConfigStale) {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "configs.staleContent"))
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version_id": id})
}

// handleLXDNetworks — GET /lxd/networks.
func (s *Server) handleLXDNetworks(w http.ResponseWriter, r *http.Request) {
	list, err := s.lxd.ListNetworks(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": list})
}

// handleLXDStorage — GET /lxd/storage.
func (s *Server) handleLXDStorage(w http.ResponseWriter, r *http.Request) {
	list, err := s.lxd.ListStoragePools(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pools": list})
}

// handleLXDNetworkCreate — POST /lxd/networks {name, ipv4, ipv6}.
func (s *Server) handleLXDNetworkCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		IPv4 string `json:"ipv4"`
		IPv6 string `json:"ipv6"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.lxd.CreateNetwork(r.Context(), auth.Username(r.Context()), req.Name, req.IPv4, req.IPv6); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLXDNetworkDelete — DELETE /lxd/networks/{name}.
func (s *Server) handleLXDNetworkDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.lxd.DeleteNetwork(r.Context(), auth.Username(r.Context()), chi.URLParam(r, "name")); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLXDImageDelete — DELETE /lxd/images/{fp}.
func (s *Server) handleLXDImageDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.lxd.DeleteImage(r.Context(), auth.Username(r.Context()), chi.URLParam(r, "fp")); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLXDImageCopyWS — GET /lxd/images/copy/ws?ref=images:debian/12:
// скачивание образа на хост заранее (lxc image copy … local:) с выводом
// прогресса; разрыв соединения скачивание не прерывает.
func (s *Server) handleLXDImageCopyWS(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if !control.ValidLXDImageRef(ref) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "control.lxdBadImageRef", ref))
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return
	}
	lxc := "lxc"
	if p, err := exec.LookPath("lxc"); err == nil {
		lxc = p
	} else if _, err := os.Stat("/snap/bin/lxc"); err == nil {
		lxc = "/snap/bin/lxc"
	}
	argv := []string{lxc, "image", "copy", ref, "local:", "--copy-aliases", "--auto-update"}
	// Образ машины — с --vm: без флага lxc берёт контейнерный вариант.
	if r.URL.Query().Get("vm") == "1" {
		argv = append(argv, "--vm")
	}
	build := func() *exec.Cmd {
		return unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, argv...)
	}
	s.runUpdateSession(w, r, "lxd-image-copy:"+ref, build, "lxd.imageCopy", ref, s.cfg.TerminalIdleTimeout)
}
