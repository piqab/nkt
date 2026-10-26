package catalog

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/plugin"
)

// Lint — проверка плагина каталога до сборки (cmd/nkt-catalog lint, CI):
// состав папки, nkt-plugin.json, опасные конструкции в коде и — если
// runtime — живой плагин: манифест на обоих языках проходит ту же
// проверку, что в хабе (plugin.Manifest.Validate), без подписи и со старой
// отметкой — 401, неизвестная операция — отказ, а не обрыв. dir —
// examples/<key>. Возвращает найденное (пусто — всё хорошо).
func Lint(dir string, runtime bool) []string {
	return LintWith(dir, LintOptions{Runtime: runtime})
}

// LintOptions — что проверять.
type LintOptions struct {
	// Runtime — запустить плагин и проверить живые ответы (только CI и
	// разработчик: на хабе чужой код не запускается).
	Runtime bool
	// Upload — свой плагин, загруженный в хаб: тест и README не
	// обязательны (их нет в архиве — плагин всё равно проверяется хабом
	// при подключении после установки).
	Upload bool
}

// LintWith — Lint с параметрами.
func LintWith(dir string, opt LintOptions) []string {
	var out []string
	bad := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	read := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			bad("no %s", rel)
			return nil
		}
		return b
	}
	raw := read("deploy/nkt-plugin.json")
	if !opt.Upload {
		read("deploy/test_plugin.py")
		read("README.md")
		read("README.en.md")
	}
	if raw == nil {
		return out
	}
	var p Plugin
	if err := json.Unmarshal(raw, &p); err != nil {
		return append(out, "nkt-plugin.json: "+err.Error())
	}
	if !plugin.ValidName(p.Name) {
		bad("nkt-plugin.json: name %q — latin lowercase, digits, -", p.Name)
	}
	if !verRe.MatchString(p.Version) {
		bad("nkt-plugin.json: version %q — X.Y.Z", p.Version)
	}
	if !verRe.MatchString(p.MinNKT) {
		bad("nkt-plugin.json: min_nkt %q — X.Y.Z (the oldest nkt it works with)", p.MinNKT)
	}
	if p.Protocol != plugin.Protocol {
		bad("nkt-plugin.json: protocol %d, the hub speaks %d", p.Protocol, plugin.Protocol)
	}
	for _, f := range []struct {
		name string
		m    map[string]string
	}{{"title", p.Title}, {"description", p.Description}} {
		if strings.TrimSpace(f.m["ru"]) == "" || strings.TrimSpace(f.m["en"]) == "" {
			bad("nkt-plugin.json: %s needs ru and en", f.name)
		}
	}
	if p.Run != "python3" {
		bad("nkt-plugin.json: run %q — the hub installs python3 plugins", p.Run)
	}
	if p.Port < 1024 || p.Port > 65535 {
		bad("nkt-plugin.json: port %d — 1024…65535", p.Port)
	}
	for _, s := range p.Settings {
		if !envNameRe.MatchString(s.Env) || strings.HasPrefix(s.Env, "NKT_") {
			bad("setting %q: A-Z, 0-9, _ and not NKT_ (the hub's own)", s.Env)
		}
		if s.Title["ru"] == "" || s.Title["en"] == "" {
			bad("setting %s: title needs ru and en", s.Env)
		}
		if secretNameRe.MatchString(s.Env) && !s.Secret {
			bad("setting %s looks like a secret — set \"secret\": true (the hub then never shows it)", s.Env)
		}
	}
	code := read("deploy/" + p.File)
	if code == nil {
		return out
	}
	if m := pyVersionRe.FindSubmatch(code); m == nil || string(m[1]) != p.Version {
		bad("%s: VERSION = %q must equal version %q in nkt-plugin.json", p.File, firstMatch(m), p.Version)
	}
	for i, line := range strings.Split(string(code), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		for _, r := range pyForbidden {
			if r.re.MatchString(t) {
				bad("%s:%d: %s", p.File, i+1, r.why)
			}
		}
	}
	for _, r := range pyRequired {
		if !strings.Contains(string(code), r.text) {
			bad("%s: %s", p.File, r.why)
		}
	}
	if opt.Runtime && len(out) == 0 {
		out = append(out, lintLive(dir, p)...)
	}
	return out
}

var (
	envNameRe    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	secretNameRe = regexp.MustCompile(`(TOKEN|SECRET|PASSWORD|PASS|_KEY|APIKEY)$|_KEY_`)
)

type pyRule struct {
	re  *regexp.Regexp
	why string
}

// Конструкции, которых в плагине быть не должно: строка из аргументов
// хаба попадает в оболочку или интерпретатор.
var pyForbidden = []pyRule{
	{regexp.MustCompile(`shell\s*=\s*True`), "shell=True — pass a list of arguments (and -- before positional ones)"},
	{regexp.MustCompile(`\bos\.(system|popen|exec\w*|spawn\w*)\s*\(`), "os.system/popen/exec — use subprocess.run with a list"},
	{regexp.MustCompile(`(^|[^\w.])(eval|exec)\(`), "eval/exec — never run text from a request"},
	{regexp.MustCompile(`\bsubprocess\.(run|Popen|call|check_call|check_output)\(\s*[fbru]*["']`), "subprocess with a command string — pass a list"},
	{regexp.MustCompile(`\b(pickle|marshal)\.loads?\(`), "pickle/marshal — use json"},
	{regexp.MustCompile(`\byaml\.load\(`), "yaml.load — use json (or yaml.safe_load)"},
	{regexp.MustCompile(`_create_unverified_context|verify\s*=\s*False`), "turning TLS checks off globally — pin the fingerprint instead (see the guide)"},
}

