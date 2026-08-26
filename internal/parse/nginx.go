// Package parse turns raw host configuration into the vendor-neutral model.
package parse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	gopath "path"
	"sort"
	"strconv"
	"strings"
	"time"

	crossplane "github.com/nginxinc/nginx-go-crossplane"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// NginxResult is everything the nginx parser produces.
type NginxResult struct {
	Status    model.SourceStatus
	Endpoints []model.Endpoint
	Upstreams []model.Upstream
	Files     []model.ManagedFile
	Global    map[string]string
}

// Nginx parses the nginx configuration tree starting from mainConfig, following
// include directives through the collector so it works against both a real host
// and a snapshot.
func Nginx(ctx context.Context, c collect.Collector, mainConfig string) NginxResult {
	started := time.Now()
	res := NginxResult{
		Status: model.SourceStatus{Name: model.ServiceNginx},
		Global: map[string]string{},
	}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()

	if !c.Exists(mainConfig) {
		res.Status.Error = fmt.Sprintf("основной конфиг %s не найден", mainConfig)
		res.Status.ErrorKey = "parse.nginxMainConfigNotFound"
		res.Status.ErrorArgs = []any{mainConfig}
		return res
	}
	res.Status.Available = true
	res.Status.Version = binaryVersion(ctx, c, "nginx", "-v")

	p := &nginxParser{
		c:            c,
		res:          &res,
		visited:      map[string]bool{},
		seenUpstream: map[string]bool{},
	}
	p.parseFile(mainConfig)

	// Resolve which endpoint references which upstream by name.
	names := map[string]bool{}
	for _, u := range res.Upstreams {
		names[u.Name] = true
	}
	for i := range res.Endpoints {
		for j := range res.Endpoints[i].Routes {
			r := &res.Endpoints[i].Routes[j]
			if r.TargetKind == "unknown" && names[r.Target] {
				r.TargetKind = "upstream"
			}
			if r.TargetKind == "upstream" {
				res.Endpoints[i].Upstream = appendUnique(res.Endpoints[i].Upstream, r.Target)
			}
		}
	}

	sort.Slice(res.Endpoints, func(i, j int) bool {
		if res.Endpoints[i].Port != res.Endpoints[j].Port {
			return res.Endpoints[i].Port < res.Endpoints[j].Port
		}
		return res.Endpoints[i].Label < res.Endpoints[j].Label
	})
	sort.Slice(res.Upstreams, func(i, j int) bool { return res.Upstreams[i].Name < res.Upstreams[j].Name })
	res.Status.Files = fileNames(res.Files)
	return res
}

// nginxParser walks the include graph itself instead of letting crossplane do
// it. crossplane resolves includes with the path/filepath package, which on
// Windows mangles absolute POSIX paths such as /etc/nginx/conf.d/*.conf; doing
// it here keeps fixtures mode and a real Linux host byte-for-byte identical.
type nginxParser struct {
	c            collect.Collector
	res          *NginxResult
	visited      map[string]bool
	seenUpstream map[string]bool
}

func (p *nginxParser) parseFile(file string) {
	if p.visited[file] {
		return
	}
	p.visited[file] = true

	payload, err := crossplane.Parse(file, &crossplane.ParseOptions{
		Open:       func(name string) (io.ReadCloser, error) { return p.c.Open(name) },
		SingleFile: true,
		// nginx installations routinely carry third-party modules; refusing to
		// parse a config because of one unknown directive would be useless in
		// practice, so unknown directives are tolerated and surfaced as warnings.
		SkipDirectiveArgsCheck:    true,
		SkipDirectiveContextCheck: true,
	})
	if err != nil {
		p.res.Status.Warnings = append(p.res.Status.Warnings, fmt.Sprintf("%s: %v", file, err))
		return
	}
	for _, e := range payload.Errors {
		if e.Error != nil {
			p.res.Status.Warnings = append(p.res.Status.Warnings, e.Error.Error())
		}
	}
	p.res.Files = append(p.res.Files, describeFile(p.c, file, model.ServiceNginx, true))

	for _, cfg := range payload.Config {
		for _, e := range cfg.Errors {
			if e.Error != nil {
				p.res.Status.Warnings = append(p.res.Status.Warnings, fmt.Sprintf("%s: %v", file, e.Error))
			}
		}
		p.walk(cfg.Parsed, file, nil)
	}
}

