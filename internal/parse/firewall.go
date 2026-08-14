package parse

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// FirewallResult is everything the packet-filter parsers produce.
type FirewallResult struct {
	Status model.SourceStatus
	State  model.FirewallState
}

// Firewall reads iptables (v4 and v6) and ufw, normalising both into one view.
func Firewall(ctx context.Context, c collect.Collector) FirewallResult {
	started := time.Now()
	res := FirewallResult{Status: model.SourceStatus{Name: "firewall"}}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()
	// Backends/Policies/Rules have no `omitempty` — a host with iptables
	// unreadable and ufw inactive would otherwise leave them nil, which
	// encoding/json marshals as `null` and crashes any frontend .filter/.map
	// expecting an array.
	res.State.Backends = []string{}
	res.State.Policies = []model.FirewallPolicy{}
	res.State.Rules = []model.FirewallRule{}

	for _, backend := range []struct{ cmd, name string }{
		{"iptables-save", "iptables"},
		{"ip6tables-save", "ip6tables"},
	} {
		out, err := c.Run(ctx, backend.cmd, "-c")
		if err != nil {
			res.Status.Warnings = append(res.Status.Warnings, fmt.Sprintf("%s: %v", backend.cmd, err))
			continue
		}
		if !out.OK() {
			res.Status.Warnings = append(res.Status.Warnings,
				fmt.Sprintf("%s завершился с кодом %d: %s", backend.cmd, out.ExitCode,
					strings.TrimSpace(out.Stderr)))
			continue
		}
		res.Status.Available = true
		res.State.Backends = append(res.State.Backends, backend.name)
		policies, rules := parseIptablesSave(out.Stdout, backend.name)
		res.State.Policies = append(res.State.Policies, policies...)
		res.State.Rules = append(res.State.Rules, rules...)
	}

	// A missing `ufw` binary and a real failure to run it need to read
	// differently to the operator: one is "install it", the other is
	// "something's actually broken". collect.Which checks with `command -v`
	// first (the same idiom parse.Packages and parse.Services use for
	// apt-get/docker/podman/etc.) rather than trying to infer "not
	// installed" from Run's error — Run itself can't distinguish that from
	// other exec-start failures reliably, and its behavior even differs
	// between the real and fixtures collectors for this exact case.
	res.State.UFWInstalled = collect.Which(ctx, c, "ufw")
	if res.State.UFWInstalled {
		if out, err := c.Run(ctx, "ufw", "status", "verbose"); err == nil && out.OK() {
			res.Status.Available = true
			active, policy, rules := parseUFWStatus(out.Stdout)
			res.State.UFWActive = active
			res.State.UFWPolicy = policy
			if active {
				res.State.Backends = append(res.State.Backends, "ufw")
				res.State.Rules = append(res.State.Rules, rules...)
			}
		} else if err != nil {
			res.Status.Warnings = append(res.Status.Warnings, fmt.Sprintf("ufw: %v", err))
		}
	}

	if !res.Status.Available {
		res.Status.Error = "не удалось прочитать ни iptables, ни ufw (нужны права root)"
	}
	return res
}

// --------------------------------------------------------------------- iptables

var (
	countersRe  = regexp.MustCompile(`^\[(\d+):(\d+)\]\s+(.*)$`)
	chainRe     = regexp.MustCompile(`^:(\S+)\s+(\S+)(?:\s+\[(\d+):(\d+)\])?`)
	portRangeRe = regexp.MustCompile(`^(\d+):(\d+)$`)
)

// parseIptablesSave reads the output of `iptables-save -c`.
func parseIptablesSave(text, backend string) ([]model.FirewallPolicy, []model.FirewallRule) {
	var (
		policies []model.FirewallPolicy
		rules    []model.FirewallRule
		table    string
		order    int
	)

	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#") || line == "COMMIT":
			continue
		case strings.HasPrefix(line, "*"):
			table = strings.TrimPrefix(line, "*")
			continue
		case strings.HasPrefix(line, ":"):
			m := chainRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			p := model.FirewallPolicy{Backend: backend, Table: table, Chain: m[1], Policy: m[2]}
			p.Packets, _ = strconv.ParseInt(m[3], 10, 64)
			p.Bytes, _ = strconv.ParseInt(m[4], 10, 64)
			policies = append(policies, p)
			continue
		}

		var packets, bytes int64
		body := line
		if m := countersRe.FindStringSubmatch(line); m != nil {
			packets, _ = strconv.ParseInt(m[1], 10, 64)
			bytes, _ = strconv.ParseInt(m[2], 10, 64)
			body = m[3]
		}
		if !strings.HasPrefix(body, "-A ") && !strings.HasPrefix(body, "-I ") {
			continue
		}

		order++
		rule := parseIptablesRule(body, backend, table, order)
		rule.Packets, rule.Bytes = packets, bytes
		rule.Raw = line
		rules = append(rules, rule)
	}
	return policies, rules
}

