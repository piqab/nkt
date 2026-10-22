// Package svcdesc — описания сервисов на YAML: где у программы
// конфигурация (файл, HTTP API, команда только для чтения), как её
// прочитать и что из неё взять — адреса, на которых она принимает
// соединения, куда пересылает, правила-находки. Результат — те же
// объекты, что у встроенных разборщиков (model.Endpoint, Upstream,
// ManagedFile, Finding), поэтому карта, находки и «Конфигурации»
// показывают свой сервис так же, как nginx.
//
// Описания берутся из трёх мест, по старшинству: хост
// (/etc/netknownsthat/services.d), профиль хаба (каталог данных) и
// встроенные; описание с тем же именем перекрывает младшее целиком,
// «disabled: true» — выключает.
package svcdesc

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// Откуда описание.
const (
	OriginBuiltin = "builtin"
	OriginProfile = "profile"
	OriginHost    = "host"
)

// DefaultPriority — приоритет описания, если не задан.
const DefaultPriority = 50

// Descriptor — описание сервиса.
type Descriptor struct {
	Name     string `yaml:"name" json:"name"`
	Title    string `yaml:"title,omitempty" json:"title,omitempty"`
	Priority int    `yaml:"priority,omitempty" json:"priority,omitempty"`
	// Disabled — выключить описание с этим именем (например, встроенное).
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	// Engine — встроенный разборщик на Go (nginx, docker…): описание задаёт
	// его пути (Options), приоритет файлов и обнаружение, разбор — Go.
	Engine string `yaml:"engine,omitempty" json:"engine,omitempty"`
	// Options — настройки встроенного разборщика (см. EngineOptions).
	Options map[string]string `yaml:"options,omitempty" json:"options,omitempty"`
	// Unit — юнит systemd: обнаружение и раздел «Сервисы».
	Unit    string   `yaml:"unit,omitempty" json:"unit,omitempty"`
	Detect  Detect   `yaml:"detect,omitempty" json:"detect,omitempty"`
	Sources []Source `yaml:"sources" json:"sources"`
	// Version — версия программы: значение из источника.
	Version *Field  `yaml:"version,omitempty" json:"version,omitempty"`
	Extract Extract `yaml:"extract,omitempty" json:"extract,omitempty"`
	Rules   []Rule  `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// Detect — есть ли сервис на хосте: срабатывает любая проверка из Any и
// все из All. Пусто — есть, если найден хоть один файл-источник.
type Detect struct {
	Any []Probe `yaml:"any,omitempty" json:"any,omitempty"`
	All []Probe `yaml:"all,omitempty" json:"all,omitempty"`
}

// Probe — одна проверка (одно поле).
type Probe struct {
	// File — файл или glob существует.
	File string `yaml:"file,omitempty" json:"file,omitempty"`
	// Unit — юнит systemd загружен.
	Unit string `yaml:"unit,omitempty" json:"unit,omitempty"`
	// Binary — программа есть в PATH.
	Binary string `yaml:"binary,omitempty" json:"binary,omitempty"`
	// Image — контейнер Docker или Podman из образа, имя которого
	// содержит эту строку.
	Image string `yaml:"image,omitempty" json:"image,omitempty"`
	// Process — процесс с таким именем слушает порт.
	Process string `yaml:"process,omitempty" json:"process,omitempty"`
	// Port — кто-то слушает этот TCP-порт.
	Port int `yaml:"port,omitempty" json:"port,omitempty"`
}

// Duration — длительность из «60s», «5m».
type Duration time.Duration

// UnmarshalYAML — из строки Go-длительности.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return msgs.Errorf("svcdesc.badDuration", n.Value)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML — обратно строкой.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Source — откуда данные: ровно одно из File, HTTP, Command.
type Source struct {
	ID string `yaml:"id" json:"id"`
	// File — путь или glob (conf.d/*.yml): каждый файл — свой документ.
	File string `yaml:"file,omitempty" json:"file,omitempty"`
	// HTTP — адрес (http/https, без логина и пароля в адресе).
	HTTP string `yaml:"http,omitempty" json:"http,omitempty"`
	// Command — команда только для чтения: argv, без оболочки.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	// Format — см. Formats; у файла по умолчанию — по расширению, у HTTP
	// и команды — json.
	Format  string        `yaml:"format,omitempty" json:"format,omitempty"`
	Options FormatOptions `yaml:"options,omitempty" json:"options,omitempty"`
	// Interval — HTTP и команда: результат живёт столько (0 — каждый скан).
	Interval Duration `yaml:"interval,omitempty" json:"interval,omitempty"`
	// Timeout — HTTP и команда (по умолчанию 5 с, не больше минуты).
	Timeout Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// Optional — нет файла или ответа — не предупреждение.
	Optional bool `yaml:"optional,omitempty" json:"optional,omitempty"`
	// Headers — HTTP: заголовки (без секретов: секрет — из файла, см.
	// BasicAuth и BearerFile).
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// BasicAuth — HTTP: логин и пароль из файла на хосте.
	BasicAuth *BasicAuth `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
	// BearerFile — HTTP: токен из файла на хосте.
	BearerFile string `yaml:"bearer_file,omitempty" json:"bearer_file,omitempty"`
	// Editable — файл можно править в «Конфигурациях» (по умолчанию да).
	Editable *bool `yaml:"editable,omitempty" json:"editable,omitempty"`
}

