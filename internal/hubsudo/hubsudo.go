// Package hubsudo — узкий sudo для хаба: `nkt hub-sudo` выполняет под root
// только запросы, подписанные ключом хаба, и только операции из списка
// ниже. Правило sudoers разрешает пользователю хаба без пароля одну эту
// команду, а не всё.
//
// Подписано всё, что может дать root: операция, её аргументы, хэши
// привезённых файлов (бинарник, юнит и env nkt — через любой из них можно
// получить root) и порядковый номер. Пользователь хоста вызвать hub-sudo
// может, но без закрытого ключа хаба ничего не выполнит; повтор уже
// выполненного запроса отвергается по номеру.
package hubsudo

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Пути на хосте: где лежит ключ, номер и куда ставится nkt. Совпадают с
// установкой хаба (internal/hub/provision.go).
const (
	PubKeyPath     = "/etc/nkt/hub-sign.pub"
	StateDir       = "/var/lib/nkt-hub-sudo"
	BinPath        = "/usr/local/bin/nkt"
	ServicePath    = "/etc/systemd/system/netknownsthat.service"
	EnvPath        = "/etc/netknownsthat/nkt.env"
	DataDir        = "/var/lib/netknownsthat"
	Unit           = "netknownsthat"
	SudoersPath    = "/etc/sudoers.d/nkt-hub"
	AptProxyConf   = "/etc/apt/apt.conf.d/99nkt-hub-proxy"
	AptProxyDetect = "/usr/local/bin/nkt-apt-proxy"
)

// Command — то, что разрешает правило sudoers (ровно эти слова).
const Command = BinPath + " hub-sudo"

// Операции.
const (
	OpPing     = "ping"
	OpInstall  = "install"
	OpActivate = "activate"
	OpService  = "service"
	OpJournal  = "journal"
	OpPasswd   = "passwd"
	OpAptProxy = "aptproxy"
	OpSudoers  = "sudoers-remove"
	OpPurge    = "purge"
	OpClamAV   = "clamav"
	// OpRekey — заменить открытый ключ хаба на хосте (переезд на новый
	// хаб). Подписывается текущим, то есть старым ключом: доверие
	// передаёт тот, кому хост уже доверяет.
	OpRekey = "rekey"
)

// Пути очистки и ClamAV.
const (
	SSHDropIn       = "/etc/ssh/sshd_config.d/99-nkt-no-password.conf"
	EnvDir          = "/etc/netknownsthat"
	LogDir          = "/var/log/netknownsthat"
	ClamDir         = "/var/lib/clamav"
	ClamStagePrefix = "/tmp/nkt-clamdb-"
)

// ClamFiles — файлы базы ClamAV, которые хаб привозит.
var ClamFiles = []string{"main.cvd", "daily.cvd", "bytecode.cvd"}

// Ops — все операции (для окна «что разрешено хабу»).
var Ops = []string{OpPing, OpInstall, OpActivate, OpService, OpJournal, OpPasswd, OpAptProxy, OpClamAV, OpPurge, OpSudoers, OpRekey}

// Request — подписываемая часть.
type Request struct {
	Op     string            `json:"op"`
	Args   map[string]string `json:"args,omitempty"`
	Files  map[string]string `json:"files,omitempty"` // имя → sha256 hex
	Serial int64             `json:"serial"`
}

// Envelope — запрос и подпись; уходит в hub-sudo через stdin.
type Envelope struct {
	Request json.RawMessage `json:"request"`
	Sig     string          `json:"sig"`
}

// KeyFromSecret — ключ подписи хаба из его мастер-ключа (отдельно не
// хранится; хаб с тем же мастер-ключом подписывает тем же ключом).
func KeyFromSecret(secret []byte) ed25519.PrivateKey {
	seed := sha256.Sum256(append([]byte("nkt-hub-sign-v1\x00"), secret...))
	return ed25519.NewKeyFromSeed(seed[:])
}

