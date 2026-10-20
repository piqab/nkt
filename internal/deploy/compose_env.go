package deploy

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Блок compose.env описания — строки .env стека прямо в описании:
//
//	env:
//	  AUTH_DOMAIN: auth.example.com   # значение
//	  ADMIN_SECRET: generate:hex32    # хаб создаст сам, один раз
//	  JWT_SECRET: "@secret"           # уже в .env конвейера («Секреты»)
//
// При сохранении описания значения уходят в зашифрованный .env конвейера,
// а в тексте описания (и в истории его версий) остаётся «@secret».
// Значения в базу, параметры заданий и журналы не попадают.

// EnvStored — значение «уже в .env конвейера».
const EnvStored = "@secret"

// envGenPrefix — значение, которое хаб создаёт сам.
const envGenPrefix = "generate:"

// EnvGenKinds — виды generate: и сколько байт случайности.
var EnvGenKinds = map[string]int{
	"hex12": 12, "hex16": 16, "hex32": 32,
	"base64-32": 32, "base64-48": 48,
	// Пароль — буквы и цифры: без знаков, которые ломают URL базы
	// (postgres://user:пароль@db) и кавычки .env.
	"password": 24,
}

// EnvEntry — строка блока env.
type EnvEntry struct {
	Key string
	// Value — значение как в описании (для generate: — «generate:вид»).
	Value string
	// Line / Col / End — где значение в тексте (строка с 1, столбцы с 0,
	// End — после значения): по ним значение заменяется на «@secret».
	Line, Col, End int
}

// Stored — значение уже в .env конвейера.
func (e EnvEntry) Stored() bool { return e.Value == EnvStored }

// Generate — вид generate: (пусто — не он).
func (e EnvEntry) Generate() string {
	if k, ok := strings.CutPrefix(e.Value, envGenPrefix); ok {
		return k
	}
	return ""
}

// envValueMax — предел значения (это строка .env, не файл).
const envValueMax = 4096

// EnvBlock — строки compose.env по порядку (нет блока — nil). Проверяет
// имена, виды generate: и что значения однострочные.
func EnvBlock(content string) ([]EnvEntry, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil || len(doc.Content) == 0 {
		return nil, nil // ошибку YAML скажет ParseSpec
	}
	comp := mapValue(doc.Content[0], "compose")
	if comp == nil || comp.Kind != yaml.MappingNode {
		return nil, nil
	}
	env := mapValue(comp, "env")
	if env == nil {
		return nil, nil
	}
	if env.Kind != yaml.MappingNode || env.Style&yaml.FlowStyle != 0 {
		return nil, msgs.Errorf("deploy.envBlockForm")
	}
	lines := strings.Split(content, "\n")
	var out []EnvEntry
	seen := map[string]bool{}
	for i := 0; i+1 < len(env.Content); i += 2 {
		k, v := env.Content[i], env.Content[i+1]
		if !envKeyRe.MatchString(k.Value) {
			return nil, msgs.Errorf("deploy.specBad", "compose.env", k.Value)
		}
		if seen[k.Value] {
			return nil, msgs.Errorf("deploy.specBad", "compose.env", k.Value)
		}
		seen[k.Value] = true
		if v.Kind != yaml.ScalarNode || v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || strings.ContainsAny(v.Value, "\r\n") {
			return nil, msgs.Errorf("deploy.envOneLine", k.Value)
		}
		if len(v.Value) > envValueMax {
			return nil, msgs.Errorf("deploy.specBad", "compose.env."+k.Value, "…")
		}
		e := EnvEntry{Key: k.Value, Value: v.Value, Line: v.Line}
		if g := e.Generate(); g != "" || strings.HasPrefix(v.Value, envGenPrefix) {
			if _, ok := EnvGenKinds[g]; !ok {
				return nil, msgs.Errorf("deploy.envGenKind", k.Value, g)
			}
		}
		if v.Line < 1 || v.Line > len(lines) {
			return nil, msgs.Errorf("deploy.envOneLine", k.Value)
		}
		e.Col, e.End = scalarSpan(lines[v.Line-1], v.Column-1, v.Style)
		if e.End <= e.Col && v.Value != "" {
			return nil, msgs.Errorf("deploy.envOneLine", k.Value)
		}
		out = append(out, e)
	}
	return out, nil
}