// BasicAuth — логин и файл с паролем.
type BasicAuth struct {
	User         string `yaml:"user" json:"user"`
	PasswordFile string `yaml:"password_file" json:"password_file"`
}

// Kind — file | http | command.
func (s Source) Kind() string {
	switch {
	case s.HTTP != "":
		return "http"
	case len(s.Command) > 0:
		return "command"
	}
	return "file"
}

// Field — значение из источника (version).
type Field struct {
	From  string `yaml:"from,omitempty" json:"from,omitempty"`
	Value any    `yaml:"value" json:"value"`

	expr *Expr
}

// Extract — что взять.
type Extract struct {
	Endpoints []EndpointRule `yaml:"endpoints,omitempty" json:"endpoints,omitempty"`
	Upstreams []UpstreamRule `yaml:"upstreams,omitempty" json:"upstreams,omitempty"`
}

// Item — общая часть: из какого источника, по каким элементам, с каким
// условием.
type Item struct {
	// From — id источника (по умолчанию первый).
	From string `yaml:"from,omitempty" json:"from,omitempty"`
	// Each — путь к элементам ($ по умолчанию — документ целиком). Один
	// узел-список — перебираются его элементы.
	Each string `yaml:"each,omitempty" json:"each,omitempty"`
	// When — условие на элемент: выражение фильтра RFC 9535 (@ — элемент).
	When string `yaml:"when,omitempty" json:"when,omitempty"`

	each *eachSel
	when fexpr
}

// EndpointRule — адреса, на которых сервис принимает соединения.
type EndpointRule struct {
	Item   `yaml:",inline" json:",inline"`
	Fields map[string]any `yaml:"fields" json:"fields"`

	fields map[string]*Expr
}

// UpstreamRule — куда сервис пересылает.
type UpstreamRule struct {
	Item    `yaml:",inline" json:",inline"`
	Fields  map[string]any `yaml:"fields" json:"fields"`
	Servers *ServersRule   `yaml:"servers,omitempty" json:"servers,omitempty"`

	fields map[string]*Expr
}

// ServersRule — серверы внутри пула.
type ServersRule struct {
	Each   string         `yaml:"each" json:"each"`
	Fields map[string]any `yaml:"fields" json:"fields"`

	each   *eachSel
	fields map[string]*Expr
}

// Rule — находка: элемент, для которого условие истинно.
type Rule struct {
	Item       `yaml:",inline" json:",inline"`
	ID         string `yaml:"id" json:"id"`
	Severity   string `yaml:"severity" json:"severity"`
	Title      I18n   `yaml:"title" json:"title"`
	Detail     I18n   `yaml:"detail,omitempty" json:"detail,omitempty"`
	Suggestion I18n   `yaml:"suggestion,omitempty" json:"suggestion,omitempty"`
	// Object — о чём находка (по умолчанию @key или файл).
	Object any `yaml:"object,omitempty" json:"object,omitempty"`

	object *Expr
}

// I18n — текст на двух языках: строка или {ru: …, en: …}; внутри —
// {{выражение}}.
type I18n struct {
	RU string `yaml:"ru,omitempty" json:"ru,omitempty"`
	EN string `yaml:"en,omitempty" json:"en,omitempty"`
}

// UnmarshalYAML — строка или объект.
func (t *I18n) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		t.RU, t.EN = n.Value, n.Value
		return nil
	}
	type plain I18n
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*t = I18n(p)
	if t.RU == "" {
		t.RU = t.EN
	}
	if t.EN == "" {
		t.EN = t.RU
	}
	return nil
}

