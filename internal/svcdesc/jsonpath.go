package svcdesc

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/piqab/nkt/internal/msgs"
)

// Пути — JSONPath по RFC 9535: $ — корень документа, @ — текущий элемент;
// .имя и ['имя'], [n], [a:b:c], * и .*, .. (потомки), фильтры [?…] со
// сравнениями, && || ! и функциями length, count, match, search, value.
// Отступления от RFC, ради конфигов, где всё — строки: число сравнивается
// со строкой-числом как число («6379» == 6379), логическое — со строкой
// true/false/yes/no/on/off/1/0.

// Node — узел результата: значение и его ключ в родителе (имя или номер;
// у корня — nil). Ключ — то, что в полях описания называется @key.
type Node struct {
	Key   any
	Value any
}

// Path — разобранный путь.
type Path struct {
	src  string
	root byte // '$' или '@'
	segs []segment
}

type segment struct {
	descendant bool
	sels       []selector
}

type selKind int

const (
	selName selKind = iota
	selWild
	selIndex
	selSlice
	selFilter
)

type selector struct {
	kind             selKind
	name             string
	index            int
	start, end, step *int
	filter           fexpr
}

// String — исходный текст пути.
func (p *Path) String() string { return p.src }

var pathCache sync.Map

// CompilePath разбирает путь (кэшируется по тексту).
func CompilePath(s string) (*Path, error) {
	if v, ok := pathCache.Load(s); ok {
		return v.(*Path), nil
	}
	ps := &pparser{src: s}
	p, err := ps.query()
	if err == nil {
		ps.ws()
		if ps.pos < len(ps.src) {
			err = ps.errf("trailing")
		}
	}
	if err != nil {
		return nil, err
	}
	pathCache.Store(s, p)
	return p, nil
}

// Eval — узлы пути: root — документ ($), cur — текущий элемент (@).
func (p *Path) Eval(root, cur any) []Node {
	start := root
	if p.root == '@' {
		start = cur
	}
	nodes := []Node{{Value: start}}
	for _, sg := range p.segs {
		var next []Node
		for _, n := range nodes {
			if sg.descendant {
				for _, d := range descendants(n) {
					next = append(next, applySels(sg.sels, d, root)...)
				}
			} else {
				next = append(next, applySels(sg.sels, n, root)...)
			}
		}
		nodes = next
	}
	return nodes
}

func descendants(n Node) []Node {
	out := []Node{n}
	switch t := n.Value.(type) {
	case *Obj:
		for _, k := range t.Keys {
			out = append(out, descendants(Node{Key: k, Value: t.Values[k]})...)
		}
	case []any:
		for i, v := range t {
			out = append(out, descendants(Node{Key: i, Value: v})...)
		}
	}
	return out
}

