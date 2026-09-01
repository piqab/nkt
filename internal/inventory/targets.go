package inventory

import (
	"context"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

// syncTargets derives the probe list from the snapshot. Targets an operator
// added by hand are never touched; auto-derived ones that disappeared from the
// configuration are removed together with their history.
func (s *Scanner) syncTargets(ctx context.Context, snap *model.Snapshot) error {
	cutoff := store.Now()
	for _, t := range DeriveTargets(snap) {
		if _, err := s.db.UpsertTarget(ctx, t); err != nil {
			return err
		}
	}
	_, err := s.db.PruneDerivedTargets(ctx, cutoff)
	return err
}

// DeriveTargets turns endpoints and backend pool members into probe targets.
func DeriveTargets(snap *model.Snapshot) []store.Target {
	seen := map[string]bool{}
	var out []store.Target

	add := func(t store.Target) {
		if t.Port <= 0 || seen[t.Key] {
			return
		}
		seen[t.Key] = true
		out = append(out, t)
	}

	for _, e := range snap.Endpoints {
		if e.Protocol == "udp" {
			continue
		}
		host := probeHost(e.Address)
		kind, path := probeKind(e)
		hostHeader := ""
		if kind != "tcp" && len(e.Names) > 0 && isHostname(e.Names[0]) {
			hostHeader = e.Names[0]
		}
		label := e.Label
		if label == "" {
			label = fmt.Sprintf("%s %s", e.Service, e.Socket())
		}
		add(store.Target{
			Key:        fmt.Sprintf("ep:%s:%s:%d:%s", kind, host, e.Port, hostHeader),
			Label:      fmt.Sprintf("%s · %s", e.Service, label),
			Kind:       kind,
			Host:       host,
			Port:       e.Port,
			Path:       path,
			HostHeader: hostHeader,
			Source:     e.Service,
			Service:    e.Service,
			NodeID:     e.ID,
		})
	}

	for _, u := range snap.Upstreams {
		for _, srv := range u.Servers {
			if srv.Port <= 0 || srv.Down {
				continue
			}
			name := srv.Name
			if name == "" {
				name = srv.Socket()
			}
			add(store.Target{
				Key:     fmt.Sprintf("up:%s:%s:%s:%d", u.Service, u.Name, srv.Host, srv.Port),
				Label:   fmt.Sprintf("backend %s · %s", u.Name, name),
				Kind:    "tcp",
				Host:    srv.Host,
				Port:    srv.Port,
				Source:  u.Service,
				Service: u.Service,
				NodeID:  u.ID,
			})
		}
	}
	return out
}

// probeHost turns a bind address into something dialable from the host itself.
func probeHost(addr string) string {
	switch addr {
	case "", "*", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	}
	return addr
}

// probeKind decides how a declared endpoint should be checked.
func probeKind(e model.Endpoint) (kind, path string) {
	switch {
	case e.TLS:
		return "https", "/"
	case e.Mode == "http" && e.Service != model.ServiceDocker:
		return "http", "/"
	default:
		return "tcp", ""
	}
}

func isHostname(name string) bool {
	if name == "" || name == "_" || strings.ContainsAny(name, "*~^$") {
		return false
	}
	return strings.Contains(name, ".")
}
