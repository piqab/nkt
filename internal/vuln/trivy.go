// Package vuln runs OS-package vulnerability scans against a host's own
// installed-package manifest (see internal/parse.Manifest), backed by
// Aqua Security's trivy — self-installed on demand (mirroring
// internal/hub/toolchain.go's own pattern for the Go toolchain), not
// assumed to already be on the machine.
//
// Deliberately centralized rather than run on every scanned host: trivy's
// own vulnerability DB is roughly 1GB uncompressed (measured directly, far
// larger than the ~100MB compressed download suggests) — shipping that to
// every managed host on a recurring refresh schedule would be a real
// bandwidth/storage cost multiplied by however many hosts a hub manages.
// The manifest a scan actually needs (os-release, debian_version, dpkg's
// own package database) is a few hundred KB at most, so it travels instead:
// wherever Scan runs keeps the one DB copy, and callers bring the manifest
// to it rather than the other way around. In practice that means the DB
// lives on a standalone nkt scanning itself, or on a hub scanning its own
// managed hosts' manifests centrally — never on an individual managed host.
package vuln

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/model"
)

// dbMaxAge is how long a cached vulnerability DB is trusted before EnsureDB
// re-downloads it. Trivy's own upstream DB is rebuilt roughly every 6
// hours, but a dashboard checked at most a few times a day does not need
// anywhere near that freshness — a day-old DB still catches a CVE within
// a day of publication, and re-running the ~100MB download less often
// matters more for a host on a metered/slow link than shaving staleness
// from a day to an hour would gain.
const dbMaxAge = 24 * time.Hour

// trivyArch maps runtime.GOARCH to the suffix trivy's own GitHub release
// assets use (trivy_<version>_Linux-<suffix>.tar.gz) — the same three
// architectures nkt itself cross-compiles for for (see Makefile/
// internal/hub's own toolchain), so every target this project already
// supports has a matching trivy build.
func trivyArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "64bit", nil
	case "arm64":
		return "ARM64", nil
	case "arm":
		return "ARM", nil
	default:
		return "", fmt.Errorf("vuln: неподдерживаемая архитектура %s", runtime.GOARCH)
	}
}

// EnsureTrivy makes sure a trivy binary exists at <dir>/trivy, downloading
// the latest GitHub release if missing. Never reinstalled once present —
// trivy's own version does not need to track nkt's release cadence, only
// pick up occasional fixes; an operator who wants a newer build can delete
// the cached binary and let this fetch it again.
func EnsureTrivy(ctx context.Context, dir string, report func(string)) (string, error) {
	bin := filepath.Join(dir, "trivy")
	if info, err := os.Stat(bin); err == nil && !info.IsDir() {
		return bin, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("создание каталога %s: %w", dir, err)
	}

	arch, err := trivyArch()
	if err != nil {
		return "", err
	}
	report("Определяю последнюю версию trivy...")
	version, err := latestTrivyVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("определение версии trivy: %w", err)
	}
	url := fmt.Sprintf(
		"https://github.com/aquasecurity/trivy/releases/download/v%s/trivy_%s_Linux-%s.tar.gz",
		version, version, arch,
	)
	report(fmt.Sprintf("Скачиваю trivy %s...", version))
	body, err := fetchTarGz(ctx, url)
	if err != nil {
		return "", fmt.Errorf("скачивание trivy: %w", err)
	}
	defer body.Close()

	tmp := bin + ".download"
	if err := extractTrivyBinary(body, tmp); err != nil {
		return "", fmt.Errorf("распаковка trivy: %w", err)
	}
	if err := os.Rename(tmp, bin); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("установка trivy: %w", err)
	}
	report("trivy установлен")
	return bin, nil
}

// latestTrivyVersion asks GitHub's own API for trivy's latest release tag
// (e.g. "v0.74.0") and returns it without the leading "v".
func latestTrivyVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/aquasecurity/trivy/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("GitHub API вернул %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return strings.TrimPrefix(payload.TagName, "v"), nil
}

