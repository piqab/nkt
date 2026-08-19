package hub

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/althq/netknownsthat/internal/store"
)

// sshDialTimeout bounds both the TCP connect and the SSH handshake.
const sshDialTimeout = 15 * time.Second

// dialSSH opens an authenticated SSH connection to a host, using its
// decrypted secret (an SSH password or a PEM-encoded private key, depending
// on authKind).
//
// HostKeyCallback intentionally accepts whatever key the host presents:
// there is no known_hosts entry to check on first contact, the same
// trust-on-first-use trade-off an interactive `ssh` makes. A future
// iteration could pin the host key after this first successful connect and
// verify it on every later one — not done here to keep the first
// installable version simple.
func dialSSH(ctx context.Context, addr string, port int, user, authKind string, secret []byte) (*ssh.Client, error) {
	var auth ssh.AuthMethod
	switch authKind {
	case store.HostAuthPassword:
		auth = ssh.Password(string(secret))
	case store.HostAuthKey:
		signer, err := ssh.ParsePrivateKey(secret)
		if err != nil {
			return nil, diagnoseKeyError(string(secret), err)
		}
		auth = ssh.PublicKeys(signer)
	default:
		return nil, fmt.Errorf("неизвестный способ входа по SSH: %q", authKind)
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // see doc comment above
		Timeout:         sshDialTimeout,
	}

	target := net.JoinHostPort(addr, fmt.Sprintf("%d", port))
	dialer := net.Dialer{Timeout: sshDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("подключение к %s: %w", target, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, target, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SSH-рукопожатие с %s: %w", target, err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// runRemote executes one command over a fresh SSH session and returns its
// combined stdout+stderr, mirroring what an interactive shell would show.
func runRemote(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("открытие SSH-сессии: %w", err)
	}
	defer session.Close()

	out, err := session.CombinedOutput(cmd)
	return string(out), err
}

// probeExistingInstall checks, over an already-open SSH connection, whether
// the target already has an nkt on it — binary present and runnable and/or
// its systemd unit active — without installing or changing anything. Both
// results are best-effort: `; true` at the end keeps the whole command's
// exit code 0 regardless of `systemctl is-active`'s own (nonzero for
// "inactive" is entirely normal, not a probe failure), so a non-nil err
// here only ever means the SSH session itself failed, never "found
// nothing" — the caller tells those apart by the empty-string results.
func probeExistingInstall(client *ssh.Client, binPath, unitName string) (versionLine, activeState string, err error) {
	const sep = "___NKT_HUB_PROBE_SEP___"
	out, err := runRemote(client,
		"test -x "+binPath+" && "+binPath+" version 2>/dev/null; echo "+sep+"; "+
			"systemctl is-active "+unitName+" 2>/dev/null; true")
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(out, sep, 2)
	versionLine = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		activeState = strings.TrimSpace(parts[1])
	}
	return versionLine, activeState, nil
}

// detectTarget reports the remote host's OS and CPU architecture in Go's
// own GOOS/GOARCH vocabulary, so the caller knows what to cross-compile.
func detectTarget(client *ssh.Client) (goos, goarch string, err error) {
	out, err := runRemote(client, "uname -s; uname -m")
	if err != nil {
		return "", "", fmt.Errorf("uname на удалённом хосте: %w: %s", err, strings.TrimSpace(out))
	}
	lines := strings.Fields(out)
	if len(lines) < 2 {
		return "", "", fmt.Errorf("неожиданный вывод uname: %q", out)
	}
	goos, err = mapUnameOS(lines[0])
	if err != nil {
		return "", "", err
	}
	goarch, err = mapUnameArch(lines[1])
	if err != nil {
		return "", "", err
	}
	return goos, goarch, nil
}

// mapUnameOS translates `uname -s` output to a Go GOOS value. nkt only ever
// runs on Linux (see internal/collect/factory.go), so anything else is
// rejected here rather than producing a binary that can never actually work.
func mapUnameOS(s string) (string, error) {
	if strings.EqualFold(s, "Linux") {
		return "linux", nil
	}
	return "", fmt.Errorf("nkt поддерживает только Linux-хосты, а удалённая ОС — %q", s)
}

// mapUnameArch translates `uname -m` output to a Go GOARCH value, covering
// the architectures real VPS offerings actually use, plus 32-bit ARM SBCs
// (Raspberry Pi and similar) — uname reports armv6l/armv7l/armv7 depending
// on hardware and distro, but Go collapses all of them into one GOARCH,
// "arm" (the float-ABI distinction between v6/v7 is GOARM, a separate build
// setting — see ensureBinary).
func mapUnameArch(s string) (string, error) {
	switch s {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	case "armv6l", "armv7l", "armv7", "arm":
		return "arm", nil
	default:
		return "", fmt.Errorf("неподдерживаемая архитектура удалённого хоста: %q", s)
	}
}

// validatePrivateKey checks a pasted secret parses as an SSH private key
// before it is ever stored, so a malformed key is caught right in the
// "добавить хост"/"изменить" form instead of only surfacing much later, as
// StartInstall's bare "ssh: no key found" — a message that gives no hint
// about which of several unrelated causes actually produced it.
func validatePrivateKey(secret string) error {
	if _, err := ssh.ParsePrivateKey([]byte(secret)); err != nil {
		return diagnoseKeyError(secret, err)
	}
	return nil
}

// diagnoseKeyError turns ssh.ParsePrivateKey's error into something a person
// can act on, covering the mistakes that are actually common here: a PuTTY
// .ppk file pasted where OpenSSH PEM was expected, a passphrase-protected
// key (auth.PublicKeys alone can never supply one), or a public key/garbled
// paste where a private key belongs.
func diagnoseKeyError(secret string, err error) error {
	trimmed := strings.TrimSpace(secret)
	switch {
	case strings.Contains(trimmed, "PuTTY-User-Key-File"):
		return fmt.Errorf(
			"это ключ в формате PuTTY (.ppk), а не OpenSSH PEM — экспортируйте его через " +
				"puttygen: Conversions → Export OpenSSH key, и вставьте результат")
	case strings.Contains(err.Error(), "passphrase protected"):
		return fmt.Errorf(
			"ключ защищён паролем — сейчас это не поддерживается; снимите пароль " +
				`(ssh-keygen -p -N "" -f <файл>) и вставьте ключ снова`)
	case !strings.Contains(trimmed, "-----BEGIN"):
		return fmt.Errorf(
			`не похоже на приватный ключ в формате PEM (нет строки "-----BEGIN ... PRIVATE KEY-----") ` +
				"— проверьте, не вставлен ли публичный ключ (.pub) или обрезанный текст")
	default:
		return fmt.Errorf("не удалось разобрать приватный ключ: %w", err)
	}
}