// resolveInclude expands an include pattern into concrete host paths, always
// using POSIX semantics.
func (p *nginxParser) resolveInclude(pattern, fromFile string) []string {
	if !strings.HasPrefix(pattern, "/") {
		pattern = gopath.Join(gopath.Dir(fromFile), pattern)
	}
	if !strings.ContainsAny(pattern, "*?[") {
		if p.c.Exists(pattern) {
			return []string{pattern}
		}
		p.res.Status.Warnings = append(p.res.Status.Warnings,
			fmt.Sprintf("%s: include %s — файл не найден", fromFile, pattern))
		p.res.Status.WarningRefs = append(p.res.Status.WarningRefs,
			model.TextRef{Key: "parse.includeFileNotFound", Args: []any{fromFile, pattern}})
		return nil
	}
	matches, err := p.c.Glob(pattern)
	if err != nil {
		p.res.Status.Warnings = append(p.res.Status.Warnings,
			fmt.Sprintf("%s: include %s: %v", fromFile, pattern, err))
		return nil
	}
	return matches
}

// walk descends the directive tree, collecting server and upstream blocks.
// Each config file is walked independently, so a snippet included into http{}
// is handled the same whether it sits in nginx.conf or in conf.d.
func (p *nginxParser) walk(dirs crossplane.Directives, file string, stack []string) {
	res, seenUpstream := p.res, p.seenUpstream
	for _, d := range dirs {
		switch d.Directive {
		case "include":
			for _, arg := range d.Args {
				for _, inc := range p.resolveInclude(arg, file) {
					p.parseFile(inc)
				}
			}
			continue

		case "upstream":
			if len(d.Args) == 0 {
				continue
			}
			name := d.Args[0]
			key := file + "|" + name
			if seenUpstream[key] {
				continue
			}
			seenUpstream[key] = true
			res.Upstreams = append(res.Upstreams, buildNginxUpstream(d, file, inStream(stack)))
			continue

		case "server":
			// "server" inside upstream{} is a pool member, handled above.
			if len(stack) > 0 && stack[len(stack)-1] == "upstream" {
				continue
			}
			res.Endpoints = append(res.Endpoints, buildNginxServers(d, file, inStream(stack))...)
			continue

		case "ssl_protocols", "server_tokens", "ssl_prefer_server_ciphers", "ssl_ciphers",
			"client_max_body_size", "worker_processes", "keepalive_timeout":
			if len(stack) <= 1 { // main or http level
				res.Global[d.Directive] = strings.Join(d.Args, " ")
			}
		}
		if d.IsBlock() {
			p.walk(d.Block, file, append(stack, d.Directive))
		}
	}
}

func inStream(stack []string) bool {
	for _, s := range stack {
		if s == "stream" {
			return true
		}
	}
	return false
}

func buildNginxUpstream(d *crossplane.Directive, file string, stream bool) model.Upstream {
	u := model.Upstream{
		Name:    d.Args[0],
		Service: model.ServiceNginx,
		Mode:    "http",
		File:    file,
		Line:    d.Line,
	}
	if stream {
		u.Mode = "tcp"
	}
	u.ID = fmt.Sprintf("nginx:upstream:%s", u.Name)

	for _, inner := range d.Block {
		switch inner.Directive {
		case "server":
			if len(inner.Args) == 0 {
				continue
			}
			host, port := splitHostPort(inner.Args[0], defaultUpstreamPort(stream))
			s := model.UpstreamServer{Host: host, Port: port, Params: inner.Args[1:]}
			for _, p := range inner.Args[1:] {
				switch {
				case p == "backup":
					s.Backup = true
				case p == "down":
					s.Down = true
				case strings.HasPrefix(p, "weight="):
					s.Weight, _ = strconv.Atoi(strings.TrimPrefix(p, "weight="))
				case strings.HasPrefix(p, "max_fails=") || strings.HasPrefix(p, "fail_timeout="):
					// Passive health checking: nginx OSS has no active checks.
					s.Checked = true
				}
			}
			u.Servers = append(u.Servers, s)
		case "least_conn", "ip_hash", "random", "hash":
			u.Algorithm = strings.TrimSpace(inner.Directive + " " + strings.Join(inner.Args, " "))
		case "health_check":
			u.Health = "health_check " + strings.Join(inner.Args, " ")
		}
	}
	if u.Algorithm == "" {
		u.Algorithm = "round-robin"
	}
	if u.Health == "" {
		passive := false
		for _, s := range u.Servers {
			if s.Checked {
				passive = true
			}
		}
		if passive {
			u.Health = "passive (max_fails/fail_timeout)"
		}
	}
	return u
}