// fetchTarGz downloads url and hands back its body for extractTrivyBinary
// to stream from directly, without buffering the whole ~50MB archive in
// memory first.
func fetchTarGz(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s вернул %d: %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp.Body, nil
}

// extractTrivyBinary pulls just the "trivy" entry out of the release
// tarball and writes it to dest with the executable bit set — the archive
// also carries a LICENSE/README/report templates nkt has no use for, so
// this does not extract the whole thing the way installGoToolchain's
// extractTarGz does for a full toolchain tree.
func extractTrivyBinary(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("архив trivy не содержит файл %q", "trivy")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Name != "trivy" {
			continue
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(f, tr); err != nil { //nolint:gosec // trusted GitHub release, no size cap needed
			return err
		}
		return nil
	}
}

// EnsureDB makes sure dir has a trivy vulnerability DB no older than
// dbMaxAge, (re)downloading it via trivy's own --download-db-only when
// missing or stale. trivyBin is the path EnsureTrivy returned.
func EnsureDB(ctx context.Context, trivyBin, dir string, report func(string)) error {
	if downloadedAt, ok := dbDownloadedAt(dir); ok && time.Since(downloadedAt) < dbMaxAge {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("создание каталога %s: %w", dir, err)
	}
	report("Обновляю базу уязвимостей trivy (может занять несколько минут)...")
	cmd := exec.CommandContext(ctx, trivyBin, "image", "--cache-dir", dir, "--download-db-only", "--quiet")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("trivy --download-db-only: %w: %s", err, strings.TrimSpace(string(out)))
	}
	report("База уязвимостей обновлена")
	return nil
}

// dbDownloadedAt reads trivy's own db/metadata.json (written by trivy
// itself after every successful download) rather than relying on file
// mtimes — DownloadedAt is trivy's own authoritative answer to "how old is
// this DB", immune to anything else touching the cache directory's mtimes.
func dbDownloadedAt(dir string) (time.Time, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "db", "metadata.json"))
	if err != nil {
		return time.Time{}, false
	}
	var meta struct {
		DownloadedAt time.Time
	}
	if json.Unmarshal(b, &meta) != nil || meta.DownloadedAt.IsZero() {
		return time.Time{}, false
	}
	return meta.DownloadedAt, true
}

// DBUpdatedAt is dbDownloadedAt exposed for callers that just want to show
// "when was this data last refreshed" (model.VulnScan.DBUpdated) without
// triggering EnsureDB's own download-if-stale side effect.
func DBUpdatedAt(dir string) time.Time {
	t, _ := dbDownloadedAt(dir)
	return t
}

// Scan writes manifest's files into a throwaway directory shaped like a
// minimal root filesystem and runs `trivy rootfs` against it — the same
// OS-package detection trivy would do against a real machine, just fed a
// reconstructed manifest instead of a live filesystem, so scanning never
// needs write access to (or even a live connection to) the host the
// manifest came from. --skip-db-update is what actually makes this
// centralized: it makes trivy trust dbDir's contents completely rather
// than reaching out to refresh them itself.
func Scan(ctx context.Context, trivyBin, dbDir string, manifest model.PackageManifest) ([]model.VulnFinding, error) {
	if !manifest.Available {
		return nil, nil
	}

	root, err := os.MkdirTemp("", "nkt-vuln-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)

	if err := writeManifestRootfs(root, manifest); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, trivyBin, "rootfs",
		"--cache-dir", dbDir,
		"--skip-db-update",
		"--scanners", "vuln",
		"--format", "json",
		"--quiet",
		root,
	)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("trivy rootfs: %w: %s", err, stderr)
	}
	return parseTrivyReport(out)
}