// Empty — текста нет.
func (t I18n) Empty() bool { return t.RU == "" && t.EN == "" }

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	idRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	unitRe   = regexp.MustCompile(`^[A-Za-z0-9@._:-]{1,128}$`)
	binaryRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)
)

// nativeNames — встроенные разборщики на Go: описание с таким именем
// пока не заводится.
var nativeNames = map[string]bool{
	model.ServiceNginx: true, model.ServiceHAProxy: true, model.ServiceCaddy: true, model.ServiceDocker: true,
	model.ServicePodman: true, model.ServiceLXD: true, model.ServiceLibvirt: true, model.ServiceK8s: true,
	model.ServiceIptables: true, model.ServiceUFW: true, model.ServiceFirewalld: true, model.ServiceFail2ban: true,
	model.ServiceHost: true, model.ServiceSystemd: true, model.ServiceNetwork: true, model.ServiceSSH: true,
	model.ServiceSysctl: true, model.ServiceCron: true,
}

var severities = map[string]bool{
	model.SeverityCritical: true, model.SeverityHigh: true, model.SeverityMedium: true,
	model.SeverityLow: true, model.SeverityInfo: true,
}

// Endpoint и Upstream: поля, которые понимает описание.
var (
	endpointFields = map[string]bool{"address": true, "port": true, "protocol": true, "tls": true, "mode": true,
		"names": true, "label": true, "kind": true, "upstreams": true}
	upstreamFields = map[string]bool{"name": true, "mode": true, "algorithm": true, "health": true}
	serverFields   = map[string]bool{"name": true, "host": true, "port": true, "url": true, "weight": true, "backup": true, "down": true}
)

// Parse читает и проверяет описание.
func Parse(data []byte) (*Descriptor, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var d Descriptor
	if err := dec.Decode(&d); err != nil {
		return nil, msgs.Errorf("svcdesc.yaml", err)
	}
	if err := d.compile(); err != nil {
		return nil, err
	}
	return &d, nil
}

// formatByExt — формат файла по расширению.
func formatByExt(file string) string {
	switch strings.ToLower(path.Ext(file)) {
	case ".yaml", ".yml":
		return FormatYAML
	case ".json":
		return FormatJSON
	case ".toml":
		return FormatTOML
	case ".ini", ".cnf":
		return FormatINI
	case ".env":
		return FormatEnv
	case ".properties":
		return FormatProperties
	case ".xml":
		return FormatXML
	case ".conf":
		return FormatLines
	}
	return ""
}

