package hub

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/api"
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// connIdleTTL bounds how long an unused SSH connection to a host stays open
// — a hub managing many hosts must not keep every one of them connected
// forever just because it was proxied to once.
const connIdleTTL = 10 * time.Minute

// sessionTTL is how long a captured remote session cookie is reused before
// cookieFor logs in again — comfortably inside the remote's own
// NKT_SESSION_TTL (12h default), so a proxied request is never the one that
// discovers a stale cookie.
const sessionTTL = 2 * time.Hour

type hostConn struct {
	// link — соединение и, для машины внутри хоста, переход под ним.
	link     *sshLink
	lastUsed time.Time
}

type sessionCache struct {
	cookie  string
	expires time.Time
}

// clientFor returns a live SSH connection to hostID, reusing a pooled one
// when available and dialing fresh otherwise.
func (m *Manager) clientFor(ctx context.Context, hostID int64) (*ssh.Client, error) {
	m.connsMu.Lock()
	if hc, ok := m.conns[hostID]; ok {
		hc.lastUsed = time.Now()
		m.connsMu.Unlock()
		return hc.link.client, nil
	}
	m.connsMu.Unlock()

	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return nil, msgs.Errorf("hub.hostFound", err)
	}
	if host.Status != store.HostStatusOnline {
		return nil, msgs.Errorf("hub.hostReadyYetStatus", host.Name, host.Status)
	}
	// Машина внутри хоста недостижима с хаба напрямую: dialHost сам
	// проложит путь через её хост.
	link, err := m.dialHost(ctx, host)
	if err != nil {
		return nil, err
	}

	m.connsMu.Lock()
	m.conns[hostID] = &hostConn{link: link, lastUsed: time.Now()}
	m.connsMu.Unlock()

	_ = m.db.TouchHostSeen(ctx, hostID)
	return link.client, nil
}

// dropClient closes and forgets a pooled connection — called whenever a
// proxied request fails, so the next one reconnects instead of repeatedly
// handing out a dead client.
func (m *Manager) dropClient(hostID int64) {
	m.connsMu.Lock()
	hc, ok := m.conns[hostID]
	delete(m.conns, hostID)
	m.connsMu.Unlock()
	if ok {
		_ = hc.link.Close()
	}
}

// evictIdleConns closes SSH connections that have sat unused past
// connIdleTTL. Meant to run as a background goroutine for the hub's
// lifetime; returns when ctx is done.
func (m *Manager) evictIdleConns(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.connsMu.Lock()
			for id, hc := range m.conns {
				if time.Since(hc.lastUsed) > connIdleTTL {
					delete(m.conns, id)
					_ = hc.link.Close()
				}
			}
			m.connsMu.Unlock()
		}
	}
}

// cookieFor returns a session cookie for hostID's own nkt, logging in as its
// bootstrap admin (with the credentials StartInstall saved) whenever no
// cached one is fresh enough. dial reaches the host over whichever channel
// dialerFor picked — SSH or the reverse-tunnel fallback.
func (m *Manager) cookieFor(ctx context.Context, hostID int64, dial dialFunc) (string, error) {
	m.sessionMu.Lock()
	if sc, ok := m.sessions[hostID]; ok && time.Now().Before(sc.expires) {
		m.sessionMu.Unlock()
		return sc.cookie, nil
	}
	m.sessionMu.Unlock()

	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return "", msgs.Errorf("hub.hostFound", err)
	}
	if host.AdminUser == "" || len(host.AdminPasswordEnc) == 0 {
		return "", msgs.Errorf("hub.adminAccountSavedHostYet", host.Name)
	}
	adminPassword, err := secretbox.Decrypt(m.key, host.AdminPasswordEnc)
	if err != nil {
		return "", msgs.Errorf("hub.decryptingAdminPassword", err)
	}

	cookie, err := bootstrapLogin(ctx, dial, m.hostAPIAddrFor(host), host.AdminUser, string(adminPassword))
	if err != nil {
		return "", msgs.Errorf("hub.loggingHost", host.Name, err)
	}

	m.sessionMu.Lock()
	m.sessions[hostID] = sessionCache{cookie: cookie, expires: time.Now().Add(sessionTTL)}
	m.sessionMu.Unlock()
	return cookie, nil
}

// Перезапуск nkt на хосте — «обновить пакеты» перезапускает службу,
// самообновление тоже — на секунды оставляет API без слушателя. Запрос,
// пришедший в это окно, раньше падал с голым «connection refused»;
// теперь прокси ждёт, если хост отвечал по SSH совсем недавно. Окно
// короче таймаута запроса в интерфейсе (30 с), чтобы отказ пришёл от
// хаба с объяснением, а не от таймера браузера.
const (
	apiRestartWait   = 20 * time.Second
	apiRestartPoll   = 2 * time.Second
	apiRecentlySeen  = 3 * time.Minute
	apiDownHintAfter = apiRestartWait
)

