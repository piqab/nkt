package script

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Step — одна команда сценария после разбора.
type Step struct {
	Line int  `json:"line"`
	Kind Kind `json:"kind"`
	// Host — хост, к которому относится шаг: у «on …» и «host …» — его
	// имя; у «install» — первый из списка (остальные в Hosts).
	Host  string   `json:"host,omitempty"`
	Hosts []string `json:"hosts,omitempty"`
	// Name — имя того, что заводится или над чем действие: группа,
	// служба, машина, профиль.
	Name string `json:"name,omitempty"`
	// Action — подкоманда: install/remove, start/stop, allow/deny, up/down.
	Action string `json:"action,omitempty"`
	// Args — именованные параметры команды («image», «cpu», «from»…).
	Args map[string]string `json:"args,omitempty"`
	// List — список без имён: пакеты.
	List []string `json:"list,omitempty"`
	// Block — содержимое блока до «end» (compose-файл, текст файла).
	Block string `json:"block,omitempty"`
	// Text — исходная строка, как её показывать в плане.
	Text string `json:"text"`
}

// Issue — ошибка разбора или проверки с номером строки.
type Issue struct {
	Line int
	Err  error
}

// Ask — что спросить при запуске: пароль хоста, параметр, секрет,
// токен. Key — по нему значение возвращается исполнителю.
type Ask struct {
	Key    string `json:"key"`
	Prompt string `json:"prompt"`
	Secret bool   `json:"secret"`
}

// Script — разобранный сценарий.
type Script struct {
	Steps []Step
	// Vars — «set ИМЯ значение» в порядке появления.
	Vars map[string]string
	// Asks — что спрашивается при запуске: «password ask» у хоста
	// (host:ИМЯ), «param» (param:ИМЯ), «set ИМЯ ask» (var:ИМЯ, секрет),
	// «token ask» у git (token:URL).
	Asks []Ask
	// Hosts — хосты, заведённые сценарием (по имени); Groups — группы.
	Hosts  []string
	Groups []string
}

// AskKeys — ключи вопросов.
func (sc Script) AskKeys() []string {
	out := make([]string, 0, len(sc.Asks))
	for _, a := range sc.Asks {
		out = append(out, a.Key)
	}
	return out
}

// onCommands — команды, которые пишутся после «on ХОСТ…»: по ним
// разбор понимает, где кончается список хостов.
var onCommands = map[string]bool{
	"packages": true, "service": true, "firewall": true, "docker": true, "vm": true,
	"apply": true, "file": true, "user": true, "system": true, "cert": true, "git": true,
}

// paramPlaceholder — чем подставляется параметр без значения при
// проверке: разбор должен пройти, значение появится при запуске.
const paramPlaceholder = "1"

var (
	nameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	varRe     = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	pkgRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
	portRe    = regexp.MustCompile(`^([0-9]{1,5})(?:/(tcp|udp))?$`)
	pathRe    = regexp.MustCompile(`^/[^\x00]*$`)
)

// Parse разбирает текст сценария. Ошибки собираются все разом — с
// номерами строк, чтобы редактор показал их списком, а не по одной.
//
// values — ответы на вопросы запуска (см. Ask): значения параметров и
// «set … ask» подставляются в ${…}; при проверке их нет, и параметр
// заменяется заглушкой, чтобы разбор прошёл.
func Parse(text string, values map[string]string) (Script, []Issue) {
	sc := Script{Vars: map[string]string{}}
	var issues []Issue
	fail := func(line int, key string, args ...any) {
		issues = append(issues, Issue{Line: line, Err: msgs.Errorf(key, args...)})
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		raw := lines[i]
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "end" {
			fail(lineNo, "script.endWithoutBlock")
			continue
		}
		// «param» и «set … ask» объявляют переменную, значение которой
		// приходит при запуске; разбираются до подстановки.
		if decl, ok, err := parseDecl(line, &sc, values); err != nil {
			issues = append(issues, Issue{Line: lineNo, Err: err})
			continue
		} else if ok {
			_ = decl
			continue
		}
		expanded, err := expandVars(line, sc.Vars)
		if err != nil {
			issues = append(issues, Issue{Line: lineNo, Err: err})
			continue
		}
		toks, err := tokenize(expanded)
		if err != nil {
			issues = append(issues, Issue{Line: lineNo, Err: err})
			continue
		}
		step, err := parseStep(toks, &sc)
		if err != nil {
			issues = append(issues, Issue{Line: lineNo, Err: err})
			continue
		}
		step.Line = lineNo
		step.Text = line
		if doc := docByKind(step.Kind); doc != nil && doc.Block {
			// Блок: строки как есть до «end» в начале строки. Отступы
			// сохраняются — это может быть YAML.
			var block []string
			closed := false
			for i++; i < len(lines); i++ {
				if strings.TrimRight(lines[i], " \t") == "end" {
					closed = true
					break
				}
				block = append(block, lines[i])
			}
			if !closed {
				fail(step.Line, "script.blockNotClosed")
			}
			step.Block = strings.Join(block, "\n")
			if len(block) > 0 {
				step.Block += "\n"
			}
		}
		if step.Kind == KindSet {
			sc.Vars[step.Name] = step.Args["value"]
			continue
		}
		if step.Kind == KindHost && step.Args["auth"] == "password-ask" {
			sc.Asks = append(sc.Asks, Ask{Key: "host:" + step.Name, Prompt: step.Name, Secret: true})
		}
		if step.Kind == KindGitClone && step.Args["token"] == "ask" {
			sc.Asks = append(sc.Asks, Ask{Key: "token:" + step.Args["url"], Prompt: step.Args["url"], Secret: true})
		}
		sc.Steps = append(sc.Steps, step)
	}
	return sc, issues
}

