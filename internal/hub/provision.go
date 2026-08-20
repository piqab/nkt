package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	gopath "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Remote install locations — deliberately the exact paths
// deploy/netknownsthat.service and deploy/nkt.env.example already assume, so
// the unit installed by the hub behaves identically to a manual install.
const (
	remoteBinPath     = "/usr/local/bin/nkt"
	remoteEnvPath     = "/etc/netknownsthat/nkt.env"
	remoteServicePath = "/etc/systemd/system/netknownsthat.service"
	remoteDataDir     = "/var/lib/netknownsthat"
)

// sudoersDropIn is the sudoers file HUB.md tells an operator to create by
// hand for a non-root SSH user's NOPASSWD access — the one file
// RemoveSudoAccess is willing to delete. A NOPASSWD rule set up any other
// way (a different file, a direct /etc/sudoers edit) is left alone: this
// only ever removes what it can name exactly.
const sudoersDropIn = "/etc/sudoers.d/nkt-hub"

// resolveSourceRoot returns a directory that actually contains the nkt
// sources (detected by go.mod), or an actionable error. The configured
// HubSourceRoot defaults to the hub process's own working directory, which
// silently stops being the checkout the moment someone launches `nkt hub`
// from elsewhere (their home directory, a systemd unit with the wrong
// WorkingDirectory) — and the resulting `go: go.mod file not found` says
// nothing about how to fix it. So: trust the configured root when it has a
// go.mod, then fall back to where the hub's own executable lives and its
// parents (running ./nkt from inside a checkout, or bin/nkt one level
// down), and only then give up — naming the paths tried and the variable
// that fixes it.
func (m *Manager) resolveSourceRoot(report func(string)) (string, error) {
	hasGoMod := func(dir string) bool {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		return err == nil
	}

	if hasGoMod(m.cfg.HubSourceRoot) {
		return m.cfg.HubSourceRoot, nil
	}

	tried := []string{m.cfg.HubSourceRoot}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			for dir := filepath.Dir(exe); ; dir = filepath.Dir(dir) {
				if hasGoMod(dir) {
					report(fmt.Sprintf("Исходники не найдены в %s — использую %s (каталог бинарника хаба)", m.cfg.HubSourceRoot, dir))
					return dir, nil
				}
				tried = append(tried, dir)
				if dir == filepath.Dir(dir) {
					break
				}
			}
		}
	}

	return "", fmt.Errorf(
		"исходники nkt не найдены (нет go.mod ни в одном из: %s) — хабу нужен каталог с исходниками для "+
			"кросс-компиляции бинарников. Задайте NKT_HUB_SOURCE_ROOT абсолютным путём к клону репозитория "+
			"(без Docker — в /etc/netknownsthat/hub.env, затем systemctl restart netknownsthat-hub)",
		strings.Join(tried, ", "))
}