// scalarSpan — где в строке однострочное значение, начатое в столбце col:
// у кавычек — до закрывающей, у простого — до « #» или конца строки.
func scalarSpan(line string, col int, style yaml.Style) (int, int) {
	if col < 0 || col > len(line) {
		return col, col
	}
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		for i := col + 1; i < len(line); i++ {
			if line[i] == '\\' {
				i++
				continue
			}
			if line[i] == '"' {
				return col, i + 1
			}
		}
	case style&yaml.SingleQuotedStyle != 0:
		for i := col + 1; i < len(line); i++ {
			if line[i] == '\'' {
				if i+1 < len(line) && line[i+1] == '\'' {
					i++
					continue
				}
				return col, i + 1
			}
		}
	default:
		end := len(line)
		if j := strings.Index(line[col:], " #"); j >= 0 {
			end = col + j
		}
		if j := strings.Index(line[col:], "\t#"); j >= 0 && col+j < end {
			end = col + j
		}
		return col, col + len(strings.TrimRight(line[col:end], " \t"))
	}
	return col, col
}

// StripEnvValues — описание, где значения блока env (кроме уже
// «@secret») заменены на «@secret»; остальной текст не меняется.
func StripEnvValues(content string, entries []EnvEntry) string {
	lines := strings.Split(content, "\n")
	for _, e := range entries {
		if e.Stored() || e.Line < 1 || e.Line > len(lines) {
			continue
		}
		l := lines[e.Line-1]
		if e.Col > len(l) || e.End > len(l) || e.Col > e.End {
			continue
		}
		lines[e.Line-1] = l[:e.Col] + `"` + EnvStored + `"` + l[e.End:]
	}
	return strings.Join(lines, "\n")
}

// HasEnvValues — в блоке env есть значения, которые надо перенести.
func HasEnvValues(entries []EnvEntry) bool {
	for _, e := range entries {
		if !e.Stored() {
			return true
		}
	}
	return false
}

// GenerateEnvValue — новое значение вида kind.
func GenerateEnvValue(kind string) string {
	n := EnvGenKinds[kind]
	b := make([]byte, n)
	_, _ = rand.Read(b)
	switch {
	case strings.HasPrefix(kind, "hex"):
		return hex.EncodeToString(b)
	case strings.HasPrefix(kind, "base64"):
		return base64.StdEncoding.EncodeToString(b)
	}
	const abc = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	out := make([]byte, n)
	for i := range b {
		out[i] = abc[int(b[i])%len(abc)]
	}
	return string(out)
}

// EnvLine — строка .env для значения: с пробелами, «#» или кавычками — в
// одинарных кавычках (там compose ничего не подставляет), с «${» — в
// двойных (ссылка на переменную выше), иначе как есть.
func EnvLine(key, val string) string {
	switch {
	case strings.Contains(val, "${"):
		return key + `="` + strings.ReplaceAll(strings.ReplaceAll(val, `\`, `\\`), `"`, `\"`) + `"`
	case strings.ContainsAny(val, " \t#'\"`$\\"):
		if !strings.Contains(val, "'") {
			return key + "='" + val + "'"
		}
		return key + `="` + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(val, `\`, `\\`), `"`, `\"`), "$", "$$") + `"`
	}
	return key + "=" + val
}

// envLineKeyRe — имя переменной строки .env.
var envLineKeyRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// MergeEnv — .env, где строки set заменили прежние строки тех же имён
// (на их месте), а новые дописаны в конце в порядке order.
func MergeEnv(text string, order []string, set map[string]string) string {
	lines := []string{}
	if text != "" {
		lines = strings.Split(strings.TrimRight(text, "\n"), "\n")
	}
	done := map[string]bool{}
	for i, l := range lines {
		m := envLineKeyRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if v, ok := set[m[1]]; ok && !done[m[1]] {
			lines[i] = EnvLine(m[1], v)
			done[m[1]] = true
		}
	}
	for _, k := range order {
		if v, ok := set[k]; ok && !done[k] {
			lines = append(lines, EnvLine(k, v))
			done[k] = true
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
