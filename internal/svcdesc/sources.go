package svcdesc

import (
	"context"
	"encoding/base64"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/msgs"
)

// Источники: файл (кэш по mtime и размеру), HTTP и команда (кэш на
// interval). Команда — только argv, без оболочки: оболочки, sudo и
// обёртки, которые запускают произвольную команду, запрещены.

// maxSourceBytes — предел файла и вывода команды.
const maxSourceBytes = 8 << 20

// defaultTimeout — HTTP и команда без timeout.
const defaultTimeout = 5 * time.Second

var commandRe = regexp.MustCompile(`^(/[A-Za-z0-9._+/-]+|[A-Za-z0-9._+-]+)$`)

// shellLike — программы, через которые argv превращается в произвольную
// команду или запуск от другого пользователя.
var shellLike = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true, "csh": true, "tcsh": true, "fish": true,
	"busybox": true, "env": true, "sudo": true, "su": true, "doas": true, "runuser": true, "nsenter": true,
	"xargs": true, "nohup": true, "setsid": true, "timeout": true, "nice": true, "ionice": true, "chroot": true,
	"systemd-run": true, "script": true, "expect": true, "python": true, "python3": true, "perl": true,
	"ruby": true, "node": true, "php": true, "lua": true, "awk": true, "gawk": true, "find": true, "eval": true,
}

func basicAuthFile(s *Source) string {
	if s.BasicAuth == nil {
		return ""
	}
	return s.BasicAuth.PasswordFile
}

// unprivilegedUID — nobody: команды описаний без as_root идут от него.
const unprivilegedUID = "65534"

// commandArgv — argv запуска: от nobody через setpriv (без новых
// привилегий и групп), если служба nkt — root и as_root не задан; иначе как
// есть. В режиме fixtures — как есть (ответы ищутся по исходному argv).
func commandArgv(s Source, mode string, euid int) []string {
	if s.AsRoot || mode == "fixtures" || euid != 0 {
		return s.Command
	}
	return append([]string{"setpriv", "--reuid=" + unprivilegedUID, "--regid=" + unprivilegedUID,
		"--clear-groups", "--no-new-privs", "--"}, s.Command...)
}

func checkCommand(argv []string) error {
	if !commandRe.MatchString(argv[0]) {
		return msgs.Errorf("svcdesc.badCommand", argv[0])
	}
	if shellLike[path.Base(argv[0])] {
		return msgs.Errorf("svcdesc.shellCommand", path.Base(argv[0]))
	}
	if len(argv) > 64 {
		return msgs.Errorf("svcdesc.badCommand", argv[0])
	}
	for _, a := range argv {
		if len(a) > 4096 || strings.ContainsRune(a, 0) {
			return msgs.Errorf("svcdesc.badCommand", argv[0])
		}
	}
	return nil
}

// Закрытые пути: описание не читает их и не отдаёт в «Конфигурации» —
// пароли учёток, sudo, закрытые ключи, данные и настройки самого nkt,
// псевдофайлы ядра.
var (
	sensitiveExact = map[string]bool{
		"/etc/shadow": true, "/etc/shadow-": true, "/etc/gshadow": true, "/etc/gshadow-": true,
		"/etc/sudoers": true, "/etc/security/opasswd": true,
	}
	sensitivePrefixes = []string{
		"/etc/sudoers.d/", "/root/", "/proc/", "/sys/", "/dev/", "/run/secrets/",
		"/etc/ssh/ssh_host_", "/etc/ssl/private/", "/etc/letsencrypt/live/", "/etc/letsencrypt/archive/",
		"/etc/netknownsthat/", "/var/lib/netknownsthat/", "/etc/nkt/", "/etc/nkt-edge/",
	}
	sensitiveBase = regexp.MustCompile(`(?i)(^id_(rsa|dsa|ecdsa|ed25519)(_sk)?$|\.key$|privkey|private)`)
)