func (d *Descriptor) compile() error {
	if !nameRe.MatchString(d.Name) {
		return msgs.Errorf("svcdesc.badName", d.Name)
	}
	if d.Engine != "" || EngineOptions[d.Name] != nil {
		return d.compileEngine()
	}
	if nativeNames[d.Name] {
		return msgs.Errorf("svcdesc.nativeName", d.Name)
	}
	if d.Priority == 0 {
		d.Priority = DefaultPriority
	}
	if d.Priority < 0 || d.Priority > 1000 {
		return msgs.Errorf("svcdesc.badPriority", d.Priority)
	}
	if d.Disabled {
		return nil
	}
	if d.Unit != "" && !unitRe.MatchString(d.Unit) {
		return msgs.Errorf("svcdesc.badUnit", d.Unit)
	}
	for _, p := range append(append([]Probe{}, d.Detect.Any...), d.Detect.All...) {
		if err := p.check(); err != nil {
			return err
		}
	}
	if len(d.Sources) == 0 {
		return msgs.Errorf("svcdesc.noSources", d.Name)
	}
	hasFile := false
	for _, s := range d.Sources {
		hasFile = hasFile || s.Kind() == "file"
	}
	if !hasFile && d.Unit == "" && len(d.Detect.Any) == 0 && len(d.Detect.All) == 0 {
		return msgs.Errorf("svcdesc.needDetect")
	}
	ids := map[string]bool{}
	for i := range d.Sources {
		s := &d.Sources[i]
		if err := s.check(); err != nil {
			return msgs.Errorf("svcdesc.inSource", s.ID, err)
		}
		if ids[s.ID] {
			return msgs.Errorf("svcdesc.dupSource", s.ID)
		}
		ids[s.ID] = true
	}
	from := func(it *Item) error {
		if it.From == "" {
			it.From = d.Sources[0].ID
		}
		if !ids[it.From] {
			return msgs.Errorf("svcdesc.unknownSource", it.From)
		}
		each := it.Each
		if each == "" {
			each = "$"
		}
		p, err := compileEach(each)
		if err != nil {
			return err
		}
		it.each = p
		if it.When != "" {
			if it.when, err = CompileCond(it.When); err != nil {
				return err
			}
		}
		return nil
	}
	exprs := func(fields map[string]any, allowed map[string]bool) (map[string]*Expr, error) {
		out := map[string]*Expr{}
		for k, v := range fields {
			if !allowed[k] {
				return nil, msgs.Errorf("svcdesc.unknownField", k)
			}
			e, err := CompileExpr(v)
			if err != nil {
				return nil, msgs.Errorf("svcdesc.inField", k, err)
			}
			out[k] = e
		}
		return out, nil
	}
	if d.Version != nil {
		if d.Version.From == "" {
			d.Version.From = d.Sources[0].ID
		}
		if !ids[d.Version.From] {
			return msgs.Errorf("svcdesc.unknownSource", d.Version.From)
		}
		e, err := CompileExpr(d.Version.Value)
		if err != nil {
			return msgs.Errorf("svcdesc.inField", "version", err)
		}
		d.Version.expr = e
	}
	for i := range d.Extract.Endpoints {
		r := &d.Extract.Endpoints[i]
		if err := from(&r.Item); err != nil {
			return msgs.Errorf("svcdesc.inEndpoint", i+1, err)
		}
		f, err := exprs(r.Fields, endpointFields)
		if err != nil {
			return msgs.Errorf("svcdesc.inEndpoint", i+1, err)
		}
		if f["port"] == nil {
			return msgs.Errorf("svcdesc.inEndpoint", i+1, msgs.Errorf("svcdesc.needField", "port"))
		}
		r.fields = f
	}
	for i := range d.Extract.Upstreams {
		r := &d.Extract.Upstreams[i]
		if err := from(&r.Item); err != nil {
			return msgs.Errorf("svcdesc.inUpstream", i+1, err)
		}
		f, err := exprs(r.Fields, upstreamFields)
		if err != nil {
			return msgs.Errorf("svcdesc.inUpstream", i+1, err)
		}
		if f["name"] == nil {
			return msgs.Errorf("svcdesc.inUpstream", i+1, msgs.Errorf("svcdesc.needField", "name"))
		}
		r.fields = f
		if r.Servers == nil {
			return msgs.Errorf("svcdesc.inUpstream", i+1, msgs.Errorf("svcdesc.needField", "servers"))
		}
		if r.Servers.each, err = compileEach(r.Servers.Each); err != nil {
			return msgs.Errorf("svcdesc.inUpstream", i+1, err)
		}
		if r.Servers.fields, err = exprs(r.Servers.Fields, serverFields); err != nil {
			return msgs.Errorf("svcdesc.inUpstream", i+1, err)
		}
		if r.Servers.fields["url"] == nil && r.Servers.fields["host"] == nil {
			return msgs.Errorf("svcdesc.inUpstream", i+1, msgs.Errorf("svcdesc.needField", "servers.url | servers.host"))
		}
	}
	ruleIDs := map[string]bool{}
	for i := range d.Rules {
		r := &d.Rules[i]
		if !idRe.MatchString(r.ID) || ruleIDs[r.ID] {
			return msgs.Errorf("svcdesc.badRuleID", r.ID)
		}
		ruleIDs[r.ID] = true
		if !severities[r.Severity] {
			return msgs.Errorf("svcdesc.inRule", r.ID, msgs.Errorf("svcdesc.badSeverity", r.Severity))
		}
		if r.Title.Empty() || r.When == "" {
			return msgs.Errorf("svcdesc.inRule", r.ID, msgs.Errorf("svcdesc.needField", "title, when"))
		}
		if err := from(&r.Item); err != nil {
			return msgs.Errorf("svcdesc.inRule", r.ID, err)
		}
		for _, t := range []I18n{r.Title, r.Detail, r.Suggestion} {
			for _, s := range []string{t.RU, t.EN} {
				if err := checkTemplate(s); err != nil {
					return msgs.Errorf("svcdesc.inRule", r.ID, err)
				}
			}
		}
		if r.Object != nil {
			e, err := CompileExpr(r.Object)
			if err != nil {
				return msgs.Errorf("svcdesc.inRule", r.ID, err)
			}
			r.object = e
		}
	}
	return nil
}

