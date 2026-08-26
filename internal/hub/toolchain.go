package hub

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// goVersionTimeout bounds a `go version` probe — it must be near-instant on
// a working toolchain, so a hang here (e.g. a broken snap shim waiting on
// something that will never come back) cannot stall an install indefinitely.
const goVersionTimeout = 10 * time.Second

// goWorks reports whether path is a `go` binary that actually runs in the
// current process's environment. Run from inside the hub's own process
// (rather than shelled out separately), so under a hardened systemd unit
// this fails exactly the way the real cross-compile later would — most
// notably for a snap-packaged Go, whose confinement (snap-confine, its own
// mount namespace) NoNewPrivileges=yes/RestrictNamespaces=yes in
// deploy/netknownsthat-hub.service deliberately block.
func goWorks(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, goVersionTimeout)
	defer cancel()
	return exec.CommandContext(ctx, path, "version").Run() == nil
}

// resolveGoBin returns a `go` binary the hub can actually invoke to
// cross-compile nkt, self-installing one under HubGoToolchainDir when
// whatever NKT_HUB_GO_BIN names doesn't work — mirroring what `make
// native-build` already does for building nkt itself: an operator should
// never have to fight PATH, install Go by hand, or work around a
// snap-confined one, on this machine any more than on their own.
// Resolved once and cached for the Manager's lifetime.
func (m *Manager) resolveGoBin(ctx context.Context, report func(key string, args ...any)) (string, error) {
	m.goBinMu.Lock()
	defer m.goBinMu.Unlock()
	if m.resolvedGoBin != "" {
		return m.resolvedGoBin, nil
	}

	if path, err := exec.LookPath(m.cfg.HubGoBin); err == nil && goWorks(ctx, path) {
		m.resolvedGoBin = path
		return path, nil
	}

	report("hub.goNotWorkingInstalling", m.cfg.HubGoBin)
	path, err := installGoToolchain(ctx, m.cfg.HubGoToolchainDir(), report)
	if err != nil {
		return "", fmt.Errorf(
			"go (%s) не запускается, а автоустановка своего Go не удалась: %w", m.cfg.HubGoBin, err)
	}
	m.resolvedGoBin = path
	return path, nil
}

// installGoToolchain downloads and unpacks the latest Go release from
// go.dev into dir (reusing an already-installed one if it already works),
// returning the path to its go binary.
func installGoToolchain(ctx context.Context, dir string, report func(key string, args ...any)) (string, error) {
	binPath := filepath.Join(dir, "go", "bin", "go")
	if goWorks(ctx, binPath) {
		report("hub.usingCachedGo", binPath)
		return binPath, nil
	}

	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("автоустановка Go поддерживается только на Linux (хаб работает на %s) — "+
			"задайте NKT_HUB_GO_BIN вручную", runtime.GOOS)
	}
	arch := goArchForRuntime()
	if arch == "" {
		return "", fmt.Errorf("автоустановка Go не поддерживает архитектуру хаба %s — задайте NKT_HUB_GO_BIN вручную",
			runtime.GOARCH)
	}

	version, err := fetchLatestGoVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("определение последней версии Go: %w", err)
	}
	report("hub.downloadingGo", version, arch)

	url := fmt.Sprintf("https://go.dev/dl/%s.linux-%s.tar.gz", version, arch)
	body, err := fetchTarGz(ctx, url)
	if err != nil {
		return "", err
	}
	defer body.Close()

	if err := extractTarGz(body, dir); err != nil {
		return "", fmt.Errorf("распаковка %s: %w", url, err)
	}

	if !goWorks(ctx, binPath) {
		return "", fmt.Errorf("установленный по %s Go не запускается", binPath)
	}
	report("hub.goInstalled", binPath)
	return binPath, nil
}

// goArchForRuntime maps the hub's own architecture to a go.dev download
// suffix, same set mapUnameArch recognizes for managed hosts. 32-bit ARM is
// a single case regardless of runtime.GOARM (Go has no public runtime
// constant for it anyway) — go.dev itself only ever publishes one 32-bit
// ARM build, named "armv6l", the same one native-build's Makefile target
// downloads for a hub running directly on a Raspberry Pi-class board.
func goArchForRuntime() string {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return runtime.GOARCH
	case "arm":
		return "armv6l"
	default:
		return ""
	}
}

// fetchLatestGoVersion reads the current stable release name (e.g.
// "go1.25.0") the same way `make native-build` does.
func fetchLatestGoVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://go.dev/VERSION?m=text", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("go.dev/VERSION вернул код %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() {
		return "", fmt.Errorf("пустой ответ от go.dev/VERSION")
	}
	version := strings.TrimSpace(scanner.Text())
	if version == "" {
		return "", fmt.Errorf("не удалось разобрать версию Go из ответа go.dev")
	}
	return version, nil
}

// fetchTarGz opens url for streaming read, checking the HTTP status before
// handing the body back so a 404/5xx fails with a clear message instead of
// a cryptic gzip decode error.
func fetchTarGz(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("загрузка %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: код %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}

// extractTarGz unpacks a gzipped tarball into destDir, replacing whatever
// was there atomically (extract-then-rename) so a previous half-finished
// attempt (interrupted download, disk full mid-extract) can never look like
// a working install to goWorks. The go.dev tarballs' own top-level "go/"
// entry is kept as-is, exactly like `tar -C dir -xzf` without
// --strip-components — so destDir ends up holding a "go/" subdirectory, the
// same layout native-build's plain shell `tar` produces.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tmpDir := destDir + ".tmp"
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir) // no-op once the rename below succeeds

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("чтение архива: %w", err)
		}

		target, err := safeJoin(tmpDir, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeExtractedFile(target, tr, os.FileMode(hdr.Mode&0o777)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}

	if err := os.RemoveAll(destDir); err != nil {
		return err
	}
	return os.Rename(tmpDir, destDir)
}

func writeExtractedFile(target string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// safeJoin resolves a tar entry name against root, rejecting anything that
// would land outside it (a "../" escape, or an absolute path) — a corrupt
// or hostile tarball must never be able to write outside the toolchain
// directory it is meant to populate. Rejects outright rather than silently
// remapping the entry back under root: either way is safe, but a tarball
// that tries this is corrupt or hostile, and that is worth surfacing as an
// error rather than quietly working around it.
func safeJoin(root, name string) (string, error) {
	cleaned := filepath.Clean(name)
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("архив содержит путь вне каталога установки: %q", name)
	}
	return filepath.Join(root, cleaned), nil
}
