package api

import (
	"context"
	"net/http"
	"os/exec"
	"strings"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/msgs"
)

// commonPackage is one entry in the Overview page's "common packages"
// panel — a deliberately small, curated set of everyday CLI tools
// (contrast with a generic apt search/install surface, which this is not),
// so an operator recognizes every name at a glance rather than hunting
// through a package index. Descriptions are static UI copy, not
// dynamic/error text — they live in the frontend's own i18n catalog,
// keyed by Name, not here.
type commonPackage struct {
	Name    string // logical name — what the API and frontend both key on
	Package string // actual apt package name, when it differs from Name
}

// commonPackages: Package differs from Name exactly where the everyday
// tool name isn't the real apt package — nvim's package is neovim, gpg's
// is gnupg, mtr's is mtr-tiny (Debian/Ubuntu ship the statically linked
// build under that name), and "ssh" here means the *server*
// (openssh-server, so the host can accept incoming SSH), not the client
// (openssh-client, almost always already present since it's what this very
// connection came in on).
var commonPackages = []commonPackage{
	{Name: "nvim", Package: "neovim"},
	{Name: "tmux", Package: "tmux"},
	{Name: "gpg", Package: "gnupg"},
	{Name: "curl", Package: "curl"},
	{Name: "ssh", Package: "openssh-server"},
	{Name: "git", Package: "git"},
	{Name: "wget", Package: "wget"},
	{Name: "htop", Package: "htop"},
	{Name: "ncdu", Package: "ncdu"},
	{Name: "tree", Package: "tree"},
	{Name: "unzip", Package: "unzip"},
	{Name: "rsync", Package: "rsync"},
	{Name: "jq", Package: "jq"},
	{Name: "net-tools", Package: "net-tools"},
	{Name: "mtr", Package: "mtr-tiny"},
	{Name: "tcpdump", Package: "tcpdump"},
	{Name: "python3", Package: "python3"},
}

func commonPackageByName(name string) (commonPackage, bool) {
	for _, p := range commonPackages {
		if p.Name == name {
			return p, true
		}
	}
	return commonPackage{}, false
}

// handleCommonPackagesStatus reports installed/not for every package in
// commonPackages, in one dpkg-query call rather than one exec per package.
func (s *Server) handleCommonPackagesStatus(w http.ResponseWriter, r *http.Request) {
	installed := installedCommonPackages(r.Context(), s.scanner.Collector())
	out := make([]map[string]any, 0, len(commonPackages))
	for _, p := range commonPackages {
		out = append(out, map[string]any{
			"name":      p.Name,
			"installed": installed[p.Package],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": out})
}

// installedCommonPackages runs one dpkg-query for the whole catalogue at
// once. dpkg-query -W keeps going and reports what it does find even when
// some of the names given aren't installed, or dpkg has never heard of
// them at all — a package missing from the output entirely (rather than
// erroring the whole call) is exactly what "not installed" looks like
// here, so nothing needs an explicit not-found branch.
func installedCommonPackages(ctx context.Context, c collect.Collector) map[string]bool {
	argv := make([]string, 0, len(commonPackages)+2)
	argv = append(argv, "-W", "-f=${Package} ${Status}\n")
	for _, p := range commonPackages {
		argv = append(argv, p.Package)
	}
	res, _ := c.Run(ctx, "dpkg-query", argv...)
	return parseDpkgQueryOutput(res.Stdout)
}

// parseDpkgQueryOutput is installedCommonPackages' own parsing, pulled out
// as a pure function so it's testable against canned dpkg-query output
// without needing a real dpkg on the test machine.
func parseDpkgQueryOutput(stdout string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		// "${Package} ${Status}" is "<name> <want> <flag> <status>" — 4
		// space-separated fields, the last one being the actual status
		// word ("installed", "not-installed", "config-files", ...).
		// Comparing the last field, not a substring match, matters
		// specifically because "not-installed" itself ends in the
		// characters "installed".
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		out[fields[0]] = fields[len(fields)-1] == "installed"
	}
	return out
}

// parsePackageNames validates a comma-separated ?pkgs= query value against
// commonPackages, translating each logical name to its real apt package —
// same allowlist role as parse.InstallTarget plays for handleServiceInstallWS,
// keeping a URL query parameter from ever reaching a shell command
// unvalidated.
func parsePackageNames(raw string) (pkgs []string, unknown string, ok bool) {
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		p, found := commonPackageByName(name)
		if !found {
			return nil, name, false
		}
		pkgs = append(pkgs, p.Package)
	}
	return pkgs, "", true
}

// handleCommonPackagesInstallWS runs `apt-get install -y <selected...>` for
// every package named in ?pkgs= (comma-separated logical names, e.g.
// "nvim,tmux,git") — one apt-get invocation for the whole selection, not
// one session per package, so "select several, install" is a single
// progress stream rather than N of them racing each other under the same
// package-manager lock.
func (s *Server) handleCommonPackagesInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	pkgs, unknown, ok := parsePackageNames(r.URL.Query().Get("pkgs"))
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.unknownPackage", unknown))
		return
	}
	if len(pkgs) == 0 {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.noPackagesSelected"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get update && apt-get install -y "+strings.Join(pkgs, " "))
	}
	s.runUpdateSession(w, r, "packages-install", buildCmd, "packages.installCommon", strings.Join(pkgs, ", "), s.cfg.TerminalIdleTimeout)
}

func (s *Server) handleCommonPackagesInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("packages-install")
	writeSessionStatus(w, active, finished, exitCode)
}

// handleCommonPackagesRemoveWS mirrors handleCommonPackagesInstallWS with
// `apt-get remove -y` — no --purge: config files are left in place, the
// same restraint apt's own plain `remove` (vs `purge`) already defaults
// to, so re-installing later doesn't start from scratch.
func (s *Server) handleCommonPackagesRemoveWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	pkgs, unknown, ok := parsePackageNames(r.URL.Query().Get("pkgs"))
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.unknownPackage", unknown))
		return
	}
	if len(pkgs) == 0 {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.noPackagesSelected"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get remove -y "+strings.Join(pkgs, " "))
	}
	s.runUpdateSession(w, r, "packages-remove", buildCmd, "packages.removeCommon", strings.Join(pkgs, ", "), s.cfg.TerminalIdleTimeout)
}

func (s *Server) handleCommonPackagesRemoveStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("packages-remove")
	writeSessionStatus(w, active, finished, exitCode)
}
