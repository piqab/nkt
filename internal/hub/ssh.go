package hub

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/store"
)

// sshDialTimeout bounds both the TCP connect and the SSH handshake.
const sshDialTimeout = 15 * time.Second

// hostKeyPin — ключ SSH хоста, запомненный при первом подключении
// (TOFU, как known_hosts у обычного ssh): Known — base64 публичного
// ключа из записи хоста (пусто — ещё не видели), Record — куда записать
// ключ при первом подключении. Подмена ключа на пути даёт
// HostKeyMismatchError, а не тихий вход с паролем к чужой машине.
type hostKeyPin struct {
	Host   string
	Known  string
	Record func(key string)
}

// HostKeyMismatchError — хост предъявил не тот ключ, что запомнен.
type HostKeyMismatchError struct {
	Host      string
	Known     string
	Presented string
}

func (e *HostKeyMismatchError) Error() string { return e.Unwrap().Error() }

// Unwrap — та же ошибка ключом каталога: API покажет её на языке
// читающего (msgs.Localize идёт по цепочке errors.As).
func (e *HostKeyMismatchError) Unwrap() error {
	return msgs.Errorf("hub.hostKeyChanged", e.Host, e.Known, e.Presented)
}

// hostKeyCallback — проверка по пину; без пина (nil) ключ принимается
// любой, как раньше (тесты и разовые подключения).
func hostKeyCallback(pin *hostKeyPin) ssh.HostKeyCallback {
	if pin == nil {
		// Сюда попадают только тесты (см. dialSSH): у боевых путей пин
		// есть всегда. Ключ принимается любой и нигде не запоминается.
		return ssh.InsecureIgnoreHostKey() //nolint:gosec // см. dialSSH
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		presented := base64.StdEncoding.EncodeToString(key.Marshal())
		if pin.Known == "" {
			if pin.Record != nil {
				pin.Record(presented)
			}
			return nil
		}
		if presented == pin.Known {
			return nil
		}
		return &HostKeyMismatchError{Host: pin.Host, Known: fingerprintOf(pin.Known), Presented: ssh.FingerprintSHA256(key)}
	}
}

// fingerprintOf — SHA256-отпечаток ключа из его base64.
func fingerprintOf(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "?"
	}
	key, err := ssh.ParsePublicKey(raw)
	if err != nil {
		return "?"
	}
	return ssh.FingerprintSHA256(key)
}

// dialSSH — подключение без пина ключа хоста. Только для тестов: у
// боевых путей пин есть всегда (dialHostDepth берёт его из записи
// хоста), поэтому отдельной функции без пина в рабочем коде нет.
func dialSSH(ctx context.Context, addr string, port int, user, authKind string, secret []byte) (*ssh.Client, error) {
	return dialSSHPinned(ctx, addr, port, user, authKind, secret, nil)
}

func dialSSHPinned(ctx context.Context, addr string, port int, user, authKind string, secret []byte, pin *hostKeyPin) (*ssh.Client, error) {
	cfg, err := sshClientConfig(user, authKind, secret, pin)
	if err != nil {
		return nil, err
	}
	target := net.JoinHostPort(addr, fmt.Sprintf("%d", port))
	dialer := net.Dialer{Timeout: sshDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, msgs.Errorf("hub.connecting", target, err)
	}
	return handshake(conn, target, cfg)
}

// dialSSHOver делает рукопожатие поверх уже открытого соединения —
// канала, пробитого через другой хост (см. dialHost). Сам канал закрывать
// здесь не нужно: он закроется вместе с клиентом.
func dialSSHOver(conn net.Conn, target, user, authKind string, secret []byte, pin *hostKeyPin) (*ssh.Client, error) {
	cfg, err := sshClientConfig(user, authKind, secret, pin)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return handshake(conn, target, cfg)
}

func handshake(conn net.Conn, target string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, target, cfg)
	if err != nil {
		_ = conn.Close()
		var mismatch *HostKeyMismatchError
		if errors.As(err, &mismatch) {
			// Не «рукопожатие не удалось», а внятное: ключ хоста сменился.
			return nil, mismatch
		}
		return nil, msgs.Errorf("hub.sshHandshake", target, err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// sshClientConfig собирает способ входа и общие настройки клиента.
func sshClientConfig(user, authKind string, secret []byte, pin *hostKeyPin) (*ssh.ClientConfig, error) {
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
		return nil, msgs.Errorf("hub.unknownSSHAccessMethod", authKind)
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: hostKeyCallback(pin),
		Timeout:         sshDialTimeout,
	}, nil
}

// runRemote executes one command over a fresh SSH session and returns its
// combined stdout+stderr, mirroring what an interactive shell would show.
//
// "export LC_ALL=C; " forces every tool's own diagnostic text (sudo's own
// messages above all — diagnoseInstallError pattern-matches them to decide
// whether to show the NOPASSWD setup hint) into English regardless of the
// remote host's configured locale — a host set up with, say, ru_RU.UTF-8
// has sudo print "требуется указать пароль" for what would be "a password
// is required" in C, which matched none of diagnoseInstallError's English
// substrings and silently fell through to the generic, unhelpful error
// instead of the actual instructions. `export` (not a `VAR=value cmd`
// prefix, which POSIX shell only applies to the single simple command
// immediately following it) so this reaches every command in a `&&`-
// chained string too, e.g. activateService's three separate `sudo -n
// systemctl ...` calls.
func runRemote(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", msgs.Errorf("hub.openingSSHSession", err)
	}
	defer session.Close()

	out, err := session.CombinedOutput("export LC_ALL=C; " + cmd)
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
		return "", "", msgs.Errorf("hub.unameRemoteHost", err, strings.TrimSpace(out))
	}
	lines := strings.Fields(out)
	if len(lines) < 2 {
		return "", "", msgs.Errorf("hub.unexpectedUnameOutput", out)
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
	return "", msgs.Errorf("hub.nktSupportsOnlyLinuxHosts", s)
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
		return "", msgs.Errorf("hub.unsupportedRemoteHostArchitecture", s)
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
		return msgs.Errorf("hub.puttyPpkKeyOpenSSHPEM")
	case strings.Contains(err.Error(), "passphrase protected"):
		return msgs.Errorf("hub.sshKeyPassphrase")
	case !strings.Contains(trimmed, "-----BEGIN"):
		return msgs.Errorf("hub.sshKeyNotPEM")
	default:
		return msgs.Errorf("hub.couldParsePrivateKey", err)
	}
}
