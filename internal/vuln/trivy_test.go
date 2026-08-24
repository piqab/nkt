package vuln

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/model"
)

// TestParseTrivyReport locks in the subset of trivy's own JSON schema Scan
// actually reads, using a canned report shaped exactly like real output
// captured from `trivy rootfs --format json` (see this package's own doc
// comment) — a schema drift in a future trivy release would otherwise only
// surface as silently-empty findings, never an error.
func TestParseTrivyReport(t *testing.T) {
	const report = `{
		"Results": [
			{
				"Target": "rootfs (debian 12.5)",
				"Class": "os-pkgs",
				"Type": "debian",
				"Vulnerabilities": [
					{
						"VulnerabilityID": "CVE-2024-12345",
						"PkgName": "openssl",
						"InstalledVersion": "3.0.11-1",
						"FixedVersion": "3.0.13-1",
						"Severity": "HIGH",
						"Title": "openssl: something bad",
						"PrimaryURL": "https://avd.aquasec.com/nvd/cve-2024-12345"
					},
					{
						"VulnerabilityID": "CVE-2023-99999",
						"PkgName": "bash",
						"InstalledVersion": "5.2.15-2",
						"Severity": "LOW"
					}
				]
			}
		]
	}`

	findings, err := parseTrivyReport([]byte(report))
	if err != nil {
		t.Fatalf("parseTrivyReport: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want 2", findings)
	}
	if findings[0].ID != "CVE-2024-12345" || findings[0].Package != "openssl" ||
		findings[0].FixedVersion != "3.0.13-1" || findings[0].Severity != "HIGH" {
		t.Errorf("findings[0] = %+v, unexpected parse", findings[0])
	}
	if findings[0].URL != "https://avd.aquasec.com/nvd/cve-2024-12345" {
		t.Errorf("findings[0].URL = %q, want trivy's own PrimaryURL carried through", findings[0].URL)
	}
	// No fix available yet must come through as an empty string, not a
	// literal "" the JSON omits some other way — this is what the UI uses
	// to tell "upgrade now" apart from "nothing to do yet".
	if findings[1].FixedVersion != "" {
		t.Errorf("findings[1].FixedVersion = %q, want empty (no fix published yet)", findings[1].FixedVersion)
	}
	// findings[1] has no PrimaryURL in the canned report at all (some
	// vendor-specific advisory IDs genuinely don't have one) — must come
	// through empty, not error or a guessed URL.
	if findings[1].URL != "" {
		t.Errorf("findings[1].URL = %q, want empty (no PrimaryURL in the source report)", findings[1].URL)
	}
}

// TestParseTrivyReportEmpty confirms a clean scan (no vulnerabilities)
// reports nil, not an error or an empty-but-non-nil slice that would render
// as an empty (rather than absent) findings section.
func TestParseTrivyReportEmpty(t *testing.T) {
	findings, err := parseTrivyReport([]byte(`{"Results":[{"Target":"rootfs","Vulnerabilities":null}]}`))
	if err != nil {
		t.Fatalf("parseTrivyReport: %v", err)
	}
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
}

// TestScanUnavailableManifest confirms Scan short-circuits (no trivy
// invocation at all) for a manifest that was never actually collected —
// the common case on a non-Debian host — rather than trying to run trivy
// against an empty rootfs and getting a confusing zero-findings result for
// the wrong reason.
func TestScanUnavailableManifest(t *testing.T) {
	findings, err := Scan(context.Background(), "/nonexistent/trivy", t.TempDir(), model.PackageManifest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
}

// TestScanImageArgs locks in the flags scanImageArgs builds — confirmed
// directly against a real trivy binary + local Docker daemon (see this
// package's own doc comment): --docker-host/--podman-host specifically
// need a "unix://" scheme prefix, a bare filesystem path fails with
// "unable to parse docker host", and --image-src must stay narrowed to
// docker,podman so a missing image fails outright instead of silently
// falling back to a registry pull.
func TestScanImageArgs(t *testing.T) {
	t.Run("both sockets set", func(t *testing.T) {
		args := scanImageArgs("/data/vuln/db", "nginx:1.25", "/var/run/docker.sock", "/run/podman/podman.sock")
		joined := " " + strings.Join(args, " ") + " "
		for _, want := range []string{
			" image ",
			" --cache-dir /data/vuln/db ",
			" --skip-db-update ",
			" --scanners vuln ",
			" --image-src docker,podman ",
			" --docker-host unix:///var/run/docker.sock ",
			" --podman-host unix:///run/podman/podman.sock ",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("scanImageArgs() missing %q in %v", strings.TrimSpace(want), args)
			}
		}
		if args[len(args)-1] != "nginx:1.25" {
			t.Errorf("scanImageArgs() target image ref not last: %v", args)
		}
	})

	t.Run("empty sockets omit their flags", func(t *testing.T) {
		args := scanImageArgs("/data/vuln/db", "alpine:3.18", "", "")
		joined := " " + strings.Join(args, " ") + " "
		if strings.Contains(joined, "--docker-host") || strings.Contains(joined, "--podman-host") {
			t.Errorf("scanImageArgs() with empty sockets still set a host flag: %v", args)
		}
	})
}