func (p Probe) check() error {
	set := 0
	for _, s := range []string{p.File, p.Unit, p.Binary, p.Image, p.Process} {
		if s != "" {
			set++
		}
	}
	if p.Port != 0 {
		set++
	}
	switch {
	case set != 1:
		return msgs.Errorf("svcdesc.probeOne")
	case p.File != "" && !strings.HasPrefix(p.File, "/"):
		return msgs.Errorf("svcdesc.absPath", p.File)
	case p.Unit != "" && !unitRe.MatchString(p.Unit):
		return msgs.Errorf("svcdesc.badUnit", p.Unit)
	case p.Binary != "" && !binaryRe.MatchString(p.Binary):
		return msgs.Errorf("svcdesc.badBinary", p.Binary)
	case p.Process != "" && !binaryRe.MatchString(p.Process):
		return msgs.Errorf("svcdesc.badBinary", p.Process)
	case p.Port < 0 || p.Port > 65535:
		return msgs.Errorf("svcdesc.badPort", p.Port)
	}
	return nil
}

// maxTimeout — предел ожидания HTTP и команды.
const maxTimeout = time.Minute

func (s *Source) check() error {
	if !idRe.MatchString(s.ID) {
		return msgs.Errorf("svcdesc.badSourceID", s.ID)
	}
	kinds := 0
	if s.File != "" {
		kinds++
	}
	if s.HTTP != "" {
		kinds++
	}
	if len(s.Command) > 0 {
		kinds++
	}
	if kinds != 1 {
		return msgs.Errorf("svcdesc.sourceOne")
	}
	switch s.Kind() {
	case "file":
		if !strings.HasPrefix(s.File, "/") {
			return msgs.Errorf("svcdesc.absPath", s.File)
		}
		if Sensitive(s.File) {
			return msgs.Errorf("svcdesc.sensitive", s.File)
		}
		if s.Format == "" {
			s.Format = formatByExt(s.File)
		}
		if s.Format == "" {
			return msgs.Errorf("svcdesc.needFormat", s.File)
		}
	case "http":
		if err := checkHTTP(s); err != nil {
			return err
		}
	case "command":
		if err := checkCommand(s.Command); err != nil {
			return err
		}
	}
	if s.Format == "" {
		s.Format = FormatJSON
	}
	known := false
	for _, f := range Formats {
		known = known || f == s.Format
	}
	if !known {
		return msgs.Errorf("svcdesc.unknownFormat", s.Format)
	}
	if s.Format == FormatRegex {
		if s.Options.Pattern == "" {
			return msgs.Errorf("svcdesc.needField", "options.pattern")
		}
		if _, err := regexp.Compile(s.Options.Pattern); err != nil {
			return msgs.Errorf("svcdesc.badRegex", err)
		}
	}
	if s.Timeout < 0 || time.Duration(s.Timeout) > maxTimeout || s.Interval < 0 {
		return msgs.Errorf("svcdesc.badTimeout", time.Duration(s.Timeout).String())
	}
	return nil
}

var tmplRe = regexp.MustCompile(`\{\{(.*?)\}\}`)

func checkTemplate(s string) error {
	for _, m := range tmplRe.FindAllStringSubmatch(s, -1) {
		if _, err := CompileExpr(strings.TrimSpace(m[1])); err != nil {
			return err
		}
	}
	return nil
}

// render — текст с подставленными {{выражениями}}.
func render(s string, c Ctx) string {
	return tmplRe.ReplaceAllStringFunc(s, func(m string) string {
		e, err := CompileExpr(strings.TrimSpace(m[2 : len(m)-2]))
		if err != nil {
			return m
		}
		return strings.Join(e.Strings(c), ", ")
	})
}

// CompileCond — условие: выражение фильтра RFC 9535 без «?».
func CompileCond(s string) (fexpr, error) {
	p := &pparser{src: s}
	e, err := p.orExpr()
	if err == nil {
		p.ws()
		if p.pos < len(p.src) {
			err = p.errf("condTrailing")
		}
	}
	return e, err
}

// Errf — ошибка описания с указанием файла.
func Errf(file string, err error) error { return fmt.Errorf("%s: %w", file, err) }