func applySels(sels []selector, n Node, root any) []Node {
	var out []Node
	for _, s := range sels {
		switch s.kind {
		case selName:
			if o, ok := n.Value.(*Obj); ok {
				if v, ok := o.Get(s.name); ok {
					out = append(out, Node{Key: s.name, Value: v})
				}
			}
		case selWild:
			out = append(out, children(n.Value)...)
		case selIndex:
			if a, ok := n.Value.([]any); ok {
				i := s.index
				if i < 0 {
					i += len(a)
				}
				if i >= 0 && i < len(a) {
					out = append(out, Node{Key: i, Value: a[i]})
				}
			}
		case selSlice:
			if a, ok := n.Value.([]any); ok {
				out = append(out, slice(a, s)...)
			}
		case selFilter:
			for _, c := range children(n.Value) {
				if truthOf(s.filter.eval(root, c.Value)) {
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func children(v any) []Node {
	switch t := v.(type) {
	case *Obj:
		out := make([]Node, 0, len(t.Keys))
		for _, k := range t.Keys {
			out = append(out, Node{Key: k, Value: t.Values[k]})
		}
		return out
	case []any:
		out := make([]Node, 0, len(t))
		for i, x := range t {
			out = append(out, Node{Key: i, Value: x})
		}
		return out
	}
	return nil
}

func slice(a []any, s selector) []Node {
	n := len(a)
	step := 1
	if s.step != nil {
		step = *s.step
	}
	if step == 0 {
		return nil
	}
	norm := func(i int) int {
		if i < 0 {
			return i + n
		}
		return i
	}
	var lo, hi int
	if step > 0 {
		start, end := 0, n
		if s.start != nil {
			start = norm(*s.start)
		}
		if s.end != nil {
			end = norm(*s.end)
		}
		lo, hi = min(max(start, 0), n), min(max(end, 0), n)
		var out []Node
		for i := lo; i < hi; i += step {
			out = append(out, Node{Key: i, Value: a[i]})
		}
		return out
	}
	start, end := n-1, -n-1
	if s.start != nil {
		start = norm(*s.start)
	}
	if s.end != nil {
		end = norm(*s.end)
	}
	hi, lo = min(max(start, -1), n-1), min(max(end, -1), n-1)
	var out []Node
	for i := hi; i > lo; i += step {
		out = append(out, Node{Key: i, Value: a[i]})
	}
	return out
}

// --- фильтры --------------------------------------------------------------

// Результат выражения фильтра: логическое, список узлов, значение или
// «ничего» (Nothing по RFC).
type fval struct {
	kind  int // 0 — ничего, 1 — значение, 2 — логическое, 3 — узлы
	value any
	logic bool
	nodes []Node
}

func truthOf(v fval) bool {
	switch v.kind {
	case 2:
		return v.logic
	case 3:
		return len(v.nodes) > 0
	case 1:
		return Truthy(v.value)
	}
	return false
}

// asValue — значение для сравнения: узлы — первое значение.
func asValue(v fval) (any, bool) {
	switch v.kind {
	case 1:
		return v.value, true
	case 3:
		if len(v.nodes) > 0 {
			return v.nodes[0].Value, true
		}
	case 2:
		return v.logic, true
	}
	return nil, false
}

type fexpr interface {
	eval(root, cur any) fval
}

type fOr struct{ l, r fexpr }
type fAnd struct{ l, r fexpr }
type fNot struct{ x fexpr }
type fLit struct{ v any }
type fQuery struct{ p *Path }
type fCmp struct {
	op   string
	l, r fexpr
}
type fFunc struct {
	name string
	args []fexpr
	re   *regexp.Regexp
}

func (e fOr) eval(root, cur any) fval {
	return fval{kind: 2, logic: truthOf(e.l.eval(root, cur)) || truthOf(e.r.eval(root, cur))}
}
func (e fAnd) eval(root, cur any) fval {
	return fval{kind: 2, logic: truthOf(e.l.eval(root, cur)) && truthOf(e.r.eval(root, cur))}
}
func (e fNot) eval(root, cur any) fval { return fval{kind: 2, logic: !truthOf(e.x.eval(root, cur))} }
func (e fLit) eval(_, _ any) fval      { return fval{kind: 1, value: e.v} }
func (e fQuery) eval(root, cur any) fval {
	return fval{kind: 3, nodes: e.p.Eval(root, cur)}
}

func (e fCmp) eval(root, cur any) fval {
	l, lok := asValue(e.l.eval(root, cur))
	r, rok := asValue(e.r.eval(root, cur))
	res := false
	switch e.op {
	case "==":
		res = (!lok && !rok) || (lok && rok && equal(l, r))
	case "!=":
		res = !((!lok && !rok) || (lok && rok && equal(l, r)))
	default:
		if lok && rok {
			if c, ok := order(l, r); ok {
				switch e.op {
				case "<":
					res = c < 0
				case "<=":
					res = c <= 0
				case ">":
					res = c > 0
				case ">=":
					res = c >= 0
				}
			}
		}
	}
	return fval{kind: 2, logic: res}
}

func (e fFunc) eval(root, cur any) fval {
	args := make([]fval, len(e.args))
	for i, a := range e.args {
		args[i] = a.eval(root, cur)
	}
	switch e.name {
	case "length":
		v, ok := asValue(args[0])
		if !ok {
			return fval{}
		}
		switch t := v.(type) {
		case string:
			return fval{kind: 1, value: float64(utf8.RuneCountInString(t))}
		case []any:
			return fval{kind: 1, value: float64(len(t))}
		case *Obj:
			return fval{kind: 1, value: float64(len(t.Keys))}
		}
		return fval{}
	case "count":
		return fval{kind: 1, value: float64(len(args[0].nodes))}
	case "value":
		if args[0].kind == 3 && len(args[0].nodes) == 1 {
			return fval{kind: 1, value: args[0].nodes[0].Value}
		}
		if args[0].kind == 1 {
			return args[0]
		}
		return fval{}
	case "match", "search":
		s, ok := asValue(args[0])
		str, isStr := s.(string)
		if !ok || !isStr {
			return fval{kind: 2}
		}
		re := e.re
		if re == nil {
			p, ok := asValue(args[1])
			ps, isStr := p.(string)
			if !ok || !isStr {
				return fval{kind: 2}
			}
			var err error
			if re, err = compileRe(e.name, ps); err != nil {
				return fval{kind: 2}
			}
		}
		return fval{kind: 2, logic: re.MatchString(str)}
	}
	return fval{}
}

func compileRe(fn, pattern string) (*regexp.Regexp, error) {
	if fn == "match" {
		pattern = "^(?:" + pattern + ")$"
	}
	return regexp.Compile(pattern)
}

func num(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

func boolWord(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1":
		return true, true
	case "false", "no", "off", "0":
		return false, true
	}
	return false, false
}

func equal(a, b any) bool {
	switch x := a.(type) {
	case float64:
		if y, ok := num(b); ok {
			return x == y
		}
	case bool:
		switch y := b.(type) {
		case bool:
			return x == y
		case string:
			if bv, ok := boolWord(y); ok {
				return x == bv
			}
		}
		return false
	case string:
		switch y := b.(type) {
		case string:
			return x == y
		case float64:
			if f, ok := num(x); ok {
				return f == y
			}
			return false
		case bool:
			return equal(b, a)
		}
	case nil:
		return b == nil
	}
	return reflect.DeepEqual(Plain(a), Plain(b))
}

func order(a, b any) (int, bool) {
	if x, ok := a.(float64); ok {
		if y, ok := num(b); ok {
			return cmpF(x, y), true
		}
	}
	if y, ok := b.(float64); ok {
		if x, ok := num(a); ok {
			return cmpF(x, y), true
		}
	}
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			return strings.Compare(x, y), true
		}
	}
	return 0, false
}

func cmpF(x, y float64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// --- разбор ----------------------------------------------------------------

type pparser struct {
	src string
	pos int
}

// errf — ошибка пути: ключ каталога svcdesc.path.<key>, аргументы —
// путь, позиция и свои.
func (p *pparser) errf(key string, a ...any) error {
	return msgs.Errorf("svcdesc.path."+key, append([]any{p.src, p.pos + 1}, a...)...)
}

func (p *pparser) ws() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\n') {
		p.pos++
	}
}

func (p *pparser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

func (p *pparser) eat(s string) bool {
	if strings.HasPrefix(p.src[p.pos:], s) {
		p.pos += len(s)
		return true
	}
	return false
}

func (p *pparser) query() (*Path, error) {
	start := p.pos
	c := p.peek()
	if c != '$' && c != '@' {
		return nil, p.errf("start")
	}
	p.pos++
	q := &Path{root: c}
	for {
		switch {
		case strings.HasPrefix(p.src[p.pos:], ".."):
			p.pos += 2
			sg := segment{descendant: true}
			switch {
			case p.peek() == '[':
				sels, err := p.bracket()
				if err != nil {
					return nil, err
				}
				sg.sels = sels
			case p.peek() == '*':
				p.pos++
				sg.sels = []selector{{kind: selWild}}
			default:
				name := p.member()
				if name == "" {
					return nil, p.errf("afterDescendant")
				}
				sg.sels = []selector{{kind: selName, name: name}}
			}
			q.segs = append(q.segs, sg)
		case p.peek() == '.':
			p.pos++
			if p.peek() == '*' {
				p.pos++
				q.segs = append(q.segs, segment{sels: []selector{{kind: selWild}}})
				continue
			}
			name := p.member()
			if name == "" {
				return nil, p.errf("afterDot")
			}
			q.segs = append(q.segs, segment{sels: []selector{{kind: selName, name: name}}})
		case p.peek() == '[':
			sels, err := p.bracket()
			if err != nil {
				return nil, err
			}
			q.segs = append(q.segs, segment{sels: sels})
		default:
			q.src = p.src[start:p.pos]
			return q, nil
		}
	}
}

// member — имя после точки: буквы, цифры, _ и -, а также не-ASCII.
func (p *pparser) member() string {
	start := p.pos
	for p.pos < len(p.src) {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		if r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) || r > 127 {
			p.pos += size
			continue
		}
		break
	}
	return p.src[start:p.pos]
}

func (p *pparser) bracket() ([]selector, error) {
	p.pos++ // [
	var sels []selector
	for {
		p.ws()
		s, err := p.selector()
		if err != nil {
			return nil, err
		}
		sels = append(sels, s)
		p.ws()
		if p.eat(",") {
			continue
		}
		if p.eat("]") {
			return sels, nil
		}
		return nil, p.errf("bracketEnd")
	}
}

func (p *pparser) selector() (selector, error) {
	switch c := p.peek(); {
	case c == '*':
		p.pos++
		return selector{kind: selWild}, nil
	case c == '\'' || c == '"':
		s, err := p.str()
		return selector{kind: selName, name: s}, err
	case c == '?':
		p.pos++
		p.ws()
		e, err := p.orExpr()
		return selector{kind: selFilter, filter: e}, err
	case c == '-' || c == ':' || (c >= '0' && c <= '9'):
		return p.indexOrSlice()
	}
	return selector{}, p.errf("selector")
}

func (p *pparser) int() (*int, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	if start == p.pos {
		return nil, nil
	}
	n, err := strconv.Atoi(p.src[start:p.pos])
	if err != nil {
		return nil, p.errf("number", p.src[start:p.pos])
	}
	return &n, nil
}

func (p *pparser) indexOrSlice() (selector, error) {
	a, err := p.int()
	if err != nil {
		return selector{}, err
	}
	p.ws()
	if p.peek() != ':' {
		if a == nil {
			return selector{}, p.errf("index")
		}
		return selector{kind: selIndex, index: *a}, nil
	}
	p.pos++
	p.ws()
	b, err := p.int()
	if err != nil {
		return selector{}, err
	}
	p.ws()
	var c *int
	if p.eat(":") {
		p.ws()
		if c, err = p.int(); err != nil {
			return selector{}, err
		}
	}
	return selector{kind: selSlice, start: a, end: b, step: c}, nil
}

func (p *pparser) str() (string, error) {
	q := p.src[p.pos]
	p.pos++
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == q:
			p.pos++
			return b.String(), nil
		case c == '\\' && p.pos+1 < len(p.src):
			p.pos++
			switch e := p.src[p.pos]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '\'', '"', '\\', '/':
				b.WriteByte(e)
			default:
				// Прочее (\s, \., \d) остаётся как есть — для регулярок.
				b.WriteByte('\\')
				b.WriteByte(e)
			}
			p.pos++
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
	return "", p.errf("string")
}

func (p *pparser) orExpr() (fexpr, error) {
	l, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	for {
		p.ws()
		if !p.eat("||") {
			return l, nil
		}
		p.ws()
		r, err := p.andExpr()
		if err != nil {
			return nil, err
		}
		l = fOr{l, r}
	}
}

func (p *pparser) andExpr() (fexpr, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		p.ws()
		if !p.eat("&&") {
			return l, nil
		}
		p.ws()
		r, err := p.unary()
		if err != nil {
			return nil, err
		}
		l = fAnd{l, r}
	}
}

