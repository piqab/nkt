package parse

import (
	"context"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
)

// TestLXDNeverReturnsNilInstances guards against a real production crash: a
// host without LXD must still get a real empty slice back, not nil — the
// field has no `omitempty`, encoding/json marshals nil as `null`, and the
// LXD page crashes calling .map on `null`.
func TestLXDNeverReturnsNilInstances(t *testing.T) {
	res := LXD(context.Background(), collect.NewFixtures(t.TempDir()))
	if res.Instances == nil {
		t.Error("Instances = nil, ожидался непустой (даже если пустой) срез")
	}
}

func TestLXDListsInstances(t *testing.T) {
	res := LXD(context.Background(), fixtureCollector(t))
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}
	if !res.Status.Available {
		t.Fatal("Status.Available = false, ожидалось true")
	}
	if len(res.Instances) != 3 {
		t.Fatalf("len(Instances) = %d, ожидалось 3", len(res.Instances))
	}

	byName := map[string]bool{}
	for _, inst := range res.Instances {
		byName[inst.Name] = true
	}
	for _, want := range []string{"build-runner", "win-testbed", "dns-cache"} {
		if !byName[want] {
			t.Errorf("инстанс %s не найден среди %v", want, byName)
		}
	}

	for _, inst := range res.Instances {
		switch inst.Name {
		case "build-runner":
			if inst.Status != "running" {
				t.Errorf("build-runner: Status = %q, ожидалось running (в нижнем регистре)", inst.Status)
			}
			if inst.Type != "container" {
				t.Errorf("build-runner: Type = %q, ожидалось container", inst.Type)
			}
			if len(inst.IPv4) != 1 || inst.IPv4[0] != "10.90.10.11" {
				t.Errorf("build-runner: IPv4 = %v, ожидалось [10.90.10.11] (без IPv6)", inst.IPv4)
			}
		case "win-testbed":
			if inst.Status != "stopped" {
				t.Errorf("win-testbed: Status = %q, ожидалось stopped", inst.Status)
			}
			if inst.Type != "virtual-machine" {
				t.Errorf("win-testbed: Type = %q, ожидалось virtual-machine", inst.Type)
			}
			if len(inst.IPv4) != 0 {
				t.Errorf("win-testbed: IPv4 = %v, ожидался пустой список (state = null)", inst.IPv4)
			}
		}
	}
}
