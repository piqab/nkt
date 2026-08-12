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
)

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

	goBin, err := m.resolveGoBin(ctx, report)
	if err != nil {
		return "", err
	}

	report(fmt.Sprintf("Собираю бинарник для %s/%s…", goos, goarch))
	if err := os.MkdirAll(m.cfg.HubBinCacheDir(), 0o750); err != nil {
		return "", fmt.Errorf("каталог кэша бинарников: %w", err)
	}

	cmd := exec.CommandContext(ctx, goBin, "build",
		"-trimpath", "-ldflags", "-s -w -X main.version="+m.version,
		"-o", path, "./cmd/nkt")
	cmd.Dir = m.cfg.HubSourceRoot
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")

	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("сборка бинарника для %s/%s: %w: %s", goos, goarch, err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// renderEnv builds /etc/netknownsthat/nkt.env for the remote host. Values
// beyond mode/addr/admin are left at defaults deploy/nkt.env.example itself
// falls back to — an operator who needs to inspect a non-default nginx/
// haproxy layout on that host can still edit the file by hand afterward.
//
// NKT_COOKIE_SECURE=false is deliberate, not an oversight: the hub only ever
// reaches this instance through the SSH tunnel dialed in tunnel.go, over
// plain HTTP on loopback — never through a browser directly. Requiring HTTPS
// there (the default) would make the remote nkt refuse to set the very
// session cookie the hub needs to capture in bootstrapLogin.
func renderEnv(adminUser, adminPassword string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "NKT_MODE=local\n")
	fmt.Fprintf(&b, "NKT_DATA_DIR=/var/lib/netknownsthat\n")
	fmt.Fprintf(&b, "NKT_ADDR=127.0.0.1:8077\n")
	fmt.Fprintf(&b, "NKT_BOOTSTRAP_ADMIN_USER=%s\n", adminUser)
	fmt.Fprintf(&b, "NKT_BOOTSTRAP_ADMIN_PASSWORD=%s\n", adminPassword)
	fmt.Fprintf(&b, "NKT_COOKIE_SECURE=false\n")
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
			"установка %s: пользователю %q нужен sudo без пароля (NOPASSWD) — добавьте правило в sudoers "+
				"на хосте, либо укажите root как SSH-пользователя: %w: %s", dst, sshUser, err, out)
	case strings.Contains(out, "not in the sudoers file"):
		return fmt.Errorf(
			"установка %s: пользователь %q не может использовать sudo на этом хосте — добавьте его в sudoers "+
				"с NOPASSWD, либо укажите root как SSH-пользователя: %w: %s", dst, sshUser, err, out)
	case strings.Contains(out, "Permission denied"), strings.Contains(out, "permission denied"):
		return fmt.Errorf(
			"установка %s: пользователю %q не хватает прав, а sudo недоступен — укажите root как "+
				"SSH-пользователя, либо дайте %s sudo с NOPASSWD: %w: %s", dst, sshUser, sshUser, err, out)
	default:
		return fmt.Errorf("установка %s: %w: %s", dst, err, out)
	}
}

// activateService enables and (re)starts the freshly installed unit —
// escalated the same way installRemoteFile is, since managing a systemd
// unit needs root just as much as writing under /etc/systemd/system does.
func activateService(client *ssh.Client, sshUser string, report func(string)) error {
	report("Запускаю systemd-сервис…")
	cmd := "systemctl daemon-reload && systemctl enable --now netknownsthat"
	if sshUser != "root" {
		cmd = "sudo -n systemctl daemon-reload && sudo -n systemctl enable --now netknownsthat"
	}
	out, err := runRemote(client, cmd)
	if err != nil {
		return diagnoseInstallError(sshUser, "netknownsthat.service", err, out)
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