// buildNginxServers turns one server{} block into one endpoint per listen directive.
func buildNginxServers(d *crossplane.Directive, file string, stream bool) []model.Endpoint {
	var (
		listens    []*crossplane.Directive
		names      []string
		routes     []model.Route
		accessLogs []string
		extra      = map[string]string{}
		tlsCert    bool
	)

	for _, inner := range d.Block {
		switch inner.Directive {
		case "listen":
			listens = append(listens, inner)
		case "server_name":
			names = append(names, inner.Args...)
		case "ssl_certificate":
			tlsCert = true
			if len(inner.Args) > 0 {
				extra["ssl_certificate"] = inner.Args[0]
			}
		case "ssl_protocols":
			extra["ssl_protocols"] = strings.Join(inner.Args, " ")
		case "access_log":
			if len(inner.Args) > 0 && inner.Args[0] != "off" {
				accessLogs = append(accessLogs, inner.Args[0])
			}
		case "add_header":
			if len(inner.Args) > 0 && strings.EqualFold(inner.Args[0], "Strict-Transport-Security") {
				extra["hsts"] = strings.Join(inner.Args[1:], " ")
			}
		case "return":
			routes = append(routes, model.Route{
				Match: "/", Target: strings.Join(inner.Args, " "),
				TargetKind: "redirect", File: file, Line: inner.Line,
			})
		case "proxy_pass":
			if len(inner.Args) > 0 {
				routes = append(routes, proxyRoute("/", inner.Args[0], file, inner.Line))
			}
		case "location":
			routes = append(routes, buildNginxLocation(inner, file)...)
		case "auth_basic":
			if len(inner.Args) > 0 && inner.Args[0] != "off" {
				extra["auth_basic"] = inner.Args[0]
			}
		case "http2":
			if len(inner.Args) > 0 {
				extra["http2"] = inner.Args[0]
			}
		}
	}

	if len(listens) == 0 {
		// nginx defaults to *:80 when no listen is given.
		listens = append(listens, &crossplane.Directive{Directive: "listen", Args: []string{"80"}, Line: d.Line})
	}

	label := strings.Join(names, ", ")
	if label == "" {
		label = "server (без server_name)"
	}

	out := make([]model.Endpoint, 0, len(listens))
	for _, l := range listens {
		addr, port, opts := parseListen(l.Args, stream)
		if port == 0 && addr == "" {
			continue // unix socket or unparsable
		}
		ep := model.Endpoint{
			Service:   model.ServiceNginx,
			Kind:      "server",
			Address:   addr,
			Port:      port,
			Protocol:  "tcp",
			TLS:       opts["ssl"] == "1" || tlsCert && opts["ssl"] != "",
			Mode:      "http",
			Names:     names,
			Routes:    routes,
			File:      file,
			Line:      l.Line,
			Label:     label,
			AccessLog: accessLogs,
			Extra:     map[string]string{},
		}
		if stream {
			ep.Mode = "tcp"
		}
		if opts["udp"] == "1" {
			ep.Protocol = "udp"
		}
		if opts["ssl"] == "1" {
			ep.TLS = true
		}
		if opts["default_server"] == "1" {
			ep.Extra["default_server"] = "yes"
		}
		for k, v := range extra {
			ep.Extra[k] = v
		}
		if ep.TLS && !tlsCert {
			ep.Extra["tls_cert_missing"] = "yes"
		}
		ep.ID = fmt.Sprintf("nginx:%s:%d:%s", ep.Address, ep.Port, strings.Join(names, ","))
		out = append(out, ep)
	}
	return out
}

func buildNginxLocation(d *crossplane.Directive, file string) []model.Route {
	match := strings.Join(d.Args, " ")
	var out []model.Route
	for _, inner := range d.Block {
		switch inner.Directive {
		case "proxy_pass", "grpc_pass", "uwsgi_pass", "fastcgi_pass":
			if len(inner.Args) > 0 {
				out = append(out, proxyRoute(match, inner.Args[0], file, inner.Line))
			}
		case "return":
			out = append(out, model.Route{Match: match, Target: strings.Join(inner.Args, " "),
				TargetKind: "redirect", File: file, Line: inner.Line})
		case "deny":
			out = append(out, model.Route{Match: match, Target: strings.Join(inner.Args, " "),
				TargetKind: "deny", File: file, Line: inner.Line})
		case "root", "alias":
			out = append(out, model.Route{Match: match, Target: strings.Join(inner.Args, " "),
				TargetKind: "static", File: file, Line: inner.Line})
		case "location":
			out = append(out, buildNginxLocation(inner, file)...)
		}
	}
	if len(out) == 0 {
		out = append(out, model.Route{Match: match, Target: "", TargetKind: "unknown", File: file, Line: d.Line})
	}
	return out
}