// parseDecl разбирает «param ИМЯ "подсказка"» и «set ИМЯ ask»: значение
// берётся из values или заменяется заглушкой; вопрос запоминается.
func parseDecl(line string, sc *Script, values map[string]string) (bool, bool, error) {
	toks, err := tokenize(line)
	if err != nil || len(toks) < 2 {
		return false, false, nil
	}
	switch {
	case toks[0] == "param":
		if !varNameRe.MatchString(toks[1]) || len(toks) > 3 {
			return false, false, msgs.Errorf("script.badParam")
		}
		prompt := toks[1]
		if len(toks) == 3 {
			prompt = toks[2]
		}
		sc.Asks = append(sc.Asks, Ask{Key: "param:" + toks[1], Prompt: prompt})
		if v, ok := values["param:"+toks[1]]; ok {
			sc.Vars[toks[1]] = v
		} else {
			sc.Vars[toks[1]] = paramPlaceholder
		}
		return true, true, nil
	case toks[0] == "set" && len(toks) == 3 && toks[2] == "ask":
		if !varNameRe.MatchString(toks[1]) {
			return false, false, msgs.Errorf("script.badSet")
		}
		sc.Asks = append(sc.Asks, Ask{Key: "var:" + toks[1], Prompt: toks[1], Secret: true})
		if v, ok := values["var:"+toks[1]]; ok {
			sc.Vars[toks[1]] = v
		} else {
			sc.Vars[toks[1]] = paramPlaceholder
		}
		return true, true, nil
	}
	return false, false, nil
}

// expandVars подставляет ${ИМЯ}; неизвестная переменная — ошибка, а не
// пустота: пустой адрес хоста молча не пройдёт.
func expandVars(line string, vars map[string]string) (string, error) {
	var missing string
	out := varRe.ReplaceAllStringFunc(line, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		v, ok := vars[name]
		if !ok && missing == "" {
			missing = name
		}
		return v
	})
	if missing != "" {
		return "", msgs.Errorf("script.unknownVar", missing)
	}
	return out, nil
}

// tokenize делит строку на слова; "…" — одно слово, внутри допустимо \".
func tokenize(line string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	inQuote, hasTok := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQuote && c == '\\' && i+1 < len(line) && line[i+1] == '"':
			cur.WriteByte('"')
			i++
		case c == '"':
			inQuote = !inQuote
			hasTok = true
		case !inQuote && (c == ' ' || c == '\t'):
			if hasTok {
				toks = append(toks, cur.String())
				cur.Reset()
				hasTok = false
			}
		case !inQuote && c == '#' && !hasTok && (i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t'):
			// Комментарий в конце строки: «# …» отдельным словом. Просто
			// «#» внутри слова — это цвет (#5aa66f), а не комментарий.
			i = len(line)
		default:
			cur.WriteByte(c)
			hasTok = true
		}
	}
	if inQuote {
		return nil, msgs.Errorf("script.unclosedQuote")
	}
	if hasTok {
		toks = append(toks, cur.String())
	}
	return toks, nil
}

