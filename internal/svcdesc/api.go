package svcdesc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// То, что нужно API хоста и хабу: отчёт по описаниям, уровень «профиль»
// (его пишет хаб), журнал разбора одного описания и образец источника
// для конструктора путей.

// ProfileDir — каталог описаний уровня «профиль».
func ProfileDir(dataDir string) string { return filepath.Join(dataDir, "services.d", "profile") }

// fresh — движок с теми же каталогами и пустым кэшем (журнал разбора не
// должен видеть «из кэша»).
func (e *Engine) fresh() *Engine {
	return &Engine{c: e.c, hostDir: e.hostDir, profileDir: e.profileDir, lastGoodDir: e.lastGoodDir,
		Lang: e.Lang, files: map[string]fileEntry{}, fetched: map[string]fetchEntry{}}
}

// ReportRow — строка отчёта: одно описание и что оно дало.
type ReportRow struct {
	Name   string `json:"name"`
	Title  string `json:"title,omitempty"`
	Origin string `json:"origin"`
	File   string `json:"file"`
	// State — found | not_found | broken | fallback | disabled |
	// overridden | engine | engine_off.
	State     string            `json:"state"`
	Error     string            `json:"error,omitempty"`
	Options   map[string]string `json:"options,omitempty"`
	Endpoints int               `json:"endpoints"`
	Upstreams int               `json:"upstreams"`
	Findings  int               `json:"findings"`
	Warnings  []string          `json:"warnings,omitempty"`
}

// Report — все описания хоста (снимок — для обнаружения по портам и
// контейнерам).
func (e *Engine) Report(ctx context.Context, snap *model.Snapshot, lang msgs.Lang) []ReportRow {
	effective, all := e.Load()
	res := e.RunLoaded(ctx, snap, effective)
	engines := e.Engines(ctx, effective, all)
	status := map[string]model.SourceStatus{}
	for _, s := range res.Sources {
		status[s.Name] = s
	}
	counts := map[string][3]int{}
	for _, ep := range res.Endpoints {
		v := counts[ep.Service]
		v[0]++
		counts[ep.Service] = v
	}
	for _, u := range res.Upstreams {
		v := counts[u.Service]
		v[1]++
		counts[u.Service] = v
	}
	for _, f := range res.Findings {
		v := counts[f.Service]
		v[2]++
		counts[f.Service] = v
	}
	active := map[string]string{}
	for _, l := range effective {
		if l.Desc != nil {
			active[l.Desc.Name] = l.File
		}
	}
	out := []ReportRow{}
	for _, l := range all {
		row := ReportRow{Origin: l.Origin, File: l.File, Name: filepath.Base(l.File)}
		if l.Desc == nil {
			row.State, row.Error = "broken", msgs.Localize(lang, l.Err)
			out = append(out, row)
			continue
		}
		d := l.Desc
		row.Name, row.Title = d.Name, d.Title
		n := counts[d.Name]
		switch {
		case d.Disabled:
			row.State = "disabled"
		case active[d.Name] != l.File:
			row.State = "overridden"
		case d.Engine != "":
			c := engines[d.Engine]
			row.State, row.Options = "engine", c.Options
			if !c.Enabled {
				row.State = "engine_off"
			}
		default:
			st := status["service:"+d.Name]
			row.State = "not_found"
			if st.Available {
				row.State = "found"
				row.Endpoints, row.Upstreams, row.Findings = n[0], n[1], n[2]
			}
			if l.Fallback {
				row.State, row.Error = "fallback", msgs.Localize(lang, l.Err)
			}
			for i, w := range st.Warnings {
				if i < len(st.WarningRefs) {
					w = msgs.T(lang, st.WarningRefs[i].Key, st.WarningRefs[i].Args...)
				}
				row.Warnings = append(row.Warnings, w)
			}
		}
		out = append(out, row)
	}
	return out
}

