package svcdesc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Форматы — как текст источника превращается в дерево. Пути, поля и
// правила одинаковы для всех; формат отвечает только за чтение.
const (
	FormatYAML       = "yaml"
	FormatJSON       = "json"
	FormatTOML       = "toml"
	FormatINI        = "ini"
	FormatEnv        = "env"
	FormatProperties = "properties"
	FormatLines      = "lines"
	FormatXML        = "xml"
	FormatRegex      = "regex"
	FormatText       = "text"
)

// Formats — все форматы (для проверки описания и справки).
var Formats = []string{FormatYAML, FormatJSON, FormatTOML, FormatINI, FormatEnv, FormatProperties, FormatLines, FormatXML, FormatRegex, FormatText}

// FormatOptions — настройки чтения.
type FormatOptions struct {
	// Pattern — regex: выражение с именованными группами; каждое
	// совпадение — объект групп.
	Pattern string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	// Blocks — lines: ключи, которые открывают блок (mosquitto «listener
	// 8883» и следующие за ним настройки).
	Blocks []string `yaml:"blocks,omitempty" json:"blocks,omitempty"`
	// LowerKeys — lines, ini: ключи в нижнем регистре (sshd_config).
	LowerKeys bool `yaml:"lower_keys,omitempty" json:"lower_keys,omitempty"`
}

// Decode читает данные в дерево.
func Decode(format string, data []byte, opt FormatOptions) (any, error) {
	switch format {
	case FormatYAML, "":
		return decodeYAML(data)
	case FormatJSON:
		return decodeJSON(data)
	case FormatTOML:
		var m map[string]any
		if _, err := toml.Decode(string(data), &m); err != nil {
			return nil, err
		}
		return fromPlain(m), nil
	case FormatINI:
		return decodeINI(data, opt), nil
	case FormatEnv:
		return decodeEnv(data), nil
	case FormatProperties:
		return decodeProperties(data), nil
	case FormatLines:
		return decodeLines(data, opt), nil
	case FormatXML:
		return decodeXML(data)
	case FormatRegex:
		re, err := regexp.Compile("(?m)" + opt.Pattern)
		if err != nil {
			return nil, err
		}
		return decodeRegex(re, data), nil
	case FormatText:
		return string(data), nil
	}
	return nil, msgs.Errorf("svcdesc.unknownFormat", format)
}

// --- yaml -----------------------------------------------------------------

func decodeYAML(data []byte) (any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []any
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, yamlNode(&n))
	}
	switch len(docs) {
	case 0:
		return nil, nil
	case 1:
		return docs[0], nil
	}
	// Несколько документов в файле (---) — список.
	return docs, nil
}

func yamlNode(n *yaml.Node) any {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return yamlNode(n.Content[0])
	case yaml.AliasNode:
		return yamlNode(n.Alias)
	case yaml.MappingNode:
		o := NewObj()
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value == "<<" && k.Tag == "!!merge" {
				// Слияние якорей: ключи из него — если своих таких нет.
				for _, src := range mergeSources(v) {
					if so, ok := yamlNode(src).(*Obj); ok {
						for _, mk := range so.Keys {
							if _, has := o.Get(mk); !has {
								o.Set(mk, so.Values[mk])
							}
						}
					}
				}
				continue
			}
			o.Set(k.Value, yamlNode(v))
		}
		return o
	case yaml.SequenceNode:
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			out[i] = yamlNode(c)
		}
		return out
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!int", "!!float":
			if f, err := strconv.ParseFloat(strings.ReplaceAll(n.Value, "_", ""), 64); err == nil {
				return f
			}
			if i, err := strconv.ParseInt(n.Value, 0, 64); err == nil {
				return float64(i)
			}
		case "!!bool":
			var b bool
			if n.Decode(&b) == nil {
				return b
			}
		case "!!null":
			return nil
		}
		return n.Value
	}
	return nil
}

func mergeSources(v *yaml.Node) []*yaml.Node {
	if v.Kind == yaml.SequenceNode {
		return v.Content
	}
	return []*yaml.Node{v}
}

// --- json -----------------------------------------------------------------

func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := jsonValue(dec)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func jsonValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := NewObj()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := jsonValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(k, v)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			out := []any{}
			for dec.More() {
				v, err := jsonValue(dec)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			_, err := dec.Token()
			return out, err
		}
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return t.String(), nil
		}
		return f, nil
	}
	return tok, nil
}

// --- ini ------------------------------------------------------------------

// decodeINI: [секция] → объект; ключи до первой секции — в корне;
// «ключ = значение» или «ключ: значение»; ключ без значения (my.cnf
// skip-name-resolve) — true; повтор ключа — список.
func decodeINI(data []byte, opt FormatOptions) any {
	root := NewObj()
	cur := root
	for _, line := range splitLines(data) {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if v, ok := root.Get(name); ok {
				if o, ok := v.(*Obj); ok {
					cur = o
					continue
				}
			}
			cur = NewObj()
			root.Set(name, cur)
			continue
		}
		i := strings.IndexAny(line, "=:")
		if i < 0 {
			k := line
			if opt.LowerKeys {
				k = strings.ToLower(k)
			}
			cur.Add(k, true)
			continue
		}
		k := strings.TrimSpace(line[:i])
		if opt.LowerKeys {
			k = strings.ToLower(k)
		}
		cur.Add(k, unquote(stripInlineComment(strings.TrimSpace(line[i+1:]), " ;", " #")))
	}
	return finish(root)
}

// --- env ------------------------------------------------------------------

