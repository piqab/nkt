package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/store"
)

// tunnelReinstallFallback reports whether hostID can be reinstalled/updated
// over its reverse-tunnel channel instead of SSH — consulted by install()
// only after an SSH dial has already failed. Two conditions beyond
// TunnelEnabled: host.Arch must already be known (set by a previous
// *successful* SSH install's detectTarget — this path never runs a fresh
// host's very first install, since there is nothing to fall back to before
// SSH has ever worked at all), and a tunnel session must actually be live
// right now — checked here only to fail fast with a clear reason before
// starting the job; installOverTunnel re-resolves the session itself on
// every dial rather than trusting this snapshot to still hold (see
// dynamicRelayDial).
func (m *Manager) tunnelReinstallFallback(host store.Host) bool {
	if !host.TunnelEnabled || host.Arch == "" {
		return false
	}
	_, ok := m.relayDial(host.ID)
	return ok
}

// tunnelReinstallFallbackWait bounds how long awaitTunnelReinstallFallback
// polls for a tunnel session to (re)appear before giving up, and
// tunnelReinstallFallbackPoll is how often it checks — both vars, not
// consts, so tests can shrink them instead of actually waiting seconds
// for a real timer.
var (
	tunnelReinstallFallbackWait = 5 * time.Second
	tunnelReinstallFallbackPoll = 250 * time.Millisecond
)

// awaitTunnelReinstallFallback is tunnelReinstallFallback with a short
// bounded wait for the "should work but isn't registered at this exact
// instant" case — the SSH failure that just triggered this check and a
// tunnel session dropping can easily share the same underlying network
// blip, so runTunnelDialer's own reconnect (see tunnelDialerMaxBackoff)
// may simply not have caught up yet at the precise moment install() asks.
// Without this, an operator hitting "обновить"/"переустановить" right as
// SSH drops would see the job fail outright with the raw SSH error and
// have to click again moments later once the tunnel reconnected on its
// own — confusing, since nothing about the feature was actually broken,
// just its timing relative to this one check. Skips the wait entirely
// when tunnelReinstallFallback's other two conditions (TunnelEnabled,
// known Arch) already rule it out for good — no session will ever appear
// for those, waiting would only delay a failure that was always coming.
func (m *Manager) awaitTunnelReinstallFallback(ctx context.Context, host store.Host) bool {
	if m.tunnelReinstallFallback(host) {
		return true
	}
	if !host.TunnelEnabled || host.Arch == "" {
		return false
	}
	deadline := time.Now().Add(tunnelReinstallFallbackWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(tunnelReinstallFallbackPoll):
		}
		if m.tunnelReinstallFallback(host) {
			return true
		}
	}
	return false
}

// dynamicRelayDial returns a dialFunc that re-resolves hostID's current
// relay session on every single call, rather than being bound to whichever
// session happened to be live when the dial was created. installOverTunnel
// needs exactly this, not a snapshot from relayDial: selfUpdateOverTunnel's
// own request ends with the remote restarting itself, which drops the very
// session that request went out over — the health check and admin-login
// calls right after it must ride out that restart and pick up the *new*
// session the freshly restarted host reconnects with, moments later, using
// its newly rendered token.
func (m *Manager) dynamicRelayDial(hostID int64) dialFunc {
	return func(network, addr string) (net.Conn, error) {
		dial, ok := m.relayDial(hostID)
		if !ok {
			return nil, fmt.Errorf("резервный канал для хоста сейчас не подключён")
		}
		return dial(network, addr)
	}
}

