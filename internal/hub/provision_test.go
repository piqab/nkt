package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderEnvContainsExpectedLines(t *testing.T) {
	out := renderEnv("admin", "s3cr3t-pw", false, "", tunnelEnvParams{})

	for _, want := range []string{
		"NKT_MODE=local",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD=s3cr3t-pw",
		// The hub only ever reaches the remote nkt through a plain-HTTP SSH
		// tunnel — requiring HTTPS here would stop the remote from setting
		// the session cookie bootstrapLogin needs to capture.
		"NKT_COOKIE_SECURE=false",
		"NKT_TERMINAL_ENABLED=false",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderEnv output missing %q, got:\n%s", want, out)
		}
	}
	for _, absent := range []string{"NKT_HUB_TUNNEL_LISTEN_ADDR", "NKT_HUB_TUNNEL_TOKEN"} {
		if strings.Contains(out, absent) {
			t.Errorf("renderEnv output with a zero-value tunnelEnvParams contains %q, want none of the NKT_HUB_TUNNEL_* vars", absent)
		}
	}
}

// TestRenderEnvPassesThroughTerminalEnabled covers the fix for a real
// limitation: a hub-managed host's nkt.env is regenerated from scratch by
// stageFiles on every install AND every "переустановить"/"обновить" — an
// operator editing NKT_TERMINAL_ENABLED by hand directly on the host would
// have it silently overwritten the next time the hub touches that host.
// The per-host store.Host.TerminalEnabled flag (see Manager.install) is
// what actually has to survive that regeneration, so this asserts renderEnv
// itself faithfully reflects whatever it is told, not a hardcoded default.
func TestRenderEnvPassesThroughTerminalEnabled(t *testing.T) {
	out := renderEnv("admin", "s3cr3t-pw", true, "", tunnelEnvParams{})
	if !strings.Contains(out, "NKT_TERMINAL_ENABLED=true") {
		t.Errorf("renderEnv(..., true) output missing NKT_TERMINAL_ENABLED=true, got:\n%s", out)
	}
}

// TestRenderEnvWritesTerminalUserForNonRootSSHUser covers handleTerminalWS's
// privilege-drop path (internal/api/handlers_terminal.go): renderEnv must
// write NKT_TERMINAL_USER for a managed host's ssh_user so the remote nkt
// process, which has no other way to learn what account the hub connects
// with, knows what to setuid the terminal shell to.
func TestRenderEnvWritesTerminalUserForNonRootSSHUser(t *testing.T) {
	out := renderEnv("admin", "s3cr3t-pw", true, "deploy", tunnelEnvParams{})
	if !strings.Contains(out, "NKT_TERMINAL_USER=deploy") {
		t.Errorf("renderEnv(..., \"deploy\", ...) output missing NKT_TERMINAL_USER=deploy, got:\n%s", out)
	}
}

// TestRenderEnvOmitsTerminalUserForRootOrEmpty covers the two cases where
// there is no privilege to drop: an empty ssh_user (standalone/no-hub
// deployments, where TerminalUser being unset means "run as root exactly as
// before") and ssh_user=root (dropping to root would be a pointless
// self-setuid).
func TestRenderEnvOmitsTerminalUserForRootOrEmpty(t *testing.T) {
	for _, sshUser := range []string{"", "root"} {
		out := renderEnv("admin", "s3cr3t-pw", true, sshUser, tunnelEnvParams{})
		if strings.Contains(out, "NKT_TERMINAL_USER") {
			t.Errorf("renderEnv(..., %q, ...) output unexpectedly contains NKT_TERMINAL_USER, got:\n%s", sshUser, out)
		}
	}
}

// TestRenderEnvWritesTunnelVarsWhenEnabled covers the reverse-tunnel
// fallback's own env vars — written only when tunnelEnvParams.Enabled is
// set (see Manager.prepareTunnelEnv).
func TestRenderEnvWritesTunnelVarsWhenEnabled(t *testing.T) {
	out := renderEnv("admin", "s3cr3t-pw", false, "", tunnelEnvParams{
		Enabled:    true,
		ListenAddr: "0.0.0.0:8078",
		Token:      "the-token",
	})
	for _, want := range []string{
		"NKT_HUB_TUNNEL_LISTEN_ADDR=0.0.0.0:8078",
		"NKT_HUB_TUNNEL_TOKEN=the-token",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderEnv output missing %q, got:\n%s", want, out)
		}
	}
}

