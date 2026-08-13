package parse

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// upgradableRe matches one data line of `apt list --upgradable`:
//
//	packagename/jammy-updates 1.2.3-1ubuntu1 amd64 [upgradable from: 1.2.2-1]
var upgradableRe = regexp.MustCompile(`^(\S+)/\S+\s+(\S+)\s+\S+\s+\[upgradable from:\s*([^\]]+)\]`)

// Packages reports pending OS package updates and whether the host is
// already waiting on a reboot to pick one up (e.g. a new kernel) —
// Debian/Ubuntu (apt) only for now, matching where this project actually
// runs; a host without apt-get gets an unavailable SourceStatus rather
// than an error, the same way other optional sources (podman, libvirt)
// degrade when their tool isn't installed.
func Packages(ctx context.Context, c collect.Collector) (model.PackageUpdates, model.SourceStatus) {
	started := time.Now()
	status := model.SourceStatus{Name: "packages"}
	defer func() { status.DurationMS = time.Since(started).Milliseconds() }()

	var result model.PackageUpdates

	if !collect.Which(ctx, c, "apt-get") {
		return result, status // Available stays false — not an error, just N/A on this distro.
	}
	status.Available = true

	res, err := c.Run(ctx, "apt", "list", "--upgradable")
	if err != nil {
		status.Warnings = append(status.Warnings, "apt list --upgradable: "+err.Error())
		return result, status
	}
	if !res.OK() {
		status.Warnings = append(status.Warnings, "apt list --upgradable: "+strings.TrimSpace(res.Stderr))
		return result, status
	}

	for _, line := range strings.Split(res.Stdout, "\n") {
		m := upgradableRe.FindStringSubmatch(line)
		if m == nil {
			continue // "Listing..." header and blank lines.
		}
		result.Packages = append(result.Packages, model.PackageUpdate{
			Name:       m[1],
			NewVersion: m[2],
			OldVersion: strings.TrimSpace(m[3]),
		})
	}

	result.RebootRequired = c.Exists("/var/run/reboot-required")
	return result, status
}
