package monitor

import (
	"testing"
	"time"
)

func TestParseHostProc(t *testing.T) {
	idle, total, ok := parseProcStat("cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 1 2 3 4\n")
	if !ok || idle != 850 || total != 1000 {
		t.Fatalf("stat: %v %v %v", idle, total, ok)
	}
	tot, avail, ok := parseMeminfo("MemTotal:       16000 kB\nMemFree:  1 kB\nMemAvailable:    4000 kB\n")
	if !ok || tot != 16000*1024 || avail != 4000*1024 {
		t.Fatalf("meminfo: %v %v", tot, avail)
	}
	rows := parseDF("Mounted on Type 1B-blocks Used\n/ ext4 1000 600\n/run tmpfs 10 1\n/boot vfat 100 30\n/snap/core squashfs 5 5\n/ ext4 1000 600\n/dev/shm tmpfs 1 0\n")
	if len(rows) != 2 || rows[0].mount != "/" || rows[0].used != 600 || rows[1].mount != "/boot" {
		t.Fatalf("df: %+v", rows)
	}
}

// Демо-диск «/» растёт: через 14 дней процент выше, чем в начале.
func TestDemoHostDiskGrows(t *testing.T) {
	val := func(ts time.Time) float64 {
		for _, s := range demoHostSamples("x", ts) {
			if s.Source == SourceDisk && s.Subject == "/" && s.Metric == "used_bytes" {
				return s.Value
			}
		}
		return -1
	}
	if old, now := val(time.Now().Add(-14*24*time.Hour)), val(time.Now()); !(now > old) || old <= 0 {
		t.Fatalf("disk: %v → %v", old, now)
	}
}
