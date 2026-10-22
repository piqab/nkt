package svcdesc

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/piqab/nkt/internal/msgs"
)

// Выражение поля: «что взять | преобразование | …».
//
//	@.address | port          — порт из «0.0.0.0:8080», «:8080», URL
//	@key                      — ключ текущего элемента (имя или номер)
//	@file                     — файл, из которого элемент
//	$.global.name | default(x)
//	tcp                       — просто текст
//
// Путь даёт список значений: поле-список (names, upstreams) берёт все,
// поле-значение — первое.

// Ctx — где вычисляется выражение.
type Ctx struct {
	Root any
	Item Node
	File string
	// Source — id источника.
	Source string
}

// Expr — разобранное выражение.
type Expr struct {
	src     string
	operand string
	path    *Path
	literal any
	pipes   []pipe
}

type pipe struct {
	name string
	args []string
}

// String — исходный текст.
func (e *Expr) String() string { return e.src }

var exprCache sync.Map

// pipeArity — преобразования и их аргументы.
var pipeArity = map[string]int{
	"port": 0, "host": 0, "scheme": 0, "lower": 0, "upper": 0, "trim": 0,
	"first": 0, "last": 0, "exists": 0, "int": 0, "bool": 0, "not": 0, "count": 0, "keys": 0,
	"split": 1, "join": 1, "nth": 1, "default": 1, "prefix": 1, "suffix": 1, "contains": 1, "matches": 1,
	"replace": 2,
}

// CompileExpr разбирает выражение поля. Значение не-строкой (число,
// логическое из YAML) — константа.
func CompileExpr(v any) (*Expr, error) {
	s, isStr := v.(string)
	if !isStr {
		return &Expr{src: Text(v), literal: v}, nil
	}
	if e, ok := exprCache.Load(s); ok {
		return e.(*Expr), nil
	}
	parts := splitPipes(s)
	e := &Expr{src: s, operand: strings.TrimSpace(parts[0])}
	switch op := e.operand; {
	case op == "@key" || op == "@file" || op == "@source" || op == "@":
	case strings.HasPrefix(op, "$") || strings.HasPrefix(op, "@"):
		p, err := CompilePath(op)
		if err != nil {
			return nil, err
		}
		e.path = p
	case len(op) >= 2 && (op[0] == '\'' && op[len(op)-1] == '\'' || op[0] == '"' && op[len(op)-1] == '"'):
		e.literal = op[1 : len(op)-1]
	default:
		e.literal = op
	}
	for _, raw := range parts[1:] {
		pp, err := parsePipe(strings.TrimSpace(raw))
		if err != nil {
			return nil, msgs.Errorf("svcdesc.exprPipe", s, err)
		}
		e.pipes = append(e.pipes, pp)
	}
	exprCache.Store(s, e)
	return e, nil
}

// splitPipes — по «|» вне кавычек и скобок, «||» — не разделитель.
func splitPipes(s string) []string {
	var out []string
	depth, start := 0, 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '\'' || c == '"':
			q = c
		case c == '[' || c == '(':
			depth++
		case c == ']' || c == ')':
			depth--
		case c == '|' && depth == 0:
			if i+1 < len(s) && s[i+1] == '|' {
				i++
				continue
			}
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func parsePipe(s string) (pipe, error) {
	name, rest, hasArgs := strings.Cut(s, "(")
	name = strings.TrimSpace(name)
	n, ok := pipeArity[name]
	if !ok {
		return pipe{}, msgs.Errorf("svcdesc.unknownPipe", name)
	}
	p := pipe{name: name}
	if hasArgs {
		rest = strings.TrimSpace(rest)
		if !strings.HasSuffix(rest, ")") {
			return pipe{}, msgs.Errorf("svcdesc.pipeParen", name)
		}
		inner := rest[:len(rest)-1]
		if n == 1 {
			// Один аргумент — вся строка: split(,) и split(', ').
			p.args = []string{unquoteArg(inner)}
		} else {
			p.args = splitArgs(inner)
		}
	}
	if len(p.args) != n {
		return pipe{}, msgs.Errorf("svcdesc.pipeArgs", name, n)
	}
	return p, nil
}

// unquoteArg — аргумент без кавычек; пробелы по краям значимы только в
// кавычках.
func unquoteArg(s string) string {
	t := strings.TrimSpace(s)
	if len(t) >= 2 && (t[0] == '\'' && t[len(t)-1] == '\'' || t[0] == '"' && t[len(t)-1] == '"') {
		return t[1 : len(t)-1]
	}
	return t
}

// splitArgs — аргументы через запятую; 'строка' или "строка" — как есть.
func splitArgs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
				continue
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			q = c
		case c == ',':
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, strings.TrimSpace(cur.String()))
}

// Values — все значения выражения.
func (e *Expr) Values(c Ctx) []any {
	var vals []any
	switch {
	case e.path != nil:
		for _, n := range e.path.Eval(c.Root, c.Item.Value) {
			vals = append(vals, n.Value)
		}
	case e.literal != nil:
		vals = []any{e.literal}
	case e.operand == "@key":
		vals = []any{keyText(c.Item.Key)}
	case e.operand == "@file":
		vals = []any{c.File}
	case e.operand == "@source":
		vals = []any{c.Source}
	case e.operand == "@":
		vals = []any{c.Item.Value}
	}
	for _, p := range e.pipes {
		vals = applyPipe(p, vals)
	}
	return vals
}

// Value — первое значение (nil — нет).
func (e *Expr) Value(c Ctx) any {
	if v := e.Values(c); len(v) > 0 {
		return v[0]
	}
	return nil
}