// PublicText — открытый ключ строкой для PubKeyPath.
func PublicText(priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)) + "\n"
}

// Sign — конверт запроса.
func Sign(priv ed25519.PrivateKey, req Request) ([]byte, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Request: raw, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw))})
}

// Ошибки проверки.
var (
	ErrNoKey     = errors.New("hub-sudo: no hub key on this host")
	ErrSignature = errors.New("hub-sudo: bad signature")
)

// ErrSerial — номер не больше уже выполненного (повтор или часы хаба
// ушли назад); Last — последний принятый, хаб повторяет с Last+1.
type ErrSerial struct{ Last int64 }

func (e ErrSerial) Error() string {
	return "hub-sudo: stale serial, last " + strconv.FormatInt(e.Last, 10)
}

// Verify — подпись открытым ключом pub.
func Verify(pub ed25519.PublicKey, envelope []byte) (Request, error) {
	var env Envelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return Request{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(env.Sig)
	if err != nil || !ed25519.Verify(pub, env.Request, sig) {
		return Request{}, ErrSignature
	}
	var req Request
	if err := json.Unmarshal(env.Request, &req); err != nil {
		return Request{}, err
	}
	return req, nil
}

// LoadPub — открытый ключ хаба с хоста.
func LoadPub(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrNoKey
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, ErrNoKey
	}
	return ed25519.PublicKey(b), nil
}

// Host — где hub-sudo выполняет операции (в тестах — во временном
// каталоге, без root).
type Host struct {
	PubKey   string
	StateDir string
	// Root — приставка ко всем путям установки (тесты); пусто — «/».
	Root string
	// Run — запуск программы (тесты подменяют systemctl и прочее).
	Run func(stdin io.Reader, name string, args ...string) (string, error)
}

// System — настоящий хост.
func System() Host {
	return Host{PubKey: PubKeyPath, StateDir: StateDir, Run: func(stdin io.Reader, name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		cmd.Stdin = stdin
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}}
}

func (h Host) path(p string) string { return filepath.Join(h.Root, p) }

