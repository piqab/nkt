package parse

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// CaddyResult is everything the Caddy parser produces.
type CaddyResult struct {
	Status    model.SourceStatus
	Endpoints []model.Endpoint
	Upstreams []model.Upstream
	Files     []model.ManagedFile
}

// Caddy parses a Caddyfile into sites (endpoints) and their reverse_proxy
// pools (upstreams). Caddyfile-only for v1 — a host running Caddy from its
// native JSON config (caddy.json, not a Caddyfile) gets no Endpoint/Upstream
// data from this parser at all (mainConfig pointing at JSON just fails to
// scan as one and comes back empty), though the file itself is still
// readable/editable as plain text via the generic Configs page. JSON is a
// tree of apps.http.servers.<name>.routes[] with no line-range "block" a
// user edits by hand the way a Caddyfile site is — the whole point of the
// Block model this shares with nginx/haproxy — so it needs its own
// different approach entirely, not an extension of this one.
func Caddy(ctx context.Context, c collect.Collector, mainConfig string) CaddyResult {
	started := time.Now()
	res := CaddyResult{Status: model.SourceStatus{Name: model.ServiceCaddy}}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()

	if mainConfig == "" {
		return res
	}
	raw, err := c.ReadFile(mainConfig)
	if err != nil {
		res.Status.Error = fmt.Sprintf("конфиг %s недоступен: %v", mainConfig, err)
		return res
	}
	res.Status.Available = true
	res.Status.Version = binaryVersion(ctx, c, "caddy", "version")
	res.Files = append(res.Files, describeFile(c, mainConfig, model.ServiceCaddy, true))
	res.Status.Files = fileNames(res.Files)

	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	sections, err := scanCaddySites(lines)
	if err != nil {
		res.Status.Warnings = append(res.Status.Warnings, err.Error())
	}

	for _, sec := range sections {
		res.Endpoints = append(res.Endpoints, caddySiteEndpoints(sec, mainConfig, &res.Upstreams)...)
	}
	sort.Slice(res.Endpoints, func(i, j int) bool { return res.Endpoints[i].Port < res.Endpoints[j].Port })
	return res
}

// caddySiteEndpoints scans one site block's body for the handful of
// directives that determine where traffic actually goes, then builds one
// Endpoint per address in the block's header (a site with several
// comma-separated addresses answers on all of them identically — the same
// one-endpoint-per-bind-point convention haproxy's per-bind Endpoint and
// nginx's per-listen Endpoint already follow).
//
// Deliberately flat: a reverse_proxy/redir/file_server inside a nested
// handle{}/route{}/@matcher block is still picked up (the scan just walks
// every line in the section regardless of nesting depth), but which
// specific path matcher gated it is not recorded beyond the raw directive
// text in Route.Match — enough to show "this site talks to that backend"
// on the resource map and in findings, not enough to reconstruct Caddy's
// own routing precedence. Caddy's own semantics (auto HTTPS, implicit
// HTTP→HTTPS redirect on :80, default HSTS) are inferred by
// parseCaddyAddress from the address alone, not scanned for here — nothing
// in a Caddyfile's text says any of that, it's Caddy's own built-in
// default behavior.
func caddySiteEndpoints(sec caddySection, file string, upstreams *[]model.Upstream) []model.Endpoint {
	addrs := splitCaddyAddresses(sec.Addr)
	var routes []model.Route
	upstreamIdx := 0

	for _, raw := range sec.Lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "}" || strings.HasSuffix(trimmed, "{") {
			continue // blank/comment lines and block open/close lines are not directives
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "reverse_proxy":
			// A leading path matcher ("/api/*") or named matcher ("@api")
			// can precede the backend list — neither is a host:port.
			var backends []string
			for _, t := range fields[1:] {
				if strings.HasPrefix(t, "/") || strings.HasPrefix(t, "@") || strings.HasPrefix(t, "*") {
					continue
				}
				backends = append(backends, t)
			}
			if len(backends) == 0 {
				continue
			}
			if len(backends) == 1 {
				routes = append(routes, model.Route{Match: trimmed, Target: backends[0], TargetKind: "address", File: file})
				continue
			}
			upstreamIdx++
			name := fmt.Sprintf("%s#%d", strings.Join(addrs, ","), upstreamIdx)
			u := model.Upstream{
				ID: "caddy:upstream:" + name, Name: name, Service: model.ServiceCaddy,
				Mode: "http", Algorithm: "round_robin", File: file,
			}
			for _, b := range backends {
				host, port := splitHostPort(b, 0)
				u.Servers = append(u.Servers, model.UpstreamServer{Host: host, Port: port})
			}
			*upstreams = append(*upstreams, u)
			routes = append(routes, model.Route{Match: trimmed, Target: name, TargetKind: "upstream", File: file})
		case "redir", "redirect":
			if len(fields) >= 2 {
				routes = append(routes, model.Route{Match: trimmed, Target: fields[1], TargetKind: "redirect", File: file})
			}
		case "file_server":
			routes = append(routes, model.Route{Match: trimmed, Target: "static", TargetKind: "static", File: file})
		case "root":
			// "root * /var/www" — file_server usually follows and is what
			// actually serves it, but root is recorded too in case it's
			// absent (Caddy's own php_fastcgi and similar shorthands imply
			// a file root without a literal file_server line).
			if len(fields) >= 2 {
				routes = append(routes, model.Route{
					Match: trimmed, Target: fields[len(fields)-1], TargetKind: "static", File: file,
				})
			}
		case "respond":
			routes = append(routes, model.Route{Match: trimmed, Target: "respond", TargetKind: "static", File: file})
		}
	}

	out := make([]model.Endpoint, 0, len(addrs))
	for _, a := range addrs {
		address, port, tls, names, ok := parseCaddyAddress(a)
		if !ok {
			continue
		}
		ep := model.Endpoint{
			ID:       fmt.Sprintf("caddy:%s:%d:%s", address, port, strings.Join(names, ",")),
			Service:  model.ServiceCaddy,
			Kind:     "site",
			Address:  address,
			Port:     port,
			Protocol: "tcp",
			TLS:      tls,
			Mode:     "http",
			Names:    names,
			Routes:   routes,
			File:     file,
			Line:     sec.Line,
			Label:    a,
			Extra:    map[string]string{},
		}
		for _, r := range routes {
			if r.TargetKind == "upstream" {
				ep.Upstream = appendUnique(ep.Upstream, r.Target)
			}
		}
		out = append(out, ep)
	}
	return out
}