// Strings — значения строками; список внутри значения раскрывается.
func (e *Expr) Strings(c Ctx) []string {
	var out []string
	for _, v := range e.Values(c) {
		if l, ok := v.([]any); ok {
			for _, x := range l {
				if s := Text(x); s != "" {
					out = append(out, s)
				}
			}
			continue
		}
		if s := Text(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func keyText(k any) string {
	switch t := k.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	}
	return ""
}

func applyPipe(p pipe, vals []any) []any {
	each := func(f func(any) any) []any {
		out := make([]any, 0, len(vals))
		for _, v := range vals {
			if r := f(v); r != nil {
				out = append(out, r)
			}
		}
		return out
	}
	switch p.name {
	case "first":
		if len(vals) > 0 {
			return vals[:1]
		}
		return nil
	case "last":
		if len(vals) > 0 {
			return vals[len(vals)-1:]
		}
		return nil
	case "nth":
		// nth(i) — элемент списка по номеру с нуля (отрицательный — с конца).
		i, err := strconv.Atoi(p.args[0])
		if err != nil {
			return nil
		}
		if i < 0 {
			i += len(vals)
		}
		if i >= 0 && i < len(vals) {
			return vals[i : i+1]
		}
		return nil
	case "count":
		return []any{float64(len(vals))}
	case "exists":
		return []any{len(vals) > 0}
	case "default":
		if len(vals) == 0 || (len(vals) == 1 && Text(vals[0]) == "") {
			return []any{p.args[0]}
		}
		return vals
	case "join":
		parts := make([]string, 0, len(vals))
		for _, v := range vals {
			if l, ok := v.([]any); ok {
				for _, x := range l {
					parts = append(parts, Text(x))
				}
				continue
			}
			parts = append(parts, Text(v))
		}
		return []any{strings.Join(parts, p.args[0])}
	case "split":
		var out []any
		for _, v := range vals {
			sep := p.args[0]
			var fields []string
			if sep == "" || sep == " " {
				fields = strings.Fields(Text(v))
			} else {
				fields = strings.Split(Text(v), sep)
			}
			for _, f := range fields {
				if f = strings.TrimSpace(f); f != "" {
					out = append(out, f)
				}
			}
		}
		return out
	case "keys":
		var out []any
		for _, v := range vals {
			if o, ok := v.(*Obj); ok {
				for _, k := range o.Keys {
					out = append(out, k)
				}
			}
		}
		return out
	case "port":
		return each(func(v any) any {
			if n := portOf(Text(v)); n > 0 {
				return float64(n)
			}
			return nil
		})
	case "host":
		return each(func(v any) any { return hostOf(Text(v)) })
	case "scheme":
		return each(func(v any) any {
			if u, err := url.Parse(Text(v)); err == nil {
				return u.Scheme
			}
			return ""
		})
	case "lower":
		return each(func(v any) any { return strings.ToLower(Text(v)) })
	case "upper":
		return each(func(v any) any { return strings.ToUpper(Text(v)) })
	case "trim":
		return each(func(v any) any { return strings.TrimSpace(Text(v)) })
	case "int":
		return each(func(v any) any {
			if f, ok := num(v); ok {
				return float64(int64(f))
			}
			return nil
		})
	case "bool":
		return each(func(v any) any { return Truthy(v) })
	case "not":
		if len(vals) == 0 {
			return []any{true}
		}
		return each(func(v any) any { return !Truthy(v) })
	case "prefix":
		return each(func(v any) any { return p.args[0] + Text(v) })
	case "suffix":
		return each(func(v any) any { return Text(v) + p.args[0] })
	case "contains":
		return each(func(v any) any { return strings.Contains(Text(v), p.args[0]) })
	case "matches":
		return each(func(v any) any {
			re, err := compileRe("search", p.args[0])
			return err == nil && re.MatchString(Text(v))
		})
	case "replace":
		return each(func(v any) any { return strings.ReplaceAll(Text(v), p.args[0], p.args[1]) })
	}
	return vals
}

// portOf — порт из «8080», «:8080», «0.0.0.0:8080», «[::]:8080»,
// «unix:…» (0) или URL (порт по схеме, если не указан).
func portOf(s string) int {
	s = strings.TrimSpace(s)
	// «:53/udp» (Traefik) — протокол после порта.
	if i := strings.LastIndexByte(s, '/'); i > 0 && !strings.Contains(s, "://") {
		if p := strings.ToLower(s[i+1:]); p == "udp" || p == "tcp" {
			s = s[:i]
		}
	}
	if s == "" {
		return 0
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return 0
		}
		if p := u.Port(); p != "" {
			n, _ := strconv.Atoi(p)
			return n
		}
		return map[string]int{"http": 80, "https": 443, "ws": 80, "wss": 443, "amqp": 5672, "amqps": 5671,
			"mqtt": 1883, "mqtts": 8883, "redis": 6379, "rediss": 6379, "postgres": 5432, "postgresql": 5432}[u.Scheme]
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if _, p, err := net.SplitHostPort(s); err == nil {
		n, _ := strconv.Atoi(p)
		return n
	}
	return 0
}

// hostOf — адрес из тех же форм; «:8080» — пусто (все адреса).
func hostOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '/'); i > 0 && !strings.Contains(s, "://") {
		if p := strings.ToLower(s[i+1:]); p == "udp" || p == "tcp" {
			s = s[:i]
		}
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			return u.Hostname()
		}
		return ""
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	if _, err := strconv.Atoi(s); err == nil {
		return ""
	}
	return strings.Trim(s, "[]")
}