// TestResolveGoBinUsesWorkingGoWithoutInstalling covers the common case —
// Docker's golang:1.26-alpine image, or any machine with a plain (non-snap)
// Go on PATH — where resolveGoBin must return immediately, never attempting
// the network-touching self-install path. It relies on the `go` that is
// itself running this test suite, so it needs no network and stays fast.
func TestResolveGoBinUsesWorkingGoWithoutInstalling(t *testing.T) {
	m, _ := newTestManager(t)
	m.cfg.HubGoBin = "go"

	var reports []string
	path, err := m.resolveGoBin(context.Background(), func(key string, args ...any) { reports = append(reports, key) })
	if err != nil {
		t.Fatalf("resolveGoBin: %v", err)
	}
	if path == "" {
		t.Fatal("resolveGoBin returned an empty path")
	}
	for _, r := range reports {
		if r == "hub.downloadingGo" || r == "hub.goNotWorkingInstalling" {
			t.Errorf("resolveGoBin attempted to install a toolchain when the existing go already works: %q", r)
		}
	}

	// Second call must reuse the cached result rather than re-resolving.
	path2, err := m.resolveGoBin(context.Background(), func(string, ...any) {})
	if err != nil {
		t.Fatalf("resolveGoBin (cached): %v", err)
	}
	if path2 != path {
		t.Errorf("resolveGoBin did not cache its result: %q then %q", path, path2)
	}
}

func TestDiagnoseInstallError(t *testing.T) {
	baseErr := errors.New("exit status 1")
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"needs password", "sudo: a password is required", "NOPASSWD"},
		{"sorry a password", "sudo: sorry, a password is required to run sudo", "NOPASSWD"},
		{"not in sudoers", "user is not in the sudoers file. This incident will be reported.", "sudoers"},
		{"plain permission denied", "install: cannot create regular file '/usr/local/bin/nkt': Permission denied", "sudo"},
		{"unrecognised output", "some other failure entirely", "unrecognised output"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := diagnoseInstallError("deploy", "/usr/local/bin/nkt", baseErr, c.out)
			if err == nil {
				t.Fatal("diagnoseInstallError returned nil")
			}
			if c.name == "unrecognised output" {
				// Falls through to the default case — must still carry the
				// original error and raw output, just without a canned hint.
				if !strings.Contains(err.Error(), c.out) {
					t.Errorf("error %q does not include the raw command output", err.Error())
				}
				return
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err.Error(), c.want)
			}
		})
	}
}

// TestSelfInstallGoToolchainLive actually downloads Go from go.dev and
// proves the result runs — the one thing the other, hermetic tests here
// cannot verify. Skipped unless NKT_TEST_LIVE_GO_INSTALL=1, since it needs
// real network access and pulls ~100MB; run it by hand after touching
// installGoToolchain/extractTarGz/fetchLatestGoVersion.
func TestSelfInstallGoToolchainLive(t *testing.T) {
	if os.Getenv("NKT_TEST_LIVE_GO_INSTALL") != "1" {
		t.Skip("set NKT_TEST_LIVE_GO_INSTALL=1 to run (downloads real Go from go.dev)")
	}

	dir := filepath.Join(t.TempDir(), "go-toolchain")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var events []string
	path, err := installGoToolchain(ctx, dir, func(key string, args ...any) { events = append(events, key) })
	if err != nil {
		t.Fatalf("installGoToolchain: %v\nevents: %v", err, events)
	}
	t.Logf("installed at %s, events: %v", path, events)

	if !goWorks(ctx, path) {
		t.Fatalf("installed go at %s does not pass goWorks", path)
	}

	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		t.Fatalf("go version: %v", err)
	}
	t.Logf("go version: %s", strings.TrimSpace(string(out)))

	// A second call must reuse the already-installed toolchain rather than
	// downloading again.
	events = nil
	path2, err := installGoToolchain(ctx, dir, func(key string, args ...any) { events = append(events, key) })
	if err != nil {
		t.Fatalf("installGoToolchain (second call): %v", err)
	}
	if path2 != path {
		t.Errorf("second installGoToolchain call returned a different path: %q vs %q", path2, path)
	}
	for _, e := range events {
		if e == "hub.downloadingGo" {
			t.Errorf("second installGoToolchain call re-downloaded instead of reusing the install: %q", e)
		}
	}
}

func TestSafeJoinRejectsEscapes(t *testing.T) {
	root := "/tmp/nkt-toolchain-test"
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"go/bin/go", false},
		{"go/pkg/mod/x.txt", false},
		{"../../etc/passwd", true},
		{"/etc/passwd", true},
		{"a/../../../etc/passwd", true},
	}
	for _, c := range cases {
		got, err := safeJoin(root, c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("safeJoin(%q): expected an error, got %q", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("safeJoin(%q): unexpected error: %v", c.name, err)
		}
	}
}

