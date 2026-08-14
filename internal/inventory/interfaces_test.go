package inventory

import (
	"context"
	"testing"

	"github.com/althq/netknownsthat/internal/model"
)

// TestAttachInterfaceOwnership covers the three ways a bridge interface's
// name gets resolved back to a docker network: an explicitly configured
// bridge name, Docker's own deterministic name for the implicit default
// "bridge" network (docker0), and Docker's deterministic auto-generated
// name for every other network ("br-<12 hex of the network ID>"). A
// bridge with nothing plugged into it (br-empty) must still resolve to
// its network with a real, explicit 0 — not be left looking unmatched.
func TestAttachInterfaceOwnership(t *testing.T) {
	snap := &model.Snapshot{
		Networks: []model.DockerNetwork{
			{ID: "aaaaaaaaaaaa1111222233334444", Name: "bridge"}, // no explicit Bridge -> docker0
			{ID: "bbbbbbbbbbbb5555666677778888", Name: "backend", Bridge: "br-custom-name"},
			{ID: "cccccccccccc9999000011112222", Name: "monitoring"}, // -> br-cccccccccccc
			{ID: "dddddddddddd3333444455556666", Name: "empty"},      // -> br-dddddddddddd, no containers
		},
		Container: []model.Container{
			{Name: "app", Networks: []model.ContainerNetwork{{Name: "backend"}}},
			{Name: "api", Networks: []model.ContainerNetwork{{Name: "backend"}}},
			{Name: "grafana", Networks: []model.ContainerNetwork{{Name: "backend"}, {Name: "monitoring"}}},
		},
		Interfaces: []model.NetworkInterface{
			{Name: "lo"},
			{Name: "docker0"},
			{Name: "br-custom-name"},
			{Name: "br-cccccccccccc"},
			{Name: "br-dddddddddddd"},
			{Name: "eth0"},
		},
	}

	attachInterfaceOwnership(snap)

	byName := map[string]model.NetworkInterface{}
	for _, i := range snap.Interfaces {
		byName[i.Name] = i
	}

	if got := byName["docker0"]; got.DockerNetwork != "bridge" {
		t.Errorf("docker0.DockerNetwork = %q, want %q (implicit default network)", got.DockerNetwork, "bridge")
	}
	// app, api and grafana all attach to "backend" (grafana attaches to
	// both networks) — 3, not 2.
	if got := byName["br-custom-name"]; got.DockerNetwork != "backend" || got.AttachedContainers != 3 {
		t.Errorf("br-custom-name = %+v, want backend/3 (explicit bridge name)", got)
	}
	if got := byName["br-cccccccccccc"]; got.DockerNetwork != "monitoring" || got.AttachedContainers != 1 {
		t.Errorf("br-cccccccccccc = %+v, want monitoring/1 (auto-generated bridge name)", got)
	}
	if got := byName["br-dddddddddddd"]; got.DockerNetwork != "empty" || got.AttachedContainers != 0 {
		t.Errorf("br-dddddddddddd = %+v, want empty/0 — an unmatched network must still resolve, not be skipped", got)
	}
	if got := byName["lo"]; got.DockerNetwork != "" {
		t.Errorf("lo.DockerNetwork = %q, want empty — not every interface is a docker bridge", got.DockerNetwork)
	}
	if got := byName["eth0"]; got.DockerNetwork != "" {
		t.Errorf("eth0.DockerNetwork = %q, want empty", got.DockerNetwork)
	}
}

// TestAttachInterfaceOwnershipAgainstFixtures proves the wiring, not just
// the matching logic in isolation: a real Scan() against fixtures/host
// must come out with br-acme-backend correctly attributed to its docker
// network and container count.
func TestAttachInterfaceOwnershipAgainstFixtures(t *testing.T) {
	s := fixtureScanner(t)
	snap, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	var backend *model.NetworkInterface
	for i := range snap.Interfaces {
		if snap.Interfaces[i].Name == "br-acme-backend" {
			backend = &snap.Interfaces[i]
		}
	}
	if backend == nil {
		t.Fatal("br-acme-backend not found in snap.Interfaces")
	}
	if backend.DockerNetwork != "acme_backend" {
		t.Errorf("DockerNetwork = %q, want %q", backend.DockerNetwork, "acme_backend")
	}
	if backend.AttachedContainers == 0 {
		t.Error("AttachedContainers = 0, want > 0 — fixtures has real containers on acme_backend")
	}
}