var (
	stageRe   = regexp.MustCompile(`^/tmp/nkt-install-[0-9]{1,24}$`)
	clamRe    = regexp.MustCompile(`^/tmp/nkt-clamdb-[0-9]{1,24}$`)
	keyBodyRe = regexp.MustCompile(`^[A-Za-z0-9+/=]{16,1000}$`)
	userRe    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	actionRe  = regexp.MustCompile(`^(start|stop|restart)$`)
	hexRe     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// installFiles — что ставит OpInstall по хэшам: имя в каталоге подготовки
// → куда и с какими правами. nkt.env (в нём пароль админа) сюда не входит:
// его содержимое приходит в самом подписанном запросе (Args["env"]) и в
// каталог подготовки не кладётся.
var installFiles = []struct {
	name, dest string
	mode       os.FileMode
}{
	{"nkt", BinPath, 0o755},
	{"netknownsthat.service", ServicePath, 0o644},
}

// envMax — предел nkt.env в запросе.
const envMax = 64 << 10

// Execute — проверка конверта и операция; вывод — для журнала хаба.
func (h Host) Execute(envelope []byte) (string, error) {
	pub, err := LoadPub(h.PubKey)
	if err != nil {
		return "", err
	}
	req, err := Verify(pub, envelope)
	if err != nil {
		return "", err
	}
	if err := h.acceptSerial(req.Serial); err != nil {
		return "", err
	}
	switch req.Op {
	case OpPing:
		return "ok", nil
	case OpInstall:
		return h.install(req)
	case OpActivate:
		if out, err := h.Run(nil, "systemctl", "daemon-reload"); err != nil {
			return out, err
		}
		if out, err := h.Run(nil, "systemctl", "enable", Unit); err != nil {
			return out, err
		}
		return h.Run(nil, "systemctl", "restart", Unit)
	case OpService:
		a := req.Args["action"]
		if !actionRe.MatchString(a) {
			return "", fmt.Errorf("hub-sudo: bad action %q", a)
		}
		return h.Run(nil, "systemctl", a, Unit)
	case OpJournal:
		return h.Run(nil, "journalctl", "-u", Unit, "-n", "25", "--no-pager", "-o", "cat")
	case OpPasswd:
		user, pw := req.Args["user"], req.Args["password"]
		if !userRe.MatchString(user) || pw == "" {
			return "", errors.New("hub-sudo: bad passwd arguments")
		}
		return h.Run(strings.NewReader(pw+"\n"), "env", "NKT_MODE=local", "NKT_DATA_DIR="+DataDir, h.path(BinPath), "passwd", user)
	case OpAptProxy:
		return h.aptProxy(req)
	case OpPurge:
		return h.purge(req)
	case OpClamAV:
		return h.clamav(req)
	case OpSudoers:
		if err := os.Remove(h.path(SudoersPath)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return "removed", nil
	case OpRekey:
		return h.rekey(req)
	}
	return "", fmt.Errorf("hub-sudo: unknown operation %q", req.Op)
}

// acceptSerial — номер строго больше последнего; запоминается до выполнения
// (повтор того же запроса после сбоя посередине тоже отвергается).
func (h Host) acceptSerial(serial int64) error {
	if err := os.MkdirAll(h.StateDir, 0o700); err != nil {
		return err
	}
	p := filepath.Join(h.StateDir, "serial")
	var last int64
	if raw, err := os.ReadFile(p); err == nil {
		last, _ = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	}
	if serial <= last {
		return ErrSerial{Last: last}
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(serial, 10)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// install — файлы из каталога подготовки: каждый сначала копируется в
// каталог root (подменить его после проверки пользователь уже не может),
// сверяется с подписанным хэшем и только потом ставится на место.
func (h Host) install(req Request) (string, error) {
	stage := req.Args["stage"]
	if !stageRe.MatchString(stage) {
		return "", fmt.Errorf("hub-sudo: bad stage dir %q", stage)
	}
	work, err := os.MkdirTemp(h.StateDir, "install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	type ready struct {
		src, dest string
		mode      os.FileMode
	}
	env := req.Args["env"]
	if env == "" || len(env) > envMax || strings.ContainsRune(env, 0) {
		return "", errors.New("hub-sudo: no nkt.env in the signed request")
	}
	envCopy := filepath.Join(work, "nkt.env")
	if err := os.WriteFile(envCopy, []byte(env), 0o600); err != nil {
		return "", err
	}
	list := []ready{{envCopy, h.path(EnvPath), 0o640}}
	for _, f := range installFiles {
		want := req.Files[f.name]
		if !hexRe.MatchString(want) {
			return "", fmt.Errorf("hub-sudo: no signed hash for %s", f.name)
		}
		copyTo := filepath.Join(work, f.name)
		got, err := copyHash(h.path(filepath.Join(stage, f.name)), copyTo)
		if err != nil {
			return "", err
		}
		if got != want {
			return "", fmt.Errorf("hub-sudo: %s does not match the signed hash", f.name)
		}
		list = append(list, ready{copyTo, h.path(f.dest), f.mode})
	}
	for _, r := range list {
		if err := placeFile(r.src, r.dest, r.mode); err != nil {
			return "", err
		}
	}
	return "installed", nil
}

// copyHash — копия src в dst (0600) и sha256 копии. Символьная ссылка в
// каталоге подготовки не принимается.
func copyHash(src, dst string) (string, error) {
	fi, err := os.Lstat(src)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("hub-sudo: %s is not a regular file", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, 512<<20)); err != nil {
		return "", errors.Join(err, out.Close())
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// placeFile — файл на место: копия рядом и переименование (запущенный
// бинарник заменяется целиком, а не переписывается поверх).
func placeFile(src, dest string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".nkt-new"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return errors.Join(err, out.Close())
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// aptProxy — конфиг apt для кэша пакетов хаба: порт на loopback (обратный
// проброс хаба) или удаление.
func (h Host) aptProxy(req Request) (string, error) {
	if req.Args["enabled"] != "true" {
		for _, p := range []string{AptProxyConf, AptProxyDetect} {
			if err := os.Remove(h.path(p)); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
		return "removed", nil
	}
	port, err := strconv.Atoi(req.Args["port"])
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("hub-sudo: bad apt proxy port")
	}
	detect, conf := AptProxyDetectScript(port), AptProxyConfText
	if err := os.MkdirAll(filepath.Dir(h.path(AptProxyDetect)), 0o755); err != nil {
		return "", err
	}
	if err := writeFile(h.path(AptProxyDetect), detect, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(h.path(AptProxyConf)), 0o755); err != nil {
		return "", err
	}
	return "configured", writeFile(h.path(AptProxyConf), conf, 0o644)
}

func writeFile(p, content string, mode os.FileMode) error {
	tmp := p + ".nkt-new"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// SudoersRule — узкое правило для пользователя хаба.
func SudoersRule(user string) string {
	return "# nkt: хабу без пароля — только подписанные им операции (nkt hub-sudo).\n" +
		user + " ALL=(root) NOPASSWD: " + Command + "\n"
}

// FileHash — sha256 файла (для подписи на хабе).
func FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// BytesHash — sha256 содержимого.
func BytesHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// AptProxyDetectScript — скрипт для apt: bash (только у него /dev/tcp);
// DIRECT при закрытом порте — штатное поведение apt: без хаба пакеты
// качаются как обычно.
func AptProxyDetectScript(port int) string {
	return fmt.Sprintf(`#!/bin/bash
# nkt: apt через кэш пакетов хаба, если хаб сейчас держит проброс порта.
if (exec 3<>/dev/tcp/127.0.0.1/%d) 2>/dev/null; then
  exec 3>&-
  echo "http://127.0.0.1:%d"
else
  echo DIRECT
fi
`, port, port)
}

// AptProxyConfText — конфиг apt.
const AptProxyConfText = `// nkt: кэш пакетов хаба; скрипт отвечает DIRECT, когда хаб не подключён.
Acquire::http::Proxy-Auto-Detect "` + AptProxyDetect + `";
`

// clamav — база ClamAV из каталога подготовки по подписанным хэшам: файлы
// принадлежат clamav (freshclam потом обновляет их сам), служба на время
// замены остановлена.
func (h Host) clamav(req Request) (string, error) {
	stage := req.Args["stage"]
	if !clamRe.MatchString(stage) {
		return "", fmt.Errorf("hub-sudo: bad clamav stage %q", stage)
	}
	work, err := os.MkdirTemp(h.StateDir, "clamav-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	var names []string
	for _, name := range ClamFiles {
		want, ok := req.Files[name]
		if !ok {
			continue
		}
		if !hexRe.MatchString(want) {
			return "", fmt.Errorf("hub-sudo: bad hash for %s", name)
		}
		got, err := copyHash(h.path(filepath.Join(stage, name)), filepath.Join(work, name))
		if err != nil {
			return "", err
		}
		if got != want {
			return "", fmt.Errorf("hub-sudo: %s does not match the signed hash", name)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", errors.New("hub-sudo: no clamav files")
	}
	_, _ = h.Run(nil, "systemctl", "stop", "clamav-freshclam")
	defer func() { _, _ = h.Run(nil, "systemctl", "start", "clamav-freshclam") }()
	for _, name := range names {
		dest := h.path(filepath.Join(ClamDir, name))
		if err := placeFile(filepath.Join(work, name), dest, 0o644); err != nil {
			return "", err
		}
		_, _ = h.Run(nil, "chown", "clamav:clamav", dest)
	}
	return "installed " + strings.Join(names, ", "), nil
}

// purge — очистка хоста при удалении из хаба, одним вызовом и в том же
// порядке, что без узкого sudo: после неё ни бинарника, ни правила уже
// нет. Каждый шаг — строка «ok|fail шаг: вывод»; провал шага не
// останавливает остальные (как и раньше).
func (h Host) purge(req Request) (string, error) {
	on := func(k string) bool { return req.Args[k] == "true" }
	var lines []string
	step := func(name string, fn func() (string, error)) {
		out, err := fn()
		if err != nil {
			lines = append(lines, "fail "+name+": "+strings.TrimSpace(out+" "+err.Error()))
			return
		}
		lines = append(lines, "ok "+name)
	}
	rm := func(paths ...string) func() (string, error) {
		return func() (string, error) {
			for _, p := range paths {
				if err := os.RemoveAll(h.path(p)); err != nil {
					return "", err
				}
			}
			return "", nil
		}
	}
	if on("restore_password") {
		step("restore_password", func() (string, error) {
			if _, err := os.Stat(h.path(SSHDropIn)); err != nil {
				return "", nil
			}
			if err := os.Remove(h.path(SSHDropIn)); err != nil {
				return "", err
			}
			if out, err := h.Run(nil, "sshd", "-t"); err != nil {
				return out, err
			}
			if out, err := h.Run(nil, "systemctl", "reload", "ssh"); err != nil {
				return h.Run(nil, "systemctl", "reload", "sshd")
			} else {
				return out, nil
			}
		})
	}
	if on("service") {
		step("service", func() (string, error) {
			_, _ = h.Run(nil, "systemctl", "disable", "--now", Unit)
			if _, err := rm(ServicePath, BinPath, EnvDir)(); err != nil {
				return "", err
			}
			return h.Run(nil, "systemctl", "daemon-reload")
		})
	}
	if on("data") {
		step("data", rm(DataDir, LogDir))
	}
	if on("access") {
		step("access", func() (string, error) {
			if body := req.Args["key_body"]; body != "" {
				user := req.Args["user"]
				if !keyBodyRe.MatchString(body) || !userRe.MatchString(user) {
					return "", errors.New("bad key arguments")
				}
				if err := h.dropAuthorizedKey(user, body); err != nil {
					return "", err
				}
			}
			return rm(SudoersPath, filepath.Dir(PubKeyPath), StateDir)()
		})
	}
	if on("delete_user") {
		user := req.Args["user"]
		step("delete_user", func() (string, error) {
			if !userRe.MatchString(user) || user == "root" {
				return "", errors.New("bad user")
			}
			return h.Run(nil, "userdel", "-r", user)
		})
	}
	return strings.Join(lines, "\n"), nil
}

// dropAuthorizedKey — убрать из authorized_keys пользователя ровно строки
// с этим телом ключа; остальные (чей-то рабочий доступ) остаются.
func (h Host) dropAuthorizedKey(user, body string) error {
	home := "/home/" + user
	if user == "root" {
		home = "/root"
	}
	p := h.path(filepath.Join(home, ".ssh", "authorized_keys"))
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var keep []string
	for _, l := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[1] == body {
			continue
		}
		keep = append(keep, l)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	tmp := p + ".nkt-new"
	if err := os.WriteFile(tmp, []byte(strings.Join(keep, "\n")), fi.Mode().Perm()); err != nil {
		return err
	}
	// Владелец — прежний: файл, принадлежащий не тому, sshd может не
	// принять (StrictModes).
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(tmp, int(st.Uid), int(st.Gid))
	}
	return os.Rename(tmp, p)
}

// rekey — новый открытый ключ хаба вместо текущего (подпись запроса уже
// проверена текущим). Пишется рядом и переименовывается: недописанный
// файл оставил бы хост без ключа вовсе.
func (h Host) rekey(req Request) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.Args["pub"]))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return "", errors.New("hub-sudo: bad new key")
	}
	text := base64.StdEncoding.EncodeToString(b) + "\n"
	tmp := h.PubKey + ".new"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, h.PubKey); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return "rekeyed", nil
}
