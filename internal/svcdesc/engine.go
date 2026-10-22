package svcdesc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

//go:embed builtin
var builtinFS embed.FS

// HostDir — описания на самом хосте (читаются через Collector: в режиме
// fixtures — из снимка).
const HostDir = "/etc/netknownsthat/services.d"

// Engine читает описания и применяет их к снимку хоста.
type Engine struct {
	c collect.Collector
	// hostDir — каталог описаний хоста (через Collector).
	hostDir string
	// profileDir — описания из профилей хаба (каталог данных nkt).
	profileDir string
	// lastGoodDir — последние исправные версии описаний хоста и профиля.
	lastGoodDir string

	// Trace — журнал разбора (nkt services test); nil — без журнала.
	Trace func(line string)
	// Lang — язык журнала.
	Lang msgs.Lang

	mu      sync.Mutex
	files   map[string]fileEntry
	fetched map[string]fetchEntry
}

// NewEngine — движок; dataDir — каталог данных nkt (профили и прошлые
// исправные версии), пусто — только встроенные и хост.
func NewEngine(c collect.Collector, hostDir, dataDir string) *Engine {
	e := &Engine{c: c, hostDir: hostDir, files: map[string]fileEntry{}, fetched: map[string]fetchEntry{}, Lang: msgs.DefaultLang}
	if dataDir != "" {
		e.profileDir = filepath.Join(dataDir, "services.d", "profile")
		e.lastGoodDir = filepath.Join(dataDir, "services.d", "lastgood")
	}
	return e
}

func (e *Engine) tracef(key string, args ...any) {
	if e.Trace != nil {
		e.Trace(msgs.T(e.Lang, key, args...))
	}
}

// Loaded — описание и откуда оно.
type Loaded struct {
	Desc   *Descriptor
	Origin string
	File   string
	// Err — ошибка описания (с Fallback — используется прошлая исправная).
	Err      error
	Fallback bool
	// Overrides — какие описания с тем же именем перекрыты (origin).
	Overrides []string
}

var originRank = map[string]int{OriginBuiltin: 1, OriginProfile: 2, OriginHost: 3}

// Load — все описания и действующие: по имени — старшее, «disabled» —
// выключает имя.
func (e *Engine) Load() (effective []Loaded, all []Loaded) {
	all = append(all, e.loadBuiltin()...)
	all = append(all, e.loadDir(OriginProfile, e.profileDir, false)...)
	all = append(all, e.loadDir(OriginHost, e.hostDir, true)...)
	byName := map[string]int{}
	var order []string
	var eff []Loaded
	for _, l := range all {
		if l.Desc == nil {
			eff = append(eff, l) // ошибка без исправной версии — видна в источниках
			continue
		}
		name := l.Desc.Name
		if i, ok := byName[name]; ok {
			prev := eff[i]
			if originRank[l.Origin] >= originRank[prev.Origin] {
				l.Overrides = append(prev.Overrides, prev.Origin)
				eff[i] = l
			}
			continue
		}
		byName[name] = len(eff)
		order = append(order, name)
		eff = append(eff, l)
	}
	for _, l := range eff {
		if l.Desc != nil && l.Desc.Disabled {
			continue
		}
		effective = append(effective, l)
	}
	return effective, all
}

func (e *Engine) loadBuiltin() []Loaded {
	entries, _ := builtinFS.ReadDir("builtin")
	var out []Loaded
	for _, en := range entries {
		if !strings.HasSuffix(en.Name(), ".yaml") {
			continue
		}
		data, err := builtinFS.ReadFile("builtin/" + en.Name())
		if err != nil {
			continue
		}
		d, err := Parse(data)
		out = append(out, Loaded{Desc: d, Origin: OriginBuiltin, File: "builtin/" + en.Name(), Err: err})
	}
	return out
}

var descFileRe = regexp.MustCompile(`^[A-Za-z0-9._-]+\.ya?ml$`)

