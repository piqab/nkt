package parse

import (
	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// Manifest reads the raw dpkg/os-release files internal/vuln needs to run a
// vulnerability scan — three plain file reads, no exec, cheap enough to call
// on demand (unlike Packages, this is never part of the regular scan
// snapshot: see model.PackageManifest's own doc comment on why). Debian/
// Ubuntu only, matching Packages — Available stays false on any host
// without /var/lib/dpkg/status, the same "not applicable, not an error"
// shape collect.Which-gated sources use elsewhere.
//
// /etc/debian_version is read even though /etc/os-release alone usually
// identifies the distro: trivy's own OS detector falls back to it when
// os-release parsing doesn't resolve a family (observed directly — a
// minimal /etc/os-release without it left trivy reporting family="none"
// and skipping OS-specific vulnerability matching entirely, silently
// returning zero findings instead of erroring).
func Manifest(c collect.Collector) model.PackageManifest {
	var m model.PackageManifest
	if !c.Exists("/var/lib/dpkg/status") {
		return m
	}
	dpkgStatus, err := c.ReadFile("/var/lib/dpkg/status")
	if err != nil {
		return m
	}
	m.Available = true
	m.DpkgStatus = string(dpkgStatus)

	if b, err := c.ReadFile("/etc/os-release"); err == nil {
		m.OSRelease = string(b)
	}
	if b, err := c.ReadFile("/etc/debian_version"); err == nil {
		m.DebianVersion = string(b)
	}
	return m
}