// Sensitive — путь закрыт для описаний.
func Sensitive(p string) bool {
	clean := path.Clean(p)
	if sensitiveExact[clean] || strings.Contains(clean, "/.ssh/") || strings.Contains(clean, "/.gnupg/") {
		return true
	}
	for _, pre := range sensitivePrefixes {
		if strings.HasPrefix(clean+"/", pre) || strings.HasPrefix(clean, pre) {
			return true
		}
	}
	return sensitiveBase.MatchString(path.Base(clean))
}

var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func checkHTTP(s *Source) error {
	u, err := url.Parse(s.HTTP)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return msgs.Errorf("svcdesc.badURL", s.HTTP)
	}
	if u.User != nil {
		return msgs.Errorf("svcdesc.urlCredentials")
	}
	if _, err := collect.CheckLocalURL(s.HTTP); err != nil {
		return err
	}
	for k, v := range s.Headers {
		if !headerNameRe.MatchString(k) || strings.ContainsAny(v, "\r\n") {
			return msgs.Errorf("svcdesc.badHeader", k)
		}
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Cookie") {
			return msgs.Errorf("svcdesc.secretHeader", k)
		}
	}
	if s.BasicAuth != nil && (s.BasicAuth.User == "" || !strings.HasPrefix(s.BasicAuth.PasswordFile, "/")) {
		return msgs.Errorf("svcdesc.needField", "basic_auth.user, basic_auth.password_file")
	}
	if s.BearerFile != "" && !strings.HasPrefix(s.BearerFile, "/") {
		return msgs.Errorf("svcdesc.absPath", s.BearerFile)
	}
	// Файл секрета уходит заголовком на адрес из описания — закрытые
	// пути (shadow, ключи, данные nkt) туда не отдаются.
	for _, f := range []string{s.BearerFile, basicAuthFile(s)} {
		if f != "" && Sensitive(f) {
			return msgs.Errorf("svcdesc.sensitive", f)
		}
	}
	return nil
}

// Doc — документ источника: файл (у glob — каждый свой) или ответ.
type Doc struct {
	File string
	Root any
}

type fileEntry struct {
	mod  time.Time
	size int64
	root any
	err  error
}

type fetchEntry struct {
	at   time.Time
	root any
	err  error
}

func optKey(s Source) string {
	return s.Format + "\x00" + s.Options.Pattern + "\x00" + strings.Join(s.Options.Blocks, ",") + "\x00" + map[bool]string{true: "l"}[s.Options.LowerKeys]
}

// read — документы источника. warn — предупреждения (ключ каталога).
func (e *Engine) read(ctx context.Context, d *Descriptor, s Source, warn func(string, ...any)) []Doc {
	switch s.Kind() {
	case "http":
		return e.fetch(d, s, warn, func() ([]byte, error) { return e.httpBody(ctx, s) })
	case "command":
		return e.fetch(d, s, warn, func() ([]byte, error) { return e.commandOutput(ctx, s) })
	}
	files := []string{s.File}
	if strings.ContainsAny(s.File, "*?[") {
		var err error
		if files, err = e.c.Glob(s.File); err != nil {
			warn("svcdesc.w.glob", s.ID, s.File, err)
			return nil
		}
	}
	var docs []Doc
	for _, f := range files {
		if Sensitive(f) {
			warn("svcdesc.w.sensitive", s.ID, f)
			continue
		}
		st, err := e.c.Stat(f)
		if err != nil {
			if !s.Optional && !strings.ContainsAny(s.File, "*?[") {
				warn("svcdesc.w.noFile", s.ID, f)
			}
			continue
		}
		// Ссылка на закрытый путь (link → /etc/shadow) — как сам путь.
		if real, err := e.c.RealPath(f); err != nil || Sensitive(real) {
			warn("svcdesc.w.sensitive", s.ID, f)
			continue
		}
		if st.Size > maxSourceBytes {
			warn("svcdesc.w.tooBig", s.ID, f)
			continue
		}
		key := f + "\x00" + optKey(s)
		e.mu.Lock()
		ent, ok := e.files[key]
		e.mu.Unlock()
		if !ok || !ent.mod.Equal(st.ModTime) || ent.size != st.Size {
			data, err := e.c.ReadFile(f)
			ent = fileEntry{mod: st.ModTime, size: st.Size, err: err}
			if err == nil {
				ent.root, ent.err = Decode(s.Format, data, s.Options)
			}
			e.mu.Lock()
			e.files[key] = ent
			e.mu.Unlock()
			e.tracef("svcdesc.t.readFile", s.ID, f, s.Format, len(data))
		} else {
			e.tracef("svcdesc.t.cachedFile", s.ID, f)
		}
		if ent.err != nil {
			warn("svcdesc.w.decode", s.ID, f, ent.err)
			continue
		}
		docs = append(docs, Doc{File: f, Root: ent.root})
	}
	if len(docs) == 0 && !s.Optional && strings.ContainsAny(s.File, "*?[") {
		warn("svcdesc.w.noFile", s.ID, s.File)
	}
	return docs
}

