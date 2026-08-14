package parse

import (
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
)

// TestInterfacesAgainstFixtures exercises the real path end to end: JSON
// from `ip -j addr show` plus /proc/net/dev, both read through the same
// fixtures tree the rest of the test suite uses.
func TestInterfacesAgainstFixtures(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	c := collect.NewFixtures(root)

	ifaces, status := Interfaces(t.Context(), c)
	if !status.Available {
		t.Fatalf("Available = false: %s", status.Error)
	}
	if len(ifaces) != 4 {
		t.Fatalf("got %d interfaces, want 4: %+v", len(ifaces), ifaces)
	}

	byName := map[string]int{}
	for i, iface := range ifaces {
		byName[iface.Name] = i
	}

	lo := ifaces[byName["lo"]]
	if !lo.Loopback || !lo.Up || !lo.LowerUp {
		t.Errorf("lo = %+v, want loopback, up and lower_up", lo)
	}
	if len(lo.Addresses) != 2 {
		t.Errorf("lo addresses = %v, want 2 (v4 + v6)", lo.Addresses)
	}

	eth0 := ifaces[byName["eth0"]]
	if eth0.Loopback {
		t.Error("eth0 marked loopback, should not be")
	}
	if !eth0.Up || !eth0.LowerUp {
		t.Errorf("eth0 = %+v, want up and lower_up (real carrier)", eth0)
	}
	if eth0.MAC != "52:54:00:1a:2b:3c" {
		t.Errorf("eth0 MAC = %q, want %q", eth0.MAC, "52:54:00:1a:2b:3c")
	}
	if eth0.MTU != 1500 {
		t.Errorf("eth0 MTU = %d, want 1500", eth0.MTU)
	}
	if eth0.RXBytes == 0 || eth0.TXBytes == 0 {
		t.Errorf("eth0 traffic counters not attached: rx=%d tx=%d", eth0.RXBytes, eth0.TXBytes)
	}
	if eth0.RXErrors != 3 || eth0.RXDropped != 145 || eth0.TXDropped != 12 {
		t.Errorf("eth0 rx_errors/rx_dropped/tx_dropped = %d/%d/%d, want 3/145/12",
			eth0.RXErrors, eth0.RXDropped, eth0.TXDropped)
	}
}

// TestInterfacesReportsMissingIP locks in that a host without `ip` (or
// where it fails) says so via SourceStatus, rather than looking identical
// to a host that genuinely has zero interfaces.
func TestInterfacesReportsMissingIP(t *testing.T) {
	c := collect.NewFixtures(t.TempDir())
	ifaces, status := Interfaces(t.Context(), c)
	if status.Available {
		t.Error("Available = true even though ip could not run")
	}
	if status.Error == "" {
		t.Error("Error is empty — the failure would be invisible in the Источники table")
	}
	if ifaces == nil {
		t.Error("Interfaces returned a nil slice — encoding/json would marshal this as null")
	}
}

func TestReadTrafficCounters(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	c := collect.NewFixtures(root)

	counters := readTrafficCounters(c)
	eth0, ok := counters["eth0"]
	if !ok {
		t.Fatal("no counters found for eth0")
	}
	if eth0.rxBytes != 4293102934 || eth0.txBytes != 812934021 {
		t.Errorf("eth0 byte counters = %+v, want rxBytes=4293102934 txBytes=812934021", eth0)
	}
	if eth0.rxErrors != 3 || eth0.rxDropped != 145 || eth0.txDropped != 12 {
		t.Errorf("eth0 error/drop counters = %+v, want rxErrors=3 rxDropped=145 txDropped=12", eth0)
	}
}