// splitCaddyAddresses splits a site header's address list on commas and/or
// whitespace — Caddy accepts either as a separator between addresses.
func splitCaddyAddresses(header string) []string {
	return strings.FieldsFunc(header, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
}

// parseCaddyAddress infers the socket and TLS state a single Caddyfile
// site address resolves to. Unlike nginx/haproxy, none of this is stated
// explicitly in most real Caddyfiles — Caddy fills in the defaults itself
// at runtime (automatic HTTPS with a real ACME certificate, port 443
// unless told otherwise), so "no scheme, no port" has to be read as
// "https on 443", not "unspecified":
//
//   - "http://" forces plaintext on 80 (or the given port).
//   - "https://" forces TLS on 443 (or the given port).
//   - A bare port ("`:8080`", no hostname) gets plain HTTP — there is no
//     domain for Caddy to obtain a certificate for.
//   - A bare hostname on port 80 explicitly is plaintext (Caddy's own
//     special case: automatic HTTPS never applies to port 80).
//   - Anything else with a real hostname (no scheme, no port, or a
//     non-80 port) is assumed to get Caddy's automatic HTTPS.
func parseCaddyAddress(addr string) (address string, port int, tls bool, names []string, ok bool) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", 0, false, nil, false
	}
	scheme := ""
	if i := strings.Index(addr, "://"); i >= 0 {
		scheme = addr[:i]
		addr = addr[i+3:]
	}
	// A path matcher can trail the address itself ("example.com/api/*") —
	// irrelevant to what socket this is.
	if j := strings.Index(addr, "/"); j >= 0 {
		addr = addr[:j]
	}

	host, portStr := addr, ""
	switch {
	case strings.HasPrefix(addr, ":"):
		portStr = strings.TrimPrefix(addr, ":")
		host = ""
	default:
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			host, portStr = addr[:i], addr[i+1:]
		}
	}

	switch {
	case scheme == "http":
		tls = false
		if portStr == "" {
			portStr = "80"
		}
	case scheme == "https":
		tls = true
		if portStr == "" {
			portStr = "443"
		}
	case host == "" || isIPish(host):
		// A bare port has no domain to get a certificate for; a literal IP
		// is the same story for the common case (Let's Encrypt, Caddy's
		// default ACME issuer, does not issue for bare IPs at all — only
		// some other configured issuer would), so automatic HTTPS is not
		// assumed for either.
		if portStr == "" {
			return "", 0, false, nil, false
		}
		tls = false
	case portStr == "80":
		tls = false
	default:
		tls = true
		if portStr == "" {
			portStr = "443"
		}
	}

	p := 0
	if _, err := fmt.Sscanf(portStr, "%d", &p); err != nil || p <= 0 {
		return "", 0, false, nil, false
	}

	address = "0.0.0.0"
	if host != "" {
		if isIPish(host) {
			address = host
		} else {
			names = []string{host}
		}
	}
	return address, p, tls, names, true
}
