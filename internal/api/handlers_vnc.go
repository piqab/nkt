package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
)

// Экран машины в браузере (noVNC): WebSocket ↔ TCP до VNC-порта машины
// на этом хосте. libvirt по умолчанию слушает VNC на 127.0.0.1 — снаружи
// его не видно, а через nkt (и туннель хаба) — видно. Ворота те же, что у
// веб-терминала: экран машины даёт ровно такой же доступ, как консоль.

// parseVNCDisplay — «127.0.0.1:1» / «:0» / «[::1]:2» из virsh vncdisplay →
// адрес для подключения (порт 5900 + номер экрана).
func parseVNCDisplay(out string) (string, bool) {
	s := strings.TrimSpace(out)
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", false
	}
	host, num := s[:i], s[i+1:]
	n, err := strconv.Atoi(num)
	if err != nil || n < 0 || n > 10000 {
		return "", false
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(5900+n)), true
}

// handleVMVNCWS — GET /vms/{name}/vnc/ws.
func (s *Server) handleVMVNCWS(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TerminalEnabled {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.disabled"))
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return
	}
	name := chi.URLParam(r, "name")
	if !containerNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "api.consoleBadTarget", "vm", name))
		return
	}
	res, err := RunTooling(r.Context(), "virsh", "vncdisplay", name)
	if err != nil || res.ExitCode != 0 {
		writeError(w, http.StatusBadGateway, msgs.T(msgs.LangFromRequest(r), "api.vncUnavailable", name, strings.TrimSpace(res.Stderr+res.Stdout)))
		return
	}
	addr, ok := parseVNCDisplay(res.Stdout)
	if !ok {
		writeError(w, http.StatusBadGateway, msgs.T(msgs.LangFromRequest(r), "api.vncUnavailable", name, strings.TrimSpace(res.Stdout)))
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "vm.vnc", name, "ok", map[string]any{"addr": addr})
	proxyWSToTCP(w, r, addr, s.cfg.TerminalIdleTimeout)
}

// proxyWSToTCP принимает WebSocket и пересылает байты в обе стороны до
// addr; закрытие любой стороны или бездействие дольше idle — конец.
func proxyWSToTCP(w http.ResponseWriter, r *http.Request, addr string, idle time.Duration) {
	proxyWSTo(w, r, "tcp", addr, idle)
}

// proxyWSTo — то же для любой сети: tcp или unix (сокет SPICE машины LXD).
func proxyWSTo(w http.ResponseWriter, r *http.Request, network, addr string, idle time.Duration) {
	var d net.Dialer
	tcp, err := d.DialContext(r.Context(), network, addr)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer tcp.Close()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(-1)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ws := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	if idle <= 0 {
		idle = 30 * time.Minute
	}
	done := make(chan struct{}, 2)
	pipe := func(dst io.Writer, src net.Conn) {
		buf := make([]byte, 64<<10)
		for {
			_ = src.SetReadDeadline(time.Now().Add(idle))
			n, err := src.Read(buf)
			if n > 0 {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go pipe(ws, tcp)
	go pipe(tcp, ws)
	<-done
}