func (p *pparser) unary() (fexpr, error) {
	p.ws()
	if p.peek() == '!' && !strings.HasPrefix(p.src[p.pos:], "!=") {
		p.pos++
		x, err := p.unary()
		return fNot{x}, err
	}
	if p.eat("(") {
		e, err := p.orExpr()
		if err != nil {
			return nil, err
		}
		p.ws()
		if !p.eat(")") {
			return nil, p.errf("paren")
		}
		return e, nil
	}
	l, err := p.operand()
	if err != nil {
		return nil, err
	}
	p.ws()
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.eat(op) {
			p.ws()
			r, err := p.operand()
			if err != nil {
				return nil, err
			}
			return fCmp{op: op, l: l, r: r}, nil
		}
	}
	return l, nil
}

func (p *pparser) operand() (fexpr, error) {
	c := p.peek()
	switch {
	case c == '$' || c == '@':
		q, err := p.query()
		return fQuery{q}, err
	case c == '\'' || c == '"':
		s, err := p.str()
		return fLit{s}, err
	case c == '-' || (c >= '0' && c <= '9'):
		start := p.pos
		p.pos++
		for p.pos < len(p.src) && strings.ContainsRune("0123456789.eE+-", rune(p.src[p.pos])) {
			p.pos++
		}
		f, err := strconv.ParseFloat(p.src[start:p.pos], 64)
		if err != nil {
			return nil, p.errf("number", p.src[start:p.pos])
		}
		return fLit{f}, nil
	}
	for _, lit := range []struct {
		s string
		v any
	}{{"true", true}, {"false", false}, {"null", nil}} {
		if p.eat(lit.s) {
			return fLit{lit.v}, nil
		}
	}
	start := p.pos
	for p.pos < len(p.src) && (p.src[p.pos] >= 'a' && p.src[p.pos] <= 'z') {
		p.pos++
	}
	name := p.src[start:p.pos]
	arity := map[string]int{"length": 1, "count": 1, "value": 1, "match": 2, "search": 2}
	n, ok := arity[name]
	if !ok || !p.eat("(") {
		p.pos = start
		return nil, p.errf("operand")
	}
	f := fFunc{name: name}
	for i := 0; i < n; i++ {
		p.ws()
		a, err := p.operand()
		if err != nil {
			return nil, err
		}
		f.args = append(f.args, a)
		p.ws()
		if i < n-1 && !p.eat(",") {
			return nil, p.errf("args", name, n)
		}
	}
	if !p.eat(")") {
		return nil, p.errf("funcParen", name)
	}
	if lit, ok := f.args[len(f.args)-1].(fLit); ok && (name == "match" || name == "search") {
		s, _ := lit.v.(string)
		re, err := compileRe(name, s)
		if err != nil {
			return nil, p.errf("regex", name, err)
		}
		f.re = re
	}
	return f, nil
}