// ensureBinary returns the path to a static nkt binary built for goos/goarch
// at version, cross-compiling it into the hub's cache the first time a given
// combination is needed. Because modernc.org/sqlite is pure Go
// (CGO_ENABLED=0 throughout, see Makefile), this cross-compile never needs a
// C toolchain for the target — the same reason `make build GOARCH=...`
// already works from any machine with Go installed. The compiler itself is
// resolved by resolveGoBin, which self-installs one if NKT_HUB_GO_BIN
// doesn't already point at something that runs.
func (m *Manager) ensureBinary(ctx context.Context, goos, goarch string, report func(string)) (string, error) {
	name := fmt.Sprintf("nkt-%s-%s-%s", goos, goarch, m.version)
	path := filepath.Join(m.cfg.HubBinCacheDir(), name)
	if _, err := os.Stat(path); err == nil {
		report(fmt.Sprintf("Использую уже собранный бинарник для %s/%s", goos, goarch))
		return path, nil
	}

	sourceRoot, err := m.resolveSourceRoot(report)
	if err != nil {
		return "", err
	}

	goBin, err := m.resolveGoBin(ctx, report)
	if err != nil {
		return "", err
	}

	report(fmt.Sprintf("Собираю бинарник для %s/%s…", goos, goarch))
	if err := os.MkdirAll(m.cfg.HubBinCacheDir(), 0o750); err != nil {
		return "", fmt.Errorf("каталог кэша бинарников: %w", err)
	}

	cmd := exec.CommandContext(ctx, goBin, "build",
		// -buildvcs=false: go build otherwise shells out to `git status` to
		// stamp VCS info into the binary, which this process doesn't even
		// use (the version is already embedded explicitly via -X below) —
		// and that shell-out fails outright under the hub's own systemd
		// unit, which runs as root while the source checkout is normally
		// owned by whatever user cloned it: git's dubious-ownership check
		// refuses to touch a repo it doesn't own, and go build surfaces
		// that as an opaque "error obtaining VCS status: exit status 1"
		// with no indication it was ever about git ownership at all.
		"-buildvcs=false",
		"-trimpath", "-ldflags", "-s -w -X main.version="+m.version,
		"-o", path, "./cmd/nkt")
	cmd.Dir = sourceRoot
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if goarch == "arm" {
		// GOARCH=arm alone is ambiguous about the float ABI — GOARM picks
		// it, and the compiler needs telling explicitly. 6 is the same
		// safe baseline the Makefile's native-build already settled on
		// for the same architecture family: it runs on armv7 hardware too
		// (armv6 is a strict instruction subset), while building for 7
		// would refuse to start on a true armv6 board.
		cmd.Env = append(cmd.Env, "GOARM=6")
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("сборка бинарника для %s/%s: %w: %s", goos, goarch, err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// renderEnv builds /etc/netknownsthat/nkt.env for the remote host. Values
// beyond mode/addr/admin/terminal are left at defaults deploy/nkt.env.example
// itself falls back to. This file is regenerated from scratch and reuploaded
// by stageFiles on every install AND every "переустановить"/"обновить" — not
// only the first install — so an operator editing it by hand directly on the
// host does not stick; anything that needs to survive across updates belongs
// in the hosts table (like TerminalEnabled below) and gets rendered here
// instead.
//
// NKT_COOKIE_SECURE=false is deliberate, not an oversight: the hub only ever
// reaches this instance through the SSH tunnel dialed in tunnel.go, over
// plain HTTP on loopback — never through a browser directly. Requiring HTTPS
// there (the default) would make the remote nkt refuse to set the very
// session cookie the hub needs to capture in bootstrapLogin.
// tunnelEnvParams carries the reverse-tunnel fallback fields renderEnv
// writes into a host's env when the feature is actually configured for it
// — see Manager.prepareTunnelEnv, which builds this. The zero value
// (Enabled: false) means "write nothing", same as if the feature didn't
// exist for this host. Only ListenAddr and Token: since the hub is the
// side that dials out (see internal/hub/tunneldial.go), the host doesn't
// need to be told a hub address or its own id — it never identifies
// itself to anyone, it just accepts on ListenAddr and checks whatever
// token an incoming connection presents against Token.
type tunnelEnvParams struct {
	Enabled    bool
	ListenAddr string
	Token      string
}

func renderEnv(adminUser, adminPassword string, terminalEnabled bool, tun tunnelEnvParams) string {
	var b strings.Builder
	fmt.Fprintf(&b, "NKT_MODE=local\n")
	fmt.Fprintf(&b, "NKT_DATA_DIR=%s\n", remoteDataDir)
	fmt.Fprintf(&b, "NKT_ADDR=127.0.0.1:8077\n")
	fmt.Fprintf(&b, "NKT_BOOTSTRAP_ADMIN_USER=%s\n", adminUser)
	fmt.Fprintf(&b, "NKT_BOOTSTRAP_ADMIN_PASSWORD=%s\n", adminPassword)
	fmt.Fprintf(&b, "NKT_COOKIE_SECURE=false\n")
	fmt.Fprintf(&b, "NKT_TERMINAL_ENABLED=%t\n", terminalEnabled)
	if tun.Enabled {
		fmt.Fprintf(&b, "NKT_HUB_TUNNEL_LISTEN_ADDR=%s\n", tun.ListenAddr)
		fmt.Fprintf(&b, "NKT_HUB_TUNNEL_TOKEN=%s\n", tun.Token)
	}
	return b.String()
}

// generatePassword returns a URL-safe random string suitable as the
// bootstrap admin password the hub itself is the only reader of — nothing
// about it needs to be memorable, so entropy wins over the shorter,
// human-typeable passwords auth.GeneratePassword produces for people.
func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("генерация пароля: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// stageFiles uploads the nkt binary, its systemd unit and a generated env
// file to a freshly reachable host, then moves them into their real
// locations via the remote shell rather than writing there directly over
// SFTP. SFTP has no notion of privilege escalation — it can only write with
// whatever permissions sshUser already has — and on most cloud VPS images
// the SSH account is a non-root user with sudo, not root itself, so a
// direct SFTP write to /usr/local/bin or /etc/systemd/system would fail
// with a bare "permission denied" no matter how correct everything else is.
// Staging under a temp directory first (writable by any user) and moving
// into place with installRemoteFile (sudo when sshUser isn't already root)
// works for both cases uniformly.
//
// unitContent is deploy/netknownsthat.service's own content, passed in by
// the caller rather than read from disk here, since the hub image may not
// keep the repository checkout at a predictable path relative to the
// running binary.
//
// Kept separate from activateService so the upload half — the genuinely new
// code here — can be exercised in a test against a plain SSH server with no
// systemd or root privileges involved at all.
func stageFiles(client *ssh.Client, sshUser, localBinaryPath, unitContent, envContent, binPath, servicePath, envPath string, report func(string)) error {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("открытие SFTP: %w", err)
	}
	defer sftpClient.Close()

	tmpDir := fmt.Sprintf("/tmp/nkt-install-%d", time.Now().UnixNano())
	defer func() { _, _ = runRemote(client, "rm -rf "+tmpDir) }()

	report("Заливаю бинарник…")
	tmpBin := gopath.Join(tmpDir, "nkt")
	if err := uploadFile(sftpClient, localBinaryPath, tmpBin, 0o644); err != nil {
		return fmt.Errorf("заливка бинарника: %w", err)
	}

	report("Заливаю systemd-юнит и конфигурацию…")
	tmpUnit := gopath.Join(tmpDir, "netknownsthat.service")
	if err := uploadBytes(sftpClient, []byte(unitContent), tmpUnit, 0o644); err != nil {
		return fmt.Errorf("заливка systemd-юнита: %w", err)
	}
	tmpEnv := gopath.Join(tmpDir, "nkt.env")
	if err := uploadBytes(sftpClient, []byte(envContent), tmpEnv, 0o644); err != nil {
		return fmt.Errorf("заливка nkt.env: %w", err)
	}

	report("Устанавливаю файлы…")
	if err := installRemoteFile(client, sshUser, tmpBin, binPath, 0o755); err != nil {
		return fmt.Errorf("установка бинарника: %w", err)
	}
	if err := installRemoteFile(client, sshUser, tmpUnit, servicePath, 0o644); err != nil {
		return fmt.Errorf("установка systemd-юнита: %w", err)
	}
	if err := installRemoteFile(client, sshUser, tmpEnv, envPath, 0o640); err != nil {
		return fmt.Errorf("установка nkt.env: %w", err)
	}
	return nil
}

// installRemoteFile moves a file already staged under a temp path (writable
// by any user) into its real destination with the right mode, via `install
// -D` on the remote shell — creating any missing parent directories in the
// same step. Prefixed with `sudo -n` unless sshUser is already root: -n
// (non-interactive) makes sudo fail fast with a diagnosable message instead
// of hanging when it would need a password nothing here can supply.
func installRemoteFile(client *ssh.Client, sshUser, src, dst string, mode os.FileMode) error {
	cmd := fmt.Sprintf("install -D -m %#o %s %s", mode, src, dst)
	if sshUser != "root" {
		cmd = "sudo -n " + cmd
	}
	out, err := runRemote(client, cmd)
	if err != nil {
		return diagnoseInstallError(sshUser, dst, err, out)
	}
	return nil
}

// sudoersHint is the exact pair of commands an operator needs to run on the
// target host (as root, or as any account that already has sudo there) to
// grant sshUser passwordless sudo through the one drop-in file
// RemoveSudoAccess later knows how to clean up again. Spelled out in full
// here rather than left as prose in diagnoseInstallError's callers, since
// "add a rule to sudoers" without the actual command is not something
// someone can act on without going to look it up.
func sudoersHint(sshUser string) string {
	return fmt.Sprintf(
		"  echo '%s ALL=(ALL) NOPASSWD:ALL' | sudo tee %s\n  sudo chmod 0440 %s",
		sshUser, sudoersDropIn, sudoersDropIn)
}

// diagnoseInstallError turns a failed install/sudo command into something
// actionable: the two things that actually go wrong here are "no write
// access" (sshUser isn't root and there's no sudo at all) and "sudo needs a
// password" (no NOPASSWD rule) — both look like a bare, unhelpful
// "permission denied" or "a password is required" otherwise.
func diagnoseInstallError(sshUser, dst string, err error, out string) error {
	out = strings.TrimSpace(out)
	switch {
	case strings.Contains(out, "a password is required"), strings.Contains(out, "sudo: sorry, a password"):
		return fmt.Errorf(
			"установка %s: пользователю %q нужен sudo без пароля (NOPASSWD) — на хосте выполните:\n%s\n"+
				"либо укажите root как SSH-пользователя: %w: %s",
			dst, sshUser, sudoersHint(sshUser), err, out)
	case strings.Contains(out, "not in the sudoers file"):
		return fmt.Errorf(
			"установка %s: пользователь %q не может использовать sudo на этом хосте — на хосте выполните:\n%s\n"+
				"либо укажите root как SSH-пользователя: %w: %s",
			dst, sshUser, sudoersHint(sshUser), err, out)
	case strings.Contains(out, "Permission denied"), strings.Contains(out, "permission denied"):
		return fmt.Errorf(
			"установка %s: пользователю %q не хватает прав, а sudo недоступен — укажите root как "+
				"SSH-пользователя, либо на хосте выполните:\n%s\n: %w: %s",
			dst, sshUser, sudoersHint(sshUser), err, out)
	default:
		return fmt.Errorf("установка %s: %w: %s", dst, err, out)
	}
}

// sudoRequiresPassword reports whether err — as stageFiles/activateService
// return it, already run through diagnoseInstallError — means sudo itself
// is the problem (needs a password, or the account isn't in sudoers at
// all), as opposed to some unrelated failure (network, disk, a genuine bug)
// that happens to have hit the same sudo -n command. Checked against the
// wrapped message's own wording rather than re-deriving it, so it only ever
// agrees with what diagnoseInstallError already decided to tell the operator.
func sudoRequiresPassword(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "нужен sudo без пароля") || strings.Contains(msg, "не может использовать sudo")
}

// activateService enables and (re)starts the freshly installed unit —
// escalated the same way installRemoteFile is, since managing a systemd
// unit needs root just as much as writing under /etc/systemd/system does.
//
// When the unit fails to start, systemctl's own output is just "Job
// failed... see journalctl" — and the journal it points to lives on the
// remote host, which the operator may have no separate shell open to. The
// hub still holds the SSH connection that just failed, so it fetches the
// journal tail itself and puts the actual failure reason in the install
// log, instead of forwarding systemd's go-look-elsewhere message alone.
func activateService(client *ssh.Client, sshUser string, report func(string)) error {
	report("Запускаю systemd-сервис…")
	// `enable --now` is a no-op on a unit that's already active — on a host
	// this install/update already had once (i.e. every "обновить"/
	// "переустановить", not just the first install), the freshly rewritten
	// nkt.env/binary would sit on disk unused: the running process keeps
	// whatever environment it started with until something actually
	// restarts it. `enable` (idempotent either way) + `restart` fixes
	// that — `restart` on a unit that was never started behaves exactly
	// like `start`, so this is correct for a first install too.
	cmd := "systemctl daemon-reload && systemctl enable netknownsthat && systemctl restart netknownsthat"
	sudo := ""
	if sshUser != "root" {
		sudo = "sudo -n "
		cmd = sudo + "systemctl daemon-reload && " + sudo + "systemctl enable netknownsthat && " +
			sudo + "systemctl restart netknownsthat"
	}
	out, err := runRemote(client, cmd)
	if err != nil {
		if journal, jerr := runRemote(client,
			sudo+"journalctl -u netknownsthat -n 25 --no-pager -o cat"); jerr == nil {
			if journal = strings.TrimSpace(journal); journal != "" {
				out = strings.TrimSpace(out) + "\n--- журнал сервиса на хосте (journalctl -u netknownsthat) ---\n" + journal
			}
		}
		return diagnoseInstallError(sshUser, "netknownsthat.service", err, out)
	}
	return nil
}

// resetRemoteAdminPassword makes the remote's own admin account match
// adminPassword by running `nkt passwd` directly on the host over SSH — the
// fallback for when bootstrapLogin fails not because anything is actually
// broken, but because the remote's accounts table was already populated by
// an earlier install attempt (auth.Service.Bootstrap only ever runs once)
// with a password this run's env file no longer carries, or never matched
// in the first place (a reinstall after the hub's own record of the host
// was lost — a delete-and-re-add, say). The hub already has root/sudo SSH
// access to the host at this point — that's how the binary got there in
// the first place — so it can simply fix the mismatch itself instead of
// leaving the operator to track down and wipe the host's stale database
// by hand.
//
// The password travels base64-encoded specifically so the whole pipeline
// can be embedded in a `sudo -n sh -c '...'` wrapper with no nested quoting
// to get wrong — generatePassword's own alphabet (base64 URL, no shell
// metacharacters) would already be safe unquoted, but a password decrypted
// from a much older host record is worth not assuming anything about.
func resetRemoteAdminPassword(client *ssh.Client, sshUser, adminUser, adminPassword, dataDir, binPath string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(adminPassword))
	inner := fmt.Sprintf("echo %s | base64 -d | env NKT_MODE=local NKT_DATA_DIR=%s %s passwd %s",
		encoded, dataDir, binPath, adminUser)

	cmd := inner
	if sshUser != "root" {
		cmd = "sudo -n sh -c '" + inner + "'"
	}
	out, err := runRemote(client, cmd)
	if err != nil {
		return diagnoseInstallError(sshUser, "пароль администратора на хосте", err, out)
	}
	return nil
}

func uploadFile(sftpClient *sftp.Client, localPath, remotePath string, mode os.FileMode) error {
	local, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer local.Close()

	if err := sftpClient.MkdirAll(gopath.Dir(remotePath)); err != nil {
		return fmt.Errorf("создание каталога %s: %w", gopath.Dir(remotePath), err)
	}
	remote, err := sftpClient.Create(remotePath)
	if err != nil {
		return err
	}
	defer remote.Close()

	if _, err := remote.ReadFrom(local); err != nil {
		return err
	}
	return sftpClient.Chmod(remotePath, mode)
}

func uploadBytes(sftpClient *sftp.Client, data []byte, remotePath string, mode os.FileMode) error {
	if err := sftpClient.MkdirAll(gopath.Dir(remotePath)); err != nil {
		return fmt.Errorf("создание каталога %s: %w", gopath.Dir(remotePath), err)
	}
	remote, err := sftpClient.Create(remotePath)
	if err != nil {
		return err
	}
	defer remote.Close()

	if _, err := remote.Write(data); err != nil {
		return err
	}
	return sftpClient.Chmod(remotePath, mode)
}