// loadDir — описания из каталога: хоста — через Collector, профиля — с
// диска nkt.
func (e *Engine) loadDir(origin, dir string, viaCollector bool) []Loaded {
	if dir == "" {
		return nil
	}
	var names []string
	read := func(p string) ([]byte, error) { return os.ReadFile(p) }
	if viaCollector {
		list, _ := e.c.Glob(dir + "/*.y*ml")
		names = list
		read = e.c.ReadFile
	} else {
		ents, _ := os.ReadDir(dir)
		for _, en := range ents {
			names = append(names, filepath.Join(dir, en.Name()))
		}
	}
	sort.Strings(names)
	var out []Loaded
	for _, p := range names {
		base := filepath.Base(p)
		if !descFileRe.MatchString(base) {
			continue
		}
		data, err := read(p)
		if err != nil {
			out = append(out, Loaded{Origin: origin, File: p, Err: err})
			continue
		}
		d, err := Parse(data)
		l := Loaded{Desc: d, Origin: origin, File: p, Err: err}
		if err == nil {
			e.saveLastGood(origin, base, data)
		} else if good := e.lastGood(origin, base); good != nil {
			l.Desc, l.Fallback = good, true
		}
		out = append(out, l)
	}
	return out
}

func (e *Engine) lastGoodPath(origin, base string) string {
	return filepath.Join(e.lastGoodDir, origin+"-"+base)
}

func (e *Engine) saveLastGood(origin, base string, data []byte) {
	if e.lastGoodDir == "" {
		return
	}
	p := e.lastGoodPath(origin, base)
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, data) {
		return
	}
	if os.MkdirAll(e.lastGoodDir, 0o700) == nil {
		_ = os.WriteFile(p, data, 0o600)
	}
}

func (e *Engine) lastGood(origin, base string) *Descriptor {
	if e.lastGoodDir == "" {
		return nil
	}
	data, err := os.ReadFile(e.lastGoodPath(origin, base))
	if err != nil {
		return nil
	}
	d, err := Parse(data)
	if err != nil {
		return nil
	}
	return d
}

// Result — что дали описания.
type Result struct {
	Endpoints []model.Endpoint
	Upstreams []model.Upstream
	Files     []model.ManagedFile
	Findings  []model.Finding
	Sources   []model.SourceStatus
	// Detected — найденные сервисы (для раздела «Сервисы»).
	Detected []Detected
	// Replaced — файлы встроенных разборщиков, которые забрало описание с
	// большим priority (из snap.Files убрать).
	Replaced []string
}

// Detected — найденный сервис.
type Detected struct {
	Name, Title, Unit, Origin string
	Files                     []string
}

type runOut struct {
	l        Loaded
	status   model.SourceStatus
	found    bool
	eps      []model.Endpoint
	ups      []model.Upstream
	files    []model.ManagedFile
	findings []model.Finding
	// unit — загруженный юнит сервиса (unit описания или из detect).
	unit string
}

// Run применяет действующие описания к снимку (адреса, контейнеры и
// процессы для обнаружения берутся из него).
func (e *Engine) Run(ctx context.Context, snap *model.Snapshot) Result {
	effective, _ := e.Load()
	return e.RunLoaded(ctx, snap, effective)
}