func TestExtractTarGz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	writeEntry := func(hdr *tar.Header, content string) {
		hdr.Size = int64(len(content))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", hdr.Name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write content %s: %v", hdr.Name, err)
		}
	}

	if err := tw.WriteHeader(&tar.Header{Name: "go/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "go/bin/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	writeEntry(&tar.Header{Name: "go/bin/go", Typeflag: tar.TypeReg, Mode: 0o755}, "fake go binary")
	writeEntry(&tar.Header{Name: "go/VERSION", Typeflag: tar.TypeReg, Mode: 0o644}, "go1.99.0")
	if err := tw.WriteHeader(&tar.Header{
		Name: "go/bin/gofmt", Typeflag: tar.TypeSymlink, Linkname: "go",
	}); err != nil {
		t.Fatalf("write symlink header: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "go-toolchain")
	if err := extractTarGz(&buf, dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	gotBin, err := os.ReadFile(filepath.Join(dest, "go", "bin", "go"))
	if err != nil {
		t.Fatalf("read extracted go binary: %v", err)
	}
	if string(gotBin) != "fake go binary" {
		t.Errorf("extracted go binary content = %q", gotBin)
	}
	if info, err := os.Stat(filepath.Join(dest, "go", "bin", "go")); err != nil {
		t.Fatalf("stat extracted go binary: %v", err)
	} else if info.Mode().Perm()&0o100 == 0 {
		t.Error("extracted go binary lost its executable bit")
	}

	link, err := os.Readlink(filepath.Join(dest, "go", "bin", "gofmt"))
	if err != nil {
		t.Fatalf("readlink gofmt: %v", err)
	}
	if link != "go" {
		t.Errorf("gofmt symlink target = %q, want %q", link, "go")
	}
}

func TestGeneratePasswordIsRandomAndLong(t *testing.T) {
	a, err := generatePassword()
	if err != nil {
		t.Fatalf("generatePassword: %v", err)
	}
	b, err := generatePassword()
	if err != nil {
		t.Fatalf("generatePassword: %v", err)
	}
	if a == b {
		t.Fatal("two calls to generatePassword produced the same value")
	}
	if len(a) < 20 {
		t.Fatalf("generatePassword produced a suspiciously short value: %q", a)
	}
}

// TestResolveSourceRoot covers the failure the configured-source-root
// default actually produces in the field: `nkt hub` launched from a
// directory that is not the checkout (its wd then has no go.mod), which
// used to surface as go's own bare "go.mod file not found" with no hint
// that NKT_HUB_SOURCE_ROOT exists.
func TestResolveSourceRoot(t *testing.T) {
	discard := func(string, ...any) {}

	t.Run("configured root with go.mod wins", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m, _ := newTestManager(t)
		m.cfg.HubSourceRoot = dir
		got, err := m.resolveSourceRoot(discard)
		if err != nil {
			t.Fatalf("resolveSourceRoot: %v", err)
		}
		if got != dir {
			t.Errorf("resolveSourceRoot = %q, want configured %q", got, dir)
		}
	})

	t.Run("no go.mod anywhere gives actionable error", func(t *testing.T) {
		// The test executable lives in a temp build dir with no go.mod
		// anywhere above it, and the configured root has none either —
		// this must fail with the actionable message, not go's own.
		m, _ := newTestManager(t)
		m.cfg.HubSourceRoot = t.TempDir()
		_, err := m.resolveSourceRoot(discard)
		if err == nil {
			t.Fatal("expected an error for a source root without go.mod")
		}
		for _, want := range []string{"go.mod", "NKT_HUB_SOURCE_ROOT", m.cfg.HubSourceRoot} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}

// TestProgressReaderExposesSize guards against a real regression: wrapping
// uploadFile's local *os.File in progressReader for upload-progress
// reporting hid Stat() (and every other size hint sftp.File.ReadFrom
// checks for) from it, which made ReadFrom fall back to sending one
// SFTP request at a time and waiting for the reply before the next —
// dozens of times slower than its concurrent-request fast path over any
// real latency, even though raw throughput to the host was fine. Size()
// is the fix: it's one of the interfaces ReadFrom's own type switch
// checks for. This doesn't exercise ReadFrom itself (that needs a real
// SFTP server, covered by TestSSHProvisioningRoundTrip), just the one
// contract progressReader must keep satisfying.
func TestProgressReaderExposesSize(t *testing.T) {
	pr := &progressReader{total: 12345, report: func(string, ...any) {}}
	var sized interface{ Size() int64 } = pr
	if got := sized.Size(); got != 12345 {
		t.Errorf("progressReader.Size() = %d, want 12345", got)
	}
}