// kv собирает «ключ значение» пары из хвоста токенов по списку известных
// ключей; флаги (без значения) перечисляются отдельно.
func kv(toks []string, keys []string, flags []string) (map[string]string, error) {
	out := map[string]string{}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		isFlag := false
		for _, f := range flags {
			if t == f {
				out[f] = "true"
				isFlag = true
				break
			}
		}
		if isFlag {
			continue
		}
		known := false
		for _, k := range keys {
			if t == k {
				known = true
				break
			}
		}
		if !known {
			return nil, msgs.Errorf("script.unexpectedWord", t)
		}
		if i+1 >= len(toks) {
			return nil, msgs.Errorf("script.missingValue", t)
		}
		if _, dup := out[t]; dup {
			return nil, msgs.Errorf("script.duplicateArg", t)
		}
		out[t] = toks[i+1]
		i++
	}
	return out, nil
}

func parseStep(toks []string, sc *Script) (Step, error) {
	switch toks[0] {
	case "set":
		if len(toks) < 3 || !varNameRe.MatchString(toks[1]) {
			return Step{}, msgs.Errorf("script.badSet")
		}
		return Step{Kind: KindSet, Name: toks[1], Args: map[string]string{"value": strings.Join(toks[2:], " ")}}, nil
	case "group":
		if len(toks) < 2 || !nameRe.MatchString(toks[1]) {
			return Step{}, msgs.Errorf("script.badName", "group")
		}
		args, err := kv(toks[2:], []string{"profile"}, nil)
		if err != nil {
			return Step{}, err
		}
		sc.Groups = append(sc.Groups, toks[1])
		return Step{Kind: KindGroup, Name: toks[1], Args: args}, nil
	case "host":
		if len(toks) < 3 || !nameRe.MatchString(toks[1]) {
			return Step{}, msgs.Errorf("script.badName", "host")
		}
		addr, port, err := splitAddr(toks[2])
		if err != nil {
			return Step{}, err
		}
		args, err := kv(toks[3:], []string{"user", "password", "key", "group"}, nil)
		if err != nil {
			return Step{}, err
		}
		if args["user"] == "" {
			return Step{}, msgs.Errorf("script.hostNeedsUser")
		}
		switch {
		case args["password"] == "ask":
			args["auth"] = "password-ask"
		case args["password"] != "":
			args["auth"] = "password"
		case args["key"] == "hub":
			args["auth"] = "key-hub"
		case args["key"] != "":
			return Step{}, msgs.Errorf("script.keyOnlyHub")
		default:
			return Step{}, msgs.Errorf("script.hostNeedsAuth")
		}
		args["addr"], args["port"] = addr, port
		sc.Hosts = append(sc.Hosts, toks[1])
		return Step{Kind: KindHost, Host: toks[1], Name: toks[1], Args: args}, nil
	case "install":
		if len(toks) < 2 {
			return Step{}, msgs.Errorf("script.installNeedsHost")
		}
		for _, h := range toks[1:] {
			if !nameRe.MatchString(h) {
				return Step{}, msgs.Errorf("script.badName", "host")
			}
		}
		return Step{Kind: KindInstall, Host: toks[1], Hosts: toks[1:]}, nil
	case "wait":
		return parseWait(toks)
	case "on":
		// Хосты — все слова до первой команды; их может быть несколько.
		i := 1
		for i < len(toks) && !onCommands[toks[i]] {
			if !nameRe.MatchString(toks[i]) {
				return Step{}, msgs.Errorf("script.badName", "host")
			}
			i++
		}
		if i == 1 || i >= len(toks) {
			return Step{}, msgs.Errorf("script.onNeedsCommand")
		}
		step, err := parseOn(toks[i:])
		if err != nil {
			return Step{}, err
		}
		step.Host = toks[1]
		if i > 2 {
			step.Hosts = toks[1:i]
		}
		return step, nil
	}
	return Step{}, msgs.Errorf("script.unknownCommand", toks[0])
}