// RunLoaded — то же для заданного списка (nkt services test).
func (e *Engine) RunLoaded(ctx context.Context, snap *model.Snapshot, list []Loaded) Result {
	// Описания встроенных разборщиков не исполняются здесь: их запускает
	// сканер (Engines), здесь — только приоритет их файлов.
	enginePrio := map[string]int{}
	var plain []Loaded
	for _, l := range list {
		if l.Desc != nil && l.Desc.Engine != "" {
			enginePrio[l.Desc.Engine] = l.Desc.Priority
			continue
		}
		plain = append(plain, l)
	}
	list = plain
	outs := make([]runOut, len(list))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, l := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outs[i] = e.runOne(ctx, snap, l)
		}()
	}
	wg.Wait()

	var res Result
	// Файл в двух описаниях (или у встроенного разборщика) — один
	// владелец: встроенный, затем больший приоритет, затем старшее место.
	owner := map[string]int{}
	native := map[string]bool{}
	if snap != nil {
		for _, f := range snap.Files {
			native[f.Path] = true
		}
	}
	claims := map[string][]string{}
	for i, o := range outs {
		for _, f := range o.files {
			claims[f.Path] = append(claims[f.Path], o.l.Desc.Name)
			j, ok := owner[f.Path]
			if !ok || better(o.l, outs[j].l) {
				owner[f.Path] = i
			}
		}
	}
	// Файл встроенного разборщика забирает описание с priority выше его.
	nativePrio := func(p string) int {
		if pr, ok := enginePrio[nativeOwner(snap, p)]; ok {
			return pr
		}
		return EnginePriority
	}
	for p, i := range owner {
		if native[p] && outs[i].l.Desc.Priority > nativePrio(p) {
			delete(native, p)
			res.Replaced = append(res.Replaced, p)
		}
	}
	sort.Strings(res.Replaced)
	for i, o := range outs {
		if o.l.Desc != nil && (o.found || o.l.Origin != OriginBuiltin || o.l.Err != nil) {
			res.Sources = append(res.Sources, o.status)
		} else if o.l.Desc == nil {
			res.Sources = append(res.Sources, o.status)
		}
		if !o.found {
			continue
		}
		res.Endpoints = append(res.Endpoints, o.eps...)
		res.Upstreams = append(res.Upstreams, o.ups...)
		res.Findings = append(res.Findings, o.findings...)
		det := Detected{Name: o.l.Desc.Name, Title: o.l.Desc.Title, Unit: o.unit, Origin: o.l.Origin}
		for _, f := range o.files {
			det.Files = append(det.Files, f.Path)
			if owner[f.Path] == i && !native[f.Path] {
				res.Files = append(res.Files, f)
			}
		}
		res.Detected = append(res.Detected, det)
	}
	paths := make([]string, 0, len(claims))
	for p := range claims {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		names := claims[p]
		if len(names) < 2 && !native[p] {
			continue
		}
		winner := outs[owner[p]].l.Desc.Name
		if native[p] {
			winner = nativeOwner(snap, p)
		}
		if replaced := sort.SearchStrings(res.Replaced, p); replaced < len(res.Replaced) && res.Replaced[replaced] == p {
			names = append(names, nativeOwner(snap, p))
		}
		sort.Strings(names)
		res.Findings = append(res.Findings, model.Finding{
			ID: "svcdesc-file-conflict:" + p, Rule: "svcdesc-file-conflict", Severity: model.SeverityLow,
			Service: winner, Object: p, File: p,
			Title:    msgs.T(msgs.DefaultLang, "svcdesc.conflict.title", p),
			TitleKey: "svcdesc.conflict.title", TitleArgs: []any{p},
			Detail:    msgs.T(msgs.DefaultLang, "svcdesc.conflict.detail", strings.Join(names, ", "), winner),
			DetailKey: "svcdesc.conflict.detail", DetailArgs: []any{strings.Join(names, ", "), winner},
			Suggestion:    msgs.T(msgs.DefaultLang, "svcdesc.conflict.suggestion"),
			SuggestionKey: "svcdesc.conflict.suggestion",
		})
	}
	return res
}

func nativeOwner(snap *model.Snapshot, p string) string {
	for _, f := range snap.Files {
		if f.Path == p {
			return f.Service
		}
	}
	return ""
}

