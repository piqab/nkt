package parse

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	parser "github.com/haproxytech/config-parser/v5"
	"github.com/haproxytech/config-parser/v5/options"
	"github.com/haproxytech/config-parser/v5/types"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// HAProxyResult is everything the haproxy parser produces.
type HAProxyResult struct {
	Status    model.SourceStatus
	Endpoints []model.Endpoint
	Upstreams []model.Upstream
	Files     []model.ManagedFile
	Global    map[string]string
}

// HAProxy parses haproxy.cfg into frontends (endpoints) and backends (upstreams).
func HAProxy(ctx context.Context, c collect.Collector, mainConfig string) HAProxyResult {
	started := time.Now()
	res := HAProxyResult{
		Status: model.SourceStatus{Name: model.ServiceHAProxy},
		Global: map[string]string{},
	}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()

	raw, err := c.ReadFile(mainConfig)
	if err != nil {
		res.Status.Error = fmt.Sprintf("конфиг %s недоступен: %v", mainConfig, err)
		res.Status.ErrorKey = "parse.configUnavailable"
		res.Status.ErrorArgs = []any{mainConfig, err}
		return res
	}
	res.Status.Available = true
	res.Status.Version = binaryVersion(ctx, c, "haproxy", "-v")
	res.Files = append(res.Files, describeFile(c, mainConfig, model.ServiceHAProxy, true))
	res.Status.Files = fileNames(res.Files)

	text := string(raw)
	sections := scanHAProxySections(text)

	// UseListenSectionParsers makes listen{} understand both frontend and
	// backend keywords, which is how haproxy itself treats that section.
	p, err := parser.New(options.String(text), options.UseListenSectionParsers)
	if err != nil {
		res.Status.Error = fmt.Sprintf("разбор %s: %v", mainConfig, err)
		res.Status.ErrorKey = "parse.configParseFailed"
		res.Status.ErrorArgs = []any{mainConfig, err}
		return res
	}

	// Defaults supply the mode for sections that do not declare one.
	defaultMode := "tcp"
	if names, err := p.SectionsGet(parser.Defaults); err == nil {
		for _, n := range names {
			if v, err := p.Get(parser.Defaults, n, "mode"); err == nil {
				if s, ok := v.(*types.StringC); ok && s.Value != "" {
					defaultMode = s.Value
				}
			}
		}
	}
	if names, err := p.SectionsGet(parser.Global); err == nil {
		for _, n := range names {
			if v, err := p.Get(parser.Global, n, "maxconn"); err == nil {
				if i, ok := v.(*types.Int64C); ok {
					res.Global["maxconn"] = strconv.FormatInt(i.Value, 10)
				}
			}
		}
	}
	res.Global["default_mode"] = defaultMode

	// Backends and listen sections become upstreams. This runs first so that
	// endpoint routing knows which listen sections actually serve their own pool.
	servesOwnPool := map[string]bool{}
	for _, sectionType := range []parser.Section{parser.Backends, parser.Listen} {
		names, err := p.SectionsGet(sectionType)
		if err != nil {
			continue
		}
		sort.Strings(names)
		for _, name := range names {
			// A backend section always defines a pool, even without server
			// lines — it may answer by itself via http-request return. A listen
			// section only counts as a pool when it actually has servers.
			u, ok := haproxyUpstream(p, sectionType, name, mainConfig, defaultMode, sections,
				sectionType == parser.Listen)
			if !ok {
				continue
			}
			res.Upstreams = append(res.Upstreams, u)
			if sectionType == parser.Listen {
				servesOwnPool[name] = true
			}
		}
	}

	// Frontends and listen sections become endpoints.
	for _, sectionType := range []parser.Section{parser.Frontends, parser.Listen} {
		names, err := p.SectionsGet(sectionType)
		if err != nil {
			continue
		}
		sort.Strings(names)
		for _, name := range names {
			res.Endpoints = append(res.Endpoints,
				haproxyEndpoints(p, sectionType, name, mainConfig, defaultMode, sections, servesOwnPool)...)
		}
	}

	sort.Slice(res.Endpoints, func(i, j int) bool { return res.Endpoints[i].Port < res.Endpoints[j].Port })
	return res
}