// TestExtractTrivyBinary builds a minimal in-memory tarball shaped like
// trivy's real release archive (a handful of unrelated files alongside the
// one that matters) and confirms only "trivy" gets extracted, executable.
func TestExtractTrivyBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct {
		name    string
		content string
	}{
		{"LICENSE", "..."},
		{"README.md", "..."},
		{"trivy", "fake trivy binary"},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e.content)),
		}); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if _, err := tw.Write([]byte(e.content)); err != nil {
			t.Fatalf("write content %s: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "trivy")
	if err := extractTrivyBinary(&buf, dest); err != nil {
		t.Fatalf("extractTrivyBinary: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if string(got) != "fake trivy binary" {
		t.Errorf("extracted content = %q", got)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Error("extracted trivy binary lost its executable bit")
	}
}

// TestDBDownloadedAt covers dbDownloadedAt's two real states: no metadata
// file yet (never downloaded) and a real trivy-shaped metadata.json.
func TestDBDownloadedAt(t *testing.T) {
	dir := t.TempDir()
	if _, ok := dbDownloadedAt(dir); ok {
		t.Error("dbDownloadedAt on an empty dir: ok = true, want false")
	}

	dbDir := filepath.Join(dir, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	meta := `{"Version":2,"DownloadedAt":"` + when.Format(time.RFC3339) + `"}`
	if err := os.WriteFile(filepath.Join(dbDir, "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := dbDownloadedAt(dir)
	if !ok {
		t.Fatal("dbDownloadedAt: ok = false, want true")
	}
	if !got.Equal(when) {
		t.Errorf("dbDownloadedAt = %v, want %v", got, when)
	}
}

// TestEnsureTrivyAndScanLive is the one test in this package that actually
// downloads trivy and a real ~1GB vulnerability DB and scans this very
// machine's own installed packages — everything else here is a fast, pure
// unit test. Skipped unless NKT_TEST_LIVE_VULN=1, matching
// internal/hub.TestSelfInstallGoToolchainLive's own precedent for a live,
// network-heavy test that shouldn't run on every `go test ./...`.
func TestEnsureTrivyAndScanLive(t *testing.T) {
	if os.Getenv("NKT_TEST_LIVE_VULN") != "1" {
		t.Skip("set NKT_TEST_LIVE_VULN=1 to run (downloads trivy + a ~1GB vulnerability DB)")
	}

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var events []string
	report := func(s string) { events = append(events, s) }

	trivyBin, err := EnsureTrivy(ctx, filepath.Join(dir, "bin"), report)
	if err != nil {
		t.Fatalf("EnsureTrivy: %v\nevents: %v", err, events)
	}
	dbDir := filepath.Join(dir, "db")
	if err := EnsureDB(ctx, trivyBin, dbDir, report); err != nil {
		t.Fatalf("EnsureDB: %v\nevents: %v", err, events)
	}

	manifest := model.PackageManifest{Available: true}
	if b, err := os.ReadFile("/var/lib/dpkg/status"); err == nil {
		manifest.DpkgStatus = string(b)
	} else {
		t.Skip("this test machine has no /var/lib/dpkg/status to scan")
	}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		manifest.OSRelease = string(b)
	}
	if b, err := os.ReadFile("/etc/debian_version"); err == nil {
		manifest.DebianVersion = string(b)
	}

	findings, err := Scan(ctx, trivyBin, dbDir, manifest)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	t.Logf("scanned this machine's own packages: %d findings", len(findings))
	if !DBUpdatedAt(dbDir).After(time.Now().Add(-time.Hour)) {
		t.Error("DBUpdatedAt: expected a very recent timestamp right after EnsureDB")
	}

	// Second EnsureDB call, same dir: must not re-download (DB is fresh).
	events = nil
	if err := EnsureDB(ctx, trivyBin, dbDir, report); err != nil {
		t.Fatalf("EnsureDB (cached): %v", err)
	}
	if len(events) != 0 {
		t.Errorf("EnsureDB re-downloaded a fresh DB: events = %v", events)
	}

	// Same trivy + already-warm DB, now against a real local Docker image
	// instead of the host's own packages — the part TestEnsureTrivyAndScanLive
	// itself doesn't cover.
	t.Run("ScanImage against a real local Docker image", func(t *testing.T) {
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("docker not on PATH in this environment")
		}
		const image = "alpine:3.18"
		pull := exec.CommandContext(ctx, "docker", "pull", image)
		if out, err := pull.CombinedOutput(); err != nil {
			t.Skipf("docker pull %s: %v: %s", image, err, out)
		}
		t.Cleanup(func() { _ = exec.Command("docker", "rmi", image).Run() })

		findings, err := ScanImage(ctx, trivyBin, dbDir, image, "/var/run/docker.sock", "")
		if err != nil {
			t.Fatalf("ScanImage: %v", err)
		}
		t.Logf("scanned local image %s: %d findings", image, len(findings))
	})
}