// better — a старше b за владение файлом.
func better(a, b Loaded) bool {
	if a.Desc.Priority != b.Desc.Priority {
		return a.Desc.Priority > b.Desc.Priority
	}
	if originRank[a.Origin] != originRank[b.Origin] {
		return originRank[a.Origin] > originRank[b.Origin]
	}
	return a.Desc.Name < b.Desc.Name
}

func (e *Engine) runOne(ctx context.Context, snap *model.Snapshot, l Loaded) (o runOut) {
	started := time.Now()
	o.l = l
	name := l.File
	if l.Desc != nil {
		name = l.Desc.Name
	}
	o.status = model.SourceStatus{Name: "service:" + name}
	defer func() { o.status.DurationMS = time.Since(started).Milliseconds() }()
	warn := func(key string, args ...any) {
		o.status.Warnings = append(o.status.Warnings, msgs.T(msgs.DefaultLang, key, args...))
		o.status.WarningRefs = append(o.status.WarningRefs, model.TextRef{Key: key, Args: args})
		e.tracef(key, args...)
	}
	if l.Err != nil {
		if l.Desc == nil {
			o.status.Error = msgs.T(msgs.DefaultLang, "svcdesc.w.broken", l.File, l.Err)
			o.status.ErrorKey, o.status.ErrorArgs = "svcdesc.w.broken", []any{l.File, l.Err}
			e.tracef("svcdesc.w.broken", l.File, l.Err)
			return o
		}
		warn("svcdesc.w.fallback", l.File, l.Err)
	}
	d := l.Desc
	e.tracef("svcdesc.t.start", d.Name, l.Origin, l.File)
	if !e.detect(ctx, snap, d) {
		e.tracef("svcdesc.t.notFound", d.Name)
		return o
	}
	o.found = true
	o.status.Available = true
	o.unit = e.loadedUnit(ctx, d)
	docs := map[string][]Doc{}
	for _, s := range d.Sources {
		docs[s.ID] = e.read(ctx, d, s, warn)
		for _, doc := range docs[s.ID] {
			if doc.File == "" {
				continue
			}
			o.status.Files = append(o.status.Files, doc.File)
			editable := s.Editable == nil || *s.Editable
			o.files = append(o.files, describeFile(e.c, doc.File, d.Name, editable && e.c.Writable(doc.File)))
		}
	}
	if d.Version != nil {
		for _, doc := range docs[d.Version.From] {
			c := Ctx{Root: doc.Root, Item: Node{Value: doc.Root}, File: doc.File, Source: d.Version.From}
			if v := Text(d.Version.expr.Value(c)); v != "" {
				o.status.Version = v
				break
			}
		}
	}
	items := func(it Item) []Ctx {
		var out []Ctx
		for _, doc := range docs[it.From] {
			nodes := it.each.nodes(doc.Root, doc.Root)
			if len(nodes) == 1 {
				if _, isList := nodes[0].Value.([]any); isList {
					nodes = children(nodes[0].Value)
				}
			}
			n := 0
			for _, nd := range nodes {
				c := Ctx{Root: doc.Root, Item: nd, File: doc.File, Source: it.From}
				if it.when != nil && !truthOf(it.when.eval(doc.Root, nd.Value)) {
					continue
				}
				out = append(out, c)
				n++
			}
			e.tracef("svcdesc.t.items", it.From, docName(doc), it.each.String(), len(nodes), n)
		}
		return out
	}
	for ri, r := range d.Extract.Endpoints {
		for ii, c := range items(r.Item) {
			ep, ok := buildEndpoint(d, r, c)
			if !ok {
				e.tracef("svcdesc.t.noPort", keyText(c.Item.Key))
				continue
			}
			ep.ID = fmt.Sprintf("%s:%d:%d:%s", d.Name, ri, ii, ep.Socket())
			o.eps = append(o.eps, ep)
			e.tracef("svcdesc.t.endpoint", ep.Socket(), ep.Protocol, ep.Label)
		}
	}
	for _, r := range d.Extract.Upstreams {
		for _, c := range items(r.Item) {
			up := buildUpstream(d, r, c)
			if up.Name == "" {
				continue
			}
			o.ups = append(o.ups, up)
			e.tracef("svcdesc.t.upstream", up.Name, len(up.Servers))
		}
	}
	for _, r := range d.Rules {
		for _, c := range items(r.Item) {
			f := buildFinding(d, r, c)
			o.findings = append(o.findings, f)
			e.tracef("svcdesc.t.finding", r.ID, f.Object)
		}
	}
	e.tracef("svcdesc.t.done", d.Name, len(o.eps), len(o.ups), len(o.findings), len(o.status.Warnings))
	return o
}

