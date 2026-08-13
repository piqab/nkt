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
	out := renderEnv("admin", "s3cr3t-pw")

	for _, want := range []string{
		"NKT_MODE=local",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD=s3cr3t-pw",
		// The hub only ever reaches the remote nkt through a plain-HTTP SSH
		// tunnel — requiring HTTPS here would stop the remote from setting
		// the session cookie bootstrapLogin needs to capture.
		"NKT_COOKIE_SECURE=false",
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
	path, err := m.resolveGoBin(context.Background(), func(s string) { reports = append(reports, s) })
	if err != nil {
		t.Fatalf("resolveGoBin: %v", err)
	}
	if path == "" {
		t.Fatal("resolveGoBin returned an empty path")
	}
	for _, r := range reports {
		if strings.Contains(r, "Скачиваю") || strings.Contains(r, "устанавливаю") {
			t.Errorf("resolveGoBin attempted to install a toolchain when the existing go already works: %q", r)
		}
	}

	// Second call must reuse the cached result rather than re-resolving.
	path2, err := m.resolveGoBin(context.Background(), func(string) {})
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
	path, err := installGoToolchain(ctx, dir, func(s string) { events = append(events, s) })
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
	path2, err := installGoToolchain(ctx, dir, func(s string) { events = append(events, s) })
	if err != nil {
		t.Fatalf("installGoToolchain (second call): %v", err)
	}
	if path2 != path {
		t.Errorf("second installGoToolchain call returned a different path: %q vs %q", path2, path)
	}
	for _, e := range events {
		if strings.Contains(e, "Скачиваю") {
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
	discard := func(string) {}

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