// Named — описание по имени (уровень «профиль» с хаба).
type Named struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// SetProfile заменяет набор описаний уровня «профиль»: все проверяются до
// записи; описания, которых в наборе нет, удаляются.
func SetProfile(dataDir string, items []Named) error {
	dir := ProfileDir(dataDir)
	keep := map[string]bool{}
	for _, it := range items {
		d, err := Parse([]byte(it.Content))
		if err != nil {
			return msgs.Errorf("svcdesc.inDescription", it.Name, err)
		}
		if d.Name != it.Name {
			return msgs.Errorf("svcdesc.nameMismatch", it.Name, d.Name)
		}
		keep[it.Name+".yaml"] = true
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, it := range items {
		tmp := filepath.Join(dir, "."+it.Name+".tmp")
		if err := os.WriteFile(tmp, []byte(it.Content), 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, it.Name+".yaml")); err != nil {
			return err
		}
	}
	ents, _ := os.ReadDir(dir)
	for _, en := range ents {
		if !en.IsDir() && descFileRe.MatchString(en.Name()) && !keep[en.Name()] {
			_ = os.Remove(filepath.Join(dir, en.Name()))
		}
	}
	return nil
}

// ProfileNames — описания уровня «профиль» на хосте.
func ProfileNames(dataDir string) []string {
	ents, _ := os.ReadDir(ProfileDir(dataDir))
	var out []string
	for _, en := range ents {
		if descFileRe.MatchString(en.Name()) {
			out = append(out, strings.TrimSuffix(strings.TrimSuffix(en.Name(), ".yaml"), ".yml"))
		}
	}
	sort.Strings(out)
	return out
}

// TestResult — журнал разбора одного описания и что оно дало.
type TestResult struct {
	Error     string           `json:"error,omitempty"`
	Log       []string         `json:"log"`
	Endpoints []model.Endpoint `json:"endpoints"`
	Upstreams []model.Upstream `json:"upstreams"`
	Findings  []model.Finding  `json:"findings"`
	Warnings  []string         `json:"warnings"`
	Version   string           `json:"version,omitempty"`
}

// Test — описание целиком на этом хосте с журналом (как nkt services test).
func (e *Engine) Test(ctx context.Context, snap *model.Snapshot, content string, lang msgs.Lang) TestResult {
	res := TestResult{Log: []string{}, Endpoints: []model.Endpoint{}, Upstreams: []model.Upstream{}, Findings: []model.Finding{}, Warnings: []string{}}
	d, err := Parse([]byte(content))
	if err != nil {
		res.Error = msgs.Localize(lang, err)
		return res
	}
	if d.Engine != "" {
		res.Error = msgs.T(lang, "svcdesc.testEngine", d.Name)
		return res
	}
	t := e.fresh()
	t.Lang = lang
	t.Trace = func(line string) { res.Log = append(res.Log, line) }
	r := t.RunLoaded(ctx, snap, []Loaded{{Desc: d, Origin: OriginHost, File: "test.yaml"}})
	res.Endpoints = append(res.Endpoints, r.Endpoints...)
	res.Upstreams = append(res.Upstreams, r.Upstreams...)
	for _, f := range r.Findings {
		f.Title = msgs.T(lang, f.TitleKey, f.TitleArgs...)
		if f.DetailKey != "" {
			f.Detail = msgs.T(lang, f.DetailKey, f.DetailArgs...)
		}
		if f.SuggestionKey != "" {
			f.Suggestion = msgs.T(lang, f.SuggestionKey, f.SuggestionArgs...)
		}
		res.Findings = append(res.Findings, f)
	}
	for _, s := range r.Sources {
		res.Version = s.Version
		for i, w := range s.Warnings {
			if i < len(s.WarningRefs) {
				w = msgs.T(lang, s.WarningRefs[i].Key, s.WarningRefs[i].Args...)
			}
			res.Warnings = append(res.Warnings, w)
		}
	}
	return res
}

// SampleDoc — документ образца: файл и дерево (JSON с порядком ключей).
type SampleDoc struct {
	File string          `json:"file,omitempty"`
	Tree json.RawMessage `json:"tree"`
}

// maxSampleBytes — предел дерева образца для браузера.
const maxSampleBytes = 1 << 20