// Что в плагине быть должно: проверка подписи и секрета.
var pyRequired = []struct{ text, why string }{
	{"hmac.compare_digest", "the signature must be compared with hmac.compare_digest"},
	{"X-NKT-Timestamp", "the request timestamp must be checked (X-NKT-Timestamp, ±300 s)"},
	{"NKT_PLUGIN_SECRET", "the secret comes from NKT_PLUGIN_SECRET"},
	{"X-NKT-Lang", "texts follow X-NKT-Lang (ru, en)"},
}

func firstMatch(m [][]byte) string {
	if m == nil {
		return ""
	}
	return string(m[1])
}

// lintLive запускает плагин и проверяет его ответы.
func lintLive(dir string, p Plugin) []string {
	var out []string
	py, err := exec.LookPath("python3")
	if err != nil {
		return []string{"python3 not found — the live check needs it"}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return []string{err.Error()}
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	secret := hex.EncodeToString(key)
	state, err := os.MkdirTemp("", "nkt-lint-")
	if err != nil {
		return []string{err.Error()}
	}
	defer os.RemoveAll(state)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-I", filepath.Join(dir, "deploy", p.File))
	// Окружение — только нужное плагину: без настроек (плагин обязан
	// запускаться и без них — их задают после установки).
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "NKT_PLUGIN_SECRET=" + secret, "NKT_PLUGIN_PORT=" + strconv.Itoa(port),
		"NKT_PLUGIN_LISTEN=127.0.0.1", "STATE_DIRECTORY=" + state, "HOME=" + state}
	var logBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logBuf, &logBuf
	if err := cmd.Start(); err != nil {
		return []string{err.Error()}
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	up := false
	for range 100 {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			_ = c.Close()
			up = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !up {
		return []string{"the plugin does not start without settings (it must — settings come after install): " + strings.TrimSpace(logBuf.String())}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	do := func(method, path string, body []byte, ts int64, sign bool, lang string) (int, []byte, error) {
		req, err := http.NewRequest(method, base+path, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		t := strconv.FormatInt(ts, 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(t + "." + path + "."))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))
		if !sign {
			sig = strings.Repeat("0", 64)
		}
		req.Header.Set(plugin.HeaderTimestamp, t)
		req.Header.Set(plugin.HeaderSignature, sig)
		req.Header.Set(plugin.HeaderLang, lang)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return resp.StatusCode, b, nil
	}
	now := time.Now().Unix()
	mans := map[string]plugin.Manifest{}
	for _, lang := range []string{"ru", "en"} {
		code, b, err := do("GET", plugin.PathManifest, nil, now, true, lang)
		if err != nil || code != 200 {
			out = append(out, fmt.Sprintf("manifest (%s): HTTP %d %v", lang, code, err))
			continue
		}
		var m plugin.Manifest
		if err := json.Unmarshal(b, &m); err != nil {
			out = append(out, fmt.Sprintf("manifest (%s): %v", lang, err))
			continue
		}
		if err := m.Validate(); err != nil {
			out = append(out, fmt.Sprintf("manifest (%s) fails the hub's check: %v", lang, err))
		}
		if m.Name != p.Name || m.Version != p.Version {
			out = append(out, fmt.Sprintf("manifest (%s): name/version %s %s, nkt-plugin.json says %s %s", lang, m.Name, m.Version, p.Name, p.Version))
		}
		for _, op := range m.Ops {
			if op.MCP && strings.TrimSpace(op.Description) == "" {
				out = append(out, fmt.Sprintf("op %s: an MCP tool needs a description (agents read it)", op.Name))
			}
		}
		for _, ht := range m.HostTypes {
			if !hasField(ht.Fields, "address") {
				out = append(out, fmt.Sprintf("host type %s: needs an address field", ht.ID))
			}
		}
		mans[lang] = m
	}
	if ru, en := mans["ru"], mans["en"]; ru.Name != "" && en.Name != "" && ru.Title+ru.Description == en.Title+en.Description {
		out = append(out, "the manifest is the same in ru and en — texts must follow X-NKT-Lang")
	}
	if code, _, _ := do("GET", plugin.PathManifest, nil, now, false, "ru"); code != 401 {
		out = append(out, fmt.Sprintf("an unsigned request got HTTP %d, want 401", code))
	}
	if code, _, _ := do("GET", plugin.PathManifest, nil, now-3600, true, "ru"); code != 401 {
		out = append(out, fmt.Sprintf("a request signed an hour ago got HTTP %d, want 401 (check the timestamp, ±300 s)", code))
	}
	body := []byte(`{"op":"no_such_op_lint","args":{}}`)
	code, b, err := do("POST", plugin.PathCall, body, now, true, "en")
	var resp plugin.Response
	if err != nil || code != 200 || json.Unmarshal(b, &resp) != nil || resp.OK {
		out = append(out, fmt.Sprintf("an unknown op must answer {ok:false, error} with HTTP 200, got %d %s", code, strings.TrimSpace(string(b))))
	}
	if strings.Contains(logBuf.String(), secret) {
		out = append(out, "the plugin printed its NKT_PLUGIN_SECRET to the log")
	}
	return out
}

func hasField(fs []plugin.Field, name string) bool {
	for _, f := range fs {
		if f.Name == name {
			return true
		}
	}
	return false
}
