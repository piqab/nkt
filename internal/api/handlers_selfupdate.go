package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/althq/netknownsthat/internal/config"
)

// selfUpdateReadTimeout overrides the server-wide 30s http.Server.
// ReadTimeout (cmd/nkt/main.go) for this one request — reading the
// multipart body (a full ~16 MiB binary, plus the unit/env text) can
// legitimately take longer than that over the reverse-tunnel channel this
// endpoint exists for in the first place: by definition, it's only ever
// called when SSH itself is unreachable, so this is never the fast path to
// begin with, and a relayed yamux stream through the hub adds its own
// latency on top of whatever the network already has. The blanket 30s stays
// as-is for every other route — this is scoped to just this request via
// http.ResponseController, not a server-wide change.
const selfUpdateReadTimeout = 5 * time.Minute

// selfUpdateMaxRequestSize bounds the whole multipart request — nkt itself
// is a ~16 MiB static binary, this just guards against a runaway or
// garbled request rather than reflecting any real expected size.
const selfUpdateMaxRequestSize = 128 << 20 // 128 MiB

// Remote install locations — must match internal/hub/provision.go's own
// remoteBinPath/remoteServicePath/remoteEnvPath exactly: an SSH install and
// a self-update both have to produce the identical result on disk, or a
// host that gets one kind this time and the other kind next time would see
// its config silently regress. Duplicated rather than imported: internal/hub
// already imports internal/api (for the embedded Server), so the reverse
// import would cycle.
const (
	selfUpdateBinPath     = "/usr/local/bin/nkt"
	selfUpdateServicePath = "/etc/systemd/system/netknownsthat.service"
	selfUpdateEnvPath     = "/etc/netknownsthat/nkt.env"
)

// handleSelfUpdate replaces this host's own nkt binary, systemd unit and
// env file, then restarts the service — the reverse-tunnel-channel
// equivalent of what the hub's install() does over SSH+SFTP+sudo, used
// specifically when SSH itself is what's unreachable but this host's own
// tunnel session is still up (see internal/hub's selfUpdateOverTunnel).
// Reached through the same RequireAuth+RequireAdmin chain as any other
// mutating endpoint — the caller authenticates with this host's own
// bootstrap-admin session cookie exactly like every other hub-proxied
// request, nothing here trusts it any more than that.
//
// ModeLocal only: a demo/fixtures instance must never overwrite the binary
// running it, and the hub proxies to *managed hosts*, never to itself this
// way — a hub upgrades by redeploying its own container/binary directly.
//
// This process cannot write /usr/local/bin or /etc itself — ProtectSystem=
// strict on its own unit makes both read-only. The staged files are
// written under NKT_DATA_DIR (the one directory the unit's own
// ReadWritePaths already grants) rather than a temp dir, specifically
// because PrivateTmp=yes gives this process its own private /tmp that a
// freshly spawned systemd-run unit — a distinct unit, sharing no mount
// namespace with this one — would not see; NKT_DATA_DIR is a real host
// path either way, visible to both. The same systemd-run escape hatch
// already used for the terminal and package updates (see pty_session.go)
// then runs the actual file replacement + restart in that new,
// unrestricted transient unit.
func (s *Server) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode != config.ModeLocal {
		writeError(w, http.StatusForbidden, "самообновление доступно только в режиме local")
		return
	}

	// Ignoring the error deliberately: it only fails when the underlying
	// ResponseWriter doesn't support deadlines at all (not net/http's own
	// server — e.g. a test's httptest.ResponseRecorder) — the server-wide
	// 30s still applies in that case, which is exactly today's existing
	// behavior, nothing to report.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(selfUpdateReadTimeout))

	r.Body = http.MaxBytesReader(w, r.Body, selfUpdateMaxRequestSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "разбор запроса: "+err.Error())
		return
	}

	binFile, _, err := r.FormFile("binary")
	if err != nil {
		writeError(w, http.StatusBadRequest, "нет файла binary: "+err.Error())
		return
	}
	defer binFile.Close()

	unit := r.FormValue("unit")
	env := r.FormValue("env")
	wantSHA := r.FormValue("sha256")
	if unit == "" || env == "" || wantSHA == "" {
		writeError(w, http.StatusBadRequest, "запрос неполный: нужны unit, env и sha256")
		return
	}

	stageDir, err := os.MkdirTemp(s.cfg.DataDir, "selfupdate-")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "каталог для обновления: "+err.Error())
		return
	}
	// Removed by the background script itself on success (its files must
	// still exist when the new, separate transient unit reads them, well
	// after this handler has already returned) — this cleans up only the
	// failure paths that return before ever starting it.
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	stageBin := filepath.Join(stageDir, "nkt")
	sum := sha256.New()
	out, err := os.OpenFile(stageBin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "запись бинарника: "+err.Error())
		return
	}
	_, copyErr := io.Copy(io.MultiWriter(out, sum), binFile)
	closeErr := out.Close()
	if copyErr != nil {
		writeError(w, http.StatusInternalServerError, "запись бинарника: "+copyErr.Error())
		return
	}
	if closeErr != nil {
		writeError(w, http.StatusInternalServerError, "запись бинарника: "+closeErr.Error())
		return
	}
	if gotSHA := hex.EncodeToString(sum.Sum(nil)); gotSHA != wantSHA {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"контрольная сумма бинарника не совпала (получено %s, ожидалось %s) — передача повреждена, попробуйте ещё раз",
			gotSHA, wantSHA))
		return
	}

	stageUnit := filepath.Join(stageDir, "netknownsthat.service")
	if err := os.WriteFile(stageUnit, []byte(unit), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "запись systemd-юнита: "+err.Error())
		return
	}
	stageEnv := filepath.Join(stageDir, "nkt.env")
	if err := os.WriteFile(stageEnv, []byte(env), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "запись nkt.env: "+err.Error())
		return
	}

	script := fmt.Sprintf(`set -e
install -D -m 0755 %s %s
install -D -m 0644 %s %s
install -D -m 0640 %s %s
systemctl daemon-reload
systemctl enable netknownsthat
rm -rf %s
sleep 1
systemctl restart netknownsthat
`,
		stageBin, selfUpdateBinPath,
		stageUnit, selfUpdateServicePath,
		stageEnv, selfUpdateEnvPath,
		stageDir)

	cmd := unrestrictedBackgroundCommand("bash", "-c", script)
	if err := cmd.Start(); err != nil {
		writeError(w, http.StatusInternalServerError, "запуск обновления: "+err.Error())
		return
	}
	// Not Wait()'d, and the stage dir is now cleaned up by the script
	// itself, not the defer above: systemctl restart near the end of the
	// script kills this very process partway through — well before a
	// Wait() here could ever return — so nothing past cmd.Start() can be
	// allowed to depend on this handler still being alive to run it.
	removeStage = false
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