func haproxyEndpoints(p parser.Parser, section parser.Section, name, file, defaultMode string,
	sections []hapSection, servesOwnPool map[string]bool) []model.Endpoint {

	sec := findSection(sections, string(section), name)
	mode := defaultMode
	if v, err := p.Get(section, name, "mode"); err == nil {
		if s, ok := v.(*types.StringC); ok && s.Value != "" {
			mode = s.Value
		}
	}

	var routes []model.Route
	if v, err := p.Get(section, name, "use_backend"); err == nil {
		if list, ok := v.([]types.UseBackend); ok {
			for _, ub := range list {
				cond := strings.TrimSpace(ub.Cond + " " + ub.CondTest)
				routes = append(routes, model.Route{
					Match: cond, Target: ub.Name, TargetKind: "upstream",
					Condition: cond, File: file,
				})
			}
		}
	}
	if v, err := p.Get(section, name, "default_backend"); err == nil {
		if s, ok := v.(*types.StringC); ok && s.Value != "" {
			routes = append(routes, model.Route{
				Match: "default", Target: s.Value, TargetKind: "upstream", File: file,
			})
		}
	}
	if section == parser.Listen && servesOwnPool[name] {
		// A listen section with its own server lines is its own backend pool.
		routes = append(routes, model.Route{
			Match: "default", Target: name, TargetKind: "upstream", File: file,
		})
	}

	acls := []string{}
	if v, err := p.Get(section, name, "acl"); err == nil {
		if list, ok := v.([]types.ACL); ok {
			for _, a := range list {
				acls = append(acls, fmt.Sprintf("%s: %s %s", a.Name, a.Criterion, a.Value))
			}
		}
	}

	binds := []types.Bind{}
	if v, err := p.Get(section, name, "bind"); err == nil {
		if list, ok := v.([]types.Bind); ok {
			binds = list
		}
	}

	kind := "frontend"
	if section == parser.Listen {
		kind = "listen"
	}

	out := make([]model.Endpoint, 0, len(binds))
	for _, b := range binds {
		addr, port, ok := parseHAProxyBind(b.Path)
		if !ok {
			continue
		}
		line, tls, crt := sec.bindInfo(b.Path)
		ep := model.Endpoint{
			ID:       fmt.Sprintf("haproxy:%s:%s:%d", name, addr, port),
			Service:  model.ServiceHAProxy,
			Kind:     kind,
			Address:  addr,
			Port:     port,
			Protocol: "tcp",
			TLS:      tls,
			Mode:     mode,
			Names:    []string{name},
			Routes:   routes,
			File:     file,
			Line:     line,
			Label:    name,
			Extra:    map[string]string{},
		}
		for _, r := range routes {
			if r.TargetKind == "upstream" {
				ep.Upstream = appendUnique(ep.Upstream, r.Target)
			}
		}
		if crt != "" {
			ep.Extra["ssl_certificate"] = crt
		}
		if len(acls) > 0 {
			ep.Extra["acl"] = strings.Join(acls, "; ")
		}
		if sec.has("stats enable") || sec.has("stats uri") {
			ep.Extra["stats"] = "enabled"
			if !sec.has("stats auth") {
				ep.Extra["stats_auth"] = "none"
			}
		}
		out = append(out, ep)
	}
	return out
}

func haproxyUpstream(p parser.Parser, section parser.Section, name, file, defaultMode string,
	sections []hapSection, requireServers bool) (model.Upstream, bool) {

	var list []types.Server
	if v, err := p.Get(section, name, "server"); err == nil {
		if servers, ok := v.([]types.Server); ok {
			list = servers
		}
	}
	if requireServers && len(list) == 0 {
		return model.Upstream{}, false
	}

	sec := findSection(sections, string(section), name)
	u := model.Upstream{
		ID:      fmt.Sprintf("haproxy:upstream:%s", name),
		Name:    name,
		Service: model.ServiceHAProxy,
		Mode:    defaultMode,
		File:    file,
		Line:    sec.Line,
	}
	if mv, err := p.Get(section, name, "mode"); err == nil {
		if s, ok := mv.(*types.StringC); ok && s.Value != "" {
			u.Mode = s.Value
		}
	}
	if bv, err := p.Get(section, name, "balance"); err == nil {
		if b, ok := bv.(*types.Balance); ok {
			u.Algorithm = b.Algorithm
		}
	}
	if u.Algorithm == "" {
		u.Algorithm = "roundrobin"
	}

	if hv, err := p.Get(section, name, "option httpchk"); err == nil {
		if h, ok := hv.(*types.OptionHttpchk); ok && !h.NoOption {
			u.Health = strings.TrimSpace(fmt.Sprintf("httpchk %s %s", h.Method, h.URI))
		}
	}
	if pv, err := p.Get(section, name, "option pgsql-check"); err == nil {
		if pg, ok := pv.(*types.OptionPgsqlCheck); ok && !pg.NoOption {
			u.Health = "pgsql-check user " + pg.User
		}
	}

	for _, s := range list {
		host, port := splitHostPort(s.Address, 0)
		// Server options are typed values whose String() renders the original
		// keyword ("check", "inter 5s"); flatten them into words for flag checks.
		opts := make([]string, 0, len(s.Params))
		for _, prm := range s.Params {
			opts = append(opts, prm.String())
		}
		srv := model.UpstreamServer{Name: s.Name, Host: host, Port: port, Params: opts}
		fields := strings.Fields(strings.Join(opts, " "))
		for i, f := range fields {
			switch f {
			case "check":
				srv.Checked = true
			case "backup":
				srv.Backup = true
			case "disabled":
				srv.Down = true
			case "weight":
				if i+1 < len(fields) {
					srv.Weight, _ = strconv.Atoi(fields[i+1])
				}
			}
		}
		u.Servers = append(u.Servers, srv)
	}
	return u, true
}