func docName(d Doc) string {
	if d.File != "" {
		return d.File
	}
	return "—"
}

func (e *Engine) detect(ctx context.Context, snap *model.Snapshot, d *Descriptor) bool {
	probe := func(p Probe) bool {
		ok := e.probe(ctx, snap, p)
		e.tracef("svcdesc.t.probe", probeText(p), ok)
		return ok
	}
	if len(d.Detect.Any) == 0 && len(d.Detect.All) == 0 {
		for _, s := range d.Sources {
			if s.Kind() == "file" && probe(Probe{File: s.File}) {
				return true
			}
		}
		if d.Unit != "" {
			return probe(Probe{Unit: d.Unit})
		}
		return false
	}
	for _, p := range d.Detect.All {
		if !probe(p) {
			return false
		}
	}
	if len(d.Detect.Any) == 0 {
		return true
	}
	for _, p := range d.Detect.Any {
		if probe(p) {
			return true
		}
	}
	return false
}

// loadedUnit — первый загруженный юнит: unit описания, затем из detect
// (redis-server в Debian, redis в RHEL).
func (e *Engine) loadedUnit(ctx context.Context, d *Descriptor) string {
	cands := []string{}
	if d.Unit != "" {
		cands = append(cands, d.Unit)
	}
	for _, p := range append(append([]Probe{}, d.Detect.Any...), d.Detect.All...) {
		if p.Unit != "" && p.Unit != d.Unit {
			cands = append(cands, p.Unit)
		}
	}
	for _, u := range cands {
		if e.probe(ctx, nil, Probe{Unit: u}) {
			return u
		}
	}
	return ""
}

func probeText(p Probe) string {
	switch {
	case p.File != "":
		return "file " + p.File
	case p.Unit != "":
		return "unit " + p.Unit
	case p.Binary != "":
		return "binary " + p.Binary
	case p.Image != "":
		return "image " + p.Image
	case p.Process != "":
		return "process " + p.Process
	}
	return fmt.Sprintf("port %d", p.Port)
}

func (e *Engine) probe(ctx context.Context, snap *model.Snapshot, p Probe) bool {
	switch {
	case p.File != "":
		if strings.ContainsAny(p.File, "*?[") {
			m, _ := e.c.Glob(p.File)
			return len(m) > 0
		}
		return e.c.Exists(p.File)
	case p.Unit != "":
		res, err := e.c.Run(ctx, "systemctl", "show", "-p", "LoadState", "--value", p.Unit)
		return err == nil && res.OK() && strings.TrimSpace(res.Stdout) == "loaded"
	case p.Binary != "":
		// Имя проверено (binaryRe) — в sh -c попадает только оно.
		return binaryRe.MatchString(p.Binary) && collect.Which(ctx, e.c, p.Binary)
	case p.Image != "":
		if snap == nil {
			return false
		}
		for _, c := range snap.Container {
			if strings.Contains(c.Image, p.Image) {
				return true
			}
		}
		for _, c := range snap.Podman {
			if strings.Contains(c.Image, p.Image) {
				return true
			}
		}
	case p.Process != "":
		if snap != nil {
			for _, l := range snap.Listeners {
				if l.Process == p.Process {
					return true
				}
			}
		}
	case p.Port != 0:
		if snap != nil {
			for _, l := range snap.Listeners {
				if l.Port == p.Port && l.Protocol == "tcp" {
					return true
				}
			}
		}
	}
	return false
}