// Sample — что даёт один источник на этом хосте (конструктор путей).
func (e *Engine) Sample(ctx context.Context, src Source, lang msgs.Lang) ([]SampleDoc, []string, error) {
	src.ID = "sample"
	if err := src.check(); err != nil {
		return nil, nil, err
	}
	var warnings []string
	warn := func(key string, args ...any) { warnings = append(warnings, msgs.T(lang, key, args...)) }
	t := e.fresh()
	d := &Descriptor{Name: "sample"}
	var out []SampleDoc
	for _, doc := range t.read(ctx, d, src, warn) {
		raw, err := MarshalTree(doc.Root)
		if err != nil {
			return nil, warnings, err
		}
		if len(raw) > maxSampleBytes {
			warn("svcdesc.sampleTooBig", docName(doc))
			continue
		}
		out = append(out, SampleDoc{File: doc.File, Tree: raw})
		if len(out) >= 20 {
			break
		}
	}
	return out, warnings, nil
}

// MarshalTree — дерево в JSON с порядком ключей.
func MarshalTree(v any) ([]byte, error) {
	var b strings.Builder
	if err := writeTree(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeTree(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case *Obj:
		b.WriteByte('{')
		for i, k := range t.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			if err := writeTree(b, t.Values[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeTree(b, x); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return err
		}
		b.Write(raw)
	}
	return nil
}

// EvalResult — путь и поле над образцом (конструктор).
type EvalResult struct {
	Items  []EvalItem `json:"items"`
	Error  string     `json:"error,omitempty"`
	Values []any      `json:"values,omitempty"`
}

// EvalItem — узел each: ключ, значение и результат выражения на нём.
type EvalItem struct {
	Key    any   `json:"key"`
	Value  any   `json:"value"`
	When   *bool `json:"when,omitempty"`
	Values []any `json:"values,omitempty"`
}

// Eval — each, when и выражение над деревом (JSON образца).
func Eval(tree []byte, each, when, expr string, lang msgs.Lang) EvalResult {
	res := EvalResult{Items: []EvalItem{}}
	root, err := Decode(FormatJSON, tree, FormatOptions{})
	if err != nil {
		res.Error = msgs.Localize(lang, err)
		return res
	}
	if each == "" {
		each = "$"
	}
	p, err := CompilePath(each)
	if err != nil {
		res.Error = msgs.Localize(lang, err)
		return res
	}
	var cond fexpr
	if when != "" {
		if cond, err = CompileCond(when); err != nil {
			res.Error = msgs.Localize(lang, err)
			return res
		}
	}
	var ex *Expr
	if expr != "" {
		if ex, err = CompileExpr(expr); err != nil {
			res.Error = msgs.Localize(lang, err)
			return res
		}
	}
	nodes := p.Eval(root, root)
	if len(nodes) == 1 {
		if _, isList := nodes[0].Value.([]any); isList {
			nodes = children(nodes[0].Value)
		}
	}
	for i, n := range nodes {
		if i >= 200 {
			break
		}
		it := EvalItem{Key: n.Key, Value: Plain(n.Value)}
		if cond != nil {
			ok := truthOf(cond.eval(root, n.Value))
			it.When = &ok
		}
		if ex != nil {
			for _, v := range ex.Values(Ctx{Root: root, Item: n}) {
				it.Values = append(it.Values, Plain(v))
			}
		}
		res.Items = append(res.Items, it)
	}
	return res
}

// HostFiles — описания на хосте как файлы «Конфигураций» (категория
// svcdesc): правка — с историей и проверкой разбором.
func HostFiles(c collect.Collector, dir string) []model.ManagedFile {
	if dir == "" {
		return nil
	}
	list, _ := c.Glob(dir + "/*.y*ml")
	var out []model.ManagedFile
	for _, p := range list {
		if descFileRe.MatchString(filepath.Base(p)) {
			out = append(out, describeFile(c, p, model.ServiceSvcDesc, c.Writable(p)))
		}
	}
	return out
}

// BuiltinFile — встроенное описание как есть (для хаба).
type BuiltinFile struct {
	File    string
	Content string
}

// Builtin — встроенные описания по порядку файлов.
func Builtin() []BuiltinFile {
	entries, _ := builtinFS.ReadDir("builtin")
	var out []BuiltinFile
	for _, en := range entries {
		if data, err := builtinFS.ReadFile("builtin/" + en.Name()); err == nil && strings.HasSuffix(en.Name(), ".yaml") {
			out = append(out, BuiltinFile{File: en.Name(), Content: string(data)})
		}
	}
	return out
}