func parseIptablesRule(body, backend, table string, order int) model.FirewallRule {
	rule := model.FirewallRule{
		Backend: backend, Table: table, Order: order, ManagedBy: "manual",
	}
	tokens := splitArgs(body)

	// Every flag we care about takes exactly one value, so consume them in pairs.
	for i := 0; i < len(tokens); i++ {
		flag := tokens[i]
		value := ""
		if i+1 < len(tokens) {
			value = tokens[i+1]
		}
		matched := true
		switch flag {
		case "-A", "-I":
			rule.Chain = value
		case "-p", "--protocol":
			rule.Protocol = value
		case "-s", "--source":
			rule.Source = value
		case "-d", "--destination":
			rule.Destination = value
		case "-i", "--in-interface":
			rule.InIface = value
		case "-o", "--out-interface":
			rule.OutIface = value
		case "-j", "--jump":
			rule.Action = value
		case "--dport", "--destination-port", "--dports", "--destination-ports":
			rule.PortSpec = value
		case "--to-destination":
			rule.DNATTo = value
		case "--comment":
			rule.Comment = strings.Trim(value, `"`)
		default:
			matched = false
		}
		if matched {
			i++
		}
	}
	rule.Ports = expandPortSpec(rule.PortSpec)

	switch {
	case strings.Contains(rule.Chain, "DOCKER") || rule.Action == "DOCKER" ||
		strings.HasPrefix(rule.OutIface, "br-") || rule.OutIface == "docker0" ||
		strings.HasPrefix(rule.InIface, "br-") || rule.InIface == "docker0":
		rule.ManagedBy = "docker"
	case strings.HasPrefix(strings.ToLower(rule.Chain), "ufw"):
		rule.ManagedBy = "ufw"
	}

	rule.ID = fmt.Sprintf("%s:%s:%s:%d", backend, table, rule.Chain, order)
	return rule
}