func field(fields map[string]*Expr, name string, c Ctx) any {
	if e := fields[name]; e != nil {
		return e.Value(c)
	}
	return nil
}

func fieldText(fields map[string]*Expr, name string, c Ctx) string {
	return Text(field(fields, name, c))
}

func buildEndpoint(d *Descriptor, r EndpointRule, c Ctx) (model.Endpoint, bool) {
	f := r.fields
	port := 0
	if v, ok := num(field(f, "port", c)); ok {
		port = int(v)
	}
	if port <= 0 || port > 65535 {
		return model.Endpoint{}, false
	}
	ep := model.Endpoint{
		Service: d.Name, Kind: "listen", Protocol: "tcp", Port: port, File: c.File,
		Address: fieldText(f, "address", c),
	}
	if v := strings.ToLower(fieldText(f, "protocol", c)); v != "" {
		ep.Protocol = v
	}
	if v := fieldText(f, "kind", c); v != "" {
		ep.Kind = v
	}
	ep.Mode = fieldText(f, "mode", c)
	if e := f["tls"]; e != nil {
		ep.TLS = Truthy(e.Value(c))
	}
	if e := f["names"]; e != nil {
		ep.Names = e.Strings(c)
	}
	if e := f["upstreams"]; e != nil {
		ep.Upstream = e.Strings(c)
	}
	ep.Label = fieldText(f, "label", c)
	if ep.Label == "" {
		title := d.Title
		if title == "" {
			title = d.Name
		}
		if k := keyText(c.Item.Key); k != "" {
			title += " · " + k
		}
		ep.Label = title
	}
	return ep, true
}

func buildUpstream(d *Descriptor, r UpstreamRule, c Ctx) model.Upstream {
	up := model.Upstream{
		Service: d.Name, File: c.File,
		Name:      fieldText(r.fields, "name", c),
		Mode:      fieldText(r.fields, "mode", c),
		Algorithm: fieldText(r.fields, "algorithm", c),
		Health:    fieldText(r.fields, "health", c),
		Servers:   []model.UpstreamServer{},
	}
	up.ID = d.Name + ":" + up.Name
	nodes := r.Servers.each.nodes(c.Root, c.Item.Value)
	if len(nodes) == 1 {
		if _, isList := nodes[0].Value.([]any); isList {
			nodes = children(nodes[0].Value)
		}
	}
	for _, n := range nodes {
		sc := Ctx{Root: c.Root, Item: n, File: c.File, Source: c.Source}
		sf := r.Servers.fields
		srv := model.UpstreamServer{Name: fieldText(sf, "name", sc)}
		if u := fieldText(sf, "url", sc); u != "" {
			srv.Host, srv.Port = hostOf(u), portOf(u)
		}
		if h := fieldText(sf, "host", sc); h != "" {
			srv.Host = hostOf(h)
			if p := portOf(h); p > 0 && srv.Port == 0 {
				srv.Port = p
			}
		}
		if v, ok := num(field(sf, "port", sc)); ok && v > 0 {
			srv.Port = int(v)
		}
		if v, ok := num(field(sf, "weight", sc)); ok {
			srv.Weight = int(v)
		}
		if e := sf["backup"]; e != nil {
			srv.Backup = Truthy(e.Value(sc))
		}
		if e := sf["down"]; e != nil {
			srv.Down = Truthy(e.Value(sc))
		}
		if srv.Host == "" && srv.Port == 0 {
			continue
		}
		up.Servers = append(up.Servers, srv)
	}
	return up
}

