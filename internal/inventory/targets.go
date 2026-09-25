package inventory

import (
	"context"
	"fmt"
	"strconv"
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
		if (t.Port <= 0 && t.Kind != "icmp") || seen[t.Key] {
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
	deriveWorkloadTargets(snap, add)
	return out
}

// deriveWorkloadTargets — опубликованные порты Podman, проброшенные порты
// LXD (устройства proxy) и сами работающие машины: инстансы LXD и машины
// libvirt проверяются ping по их адресу.
func deriveWorkloadTargets(snap *model.Snapshot, add func(store.Target)) {
	for _, ct := range snap.Podman {
		if ct.State != "running" {
			continue
		}
		for _, p := range ct.Ports {
			if !p.Published() || (p.Protocol != "" && p.Protocol != "tcp") {
				continue
			}
			host := probeHost(p.HostIP)
			add(store.Target{
				Key:   fmt.Sprintf("podman:%s:%s:%d", ct.Name, host, p.HostPort),
				Label: fmt.Sprintf("podman · %s %d→%d", ct.Name, p.HostPort, p.ContainerPort),
				Kind:  "tcp", Host: host, Port: p.HostPort,
				Source: model.ServicePodman, Service: model.ServicePodman, NodeID: "podman:" + ct.Name,
			})
		}
	}
	for _, in := range snap.LXD {
		if !strings.EqualFold(in.Status, "running") {
			continue
		}
		for _, p := range in.Ports {
			proto, rest, ok := strings.Cut(p.Listen, ":")
			if !ok || proto != "tcp" {
				continue
			}
			i := strings.LastIndex(rest, ":")
			if i < 0 {
				continue
			}
			port, err := strconv.Atoi(rest[i+1:])
			if err != nil {
				continue
			}
			host := probeHost(strings.Trim(rest[:i], "[]"))
			add(store.Target{
				Key:   fmt.Sprintf("lxd-port:%s:%s:%s:%d", in.Name, p.Device, host, port),
				Label: fmt.Sprintf("lxd · %s %d (%s)", in.Name, port, p.Device),
				Kind:  "tcp", Host: host, Port: port,
				Source: model.ServiceLXD, Service: model.ServiceLXD, NodeID: "lxd:" + in.Name,
			})
		}
		if len(in.IPv4) > 0 {
			add(store.Target{
				Key:   "lxd-ip:" + in.Name,
				Label: fmt.Sprintf("lxd · %s", in.Name),
				Kind:  "icmp", Host: in.IPv4[0],
				Source: model.ServiceLXD, Service: model.ServiceLXD, NodeID: "lxd:" + in.Name,
			})
		}
	}
	for _, vm := range snap.VMs {
		if vm.State != "running" {
			continue
		}
		for _, n := range vm.Networks {
			if n.IP == "" {
				continue
			}
			add(store.Target{
				Key:   "vm-ip:" + vm.Name,
				Label: fmt.Sprintf("libvirt · %s", vm.Name),
				Kind:  "icmp", Host: n.IP,
				Source: model.ServiceLibvirt, Service: model.ServiceLibvirt, NodeID: "vm:" + vm.Name,
			})
			break
		}
	}
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
