package fail2ban

import (
	"net/netip"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/collect"
)

// Файлы, которые пишет nkt. Все — в jail.d/ и filter.d/ под своими
// именами: jail.conf пакета не трогается (его перезаписывает обновление),
// чужие jail.local и jail.d/*.local — тоже.
//
// Порядок чтения у fail2ban: jail.conf, jail.d/*.conf, jail.local,
// jail.d/*.local — по алфавиту внутри каталога. Файл защиты хаба назван
// «zz-…», чтобы его [DEFAULT] читался последним и не перекрывался.

// HubIgnoreFile — [DEFAULT] ignoreip с внешним адресом хаба.
const HubIgnoreFile = "jail.d/zz-nkt-hub.local"

// JailFile — файл джейла nkt: jail.d/nkt-<имя>.local.
func JailFile(name string) string { return "jail.d/nkt-" + name + ".local" }

// FilterFile — фильтр шаблона: filter.d/nkt-<имя>.conf (.conf, а не
// .local: у фильтра без .conf старые версии не находят .local).
func FilterFile(name string) string { return "filter.d/nkt-" + name + ".conf" }

// FilterName — имя фильтра шаблона для строки «filter = …».
func FilterName(name string) string { return "nkt-" + name }

// ManualJailFile / ManualFilterFile — джейл ручных банов и его пустой
// фильтр.
var (
	ManualJailFile   = JailFile("manual")
	ManualFilterFile = FilterFile("manual")
)

// ManualJailContent — джейл ручных банов: журнал /dev/null, фильтр,
// который ничего не находит; баны — только командой. Порты — все:
// ручной бан означает «этого адреса здесь быть не должно».
func ManualJailContent(banTime int64) string {
	if banTime <= 0 {
		banTime = DefaultManualBanTime
	}
	return "# Managed by nkt: manual bans (from the UI and \"Ban on all hosts\").\n" +
		"[" + ManualJail + "]\n" +
		"enabled = true\n" +
		"filter = " + FilterName("manual") + "\n" +
		"backend = polling\n" +
		"logpath = /dev/null\n" +
		"maxretry = 1\n" +
		"bantime = " + strconv.FormatInt(banTime, 10) + "\n" +
		"banaction = %(banaction_allports)s\n"
}

// ManualFilterContent — фильтр ручного джейла: ни с чем не совпадает.
const ManualFilterContent = "# Managed by nkt: filter of the manual jail, matches nothing.\n" +
	"[Definition]\n" +
	"failregex = ^nkt-manual-never-matches$\n" +
	"ignoreregex =\n"

// INI — разобранный файл конфигурации fail2ban: секция → ключ →
// значение (строки продолжения склеены пробелом).
type INI map[string]map[string]string

// ParseINI — разбор диалекта fail2ban (configparser): «ключ = значение»
// или «ключ: значение», продолжение — строки с отступом, комментарии
// «#» и «;».
func ParseINI(text string) INI {
	out := INI{}
	section := ""
	lastKey := ""
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") {
			continue
		}
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			section = strings.TrimSpace(trim[1 : len(trim)-1])
			if out[section] == nil {
				out[section] = map[string]string{}
			}
			lastKey = ""
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && lastKey != "" && section != "" {
			out[section][lastKey] = strings.TrimSpace(out[section][lastKey] + " " + trim)
			continue
		}
		i := strings.IndexAny(trim, "=:")
		if i <= 0 || section == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trim[:i]))
		out[section][key] = strings.TrimSpace(trim[i+1:])
		lastKey = key
	}
	return out
}

// DefaultIgnoreIP — ignoreip из [DEFAULT] в порядке чтения fail2ban
// (jail.conf, jail.d/*.conf, jail.local, jail.d/*.local), кроме нашего
// файла защиты: последнее значение побеждает. Пусто — как в jail.conf:
// только loopback.
func DefaultIgnoreIP(c collect.Collector, root string) []string {
	list, _, _ := DefaultIgnoreSource(c, root)
	return list
}