// ScanImage runs `trivy image` against ref (e.g. "nginx:1.25" or
// "docker.io/library/nginx:1.25") as it already sits on THIS host's local
// Docker/Podman daemon, reached via dockerSocket/podmanSocket — the same
// socket paths internal/collect already uses (config.Config's own
// DockerSocket/PodmanSocket) — rather than pulled fresh. --image-src is
// narrowed to just docker,podman (trivy's default also tries containerd
// and a remote registry) so a typo'd or since-removed image reference
// fails outright instead of silently falling back to a slow, surprising
// registry pull for something this is specifically meant to avoid ever
// doing. Either socket argument may be empty (that engine isn't in use on
// this host) — trivy's own default path is still tried for that source in
// that case, matching how a bare `docker` CLI behaves with no DOCKER_HOST.
//
// Confirmed directly (not just from trivy's own docs) that --docker-host/
// --podman-host need a "unix://" scheme prefix, not a bare filesystem path
// — trivy's docker client rejects a bare path with "unable to parse
// docker host" otherwise.
func ScanImage(ctx context.Context, trivyBin, dbDir, ref, dockerSocket, podmanSocket string) ([]model.VulnFinding, error) {
	cmd := exec.CommandContext(ctx, trivyBin, scanImageArgs(dbDir, ref, dockerSocket, podmanSocket)...)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("trivy image %s: %w: %s", ref, err, stderr)
	}
	return parseTrivyReport(out)
}

// scanImageArgs builds ScanImage's trivy argv — split out as a pure
// function so the flags themselves are unit-testable without a real trivy
// binary or Docker daemon, matching internal/api/pty_session.go's own
// systemdRunArgs/nsenterArgs pattern for the same reason.
func scanImageArgs(dbDir, ref, dockerSocket, podmanSocket string) []string {
	args := []string{"image",
		"--cache-dir", dbDir,
		"--skip-db-update",
		"--scanners", "vuln",
		"--image-src", "docker,podman",
		"--format", "json",
		"--quiet",
	}
	if dockerSocket != "" {
		args = append(args, "--docker-host", "unix://"+dockerSocket)
	}
	if podmanSocket != "" {
		args = append(args, "--podman-host", "unix://"+podmanSocket)
	}
	return append(args, ref)
}

func writeManifestRootfs(root string, manifest model.PackageManifest) error {
	writes := []struct {
		rel     string
		content string
	}{
		{"var/lib/dpkg/status", manifest.DpkgStatus},
		{"etc/os-release", manifest.OSRelease},
		{"etc/debian_version", manifest.DebianVersion},
	}
	for _, w := range writes {
		if w.content == "" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(w.rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(w.content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// trivyReport is the slice of trivy's own JSON report format Scan actually
// reads — trivy's real schema carries much more (licenses, secrets,
// misconfigurations, per-layer detail) that a plain OS-package vulnerability
// scan never populates, so unmarshalling into a wider struct would just be
// dead fields.
type trivyReport struct {
	Results []struct {
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
			Title            string `json:"Title"`
			// PrimaryURL is trivy's own choice of the single most
			// authoritative reference for this exact finding — NVD,
			// a GitHub Security Advisory, a vendor bulletin, whichever
			// applies — rather than nkt guessing a URL pattern (e.g.
			// assuming NVD) that would be wrong for a vendor-specific ID
			// like "TEMP-..." that NVD never carries at all.
			PrimaryURL string `json:"PrimaryURL"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

func parseTrivyReport(out []byte) ([]model.VulnFinding, error) {
	var report trivyReport
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("разбор отчёта trivy: %w", err)
	}
	var findings []model.VulnFinding
	for _, res := range report.Results {
		for _, v := range res.Vulnerabilities {
			findings = append(findings, model.VulnFinding{
				ID:               v.VulnerabilityID,
				Package:          v.PkgName,
				InstalledVersion: v.InstalledVersion,
				FixedVersion:     v.FixedVersion,
				Severity:         v.Severity,
				Title:            v.Title,
				URL:              v.PrimaryURL,
			})
		}
	}
	return findings, nil
}
