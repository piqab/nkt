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
// already works from any machine with Go installed.
func (m *Manager) ensureBinary(ctx context.Context, goos, goarch string, report func(string)) (string, error) {
	name := fmt.Sprintf("nkt-%s-%s-%s", goos, goarch, m.version)
	path := filepath.Join(m.cfg.HubBinCacheDir(), name)
	if _, err := os.Stat(path); err == nil {
		report(fmt.Sprintf("Использую уже собранный бинарник для %s/%s", goos, goarch))
		return path, nil
	}

	report(fmt.Sprintf("Собираю бинарник для %s/%s…", goos, goarch))
	if err := os.MkdirAll(m.cfg.HubBinCacheDir(), 0o750); err != nil {
		return "", fmt.Errorf("каталог кэша бинарников: %w", err)
	}

	cmd := exec.CommandContext(ctx, "go", "build",
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
// file to a freshly reachable host. unitContent is deploy/netknownsthat.
// service's own content, passed in by the caller rather than read from disk
// here, since the hub image may not keep the repository checkout at a
// predictable path relative to the running binary.
//
// Kept separate from activateService so the upload half — the genuinely new
// code here — can be exercised in a test against a plain SSH server with no
// systemd or root privileges involved at all.
func stageFiles(client *ssh.Client, localBinaryPath, unitContent, envContent, binPath, servicePath, envPath string, report func(string)) error {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("открытие SFTP: %w", err)
	}
	defer sftpClient.Close()

	report("Заливаю бинарник…")
	if err := uploadFile(sftpClient, localBinaryPath, binPath, 0o755); err != nil {
		return fmt.Errorf("заливка бинарника: %w", err)
	}

	report("Заливаю systemd-юнит и конфигурацию…")
	if err := uploadBytes(sftpClient, []byte(unitContent), servicePath, 0o644); err != nil {
		return fmt.Errorf("заливка systemd-юнита: %w", err)
	}
	if err := uploadBytes(sftpClient, []byte(envContent), envPath, 0o640); err != nil {
		return fmt.Errorf("заливка nkt.env: %w", err)
	}
	return nil
}

// activateService enables and (re)starts the freshly installed unit.
func activateService(client *ssh.Client, report func(string)) error {
	report("Запускаю systemd-сервис…")
	out, err := runRemote(client, "systemctl daemon-reload && systemctl enable --now netknownsthat")
	if err != nil {
		return fmt.Errorf("запуск сервиса: %w: %s", err, strings.TrimSpace(out))
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