func parseOn(toks []string) (Step, error) {
	oneOf := func(v string, allowed ...string) bool {
		for _, a := range allowed {
			if v == a {
				return true
			}
		}
		return false
	}
	switch toks[0] {
	case "packages":
		if len(toks) < 3 || !oneOf(toks[1], "install", "remove") {
			return Step{}, msgs.Errorf("script.badPackages")
		}
		for _, p := range toks[2:] {
			if !pkgRe.MatchString(p) {
				return Step{}, msgs.Errorf("script.badPackageName", p)
			}
		}
		return Step{Kind: KindPackages, Action: toks[1], List: toks[2:]}, nil
	case "service":
		if len(toks) != 3 || !nameRe.MatchString(toks[1]) || !oneOf(toks[2], "start", "stop", "restart", "reload", "enable", "disable") {
			return Step{}, msgs.Errorf("script.badService")
		}
		return Step{Kind: KindService, Name: toks[1], Action: toks[2]}, nil
	case "firewall":
		if len(toks) < 3 || !oneOf(toks[1], "allow", "deny") {
			return Step{}, msgs.Errorf("script.badFirewall")
		}
		m := portRe.FindStringSubmatch(toks[2])
		if m == nil {
			return Step{}, msgs.Errorf("script.badPort", toks[2])
		}
		if n, _ := strconv.Atoi(m[1]); n < 1 || n > 65535 {
			return Step{}, msgs.Errorf("script.badPort", toks[2])
		}
		args, err := kv(toks[3:], []string{"from"}, nil)
		if err != nil {
			return Step{}, err
		}
		if from := args["from"]; from != "" {
			if _, _, err := net.ParseCIDR(from); err != nil && net.ParseIP(from) == nil {
				return Step{}, msgs.Errorf("script.badFrom", from)
			}
		}
		args["port"] = m[1]
		args["proto"] = m[2]
		if args["proto"] == "" {
			args["proto"] = "tcp"
		}
		return Step{Kind: KindFirewall, Action: toks[1], Args: args}, nil
	case "docker":
		if len(toks) >= 2 && toks[1] == "install" {
			if len(toks) != 2 {
				return Step{}, msgs.Errorf("script.unexpectedWord", toks[2])
			}
			return Step{Kind: KindDockerInst}, nil
		}
		if len(toks) != 4 || toks[1] != "stack" || !pathRe.MatchString(toks[2]) || !oneOf(toks[3], "up", "down") {
			return Step{}, msgs.Errorf("script.badDockerStack")
		}
		return Step{Kind: KindDockerStack, Action: toks[3], Args: map[string]string{"path": toks[2]}}, nil
	case "vm":
		if len(toks) >= 3 && toks[1] == "create" {
			if !nameRe.MatchString(toks[2]) {
				return Step{}, msgs.Errorf("script.badName", "vm")
			}
			args, err := kv(toks[3:], []string{"image", "cpu", "mem", "disk", "user", "network", "profile"}, []string{"install"})
			if err != nil {
				return Step{}, err
			}
			if args["image"] == "" {
				return Step{}, msgs.Errorf("script.vmNeedsImage")
			}
			for _, k := range []string{"cpu", "mem", "disk"} {
				if v, ok := args[k]; ok {
					if n, err := strconv.Atoi(v); err != nil || n < 1 {
						return Step{}, msgs.Errorf("script.badNumber", k, v)
					}
				}
			}
			return Step{Kind: KindVMCreate, Name: toks[2], Args: args}, nil
		}
		if len(toks) == 3 && oneOf(toks[1], "start", "shutdown", "destroy") && nameRe.MatchString(toks[2]) {
			return Step{Kind: KindVMAction, Action: toks[1], Name: toks[2]}, nil
		}
		return Step{}, msgs.Errorf("script.badVM")
	case "apply":
		if len(toks) != 3 || toks[1] != "profile" {
			return Step{}, msgs.Errorf("script.badApply")
		}
		return Step{Kind: KindApplyProfile, Name: toks[2]}, nil
	case "user":
		if len(toks) < 3 || toks[1] != "add" || !nameRe.MatchString(toks[2]) {
			return Step{}, msgs.Errorf("script.badUserAdd")
		}
		args, err := kv(toks[3:], []string{"key"}, []string{"sudo"})
		if err != nil {
			return Step{}, err
		}
		if k := args["key"]; k != "" && !strings.HasPrefix(k, "ssh-") && !strings.HasPrefix(k, "ecdsa-") {
			return Step{}, msgs.Errorf("script.badSSHKey")
		}
		return Step{Kind: KindUserAdd, Name: toks[2], Args: args}, nil
	case "system":
		if len(toks) < 3 {
			return Step{}, msgs.Errorf("script.badSystem")
		}
		switch toks[1] {
		case "hostname", "timezone", "locale":
			if len(toks) != 3 {
				return Step{}, msgs.Errorf("script.badSystem")
			}
			return Step{Kind: KindSystem, Action: toks[1], Args: map[string]string{"value": toks[2]}}, nil
		case "ntp":
			for _, srv := range toks[2:] {
				if !nameRe.MatchString(srv) {
					return Step{}, msgs.Errorf("script.badName", "ntp")
				}
			}
			return Step{Kind: KindSystem, Action: "ntp", List: toks[2:]}, nil
		}
		return Step{}, msgs.Errorf("script.badSystem")
	case "cert":
		if len(toks) < 3 || !oneOf(toks[1], "issue", "renew") || (toks[1] == "renew" && len(toks) != 3) {
			return Step{}, msgs.Errorf("script.badCert")
		}
		for _, d := range toks[2:] {
			if !nameRe.MatchString(d) || !strings.Contains(d, ".") {
				return Step{}, msgs.Errorf("script.badDomain", d)
			}
		}
		return Step{Kind: KindCert, Action: toks[1], Name: toks[2], List: toks[2:]}, nil
	case "git":
		if len(toks) < 4 || toks[1] != "clone" || !pathRe.MatchString(toks[3]) {
			return Step{}, msgs.Errorf("script.badGitClone")
		}
		args, err := kv(toks[4:], []string{"branch", "token", "key"}, nil)
		if err != nil {
			return Step{}, err
		}
		if args["token"] != "" && args["token"] != "ask" {
			return Step{}, msgs.Errorf("script.tokenOnlyAsk")
		}
		if args["key"] != "" && args["key"] != "hub" {
			return Step{}, msgs.Errorf("script.keyOnlyHub")
		}
		args["url"], args["dir"] = toks[2], toks[3]
		return Step{Kind: KindGitClone, Args: args}, nil
	case "file":
		if len(toks) < 3 || toks[1] != "put" || !pathRe.MatchString(toks[2]) {
			return Step{}, msgs.Errorf("script.badFilePut")
		}
		args, err := kv(toks[3:], []string{"mode"}, nil)
		if err != nil {
			return Step{}, err
		}
		if m := args["mode"]; m != "" {
			if _, err := strconv.ParseUint(m, 8, 32); err != nil || len(m) > 4 {
				return Step{}, msgs.Errorf("script.badMode", m)
			}
		} else {
			args["mode"] = "0644"
		}
		args["path"] = toks[2]
		return Step{Kind: KindFilePut, Args: args}, nil
	}
	return Step{}, msgs.Errorf("script.unknownOnCommand", toks[0])
}