// isAPIDown — ошибка входа означает «SSH есть, а API nkt не слушает»:
// отказ соединения через туннель или оборванный ответ, а не неверный
// пароль и не недоступный SSH.
func isAPIDown(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "unable to authenticate") || strings.Contains(s, "handshake") {
		return false
	}
	for _, needle := range []string{"connection refused", "connect failed", "connection reset", "eof"} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// seenRecently — хост успешно отвечал по SSH не дальше apiRecentlySeen
// назад: тогда отказ API — скорее всего перезапуск, и его стоит
// подождать.
func (m *Manager) seenRecently(ctx context.Context, hostID int64) bool {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil || host.LastSeenAt == "" {
		return false
	}
	seen, err := time.Parse(time.RFC3339, host.LastSeenAt)
	return err == nil && time.Since(seen) < apiRecentlySeen
}

// cookieForWithWait — cookieFor с ожиданием перезапуска API: пока хост
// недавно отвечал по SSH, а вход падает отказом соединения, пробует
// заново каждые apiRestartPoll до apiRestartWait. Если API так и не
// поднялся — ошибка с подсказкой: служба на хосте не запущена, «обновить»
// с хаба переустановит и перезапустит её.
func (m *Manager) cookieForWithWait(ctx context.Context, hostID int64, dial dialFunc) (string, error) {
	cookie, err := m.cookieFor(ctx, hostID, dial)
	if err == nil || !isAPIDown(err) || !m.seenRecently(ctx, hostID) {
		return cookie, err
	}
	deadline := time.Now().Add(apiRestartWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", err
		case <-time.After(apiRestartPoll):
		}
		cookie, err = m.cookieFor(ctx, hostID, dial)
		if err == nil || !isAPIDown(err) {
			return cookie, err
		}
	}
	return "", m.apiDownError(ctx, hostID, err)
}

// apiDownError — «SSH отвечает, API nkt нет» с диагностикой, снятой по
// SSH: состояние юнита, кто слушает порт API, хвост журнала службы.
// «Служба active» у systemd появляется сразу после запуска процесса,
// до открытия порта, поэтому одного is-active мало — нужен и слушатель,
// и журнал (старт по кругу, занятый порт, ошибка конфигурации).
func (m *Manager) apiDownError(ctx context.Context, hostID int64, cause error) error {
	diag := m.apiDownDiagnosis(ctx, hostID)
	if diag == "" {
		return msgs.Errorf("hub.hostAPIDown", cause)
	}
	return msgs.Errorf("hub.hostAPIDownDiag", cause, diag)
}

// apiDownDiagCmd — одной сессией, с нулевым кодом выхода: нужен вывод,
// а не статус. Без root журнал может быть недоступен — тогда его просто
// не будет.
const apiDownDiagCmd = `systemctl is-active netknownsthat.service 2>&1; echo '--'; ss -ltnH 'sport = :%d' 2>/dev/null; echo '--'; journalctl -u netknownsthat.service -n 5 --no-pager -o cat 2>/dev/null | tail -n 5; true`

func (m *Manager) apiDownDiagnosis(ctx context.Context, hostID int64) string {
	client, err := m.clientFor(ctx, hostID)
	if err != nil {
		return ""
	}
	_, portStr, _ := net.SplitHostPort(m.hostAPIAddr(ctx, hostID))
	port, _ := strconv.Atoi(portStr)
	out, err := runRemote(client, fmt.Sprintf(apiDownDiagCmd, port))
	if err != nil && strings.TrimSpace(out) == "" {
		return ""
	}
	return formatAPIDiag(out, port)
}

