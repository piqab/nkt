package api

import (
	"context"
	"reflect"
	"testing"

	"github.com/piqab/nkt/internal/model"
)

func TestNetReachable(t *testing.T) {
	for addr, want := range map[string]bool{
		"0.0.0.0": true, "*": true, "::": true, "[::]": true, "": true,
		"10.10.0.2": true, "203.0.113.5": true, "fe80::1%eth0": true,
		"127.0.0.1": false, "127.0.0.53": false, "::1": false, "[::1]": false, "localhost": false, "::ffff:127.0.0.1": false,
	} {
		if got := netReachable(addr); got != want {
			t.Errorf("netReachable(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestParseDpkgOwners(t *testing.T) {
	owners := map[string][]string{}
	parseDpkgOwners("openssh-server: /usr/sbin/sshd\nnginx-core:amd64: /usr/sbin/nginx\nlibc6:amd64, libc6:i386: /usr/lib/x\ndpkg-query: no path found matching pattern /opt/app\ndiversion by foo from: /usr/bin/bar\n", owners)
	want := map[string][]string{
		"/usr/sbin/sshd":  {"openssh-server"},
		"/usr/sbin/nginx": {"nginx-core"},
		"/usr/lib/x":      {"libc6"},
	}
	if !reflect.DeepEqual(owners, want) {
		t.Fatalf("owners = %v, want %v", owners, want)
	}
}

// Без dpkg (коллектора нет) — пакеты по имени процесса; loopback, порты
// контейнеров через docker-proxy и неопубликованные порты не считаются.
func TestComputeVulnExposure(t *testing.T) {
	snap := &model.Snapshot{
		Listeners: []model.Listener{
			{Address: "0.0.0.0", Port: 22, Process: "sshd", PID: 1},
			{Address: "0.0.0.0", Port: 443, Process: "nginx", PID: 2},
			{Address: "0.0.0.0", Port: 80, Process: "nginx", PID: 2},
			{Address: "127.0.0.1", Port: 11211, Process: "memcached", PID: 3},
			{Address: "0.0.0.0", Port: 6379, Process: "docker-proxy", PID: 4},
			{Address: "10.0.0.2", Port: 5432, Process: "haproxy", PID: 5},
		},
		Container: []model.Container{
			{Image: "redis:7-alpine", State: "running", Ports: []model.PortMapping{{HostIP: "0.0.0.0", HostPort: 6379}}},
			{Image: "grafana/grafana:11", State: "running", Ports: []model.PortMapping{{HostIP: "127.0.0.1", HostPort: 3000}}},
			{Image: "old:1", State: "exited", Ports: []model.PortMapping{{HostIP: "0.0.0.0", HostPort: 81}}},
		},
	}
	got := computeVulnExposure(context.Background(), nil, snap)
	for pkg, ports := range map[string][]int{"openssh-server": {22}, "nginx": {80, 443}, "nginx-core": {80, 443}, "haproxy": {5432}} {
		if !reflect.DeepEqual(got.Packages[pkg], ports) {
			t.Errorf("Packages[%s] = %v, want %v", pkg, got.Packages[pkg], ports)
		}
	}
	for _, pkg := range []string{"memcached", "docker-proxy", "docker.io"} {
		if _, ok := got.Packages[pkg]; ok {
			t.Errorf("Packages[%s] should be absent", pkg)
		}
	}
	if want := map[string][]int{"redis:7-alpine": {6379}}; !reflect.DeepEqual(got.Images, want) {
		t.Errorf("Images = %v, want %v", got.Images, want)
	}
}