// splitAddr делит «адрес[:порт]»; IPv6 — в квадратных скобках.
func splitAddr(s string) (addr, port string, err error) {
	port = "22"
	if strings.HasPrefix(s, "[") {
		host, p, e := net.SplitHostPort(s)
		if e != nil {
			return "", "", msgs.Errorf("script.badAddr", s)
		}
		return host, p, nil
	}
	if i := strings.LastIndex(s, ":"); i > 0 && strings.Count(s, ":") == 1 {
		addr, port = s[:i], s[i+1:]
	} else {
		addr = s
	}
	if addr == "" {
		return "", "", msgs.Errorf("script.badAddr", s)
	}
	if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
		return "", "", msgs.Errorf("script.badAddr", s)
	}
	return addr, port, nil
}

// parseWait разбирает «wait ХОСТ online|port|http|service …».
func parseWait(toks []string) (Step, error) {
	if len(toks) < 3 || !nameRe.MatchString(toks[1]) {
		return Step{}, msgs.Errorf("script.badWait")
	}
	args := map[string]string{"timeout": "5m"}
	rest := toks[3:]
	st := Step{Kind: KindWait, Host: toks[1], Action: toks[2], Args: args}
	switch toks[2] {
	case "online":
	case "port":
		if len(rest) < 1 {
			return Step{}, msgs.Errorf("script.badWait")
		}
		if n, err := strconv.Atoi(rest[0]); err != nil || n < 1 || n > 65535 {
			return Step{}, msgs.Errorf("script.badPort", rest[0])
		}
		args["port"] = rest[0]
		rest = rest[1:]
		args["timeout"] = "1m"
	case "http":
		if len(rest) < 1 || (!strings.HasPrefix(rest[0], "http://") && !strings.HasPrefix(rest[0], "https://")) {
			return Step{}, msgs.Errorf("script.badWaitHTTP")
		}
		args["url"] = rest[0]
		args["code"] = "200"
		rest = rest[1:]
		if len(rest) > 0 {
			if n, err := strconv.Atoi(rest[0]); err == nil && n >= 100 && n < 600 {
				args["code"] = rest[0]
				rest = rest[1:]
			}
		}
		args["timeout"] = "1m"
	case "service":
		if len(rest) < 2 || !nameRe.MatchString(rest[0]) || rest[1] != "active" {
			return Step{}, msgs.Errorf("script.badWait")
		}
		st.Name = rest[0]
		rest = rest[2:]
		args["timeout"] = "1m"
	default:
		return Step{}, msgs.Errorf("script.badWait")
	}
	if len(rest) > 1 {
		return Step{}, msgs.Errorf("script.unexpectedWord", rest[1])
	}
	if len(rest) == 1 {
		if _, err := time.ParseDuration(rest[0]); err != nil {
			return Step{}, msgs.Errorf("script.badDuration", rest[0])
		}
		args["timeout"] = rest[0]
	}
	return st, nil
}
