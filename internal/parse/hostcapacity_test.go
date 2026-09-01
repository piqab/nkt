package parse

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

func TestHostCapacityParsesFixture(t *testing.T) {
	result, status := HostCapacity(context.Background(), fixtureCollector(t))
	if !status.Available {
		t.Fatalf("status.Available = false, want true (fixture stubs /proc/meminfo and /proc/cpuinfo)")
	}
	if want := int64(16386420 * 1024); result.MemTotalBytes != want {
		t.Errorf("MemTotalBytes = %d, want %d", result.MemTotalBytes, want)
	}
	if result.CPUCores != 4 {
		t.Errorf("CPUCores = %d, want 4", result.CPUCores)
	}
}

// TestHostCapacityUnavailableWithoutProc guards the degrade-gracefully
// path: a host/container without a real /proc (or one where these files
// are hidden) must report Available=false, not an error — matching how
// every other optional source in this package behaves when its input
// simply isn't there.
func TestHostCapacityUnavailableWithoutProc(t *testing.T) {
	result, status := HostCapacity(context.Background(), collect.NewFixtures(t.TempDir()))
	if status.Available {
		t.Error("status.Available = true with no /proc files present, want false")
	}
	if result.MemTotalBytes != 0 || result.CPUCores != 0 {
		t.Errorf("result = %+v, want zero value", result)
	}
}

func TestParseMemTotalKB(t *testing.T) {
	t.Run("normal file", func(t *testing.T) {
		raw := []byte("MemTotal:       16386420 kB\nMemFree:         1234832 kB\n")
		kb, ok := parseMemTotalKB(raw)
		if !ok || kb != 16386420 {
			t.Errorf("parseMemTotalKB() = %d, %v, want 16386420, true", kb, ok)
		}
	})

	t.Run("no MemTotal line", func(t *testing.T) {
		if _, ok := parseMemTotalKB([]byte("MemFree: 100 kB\n")); ok {
			t.Error("parseMemTotalKB() ok = true with no MemTotal line, want false")
		}
	})

	t.Run("malformed value", func(t *testing.T) {
		if _, ok := parseMemTotalKB([]byte("MemTotal: notanumber kB\n")); ok {
			t.Error("parseMemTotalKB() ok = true for a malformed value, want false")
		}
	})
}

func TestCountCPUCores(t *testing.T) {
	raw := []byte("processor\t: 0\nvendor_id\t: GenuineIntel\n\nprocessor\t: 1\nvendor_id\t: GenuineIntel\n")
	if n := countCPUCores(raw); n != 2 {
		t.Errorf("countCPUCores() = %d, want 2", n)
	}
	if n := countCPUCores(nil); n != 0 {
		t.Errorf("countCPUCores(nil) = %d, want 0", n)
	}
}
