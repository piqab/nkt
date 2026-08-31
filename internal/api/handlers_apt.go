package api

import (
	"context"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/msgs"
)

// aptPackageNameRe follows Debian policy's package-name charset — lowercase
// letters, digits, +, -, . — and must start with an alphanumeric. Gates
// every user-supplied package name before it reaches a shell string (see
// handleAptInstallWS/handleAptRemoveWS's "bash -c" invocation) — the
// arbitrary-input equivalent of commonPackageByName/parse.InstallTarget's
// fixed-set lookups, which is all that keeps a query parameter or path
// param out of a shell command unvalidated for the curated/service cases.
var aptPackageNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)

// dpkgQueryInstalled runs one dpkg-query for exactly the given package
// names and reports which are installed — same shape as
// installedCommonPackages, generalized to an arbitrary name list instead of
// the fixed commonPackages catalogue (used by handleAptSearch to
// cross-reference free-text results against what's actually on the host).
func dpkgQueryInstalled(ctx context.Context, c collect.Collector, packages []string) map[string]bool {
	if len(packages) == 0 {
		return map[string]bool{}
	}
	argv := append([]string{"-W", "-f=${Package} ${Status}\n"}, packages...)
	res, _ := c.Run(ctx, "dpkg-query", argv...)
	return parseDpkgQueryOutput(res.Stdout)
}

type aptSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
}

// aptSearchResultLimit bounds how many matches handleAptSearch returns to
// the browser — apt-cache search's own match count for a short/common
// query can run into the thousands on a fully populated apt cache.
const aptSearchResultLimit = 200

// handleAptSearch runs `apt-cache search <q>` — a full-text search over the
// host's own local apt package index/cache, matching name AND description,
// unlike commonPackages' small fixed allowlist. Read-only and available to
// any authenticated viewer, same as handleCommonPackagesStatus: no
// ModeFixtures gate, since it just runs through the scanner's ordinary
// collector — a fixtures snapshot with no real apt-cache on PATH simply
// returns no matches, the same as installedCommonPackages already does for
// dpkg-query.
func (s *Server) handleAptSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	// A query shorter than this matches an unhelpfully huge slice of any
	// real apt cache (tens of thousands of packages) for no benefit over
	// just asking the operator to type a bit more.
	if len(q) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"results": []aptSearchResult{}, "truncated": false})
		return
	}
	c := s.scanner.Collector()
	res, _ := c.Run(r.Context(), "apt-cache", "search", q)
	results := parseAptCacheSearch(res.Stdout)

	names := make([]string, len(results))
	for i, p := range results {
		names[i] = p.Name
	}
	installed := dpkgQueryInstalled(r.Context(), c, names)
	for i := range results {
		results[i].Installed = installed[results[i].Name]
	}

	truncated := len(results) > aptSearchResultLimit
	if truncated {
		results = results[:aptSearchResultLimit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "truncated": truncated})
}

// parseAptCacheSearch parses "apt-cache search"'s own output — one match
// per line, "<package> - <description>". Split on the FIRST " - " only:
// some real descriptions contain that exact separator again later in the
// text (e.g. "mtr-tiny - Full screen ping and traceroute tool - non-gui
// version"), so splitting on the last occurrence or a naive Split would
// truncate those descriptions. Results are sorted by name for a stable,
// predictable listing — apt-cache's own match order is not meaningfully
// relevance-ranked.
func parseAptCacheSearch(stdout string) []aptSearchResult {
	out := []aptSearchResult{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " - ", 2)
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		desc := ""
		if len(parts) == 2 {
			desc = strings.TrimSpace(parts[1])
		}
		out = append(out, aptSearchResult{Name: name, Description: desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type aptInstalledPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// handleAptInstalled lists every package dpkg currently considers
// installed on the host — not just commonPackages' curated catalogue.
// Deliberately omits ${Description}: dpkg's extended description field can
// span multiple lines (a synopsis plus a longer body), which would break
// this handler's own newline-per-record parsing; the frontend table only
// needs name/version to let an operator find and remove something.
func (s *Server) handleAptInstalled(w http.ResponseWriter, r *http.Request) {
	c := s.scanner.Collector()
	res, _ := c.Run(r.Context(), "dpkg-query", "-W", "-f=${Package}\t${Version}\t${Status}\n")
	writeJSON(w, http.StatusOK, map[string]any{"packages": parseDpkgQueryVersions(res.Stdout)})
}

func parseDpkgQueryVersions(stdout string) []aptInstalledPackage {
	out := []aptInstalledPackage{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			continue
		}
		status := strings.Fields(fields[2])
		if len(status) == 0 || status[len(status)-1] != "installed" {
			continue
		}
		out = append(out, aptInstalledPackage{Name: fields[0], Version: fields[1]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// handleAptInstallWS installs an arbitrary apt package by name — the
// free-text-search counterpart to handleCommonPackagesInstallWS's curated
// batch and handleServiceInstallWS's per-service install. {name} is
// validated against aptPackageNameRe rather than an allowlist, since any
// real apt package is a valid target here by design.
func (s *Server) handleAptInstallWS(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !aptPackageNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.invalidPackageName", name))
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get update && apt-get install -y "+name)
	}
	s.runUpdateSession(w, r, "apt-install:"+name, buildCmd, "packages.installApt", name, s.cfg.TerminalIdleTimeout)
}

func (s *Server) handleAptInstallStatus(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	active, finished, exitCode := s.sessionStatus("apt-install:" + name)
	writeSessionStatus(w, active, finished, exitCode)
}

// handleAptRemoveWS mirrors handleAptInstallWS with `apt-get remove -y` —
// no --purge, the same restraint handleCommonPackagesRemoveWS already
// applies, so config files are left in place.
func (s *Server) handleAptRemoveWS(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !aptPackageNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.invalidPackageName", name))
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get remove -y "+name)
	}
	s.runUpdateSession(w, r, "apt-remove:"+name, buildCmd, "packages.removeApt", name, s.cfg.TerminalIdleTimeout)
}

func (s *Server) handleAptRemoveStatus(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	active, finished, exitCode := s.sessionStatus("apt-remove:" + name)
	writeSessionStatus(w, active, finished, exitCode)
}

// handleAptUpdates reports pending OS package upgrades — the narrow slice
// of handleOverview's payload the Packages page actually needs (the
// pending-upgrade list, whether a reboot is already required, and whether
// the "packages" source could even be checked), without re-shipping the
// rest of the dashboard snapshot just for this.
func (s *Server) handleAptUpdates(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	available := false
	for _, src := range snap.Sources {
		if src.Name == "packages" {
			available = src.Available
			break
		}
	}
	packages := snap.Packages.Packages
	if packages == nil {
		packages = []model.PackageUpdate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available":       available,
		"packages":        packages,
		"reboot_required": snap.Packages.RebootRequired,
	})
}