// parseHAProxyBind understands "*:8090", "10.0.0.2:5432", ":::443", "[::]:443",
// ":8080" and unix socket paths (which are skipped).
func parseHAProxyBind(spec string) (addr string, port int, ok bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, "unix@") ||
		strings.HasPrefix(spec, "abns@") {
		return "", 0, false
	}
	spec = strings.TrimPrefix(spec, "ipv4@")
	spec = strings.TrimPrefix(spec, "ipv6@")

	if strings.HasPrefix(spec, "[") {
		if end := strings.LastIndex(spec, "]"); end > 0 {
			addr = spec[1:end]
			if rest := strings.TrimPrefix(spec[end+1:], ":"); rest != "" {
				port, _ = strconv.Atoi(rest)
			}
			return normaliseBindAddr(addr), port, port > 0
		}
	}
	i := strings.LastIndex(spec, ":")
	if i < 0 {
		return "", 0, false
	}
	addr = spec[:i]
	port, err := strconv.Atoi(spec[i+1:])
	if err != nil {
		return "", 0, false
	}
	return normaliseBindAddr(addr), port, port > 0
}

func normaliseBindAddr(addr string) string {
	switch addr {
	case "", "*", "::":
		return "0.0.0.0"
	}
	return addr
}

// --------------------------------------------------------- raw section scanner

// hapSection is a raw slice of haproxy.cfg. The structured parser gives us the
// data; the raw text gives us line numbers and the handful of keywords the
// typed API does not expose.
type hapSection struct {
	Kind  string
	Name  string
	Line  int
	Lines []string
}

func (s hapSection) has(keyword string) bool {
	for _, l := range s.Lines {
		if strings.HasPrefix(strings.TrimSpace(l), keyword) {
			return true
		}
	}
	return false
}

// bindInfo returns the line number of a bind directive, whether it enables TLS,
// and the certificate path it serves.
func (s hapSection) bindInfo(path string) (line int, tls bool, crt string) {
	for i, l := range s.Lines {
		trimmed := strings.TrimSpace(l)
		if !strings.HasPrefix(trimmed, "bind ") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 || fields[1] != path {
			continue
		}
		for j, f := range fields[2:] {
			switch {
			case f == "ssl":
				tls = true
			case f == "crt" || f == "crt-list":
				tls = true
				// The value follows the keyword; index is offset by the slice start.
				if idx := j + 3; idx < len(fields) {
					crt = fields[idx]
				}
			}
		}
		return s.Line + i, tls, crt
	}
	return s.Line, false, ""
}

var hapSectionRe = regexp.MustCompile(
	`^(global|defaults|frontend|backend|listen|resolvers|peers|cache|ring|userlist|mailers|http-errors|program|log-forward|fcgi-app)(\s+(\S+))?`)

func scanHAProxySections(text string) []hapSection {
	var out []hapSection
	var cur *hapSection
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if m := hapSectionRe.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, " ") &&
			!strings.HasPrefix(line, "\t") {
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &hapSection{Kind: m[1], Name: m[3], Line: i + 1}
			continue
		}
		if cur != nil {
			cur.Lines = append(cur.Lines, line)
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

func findSection(sections []hapSection, kind, name string) hapSection {
	for _, s := range sections {
		if s.Kind == kind && s.Name == name {
			return s
		}
	}
	// Section kinds from the typed API are singular ("frontend"), which already
	// matches; fall back to name-only matching for defaults/global.
	for _, s := range sections {
		if s.Name == name {
			return s
		}
	}
	return hapSection{Kind: kind, Name: name}
}