// DefaultIgnoreSource — общий список ignoreip и файл, где он задан
// (последний по порядку чтения, кроме файла защиты). Не задан нигде —
// файл jail.local (туда его и запишет правка), defined=false.
func DefaultIgnoreSource(c collect.Collector, root string) (list []string, file string, defined bool) {
	files := []string{path.Join(root, "jail.conf")}
	confs, _ := c.Glob(path.Join(root, "jail.d", "*.conf"))
	sort.Strings(confs)
	files = append(files, confs...)
	files = append(files, path.Join(root, "jail.local"))
	locals, _ := c.Glob(path.Join(root, "jail.d", "*.local"))
	sort.Strings(locals)
	files = append(files, locals...)

	value := ""
	file = path.Join(root, "jail.local")
	for _, f := range files {
		if f == path.Join(root, HubIgnoreFile) {
			continue
		}
		raw, err := c.ReadFile(f)
		if err != nil {
			continue
		}
		if v, ok := ParseINI(string(raw))["DEFAULT"]["ignoreip"]; ok {
			value, file, defined = v, f, true
		}
	}
	list = strings.Fields(strings.ReplaceAll(value, ",", " "))
	if len(list) == 0 {
		list = []string{"127.0.0.1/8", "::1"}
	}
	return list, file, defined
}

var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}))*$`)

// ValidIgnoreEntry — элемент ignoreip: адрес, сеть CIDR или имя хоста
// (fail2ban разрешает и имена).
func ValidIgnoreEntry(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if _, err := netip.ParsePrefix(s); err == nil {
		return true
	}
	return len(s) <= 253 && hostnameRe.MatchString(s)
}

// SetINIKey задаёт (value не пусто) или убирает ключ в секции, не трогая
// остального текста: комментарии, свои ключи, отступы. Секции нет —
// добавляется в конец.
func SetINIKey(text, section, key, value string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	header := func(l string) (string, bool) {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			return strings.TrimSpace(t[1 : len(t)-1]), true
		}
		return "", false
	}
	keyOf := func(l string) string {
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			return ""
		}
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
			return ""
		}
		if i := strings.IndexAny(t, "=:"); i > 0 {
			return strings.ToLower(strings.TrimSpace(t[:i]))
		}
		return ""
	}
	start, end := -1, len(lines)
	for i, l := range lines {
		h, ok := header(l)
		if !ok {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if h == section {
			start = i
		}
	}
	remove := strings.TrimSpace(value) == ""
	join := func() string { return strings.Join(lines, "\n") + "\n" }
	if start < 0 {
		if remove {
			return join()
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+section+"]", key+" = "+value)
		return join()
	}
	for i := start + 1; i < end; i++ {
		if keyOf(lines[i]) != strings.ToLower(key) {
			continue
		}
		j := i + 1
		for j < end && len(lines[j]) > 0 && (lines[j][0] == ' ' || lines[j][0] == '\t') && strings.TrimSpace(lines[j]) != "" {
			j++
		}
		repl := []string{}
		if !remove {
			repl = []string{key + " = " + value}
		}
		lines = append(append(append([]string{}, lines[:i]...), repl...), lines[j:]...)
		return join()
	}
	if remove {
		return join()
	}
	at := end
	for at > start+1 && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}
	lines = append(append(append([]string{}, lines[:at]...), key+" = "+value), lines[at:]...)
	return join()
}

// HubIgnoreContent — файл защиты: прежний список ignoreip [DEFAULT] плюс
// адрес хаба. Список прежний целиком — строка ignoreip в DEFAULT не
// складывается с предыдущими, а заменяет их.
func HubIgnoreContent(existing []string, hub netip.Addr) string {
	list := append([]string(nil), existing...)
	if !Covers(list, hub) {
		list = append(list, hub.String())
	}
	return "# Managed by nkt: the hub's external address is never banned, otherwise the\n" +
		"# hub would lock itself out of this host after a few failed connections.\n" +
		"# The other addresses are carried over from the previous [DEFAULT] ignoreip.\n" +
		"[DEFAULT]\n" +
		"ignoreip = " + strings.Join(list, " ") + "\n"
}

// Template — шаблон джейла: встроенный или свой (хранится на хабе).
type Template struct {
	Name string `json:"name"`
	// Description — для своих; у встроенных текст в каталоге интерфейса
	// по имени.
	Description string `json:"description,omitempty"`
	// Service — для какой программы (встроенные предлагаются, только
	// если она есть на хосте); пусто — всегда.
	Service string `json:"service,omitempty"`
	Jail    string `json:"jail"`
	Filter  string `json:"filter,omitempty"`
	Builtin bool   `json:"builtin"`
	// Available — программа шаблона есть на этом хосте.
	Available bool `json:"available"`
}

var templateNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,40}$`)