// proxyRoute classifies a proxy_pass target as a named upstream or a direct address.
func proxyRoute(match, target, file string, line int) model.Route {
	r := model.Route{Match: match, Target: target, TargetKind: "unknown", File: file, Line: line}
	rest := target
	for _, scheme := range []string{"http://", "https://", "grpc://", "grpcs://"} {
		if strings.HasPrefix(rest, scheme) {
			rest = strings.TrimPrefix(rest, scheme)
			break
		}
	}
	host := rest
	if i := strings.IndexAny(host, "/;"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return r
	}
	if strings.ContainsAny(host, ":") || isIPish(host) {
		r.TargetKind = "address"
		r.Target = host
		return r
	}
	// A bare name is either an upstream block or a DNS name; the caller resolves it.
	r.Target = host
	return r
}

// parseListen understands the forms nginx accepts: "80", "*:80", "1.2.3.4:80",
// "[::]:80", "unix:/path", plus trailing option flags.
func parseListen(args []string, stream bool) (addr string, port int, opts map[string]string) {
	opts = map[string]string{}
	if len(args) == 0 {
		return "0.0.0.0", 80, opts
	}
	spec := args[0]
	for _, a := range args[1:] {
		switch {
		case a == "ssl":
			opts["ssl"] = "1"
		case a == "udp":
			opts["udp"] = "1"
		case a == "default_server", a == "default":
			opts["default_server"] = "1"
		case a == "http2", a == "quic", a == "proxy_protocol":
			opts[a] = "1"
		}
	}
	if strings.HasPrefix(spec, "unix:") {
		return "", 0, opts
	}

	defPort := 80
	if opts["ssl"] == "1" {
		defPort = 443
	}
	if stream {
		defPort = 0
	}

	switch {
	case strings.HasPrefix(spec, "["): // [::]:80 or [::1]:8080
		end := strings.LastIndex(spec, "]")
		if end < 0 {
			return spec, defPort, opts
		}
		addr = spec[1:end]
		rest := strings.TrimPrefix(spec[end+1:], ":")
		if rest != "" {
			port, _ = strconv.Atoi(rest)
		} else {
			port = defPort
		}
	case strings.Contains(spec, ":"):
		i := strings.LastIndex(spec, ":")
		addr = spec[:i]
		port, _ = strconv.Atoi(spec[i+1:])
	default:
		if p, err := strconv.Atoi(spec); err == nil {
			addr, port = "0.0.0.0", p
		} else {
			// A bare hostname/IP listen with no port.
			addr, port = spec, defPort
		}
	}
	if addr == "*" || addr == "" {
		addr = "0.0.0.0"
	}
	return addr, port, opts
}

func defaultUpstreamPort(stream bool) int {
	if stream {
		return 0
	}
	return 80
}

// --------------------------------------------------------------------- helpers

func splitHostPort(spec string, defPort int) (string, int) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "[") {
		end := strings.LastIndex(spec, "]")
		if end > 0 {
			host := spec[1:end]
			if rest := strings.TrimPrefix(spec[end+1:], ":"); rest != "" {
				p, _ := strconv.Atoi(rest)
				return host, p
			}
			return host, defPort
		}
	}
	if i := strings.LastIndex(spec, ":"); i >= 0 {
		if p, err := strconv.Atoi(spec[i+1:]); err == nil {
			return spec[:i], p
		}
	}
	return spec, defPort
}

func isIPish(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' && r != ':' {
			return false
		}
	}
	return true
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func fileNames(files []model.ManagedFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// describeFile builds the metadata record for a config file.
func describeFile(c collect.Collector, path, service string, editable bool) model.ManagedFile {
	f := model.ManagedFile{Path: path, Service: service, Editable: editable}
	if st, err := c.Stat(path); err == nil {
		f.Size, f.ModTime, f.Readable = st.Size, st.ModTime, true
	}
	if data, err := c.ReadFile(path); err == nil {
		sum := sha256.Sum256(data)
		f.SHA256 = hex.EncodeToString(sum[:])
		f.Readable = true
	} else {
		f.Readable = false
		f.Note = err.Error()
		f.Editable = false
	}
	return f
}

// binaryVersion asks a tool for its version string, tolerating its habit of
// printing to stderr.
func binaryVersion(ctx context.Context, c collect.Collector, name string, args ...string) string {
	res, err := c.Run(ctx, name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(res.Output(), "\n", 2)[0])
}