// EngineOptions — встроенные разборщики и их настройки (пути). Пустая
// настройка — значение из окружения (NKT_NGINX_MAIN_CONFIG и т. п.).
var EngineOptions = map[string]map[string]string{
	model.ServiceNginx:    {"main_config": "path"},
	model.ServiceHAProxy:  {"main_config": "path"},
	model.ServiceCaddy:    {"main_config": "path"},
	model.ServiceDocker:   {"compose_files": "paths"},
	model.ServicePodman:   {},
	model.ServiceLXD:      {},
	model.ServiceLibvirt:  {"uri": "uri"},
	model.ServiceK8s:      {},
	EngineFirewall:        {},
	model.ServiceFail2ban: {"root": "path"},
}

// EngineFirewall — разборщик файрвола (iptables, ufw, firewalld вместе).
const EngineFirewall = "firewall"

// EnginePriority — приоритет файлов встроенного разборщика по умолчанию:
// выше, чем у описаний (50).
const EnginePriority = 100

var libvirtURIRe = regexp.MustCompile(`^(qemu|lxc|xen|vbox|test)(\+[a-z]+)?://[A-Za-z0-9._@:/-]*(\?[A-Za-z0-9=&._/-]*)?$`)

// compileEngine — описание встроенного разборщика: только имя, engine
// (= имя), приоритет, disabled, options и detect (file, unit, binary).
func (d *Descriptor) compileEngine() error {
	opts, ok := EngineOptions[d.Engine]
	if d.Engine == "" {
		d.Engine = d.Name
		opts, ok = EngineOptions[d.Engine]
	}
	if !ok || d.Engine != d.Name {
		return msgs.Errorf("svcdesc.badEngine", d.Engine, d.Name)
	}
	if d.Priority == 0 {
		d.Priority = EnginePriority
	}
	if d.Priority < 0 || d.Priority > 1000 {
		return msgs.Errorf("svcdesc.badPriority", d.Priority)
	}
	if len(d.Sources) > 0 || d.Unit != "" || d.Version != nil || len(d.Extract.Endpoints) > 0 ||
		len(d.Extract.Upstreams) > 0 || len(d.Rules) > 0 {
		return msgs.Errorf("svcdesc.engineOnly", d.Name)
	}
	for k, v := range d.Options {
		kind, ok := opts[k]
		if !ok {
			return msgs.Errorf("svcdesc.badOption", d.Name, k)
		}
		switch kind {
		case "path":
			if !strings.HasPrefix(v, "/") || Sensitive(v) || strings.ContainsAny(v, "\n\x00") {
				return msgs.Errorf("svcdesc.badOptionValue", k, v)
			}
		case "paths":
			for _, p := range strings.Split(v, ",") {
				p = strings.TrimSpace(p)
				if !strings.HasPrefix(p, "/") || Sensitive(p) {
					return msgs.Errorf("svcdesc.badOptionValue", k, p)
				}
			}
		case "uri":
			if len(v) > 256 || !libvirtURIRe.MatchString(v) {
				return msgs.Errorf("svcdesc.badOptionValue", k, v)
			}
		}
	}
	for _, p := range append(append([]Probe{}, d.Detect.Any...), d.Detect.All...) {
		if err := p.check(); err != nil {
			return err
		}
		if p.Image != "" || p.Process != "" || p.Port != 0 {
			return msgs.Errorf("svcdesc.engineProbe")
		}
	}
	return nil
}

// eachSel — путь к элементам или путь с преобразованиями: «$.listeners |
// split(,)» перебирает части строки (Kafka: несколько адресов в одной).
type eachSel struct {
	src  string
	path *Path
	expr *Expr
}

func compileEach(s string) (*eachSel, error) {
	if len(splitPipes(s)) > 1 {
		e, err := CompileExpr(s)
		if err != nil {
			return nil, err
		}
		return &eachSel{src: s, expr: e}, nil
	}
	p, err := CompilePath(s)
	if err != nil {
		return nil, err
	}
	return &eachSel{src: s, path: p}, nil
}

func (e *eachSel) String() string { return e.src }

// nodes — узлы: у пути — с ключами, у преобразования — с номерами.
func (e *eachSel) nodes(root, cur any) []Node {
	if e.path != nil {
		return e.path.Eval(root, cur)
	}
	var out []Node
	for i, v := range e.expr.Values(Ctx{Root: root, Item: Node{Value: cur}}) {
		out = append(out, Node{Key: i, Value: v})
	}
	return out
}