func buildFinding(d *Descriptor, r Rule, c Ctx) model.Finding {
	object := ""
	if r.object != nil {
		object = strings.Join(r.object.Strings(c), ", ")
	}
	if object == "" {
		object = keyText(c.Item.Key)
	}
	if object == "" {
		object = c.File
	}
	if object == "" {
		object = d.Name
	}
	rule := "svc." + d.Name + "." + r.ID
	ru := func(t I18n) string { return render(t.RU, c) }
	en := func(t I18n) string { return render(t.EN, c) }
	f := model.Finding{
		ID: rule + ":" + object, Rule: rule, Severity: r.Severity, Service: d.Name,
		Object: object, File: c.File,
		Title: ru(r.Title), TitleKey: "svcdesc.text", TitleArgs: []any{ru(r.Title), en(r.Title)},
	}
	if !r.Detail.Empty() {
		f.Detail, f.DetailKey, f.DetailArgs = ru(r.Detail), "svcdesc.text", []any{ru(r.Detail), en(r.Detail)}
	}
	if !r.Suggestion.Empty() {
		f.Suggestion, f.SuggestionKey, f.SuggestionArgs = ru(r.Suggestion), "svcdesc.text", []any{ru(r.Suggestion), en(r.Suggestion)}
	}
	return f
}

// describeFile — запись о файле для «Конфигураций».
func describeFile(c collect.Collector, path, service string, editable bool) model.ManagedFile {
	f := model.ManagedFile{Path: path, Service: service, Editable: editable, InUse: true}
	if st, err := c.Stat(path); err == nil {
		f.Size, f.ModTime, f.Readable = st.Size, st.ModTime, true
	}
	if data, err := c.ReadFile(path); err == nil {
		sum := sha256.Sum256(data)
		f.SHA256 = hex.EncodeToString(sum[:])
	} else {
		f.Readable, f.Editable, f.Note = false, false, err.Error()
	}
	return f
}

// EngineConf — как запускать встроенный разборщик.
type EngineConf struct {
	Enabled  bool
	Options  map[string]string
	Priority int
	Origin   string
	File     string
	// Reason — почему выключен: disabled (описанием) или detect.
	Reason string
}

// EngineConfs — настройки всех встроенных разборщиков.
type EngineConfs map[string]EngineConf

// On — разборщик включён (неизвестный — включён).
func (m EngineConfs) On(name string) bool {
	c, ok := m[name]
	return !ok || c.Enabled
}

// Opt — настройка разборщика или значение по умолчанию (из окружения).
func (m EngineConfs) Opt(name, key, def string) string {
	if v := m[name].Options[key]; v != "" {
		return v
	}
	return def
}

// List — настройка-список через запятую.
func (m EngineConfs) List(name, key string, def []string) []string {
	v := m[name].Options[key]
	if v == "" {
		return def
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Engines — настройки встроенных разборщиков из описаний: выключенное
// имя — разборщик не запускается; detect не сработал — тоже.
func (e *Engine) Engines(ctx context.Context, effective, all []Loaded) EngineConfs {
	out := EngineConfs{}
	for name := range EngineOptions {
		out[name] = EngineConf{Enabled: true, Priority: EnginePriority, Origin: OriginBuiltin}
	}
	for _, l := range all {
		if l.Desc != nil && l.Desc.Engine != "" && l.Desc.Disabled {
			out[l.Desc.Engine] = EngineConf{Enabled: false, Priority: EnginePriority, Origin: l.Origin, File: l.File, Reason: "disabled"}
		}
	}
	for _, l := range effective {
		if l.Desc == nil || l.Desc.Engine == "" {
			continue
		}
		d := l.Desc
		c := EngineConf{Enabled: true, Options: d.Options, Priority: d.Priority, Origin: l.Origin, File: l.File}
		if (len(d.Detect.Any) > 0 || len(d.Detect.All) > 0) && !e.detect(ctx, nil, d) {
			c.Enabled, c.Reason = false, "detect"
		}
		out[d.Engine] = c
	}
	return out
}
