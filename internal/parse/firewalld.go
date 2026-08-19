package parse

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// parseFirewalld checks and (if active) reads firewalld, appending anything
// it finds straight onto res — same shape as parseUFW, so the two coexist
// on one host without either clobbering the other's Backends/Rules/Policies.
func parseFirewalld(ctx context.Context, c collect.Collector, res *FirewallResult) []model.FirewallManagerState {
	installed := collect.Which(ctx, c, "firewall-cmd")
	if !installed {
		return []model.FirewallManagerState{{Name: "firewalld"}}
	}

	active := false
	if out, err := c.Run(ctx, "firewall-cmd", "--state"); err == nil && out.OK() &&
		strings.TrimSpace(out.Stdout) == "running" {
		active = true
	}

	defaultZone := ""
	if out, err := c.Run(ctx, "firewall-cmd", "--get-default-zone"); err == nil && out.OK() {
		defaultZone = strings.TrimSpace(out.Stdout)
	}

	if !active {
		return []model.FirewallManagerState{{Name: "firewalld", Installed: true, Policy: defaultZone}}
	}
	res.Status.Available = true
	res.State.Backends = append(res.State.Backends, "firewalld")

	// Runtime (what's actually in effect right now) and permanent (what
	// survives `firewall-cmd --reload`/a reboot) are two independent views
	// — firewalld's own defining quirk, the analogue of ufw's numbered vs
	// added split. Unlike ufw, both are always readable regardless of the
	// other, so — unlike parseUFW — there's no fallback-when-inactive case
	// to handle here; they can simply both be read straight through.
	if out, err := c.Run(ctx, "firewall-cmd", "--list-all-zones"); err == nil && out.OK() {
		policies, rules := parseFirewalldZones(out.Stdout, false)
		res.State.Policies = append(res.State.Policies, policies...)
		res.State.Rules = append(res.State.Rules, rules...)
	} else if err != nil {
		res.Status.Warnings = append(res.Status.Warnings, fmt.Sprintf("firewall-cmd --list-all-zones: %v", err))
	}
	if out, err := c.Run(ctx, "firewall-cmd", "--permanent", "--list-all-zones"); err == nil && out.OK() {
		_, rules := parseFirewalldZones(out.Stdout, true)
		res.State.Rules = append(res.State.Rules, rules...)
	} else if err != nil {
		res.Status.Warnings = append(res.Status.Warnings,
			fmt.Sprintf("firewall-cmd --permanent --list-all-zones: %v", err))
	}

	return []model.FirewallManagerState{{Name: "firewalld", Installed: true, Active: true, Policy: defaultZone}}
}

var firewalldZoneHeaderRe = regexp.MustCompile(`^(\S+)\s*(?:\(([^)]*)\))?\s*$`)

// parseFirewalldZones reads `firewall-cmd [--permanent] --list-all-zones`'s
// own text format — there is no stable machine-readable output mode covering
// every firewalld version, so this scrapes the same human-readable listing
// the `ufw status verbose` parser does for ufw. Each zone is a block: a
// column-0 header line ("public (active)"), then indented "key: value"
// lines, with "rich rules:" being the one key whose values are further-
// indented continuation lines rather than a single line.
//
// Every port/service/rich-rule becomes its own FirewallRule — like ufw's
// app profiles, a firewalld "service" name (ssh, dhcpv6-client, ...) has no
// numeric ports of its own in this output, so it's kept in PortSpec with an
// empty Ports slice rather than guessed at.
func parseFirewalldZones(text string, permanent bool) ([]model.FirewallPolicy, []model.FirewallRule) {
	var (
		policies []model.FirewallPolicy
		rules    []model.FirewallRule
		zone     string
		inRich   bool
		order    int
	)
	tag := "run"
	if permanent {
		tag = "perm"
	}

	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(raw) == "" {
			inRich = false
			continue
		}
		if !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
			m := firewalldZoneHeaderRe.FindStringSubmatch(strings.TrimSpace(raw))
			if m == nil {
				continue
			}
			zone = m[1]
			inRich = false
			continue
		}
		if zone == "" {
			continue
		}
		trimmed := strings.TrimSpace(raw)

		if inRich {
			order++
			action := ""
			switch {
			case strings.Contains(trimmed, "accept"):
				action = "ACCEPT"
			case strings.Contains(trimmed, "reject"):
				action = "REJECT"
			case strings.Contains(trimmed, "drop"):
				action = "DROP"
			}
			rules = append(rules, model.FirewallRule{
				ID:        fmt.Sprintf("firewalld:%s:%s:rich:%d", zone, tag, order),
				Backend:   "firewalld",
				Chain:     zone,
				Zone:      zone,
				Order:     order,
				Action:    action,
				Raw:       trimmed,
				ManagedBy: "firewalld",
				Permanent: permanent,
				Runtime:   !permanent,
			})
			continue
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "target":
			// firewalld wraps its two ICMP-reject built-in targets in
			// "%%" (e.g. "%%REJECT%%") — cosmetic, stripped for display.
			policies = append(policies, model.FirewallPolicy{
				Backend: "firewalld", Chain: zone, Policy: strings.Trim(value, "%"),
			})
		case "services":
			for _, svc := range strings.Fields(value) {
				order++
				rules = append(rules, model.FirewallRule{
					ID:        fmt.Sprintf("firewalld:%s:%s:service:%s", zone, tag, svc),
					Backend:   "firewalld",
					Chain:     zone,
					Zone:      zone,
					Order:     order,
					Action:    "ACCEPT",
					PortSpec:  svc,
					ManagedBy: "firewalld",
					Permanent: permanent,
					Runtime:   !permanent,
				})
			}
		case "ports":
			for _, p := range strings.Fields(value) {
				portPart, proto := p, ""
				if i := strings.LastIndex(p, "/"); i >= 0 {
					portPart, proto = p[:i], p[i+1:]
				}
				order++
				rules = append(rules, model.FirewallRule{
					ID:       fmt.Sprintf("firewalld:%s:%s:port:%s", zone, tag, p),
					Backend:  "firewalld",
					Chain:    zone,
					Zone:     zone,
					Order:    order,
					Action:   "ACCEPT",
					Protocol: proto, PortSpec: portPart,
					// firewalld ranges are "lo-hi" (a hyphen), unlike the
					// ":"-separated ranges expandPortSpec was written for
					// (nginx/iptables/ufw all use ":") — translating the
					// first hyphen buys range expansion for free instead
					// of duplicating that function.
					Ports:     expandPortSpec(strings.Replace(portPart, "-", ":", 1)),
					ManagedBy: "firewalld",
					Permanent: permanent,
					Runtime:   !permanent,
				})
			}
		case "rich rules":
			inRich = true
		}
	}
	return policies, rules
}
