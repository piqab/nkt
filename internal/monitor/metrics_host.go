package monitor

import (
	"context"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// Ряды загрузки самого хоста — для «Мониторинга» хаба и прогнозов:
// CPU, память, нагрузка (source "host", subject "host") и заполнение
// каждой файловой системы (source "disk", subject — точка монтирования).

// Источники рядов хоста.
const (
	SourceHost = "host"
	SourceDisk = "disk"
)

// HostSubject — subject рядов CPU, памяти и нагрузки.
const HostSubject = "host"

// cpuState — прошлое чтение /proc/stat (CPU — по разнице двух чтений).
type cpuState struct {
	mu          sync.Mutex
	idle, total float64
	ok          bool
}

func (m *MetricsCollector) hostSamples(ctx context.Context, ts string, now time.Time) ([]store.MetricSample, error) {
	if m.cfg.IsFixtures() {
		return demoHostSamples(ts, now), nil
	}
	var out []store.MetricSample
	if raw, err := m.c.ReadFile("/proc/stat"); err == nil {
		if idle, total, ok := parseProcStat(string(raw)); ok {
			m.cpu.mu.Lock()
			if m.cpu.ok && total > m.cpu.total {
				busy := 1 - (idle-m.cpu.idle)/(total-m.cpu.total)
				out = append(out, sample(ts, SourceHost, HostSubject, "cpu_pct", math.Round(math.Max(0, busy)*1000)/10))
			}
			m.cpu.idle, m.cpu.total, m.cpu.ok = idle, total, true
			m.cpu.mu.Unlock()
		}
	}
	if raw, err := m.c.ReadFile("/proc/meminfo"); err == nil {
		if total, avail, ok := parseMeminfo(string(raw)); ok {
			out = append(out, sample(ts, SourceHost, HostSubject, "mem_used_bytes", total-avail),
				sample(ts, SourceHost, HostSubject, "mem_total_bytes", total))
		}
	}
	if raw, err := m.c.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(raw)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				out = append(out, sample(ts, SourceHost, HostSubject, "load1", v))
			}
		}
	}
	if res, err := m.c.Run(ctx, "df", "-B1", "--output=target,fstype,size,used"); err == nil && res.OK() {
		for _, d := range parseDF(res.Stdout) {
			out = append(out, sample(ts, SourceDisk, d.mount, "used_bytes", d.used), sample(ts, SourceDisk, d.mount, "size_bytes", d.size))
		}
	}
	return out, nil
}

// parseProcStat — простой и общий счётчики первой строки cpu.
func parseProcStat(s string) (idle, total float64, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		for i, v := range f[1:] {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, 0, false
			}
			// user nice system idle iowait irq softirq steal (guest уже в user).
			if i >= 8 {
				break
			}
			total += n
			if i == 3 || i == 4 {
				idle += n
			}
		}
		return idle, total, total > 0
	}
	return 0, 0, false
}

// parseMeminfo — MemTotal и MemAvailable в байтах.
func parseMeminfo(s string) (total, avail float64, ok bool) {
	var haveT, haveA bool
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, haveT = v*1024, true
		case "MemAvailable:":
			avail, haveA = v*1024, true
		}
	}
	return total, avail, haveT && haveA
}

type dfRow struct {
	mount      string
	size, used float64
}

// skipFS — служебные и виртуальные файловые системы: их заполнение
// ничего не говорит о месте на диске.
var skipFS = map[string]bool{"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true, "efivarfs": true,
	"ramfs": true, "proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "devpts": true, "nsfs": true, "fuse.lxcfs": true}

func parseDF(out string) []dfRow {
	var rows []dfRow
	seen := map[string]bool{}
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 4 {
			continue
		}
		mount, fstype := f[0], f[1]
		if skipFS[fstype] || strings.HasPrefix(mount, "/snap/") || strings.HasPrefix(mount, "/run") ||
			strings.HasPrefix(mount, "/sys") || strings.HasPrefix(mount, "/proc") || strings.HasPrefix(mount, "/dev") || seen[mount] {
			continue
		}
		size, err1 := strconv.ParseFloat(f[2], 64)
		used, err2 := strconv.ParseFloat(f[3], 64)
		if err1 != nil || err2 != nil || size <= 0 {
			continue
		}
		seen[mount] = true
		rows = append(rows, dfRow{mount: mount, size: size, used: used})
	}
	return rows
}

// Демо (fixtures): правдоподобные ряды хоста. Диск «/» медленно
// заполняется, чтобы прогноз на «Мониторинге» было что показать.
const demoMemTotal = 16 << 30

var demoDisks = []struct {
	mount     string
	size      float64
	startPct  float64
	perDayPct float64
}{
	{"/", 100 << 30, 58, 1.1},
	{"/var/lib/docker", 400 << 30, 41, 0.35},
	{"/boot", 1 << 30, 31, 0},
}

func demoHostSamples(ts string, now time.Time) []store.MetricSample {
	s := dailyShape(now, "host")
	out := []store.MetricSample{
		sample(ts, SourceHost, HostSubject, "cpu_pct", math.Round((8+s*45)*10)/10),
		sample(ts, SourceHost, HostSubject, "mem_used_bytes", demoMemTotal*(0.55+0.15*s)),
		sample(ts, SourceHost, HostSubject, "mem_total_bytes", demoMemTotal),
		sample(ts, SourceHost, HostSubject, "load1", math.Round((0.3+s*2.4)*100)/100),
	}
	// Рост считается от «14 дней назад»: демо-диск «/» всегда примерно за
	// три недели до заполнения, сколько бы ни работал стенд.
	elapsed := now.Sub(time.Now().Add(-14*24*time.Hour)).Hours() / 24
	for _, d := range demoDisks {
		pct := d.startPct + d.perDayPct*elapsed + s*0.3
		out = append(out, sample(ts, SourceDisk, d.mount, "used_bytes", math.Round(d.size*math.Min(pct, 99)/100)),
			sample(ts, SourceDisk, d.mount, "size_bytes", d.size))
	}
	return out
}