// formatAPIDiag сворачивает вывод apiDownDiagCmd в одну строку.
func formatAPIDiag(out string, port int) string {
	parts := strings.SplitN(strings.ReplaceAll(out, "\r\n", "\n"), "\n--\n", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	state := strings.TrimSpace(parts[0])
	if state == "" {
		state = "?"
	}
	listen := strings.TrimSpace(parts[1])
	if listen == "" {
		listen = msgs.T(msgs.DefaultLang, "hub.apiDiagNobody")
	} else {
		// Из строк ss нужен только адрес: «LISTEN 0 4096 127.0.0.1:8077 0.0.0.0:*».
		var addrs []string
		for _, line := range strings.Split(listen, "\n") {
			f := strings.Fields(line)
			if len(f) >= 4 {
				addrs = append(addrs, f[3])
			}
		}
		if len(addrs) > 0 {
			listen = strings.Join(addrs, ", ")
		}
	}
	journal := strings.TrimSpace(parts[2])
	if journal == "" {
		journal = "—"
	} else {
		journal = strings.ReplaceAll(journal, "\n", " | ")
		if len(journal) > 400 {
			journal = journal[:400] + "…"
		}
	}
	return fmt.Sprintf(msgs.T(msgs.DefaultLang, "hub.apiDiag"), state, port, listen, journal)
}

// dropSession forgets a cached cookie — called alongside dropClient so a
// reconnect also gets a fresh login instead of replaying a cookie tied to a
// connection that's gone.
func (m *Manager) dropSession(hostID int64) {
	m.sessionMu.Lock()
	delete(m.sessions, hostID)
	m.sessionMu.Unlock()
}

// channelSSH and channelTunnel identify which path dialerFor picked, for
// callers that surface it to the operator (see recordChannel) — a plain
// string rather than a typed enum since its only consumers are a log field
// and a JSON API response.
const (
	channelSSH    = "ssh"
	channelTunnel = "tunnel"
)

// dialerFor returns a way to reach hostID's own nkt API: SSH first — the
// primary, fully-capable path, unchanged — falling back to the
// reverse-tunnel channel (internal/tunnel, see relay.go) only when the SSH
// dial itself fails and a live tunnel session happens to be registered for
// this host. Install/update/SFTP/sudo commands never go through this —
// they need real SSH regardless and call clientFor/dialSSH directly (except
// installOverTunnel's own deliberate fallback, which never calls this
// either — it already knows which channel it's using).
//
// The returned onFail must be called if something reached via dial later
// fails too (a stale pooled SSH conn dying mid-use, say) — always safe to
// call even when there's nothing to drop.
func (m *Manager) dialerFor(ctx context.Context, hostID int64) (dial dialFunc, channel string, onFail func(), err error) {
	client, sshErr := m.clientFor(ctx, hostID)
	if sshErr == nil {
		return client.Dial, channelSSH, func() { m.dropClient(hostID); m.dropSession(hostID) }, nil
	}
	if relay, ok := m.relayDial(hostID); ok {
		return relay, channelTunnel, func() {}, nil
	}
	return nil, "", nil, sshErr
}

// Proxy returns a handler that forwards every request it receives to
// hostID's own nkt API — over SSH, or the reverse-tunnel fallback when SSH
// is unreachable (see dialerFor) — injecting that host's own session
// cookie either way: the browser only ever authenticates to the hub
// itself, never to each managed host individually. The caller is expected
// to have already rewritten the request path to what the remote's own API
// expects (see server.go's proxyHost).
func (m *Manager) Proxy(hostID int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		apiAddr := m.hostAPIAddr(ctx, hostID)

		dial, channel, onFail, err := m.dialerFor(ctx, hostID)
		if err != nil {
			// writeError, not the stdlib http.Error used here until this fix:
			// http.Error writes plain text (Content-Type: text/plain), but the
			// frontend's api() only ever looks for a JSON {"error": "..."}
			// body (see api.ts) — a plain-text body silently fails that
			// lookup and falls back to a bare "Ошибка 502", discarding
			// whatever actually useful diagnosis dialerFor/cookieFor/the
			// proxy's own ErrorHandler had already put together (which SSH
			// path failed, whether the tunnel is even connected, ...).
			writeErr(w, r, http.StatusBadGateway, err)
			return
		}
		m.recordChannel(hostID, channel)
		cookie, err := m.cookieForWithWait(ctx, hostID, dial)
		if err != nil {
			onFail()
			writeErr(w, r, http.StatusBadGateway, err)
			return
		}

		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = "http"
				req.URL.Host = apiAddr
				// req.Host deliberately left as whatever the browser sent
				// (the hub's own address) — NOT rewritten to remoteAPIAddr.
				// dial() below already hardcodes the real network target
				// regardless of what's in req.URL.Host, and internal/api
				// never itself reads r.Host — but coder/websocket's
				// Accept() (handleTerminalWS/handleUpdatesWS on the remote)
				// does, as part of its default same-origin check: it
				// compares the browser's Origin header (always the hub's
				// own address, since the browser only ever talks to the
				// hub) against r.Host. Rewriting r.Host to remoteAPIAddr
				// here made those two permanently disagree, so every
				// proxied terminal/package-update WebSocket upgrade was
				// rejected with 403 regardless of NKT_TERMINAL_ENABLED —
				// on literally every managed host, unconditionally.
				//
				// The incoming request still carries the browser's own hub
				// session cookie (proxyHost clones it as-is) — same name
				// (auth.SessionCookie) as the one injected below, but
				// meaningless to this host. Left in place, the two would
				// travel together and the host would resolve whichever one
				// net/http's Cookie() returns first — the hub's, in
				// practice — instead of the one actually meant for it,
				// failing auth on every single request. It must be gone
				// before AddCookie puts the right one in its place.
				req.Header.Del("Cookie")
				req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
				// Хост должен уметь отличить запрос, пришедший по
				// SSH-туннелю, от прямого обращения к его собственному
				// веб-интерфейсу: для правки sshd_config это разница между
				// «управляющий канал зависит от sshd» и «не зависит» (см.
				// control.SSHReserveChannel). Заголовок ставится здесь, где
				// туннель и создаётся; браузерный заголовок с тем же именем
				// затирается, а не дополняется.
				req.Header.Set(api.HeaderVia, api.ViaHubTunnel)
			},
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					conn, err := dial("tcp", apiAddr)
					if err == nil {
						return conn, nil
					}
					// The pooled connection dialerFor handed back a moment
					// ago can have silently died since — no keepalive ever
					// proves it's still alive before reuse, so an idle
					// network blip or a NAT/router timeout only surfaces the
					// next time something actually tries to open a channel
					// on it. That single stale connection's teardown fails
					// every channel-open racing it at once (mux.loop's own
					// cleanup, see x/crypto/ssh), which is exactly what a
					// page that fires several concurrent proxied requests on
					// open (e.g. Usage.tsx's usage/top/heatmap trio) turns
					// into several simultaneous "unexpected packet in
					// response to channel open: <nil>" failures instead of
					// one. Evict it and dial fresh exactly once before
					// giving up — a routine dead-pooled-connection blip
					// should never reach the user as an error at all.
					onFail()
					freshDial, _, freshOnFail, dialErr := m.dialerFor(ctx, hostID)
					if dialErr != nil {
						return nil, err
					}
					conn, err = freshDial("tcp", apiAddr)
					if err != nil {
						freshOnFail()
						return nil, err
					}
					return conn, nil
				},
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				// Localizes against the outer r (this handler's own request
				// param is unused — httputil.ReverseProxy passes it the
				// proxied request, but that request already carries whatever
				// headers came from the browser, same as r itself here; using
				// the outer r just avoids relying on that being true).
				onFail()
				writeError(w, http.StatusBadGateway, msgs.T(msgs.LangFromRequest(r), "hub.hostUnreachable", err.Error()))
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// EnsureLive проверяет, что соединение с хостом ещё живое, и меняет
// мёртвое на свежее.
//
// Нужно перед передачей файла: у запроса с телом нет второй попытки —
// тело уже вычитано, и Proxy не может передознить, как делает для
// обычного GET. Мёртвое соединение из пула (NAT закрыл сессию, sshd
// перезапустили) обнаруживалось бы уже в середине PUT и возвращало
// «хост недоступен», теряя файл. Проба дешёвая: открыть канал и
// спросить /api/health — миллисекунды по живому соединению.
func (m *Manager) EnsureLive(ctx context.Context, hostID int64) {
	dial, _, onFail, err := m.dialerFor(ctx, hostID)
	if err != nil {
		return
	}
	addr := m.hostAPIAddr(ctx, hostID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/health", nil)
	if err != nil {
		return
	}
	resp, err := tunnelHTTPClient(dial, addr).Do(req)
	if err != nil {
		// Пул отдал мёртвое соединение — выбросить, следующий вызов
		// dialerFor (уже внутри Proxy) дозвонится заново.
		onFail()
		return
	}
	_ = resp.Body.Close()
}

// CloseHost drops any pooled connection/session/cached overview/tunnel
// session for a host — called when a host is removed from the registry
// entirely. Deliberately not used by UpdateHost/UpdateHostGenerated (see
// dropSSHPool): editing a host's SSH details has nothing to do with
// whether its reverse-tunnel session is still good, and dropping it there
// too would force every "изменить" to wait out the host's own reconnect
// backoff (up to 30s) before install()'s SSH-down fallback could find a
// live session again — exactly the gap that let a save-then-reinstall
// (e.g. flipping the terminal checkbox) fail over to raw SSH errors
// instead of the tunnel that was working a moment before.
func (m *Manager) CloseHost(hostID int64) {
	m.dropSSHPool(hostID)
	m.dropOverview(hostID)
	m.dropRelayAll(hostID)
	m.dropVulnScan(hostID)
}

// dropSSHPool forgets hostID's pooled SSH connection and cached session
// cookie — everything UpdateHost/UpdateHostGenerated need to drop after
// changing connection details (address, port, user, credential) so the
// next request reconnects with the new ones, without touching the
// unrelated reverse-tunnel session or overview cache CloseHost also clears.
func (m *Manager) dropSSHPool(hostID int64) {
	m.dropClient(hostID)
	m.dropSession(hostID)
	// Порт API мог измениться вместе с остальными реквизитами.
	m.connsMu.Lock()
	delete(m.apiPorts, hostID)
	m.connsMu.Unlock()
}

// DropSSHPool — dropSSHPool для обработчиков: после «забыть ключ хоста»
// следующее подключение должно идти заново и запомнить новый ключ.
func (m *Manager) DropSSHPool(hostID int64) { m.dropSSHPool(hostID) }