func (e *Engine) fetch(d *Descriptor, s Source, warn func(string, ...any), get func() ([]byte, error)) []Doc {
	key := d.Name + "\x00" + s.ID + "\x00" + s.HTTP + "\x00" + strings.Join(s.Command, "\x00") + "\x00" + optKey(s)
	e.mu.Lock()
	ent, ok := e.fetched[key]
	e.mu.Unlock()
	if !ok || s.Interval == 0 || time.Since(ent.at) >= time.Duration(s.Interval) {
		data, err := get()
		ent = fetchEntry{at: time.Now(), err: err}
		if err == nil {
			ent.root, ent.err = Decode(s.Format, data, s.Options)
		}
		e.mu.Lock()
		e.fetched[key] = ent
		e.mu.Unlock()
		e.tracef("svcdesc.t.fetched", s.ID, s.Kind(), len(data))
	} else {
		e.tracef("svcdesc.t.cachedFetch", s.ID, time.Since(ent.at).Round(time.Second).String())
	}
	if ent.err != nil {
		if !s.Optional {
			warn("svcdesc.w.fetch", s.ID, ent.err)
		}
		return nil
	}
	return []Doc{{File: "", Root: ent.root}}
}

func timeoutOf(s Source) time.Duration {
	if s.Timeout > 0 {
		return time.Duration(s.Timeout)
	}
	return defaultTimeout
}

func (e *Engine) httpBody(ctx context.Context, s Source) ([]byte, error) {
	headers := map[string]string{"Accept": "application/json"}
	for k, v := range s.Headers {
		headers[k] = v
	}
	// Секреты — из файлов на хосте: в описании (и в истории на хабе) их нет.
	for _, f := range []string{s.BearerFile, basicAuthFile(&s)} {
		if f == "" {
			continue
		}
		if real, err := e.c.RealPath(f); err != nil || Sensitive(real) {
			return nil, msgs.Errorf("svcdesc.sensitive", f)
		}
	}
	if s.BasicAuth != nil {
		pw, err := e.c.ReadFile(s.BasicAuth.PasswordFile)
		if err != nil {
			return nil, msgs.Errorf("svcdesc.secretFile", s.BasicAuth.PasswordFile, err)
		}
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(s.BasicAuth.User+":"+strings.TrimSpace(string(pw))))
	}
	if s.BearerFile != "" {
		tok, err := e.c.ReadFile(s.BearerFile)
		if err != nil {
			return nil, msgs.Errorf("svcdesc.secretFile", s.BearerFile, err)
		}
		headers["Authorization"] = "Bearer " + strings.TrimSpace(string(tok))
	}
	body, code, err := e.c.HTTPGet(ctx, s.HTTP, headers, timeoutOf(s))
	if err != nil {
		return nil, err
	}
	if code < 200 || code > 299 {
		return nil, msgs.Errorf("svcdesc.httpCode", code)
	}
	return body, nil
}

func (e *Engine) commandOutput(ctx context.Context, s Source) ([]byte, error) {
	argv := commandArgv(s, e.c.Mode(), os.Geteuid())
	res, err := e.c.RunTimeout(ctx, timeoutOf(s), argv[0], argv[1:]...)
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, msgs.Errorf("svcdesc.commandExit", res.ExitCode, firstLine(res.Stderr))
	}
	out := res.Stdout
	if len(out) > maxSourceBytes {
		out = out[:maxSourceBytes]
	}
	return []byte(out), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
