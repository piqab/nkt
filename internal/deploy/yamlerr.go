package deploy

import (
	"errors"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Ошибки YAML описания конвейера — по-человечески: строка описания, её
// текст и что, скорее всего, не так («did not find expected key» сам по
// себе ничего не объясняет).

var (
	yamlLineRe  = regexp.MustCompile(`line (\d+): (.*)`)
	yamlFieldRe = regexp.MustCompile(`field (\S+) not found in type deploy\.(\w+)`)
	yamlTypeRe  = regexp.MustCompile("cannot unmarshal !!(\\w+)(?: `[^`]*`)? into (\\S+)")
)

// yamlSections — типы описания: где ключ (для сообщения) и сам тип (для
// списка допустимых ключей).
var yamlSections = map[string]struct {
	where string
	typ   reflect.Type
}{
	"Spec":        {"", reflect.TypeOf(Spec{})},
	"ComposeSpec": {"compose", reflect.TypeOf(ComposeSpec{})},
	"HelmSpec":    {"helm", reflect.TypeOf(HelmSpec{})},
}

func yamlKeys(t reflect.Type) string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag != "" && tag != "-" {
			keys = append(keys, tag)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// yamlHint — пояснение к одной ошибке yaml.v3.
func yamlHint(msg string) error {
	switch {
	case strings.Contains(msg, "did not find expected key"):
		return msgs.Errorf("deploy.yamlHintKey")
	case strings.Contains(msg, "mapping values are not allowed"):
		return msgs.Errorf("deploy.yamlHintColon")
	case strings.Contains(msg, "tab character") || strings.Contains(msg, "cannot start any token"):
		return msgs.Errorf("deploy.yamlHintTab")
	case strings.Contains(msg, "did not find expected") || strings.Contains(msg, "could not find expected"):
		return msgs.Errorf("deploy.yamlHintQuote")
	}
	if m := yamlFieldRe.FindStringSubmatch(msg); m != nil {
		sec, ok := yamlSections[m[2]]
		if !ok {
			return msgs.Errorf("deploy.yamlHintFieldAny", m[1])
		}
		if sec.where == "" {
			return msgs.Errorf("deploy.yamlHintFieldTop", m[1], yamlKeys(sec.typ))
		}
		return msgs.Errorf("deploy.yamlHintField", m[1], sec.where, yamlKeys(sec.typ))
	}
	if m := yamlTypeRe.FindStringSubmatch(msg); m != nil {
		from, into := m[1], m[2]
		switch {
		case strings.HasPrefix(into, "int") || strings.HasPrefix(into, "uint"):
			return msgs.Errorf("deploy.yamlHintInt")
		case strings.HasPrefix(into, "[]"):
			return msgs.Errorf("deploy.yamlHintList")
		case strings.HasPrefix(into, "map"):
			return msgs.Errorf("deploy.yamlHintMap")
		case into == "string" && (from == "map" || from == "seq"):
			return msgs.Errorf("deploy.yamlHintNotString")
		case into == "bool":
			return msgs.Errorf("deploy.yamlHintBool")
		}
	}
	return nil
}

// valueAndBlock — частая ошибка: у ключа и значение, и вложенный блок
// («site: имя» и строки глубже). yaml.v3 сообщает её не там (строкой ниже
// или началом родительского блока) — ищется сама такая строка, начиная с
// указанной; ошибка переносится на неё.
func valueAndBlock(lines []string, n int, msg string) (int, error) {
	if !strings.Contains(msg, "mapping values are not allowed") && !strings.Contains(msg, "did not find expected key") {
		return 0, nil
	}
	indent := func(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }
	meaningful := func(l string) bool {
		t := strings.TrimSpace(l)
		return t != "" && !strings.HasPrefix(t, "#")
	}
	check := func(i int) error {
		l := lines[i]
		if !meaningful(l) {
			return nil
		}
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "- ") {
			return nil
		}
		k, v, ok := strings.Cut(t, ":")
		if j := strings.Index(v, " #"); j >= 0 {
			v = v[:j]
		}
		v = strings.TrimSpace(v)
		if !ok || v == "" || strings.HasPrefix(v, "#") || strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">") || strings.ContainsAny(k, "\"'") {
			return nil
		}
		for j := i + 1; j < len(lines); j++ {
			if !meaningful(lines[j]) {
				continue
			}
			if indent(lines[j]) > indent(l) {
				return msgs.Errorf("deploy.yamlHintValueBlock", strings.TrimSpace(k), i+1, v)
			}
			return nil
		}
		return nil
	}
	start := n - 2 // строкой выше указанной
	if start < 0 {
		start = 0
	}
	for i := start; i < len(lines); i++ {
		if err := check(i); err != nil {
			return i + 1, err
		}
	}
	return 0, nil
}

// explainYAML — ошибка разбора с номером строки, её текстом и пояснением.
func explainYAML(content string, err error) error {
	var me *msgs.Err
	if errors.As(err, &me) {
		return err // своя проверка (compose.site) уже говорит по-русски
	}
	var list []string
	var te *yaml.TypeError
	if errors.As(err, &te) {
		list = te.Errors
	} else {
		list = []string{err.Error()}
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var parts []error
	for _, e := range list {
		m := yamlLineRe.FindStringSubmatch(e)
		if m == nil {
			parts = append(parts, msgs.Errorf("deploy.specYAML", e))
			continue
		}
		n, _ := strconv.Atoi(m[1])
		hint := yamlHint(m[2])
		if at, vb := valueAndBlock(lines, n, m[2]); vb != nil {
			n, hint = at, vb
		}
		text := ""
		if n >= 1 && n <= len(lines) {
			text = strings.TrimRight(lines[n-1], " \t")
			if r := []rune(text); len(r) > 80 {
				text = string(r[:80]) + "…"
			}
		}
		if hint == nil {
			parts = append(parts, msgs.Errorf("deploy.yamlAtRaw", n, text, m[2]))
		} else {
			parts = append(parts, msgs.Errorf("deploy.yamlAt", n, text, hint))
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	// Первая — с пояснением; остальные обычно её следствие.
	return msgs.Errorf("deploy.yamlMore", parts[0], len(parts)-1)
}
