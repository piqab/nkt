package parse

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// HostCapacity reads the host's total installed memory and CPU core count
// straight from /proc — the reference numbers the "Использование сетевых
// ресурсов" page draws as a dashed ceiling on the CPU/memory charts, so a
// container using "400%" or "4 GiB" has something concrete to be read
// against instead of an arbitrary auto-scaled axis. Unavailable (rather
// than an error) when either file can't be read — a container or a
// restricted environment without a real /proc is not a broken host, just
// one this particular reference can't be computed for.
func HostCapacity(ctx context.Context, c collect.Collector) (model.HostCapacity, model.SourceStatus) {
	started := time.Now()
	status := model.SourceStatus{Name: "host_capacity"}
	defer func() { status.DurationMS = time.Since(started).Milliseconds() }()

	var capacity model.HostCapacity

	memRaw, err := c.ReadFile("/proc/meminfo")
	if err != nil {
		status.Warnings = append(status.Warnings, "/proc/meminfo: "+err.Error())
	} else if kb, ok := parseMemTotalKB(memRaw); ok {
		capacity.MemTotalBytes = kb * 1024
	} else {
		status.Warnings = append(status.Warnings, "/proc/meminfo: строка MemTotal не найдена")
	}

	cpuRaw, err := c.ReadFile("/proc/cpuinfo")
	if err != nil {
		status.Warnings = append(status.Warnings, "/proc/cpuinfo: "+err.Error())
	} else {
		capacity.CPUCores = countCPUCores(cpuRaw)
	}

	status.Available = capacity.MemTotalBytes > 0 || capacity.CPUCores > 0
	return capacity, status
}

// parseMemTotalKB extracts the value (in kB, as /proc/meminfo always
// reports it) of the "MemTotal:" line — the first line of the file on
// every Linux kernel version this project targets, but scanned line by
// line rather than assumed to be first, in case that ever changes.
func parseMemTotalKB(raw []byte) (int64, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb, true
	}
	return 0, false
}

// countCPUCores counts "processor\t: N" lines in /proc/cpuinfo — one per
// logical CPU (hyperthreads/SMT siblings included, matching what 100% of
// a single core means to `docker stats`' own cpu_pct: a host with 8
// logical CPUs tops out at 800%, not 400%, regardless of physical core
// count).
func countCPUCores(raw []byte) int {
	n := 0
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "processor") {
			n++
		}
	}
	return n
}