// selfUpdateOverTunnel pushes a freshly built binary plus rendered
// unit/env to hostID's own POST /api/self-update, authenticated the same
// way any other proxied request is (cookieFor's cached bootstrap-admin
// session). It is the reverse-tunnel-channel equivalent of what
// stageFiles+activateService do over SSH+SFTP+sudo — the remote's own
// systemd-run escape hatch (internal/api/handlers_selfupdate.go) carries
// out the actual file replacement and restart locally, since this dial has
// no SFTP/exec of its own to offer, only HTTP to the remote's API.
//
// Uses its own http.Client rather than tunnelHTTPClient: that one's fixed
// 30s timeout comfortably covers a health check or a login, but not
// necessarily uploading a multi-megabyte binary over a channel that, by
// definition, is only in play here because the primary one (SSH) is
// already having trouble. ctx's own deadline (the install job's 10-minute
// budget) is what actually bounds this instead.
func (m *Manager) selfUpdateOverTunnel(ctx context.Context, hostID int64, dial dialFunc, binPath, unitContent, envContent string, report func(string)) error {
	binBytes, err := os.ReadFile(binPath)
	if err != nil {
		return fmt.Errorf("чтение собранного бинарника: %w", err)
	}
	sum := sha256.Sum256(binBytes)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("binary", "nkt")
	if err != nil {
		return err
	}
	if _, err := part.Write(binBytes); err != nil {
		return err
	}
	if err := writer.WriteField("unit", unitContent); err != nil {
		return err
	}
	if err := writer.WriteField("env", envContent); err != nil {
		return err
	}
	if err := writer.WriteField("sha256", hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	cookie, err := m.cookieFor(ctx, hostID, dial)
	if err != nil {
		return fmt.Errorf("вход на хост через резервный канал: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+remoteAPIAddr+"/api/self-update", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})

	report("Отправляю бинарник и конфигурацию через резервный канал…")
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return dial("tcp", remoteAPIAddr)
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("запрос обновления через резервный канал: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("обновление через резервный канал не удалось (код %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	report("Хост принял обновление и перезапускает сервис…")
	return nil
}

// installOverTunnel is install()'s counterpart for when SSH itself is what
// failed but tunnelReinstallFallback found a live reverse-tunnel session to
// use instead. It covers the same ground — build/reuse a binary, render the
// unit/env, push them, wait for health, confirm the admin login — except
// the two steps that structurally need SSH and are simply skipped here:
// detecting the target architecture (host.Arch is already known, from
// whatever earlier SSH install got this host running in the first place)
// and SFTP+sudo (selfUpdateOverTunnel replaces both).
func (m *Manager) installOverTunnel(ctx context.Context, hostID int64, host store.Host, job *installJob) error {
	report := job.append
	fail := func(err error) error {
		_ = m.db.SetHostStatus(ctx, hostID, store.HostStatusError, err.Error())
		return err
	}
	dial := m.dynamicRelayDial(hostID)

	parts := strings.SplitN(host.Arch, "/", 2)
	if len(parts) != 2 {
		return fail(fmt.Errorf("SSH недоступен, и для хоста ещё не известна архитектура для обновления через резервный канал"))
	}
	goos, goarch := parts[0], parts[1]
	report(fmt.Sprintf("SSH недоступен — обновляю через резервный канал (%s/%s)…", goos, goarch))

	binPath, err := m.ensureBinary(ctx, goos, goarch, report)
	if err != nil {
		return fail(err)
	}
	unitContent, err := m.loadUnitTemplate()
	if err != nil {
		return fail(err)
	}
	adminUser, adminPassword, err := m.resolveAdminCredential(ctx, hostID, host)
	if err != nil {
		return fail(err)
	}
	tun, err := m.prepareTunnelEnv(ctx, hostID, host)
	if err != nil {
		return fail(err)
	}
	envContent := renderEnv(adminUser, adminPassword, host.TerminalEnabled, tun)

	// A single attempt sometimes hits a transient stall on the relay itself
	// (read i/o timeout while parsing the multipart body) — plausible on
	// the very link degraded enough that SSH fell back to this channel in
	// the first place, and confirmed by users seeing it succeed instantly
	// on a manual second click. Safe to retry outright: a failed parse
	// never reaches install -D/systemctl restart (those run only after
	// ParseMultipartForm and the sha256 check both succeed), so there is no
	// partial state a retry could double-apply.
	const selfUpdateAttempts = 3
	var updateErr error
	for i := 0; i < selfUpdateAttempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return fail(fmt.Errorf("обновление через резервный канал не удалось: %w", updateErr))
			case <-time.After(2 * time.Second):
			}
			report("Повторяю отправку обновления через резервный канал…")
		}
		if updateErr = m.selfUpdateOverTunnel(ctx, hostID, dial, binPath, unitContent, envContent, report); updateErr == nil {
			break
		}
	}
	if updateErr != nil {
		return fail(updateErr)
	}

	report("Жду, пока сервис ответит на /health…")
	healthCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := waitForHealth(healthCtx, dial); err != nil {
		return fail(err)
	}

	report("Проверяю учётную запись администратора…")
	// waitForHealth just above only proves the freshly restarted host's
	// /health responds — it says nothing about whether the very next
	// request over the very same (or a moments-later-replaced) relay
	// session lands cleanly too. In practice it sometimes doesn't: right
	// after systemctl restart, the tunnel session itself can flap once
	// more (the old process's session closing a beat after health already
	// answered, or the new one still settling), and a login attempt that
	// lands in that gap fails with a bare connection error (EOF) that has
	// nothing to do with the credentials themselves. A single flaky
	// attempt here must not fail the whole job when the update itself
	// (the part that actually matters) already succeeded — a few quick
	// retries ride out exactly that window, the same way waitForHealth's
	// own loop already does for the health check right before this.
	const loginAttempts = 4
	var loginErr error
	for i := 0; i < loginAttempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return fail(fmt.Errorf("вход администратора через резервный канал не удался: %w", loginErr))
			case <-time.After(time.Second):
			}
		}
		if _, err := bootstrapLogin(ctx, dial, adminUser, adminPassword); err != nil {
			loginErr = err
			continue
		}
		loginErr = nil
		break
	}
	if loginErr != nil {
		return fail(fmt.Errorf("вход администратора через резервный канал не удался: %w", loginErr))
	}

	if err := m.db.SetHostVersion(ctx, hostID, m.version); err != nil {
		return fail(err)
	}
	_ = m.db.TouchHostSeen(ctx, hostID)
	if err := m.db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
		return fail(err)
	}

	report("Готово (через резервный канал)")
	return nil
}
