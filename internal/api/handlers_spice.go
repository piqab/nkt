package api

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
)

// Экран по SPICE (клиент spice-html5): тот же мост WebSocket ↔ сокет, что
// у VNC. spice-html5 открывает отдельный WebSocket на каждый канал (main,
// display, inputs, cursor…) — каждый становится своим соединением с тем
// же SPICE-сервером, как у websockify.

// parseSpiceDisplay — адрес из `virsh domdisplay --type spice`:
// «spice://127.0.0.1:5901», «spice+unix:///run/…/spice.sock». Только-TLS
// порт (без обычного) не поддерживается: spice-html5 TLS не умеет.
func parseSpiceDisplay(out string) (network, addr string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(out))
	if err != nil {
		return "", "", false
	}
	switch u.Scheme {
	case "spice+unix":
		if u.Path == "" {
			return "", "", false
		}
		return "unix", u.Path, true
	case "spice":
		host, port := u.Hostname(), u.Port()
		if port == "" {
			return "", "", false
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		return "tcp", net.JoinHostPort(host, port), true
	}
	return "", "", false
}

func (s *Server) screenAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.cfg.TerminalEnabled {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.disabled"))
		return false
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return false
	}
	return true
}

// handleVMSpiceWS — GET /vms/{name}/spice/ws.
func (s *Server) handleVMSpiceWS(w http.ResponseWriter, r *http.Request) {
	if !s.screenAllowed(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	if !containerNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "api.consoleBadTarget", "vm", name))
		return
	}
	res, err := RunTooling(r.Context(), "virsh", "domdisplay", "--type", "spice", name)
	if err != nil || res.ExitCode != 0 {
		writeError(w, http.StatusBadGateway, msgs.T(msgs.LangFromRequest(r), "api.spiceUnavailable", name, strings.TrimSpace(res.Stderr+res.Stdout)))
		return
	}
	network, addr, ok := parseSpiceDisplay(res.Stdout)
	if !ok {
		writeError(w, http.StatusBadGateway, msgs.T(msgs.LangFromRequest(r), "api.spiceUnavailable", name, strings.TrimSpace(res.Stdout)))
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "vm.spice", name, "ok", map[string]any{"addr": addr})
	proxyWSTo(w, r, network, addr, s.cfg.TerminalIdleTimeout)
}

// lxdSpiceSockets — где LXD держит SPICE-сокет машины: snap и пакетные
// сборки; у проекта не по умолчанию каталог — «проект_имя».
func lxdSpiceSockets(name string) []string {
	var out []string
	for _, root := range []string{"/var/snap/lxd/common/lxd/logs", "/var/lib/lxd/logs", "/var/log/lxd"} {
		out = append(out, filepath.Join(root, name, "qemu.spice"))
	}
	return out
}

// handleLXDSpiceWS — GET /lxd/instances/{name}/spice/ws: экран машины LXD
// (у каждой VM LXD qemu слушает SPICE на unix-сокете).
func (s *Server) handleLXDSpiceWS(w http.ResponseWriter, r *http.Request) {
	if !s.screenAllowed(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	if !containerNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "api.consoleBadTarget", "lxd", name))
		return
	}
	for _, sock := range lxdSpiceSockets(name) {
		if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
			s.db.Audit(r.Context(), auth.Username(r.Context()), "lxd.spice", name, "ok", map[string]any{"socket": sock})
			proxyWSTo(w, r, "unix", sock, s.cfg.TerminalIdleTimeout)
			return
		}
	}
	writeError(w, http.StatusBadGateway, msgs.T(msgs.LangFromRequest(r), "api.lxdSpiceMissing", name))
}