// ValidTemplateName — имя шаблона (оно же часть имён файлов).
func ValidTemplateName(name string) bool { return templateNameRe.MatchString(name) }

// Builtin — встроенные шаблоны. sshJournal — у sshd нет журнала в файле
// (Debian 12+: только journald), тогда джейлу нужен backend = systemd.
func Builtin(sshJournal bool) []Template {
	sshd := "[sshd]\nenabled = true\nport = ssh\nmaxretry = 5\nfindtime = 10m\nbantime = 1h\nbantime.increment = true\n"
	if sshJournal {
		sshd += "backend = systemd\n"
	}
	return []Template{
		{Name: "sshd", Service: "ssh", Jail: sshd},
		{Name: "nginx-http-auth", Service: "nginx", Jail: "[nginx-http-auth]\nenabled = true\nport = http,https\nlogpath = /var/log/nginx/error.log\n"},
		{Name: "nginx-botsearch", Service: "nginx", Jail: "[nginx-botsearch]\nenabled = true\nport = http,https\nlogpath = /var/log/nginx/access.log\nmaxretry = 2\n"},
		{Name: "nginx-limit-req", Service: "nginx", Jail: "[nginx-limit-req]\nenabled = true\nport = http,https\nlogpath = /var/log/nginx/error.log\n"},
		{Name: "haproxy-http-auth", Service: "haproxy", Jail: "[haproxy-http-auth]\nenabled = true\nport = http,https\nlogpath = /var/log/haproxy.log\n"},
		{Name: "postfix", Service: "postfix", Jail: "[postfix]\nenabled = true\nmode = more\nport = smtp,465,submission\nlogpath = %(postfix_log)s\nbackend = %(postfix_backend)s\n"},
		{Name: "dovecot", Service: "dovecot", Jail: "[dovecot]\nenabled = true\nport = pop3,pop3s,imap,imaps,submission,465,sieve\nlogpath = %(dovecot_log)s\nbackend = %(dovecot_backend)s\n"},
		{Name: "recidive", Jail: "[recidive]\nenabled = true\nlogpath = /var/log/fail2ban.log\nbanaction = %(banaction_allports)s\nbantime = 1w\nfindtime = 1d\nmaxretry = 5\n"},
	}
}

// ServiceBinary — по какой программе узнаётся служба шаблона.
var ServiceBinary = map[string]string{
	"ssh": "sshd", "nginx": "nginx", "haproxy": "haproxy", "postfix": "postfix", "dovecot": "dovecot",
}

// TemplateJailName — имя джейла шаблона: первая секция его текста.
func TemplateJailName(jail string) string {
	for _, line := range strings.Split(jail, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			return strings.TrimSpace(t[1 : len(t)-1])
		}
	}
	return ""
}

// WithFilter — у своего шаблона с фильтром строка «filter = nkt-<имя>»
// в секции джейла обязательна: без неё джейл возьмёт фильтр по своему
// имени, а не написанный в шаблоне. Если её нет — добавляется после
// заголовка секции.
func WithFilter(name, jail, filter string) string {
	if strings.TrimSpace(filter) == "" {
		return jail
	}
	for _, line := range strings.Split(jail, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "filter") {
			if i := strings.IndexAny(t, "=:"); i > 0 && strings.TrimSpace(t[:i]) == "filter" {
				return jail
			}
		}
	}
	lines := strings.Split(jail, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			out := append(append(append([]string{}, lines[:i+1]...), "filter = "+FilterName(name)), lines[i+1:]...)
			return strings.Join(out, "\n")
		}
	}
	return jail
}