// decodeEnv: KEY=VALUE, необязательный export, кавычки ' и ".
func decodeEnv(data []byte) any {
	root := NewObj()
	for _, line := range splitLines(data) {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
			v = closingQuote(v)
		} else {
			v = stripInlineComment(v, " #")
		}
		root.Add(strings.TrimSpace(k), unquote(v))
	}
	return finish(root)
}

// --- properties -----------------------------------------------------------

// decodeProperties: Java .properties — «ключ=значение», «ключ: значение»
// или «ключ значение», комментарии # и !, перенос строки обратной
// чертой, \uXXXX. Ключи — плоские: ['server.port'].
func decodeProperties(data []byte) any {
	root := NewObj()
	lines := splitLines(data)
	for i := 0; i < len(lines); i++ {
		line := strings.TrimLeft(lines[i], " \t\f")
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		for strings.HasSuffix(line, "\\") && !strings.HasSuffix(line, "\\\\") && i+1 < len(lines) {
			i++
			line = line[:len(line)-1] + strings.TrimLeft(lines[i], " \t\f")
		}
		k, v := splitProperty(line)
		root.Add(unescapeProperty(k), unescapeProperty(v))
	}
	return finish(root)
}

func splitProperty(line string) (string, string) {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '=', ':':
			return strings.TrimSpace(line[:i]), strings.TrimLeft(line[i+1:], " \t\f")
		case ' ', '\t', '\f':
			rest := strings.TrimLeft(line[i:], " \t\f")
			if rest != "" && (rest[0] == '=' || rest[0] == ':') {
				rest = strings.TrimLeft(rest[1:], " \t\f")
			}
			return line[:i], rest
		}
	}
	return line, ""
}

func unescapeProperty(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'u':
			if i+4 < len(s) {
				if r, err := strconv.ParseUint(s[i+1:i+5], 16, 16); err == nil {
					b.WriteRune(rune(r))
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// --- строки «ключ значение» -----------------------------------------------

// decodeLines: redis.conf, postgresql.conf, mosquitto.conf, sshd_config —
// «ключ значение» или «ключ = значение», комментарии #, повтор ключа —
// список; ключ из Blocks открывает блок: объект с «@value» и следующими
// за ним ключами до следующего такого же ключа.
func decodeLines(data []byte, opt FormatOptions) any {
	root := NewObj()
	cur := root
	blocks := map[string]bool{}
	for _, b := range opt.Blocks {
		blocks[strings.ToLower(b)] = true
	}
	for _, line := range splitLines(data) {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		line = stripInlineComment(line, " #", "\t#")
		k, v := line, ""
		if i := strings.IndexAny(line, " \t="); i >= 0 {
			k, v = line[:i], strings.TrimSpace(line[i:])
			v = strings.TrimSpace(strings.TrimPrefix(v, "="))
		}
		if opt.LowerKeys {
			k = strings.ToLower(k)
		}
		v = unquote(v)
		if blocks[strings.ToLower(k)] {
			blk := NewObj()
			blk.Set("@value", v)
			root.Add(k, blk)
			cur = blk
			continue
		}
		cur.Add(k, v)
	}
	return finish(root)
}

// --- xml ------------------------------------------------------------------

// decodeXML: элемент — объект; атрибут — ключ «@имя», текст — «#text»;
// повторяющиеся дочерние элементы — список, одиночный — объект. Корень —
// объект с одним ключом — именем корневого элемента.
func decodeXML(data []byte) (any, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	root := NewObj()
	stack := []*Obj{root}
	var texts []*strings.Builder
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			el := NewObj()
			for _, a := range t.Attr {
				el.Set("@"+xmlName(a.Name), a.Value)
			}
			stack[len(stack)-1].Add(xmlName(t.Name), el)
			stack = append(stack, el)
			texts = append(texts, &strings.Builder{})
		case xml.CharData:
			if len(texts) > 0 {
				texts[len(texts)-1].Write(t)
			}
		case xml.EndElement:
			if len(stack) > 1 {
				el := stack[len(stack)-1]
				if s := strings.TrimSpace(texts[len(texts)-1].String()); s != "" {
					el.Set("#text", s)
				}
				stack = stack[:len(stack)-1]
				texts = texts[:len(texts)-1]
			}
		}
	}
	return finish(root), nil
}

func xmlName(n xml.Name) string { return n.Local }

// --- regex ----------------------------------------------------------------

// decodeRegex: список совпадений; у каждого — именованные группы и
// «#match» — совпадение целиком.
func decodeRegex(re *regexp.Regexp, data []byte) any {
	out := []any{}
	names := re.SubexpNames()
	for _, m := range re.FindAllSubmatch(data, -1) {
		o := NewObj()
		o.Set("#match", string(m[0]))
		for i, n := range names {
			if n != "" && i < len(m) {
				o.Set(n, string(m[i]))
			}
		}
		out = append(out, o)
	}
	return out
}

// --- общее ----------------------------------------------------------------

func splitLines(data []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		out = append(out, strings.TrimSuffix(sc.Text(), "\r"))
	}
	return out
}

// stripInlineComment — хвостовой комментарий вне кавычек.
func stripInlineComment(s string, marks ...string) string {
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		default:
			for _, m := range marks {
				if strings.HasPrefix(s[i:], m) {
					return strings.TrimSpace(s[:i])
				}
			}
		}
	}
	return s
}

// closingQuote — значение в кавычках до закрывающей (хвост — комментарий).
func closingQuote(s string) string {
	q := s[0]
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' && q == '"' {
			i++
			continue
		}
		if s[i] == q {
			return s[:i+1]
		}
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		if s[0] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u
			}
		}
		return s[1 : len(s)-1]
	}
	return s
}