// expandPortSpec turns "80", "80,443" and "8000:8010" into concrete port numbers.
// Ranges wider than 64 ports are represented by their bounds only, so a rule
// covering the whole ephemeral range does not blow up the model.
func expandPortSpec(spec string) []int {
	if spec == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if m := portRangeRe.FindStringSubmatch(part); m != nil {
			lo, _ := strconv.Atoi(m[1])
			hi, _ := strconv.Atoi(m[2])
			if hi-lo > 64 {
				out = append(out, lo, hi)
				continue
			}
			for p := lo; p <= hi; p++ {
				out = append(out, p)
			}
			continue
		}
		if p, err := strconv.Atoi(part); err == nil {
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

// splitArgs splits a rule line, honouring double quotes around comments.
func splitArgs(s string) []string {
	var (
		out     []string
		cur     strings.Builder
		inQuote bool
	)
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// --------------------------------------------------------------------------- ufw

var ufwRuleRe = regexp.MustCompile(`^(?:\[\s*\d+\]\s+)?(\S+)\s+(ALLOW|DENY|REJECT|LIMIT)\s+(IN|OUT|FWD)?\s*(.*)$`)

// parseUFWStatus reads `ufw status verbose`.
func parseUFWStatus(text string) (active bool, policy string, rules []model.FirewallRule) {
	inTable := false
	order := 0

	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Status:"):
			active = strings.Contains(trimmed, "active")
			continue
		case strings.HasPrefix(trimmed, "Default:"):
			policy = strings.TrimSpace(strings.TrimPrefix(trimmed, "Default:"))
			continue
		case strings.HasPrefix(trimmed, "--"):
			inTable = true
			continue
		case trimmed == "" || strings.HasPrefix(trimmed, "To ") || strings.HasPrefix(trimmed, "Logging:") ||
			strings.HasPrefix(trimmed, "New profiles:"):
			continue
		}
		if !inTable {
			continue
		}

		m := ufwRuleRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		order++
		to, action, direction, from := m[1], m[2], m[3], strings.TrimSpace(m[4])
		ipv6 := strings.Contains(trimmed, "(v6)")
		from = strings.TrimSpace(strings.ReplaceAll(from, "(v6)", ""))

		rule := model.FirewallRule{
			ID:        fmt.Sprintf("ufw:%d", order),
			Backend:   "ufw",
			Chain:     "ufw-user-input",
			Order:     order,
			Action:    action,
			Source:    from,
			ManagedBy: "ufw",
			Raw:       trimmed,
			Comment:   direction,
		}
		if ipv6 {
			rule.Backend = "ufw6"
		}
		if from == "" {
			rule.Source = "Anywhere"
		}

		portPart, proto := to, ""
		if i := strings.LastIndex(to, "/"); i >= 0 {
			portPart, proto = to[:i], to[i+1:]
		}
		rule.Protocol = proto
		rule.PortSpec = portPart
		rule.Ports = expandPortSpec(portPart)
		if len(rule.Ports) == 0 {
			// Application profile or "Anywhere": keep the literal for display.
			rule.PortSpec = to
			rule.Destination = to
		}
		rules = append(rules, rule)
	}
	return active, policy, rules
}

// --------------------------------------------------------------------- listeners

var ssProcessRe = regexp.MustCompile(`\("([^"]+)",pid=(\d+)`)

// Listeners parses `ss -tulpnH`, the ground truth of what is actually bound.
func Listeners(ctx context.Context, c collect.Collector) ([]model.Listener, model.SourceStatus) {
	started := time.Now()
	status := model.SourceStatus{Name: "listeners"}
	defer func() { status.DurationMS = time.Since(started).Milliseconds() }()

	// No `omitempty` on the JSON field this feeds — every return path below
	// must hand back a real (even if empty) slice, never nil, or
	// encoding/json marshals it as `null` and the frontend crashes calling
	// .filter/.map on it.
	listeners := []model.Listener{}

	out, err := c.Run(ctx, "ss", "-tulpnH")
	if err != nil {
		status.Error = fmt.Sprintf("ss: %v", err)
		return listeners, status
	}
	if !out.OK() {
		status.Error = fmt.Sprintf("ss завершился с кодом %d: %s", out.ExitCode, strings.TrimSpace(out.Stderr))
		return listeners, status
	}
	status.Available = true

	for _, line := range strings.Split(strings.ReplaceAll(out.Stdout, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		state := fields[1]
		if state != "LISTEN" && state != "UNCONN" {
			continue
		}
		addr, port, ok := splitListenAddr(fields[4])
		if !ok {
			continue
		}
		l := model.Listener{Protocol: fields[0], Address: addr, Port: port, Raw: strings.TrimSpace(line)}
		if m := ssProcessRe.FindStringSubmatch(line); m != nil {
			l.Process = m[1]
			l.PID, _ = strconv.Atoi(m[2])
		}
		listeners = append(listeners, l)
	}
	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].Port != listeners[j].Port {
			return listeners[i].Port < listeners[j].Port
		}
		return listeners[i].Address < listeners[j].Address
	})
	return listeners, status
}

// splitListenAddr handles "0.0.0.0:80", "[::]:443", "127.0.0.53%lo:53" and "*:111".
func splitListenAddr(spec string) (string, int, bool) {
	if i := strings.Index(spec, "%"); i >= 0 {
		if j := strings.LastIndex(spec, ":"); j > i {
			spec = spec[:i] + spec[j:]
		}
	}
	if strings.HasPrefix(spec, "[") {
		end := strings.LastIndex(spec, "]")
		if end < 0 {
			return "", 0, false
		}
		port, err := strconv.Atoi(strings.TrimPrefix(spec[end+1:], ":"))
		return spec[1:end], port, err == nil
	}
	i := strings.LastIndex(spec, ":")
	if i < 0 {
		return "", 0, false
	}
	addr := spec[:i]
	if addr == "*" {
		addr = "0.0.0.0"
	}
	port, err := strconv.Atoi(spec[i+1:])
	return addr, port, err == nil
}
