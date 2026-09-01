package hub

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// downloadReleaseBinary fetches a prebuilt nkt binary for goos/goarch from
// this project's own GitHub Releases — the fallback ensureBinary reaches for
// when resolveSourceRoot finds no local checkout to cross-compile from at
// all. That happens whenever the hub itself was installed from the prebuilt
// binary (README's own recommended path for a plain nkt) rather than
// `git clone`d: NKT_HUB_SOURCE_ROOT has no source tree to point at either
// way in that case. destPath is exactly where ensureBinary's own cache
// lookup expects the result (nkt-<goos>-<goarch>-<version> under
// HubBinCacheDir), so a downloaded binary is indistinguishable from a
// cross-compiled one on every later cache hit.
//
// Release assets only ever cover linux/{amd64,arm64,arm}
// (.github/workflows/release.yml's build matrix) — the same three
// combinations mapUnameArch ever returns for a managed host — so in
// practice every call here targets a real asset built with the identical
// -ldflags this hub would have used to cross-compile it itself. A version
// with no matching release (a local/dev build never tagged, or a fork with
// no Releases page yet) surfaces as a plain 404, reported as-is.
func (m *Manager) downloadReleaseBinary(ctx context.Context, goos, goarch, destPath string, report func(key string, args ...any)) error {
	assetName := fmt.Sprintf("nkt-%s-%s", goos, goarch)
	base := fmt.Sprintf("https://github.com/%s/releases/download/v%s", m.cfg.HubReleaseRepo, m.version)

	report("hub.downloadingReleaseBinary", goos, goarch, m.version)

	sums, err := fetchReleaseBytes(ctx, base+"/SHA256SUMS")
	if err != nil {
		return fmt.Errorf("контрольные суммы релиза v%s: %w", m.version, err)
	}
	want, err := findSHA256(sums, assetName)
	if err != nil {
		return fmt.Errorf("релиз v%s: %w", m.version, err)
	}

	binBytes, err := fetchReleaseBytes(ctx, base+"/"+assetName)
	if err != nil {
		return fmt.Errorf("бинарник релиза v%s: %w", m.version, err)
	}

	got := sha256.Sum256(binBytes)
	if gotHex := hex.EncodeToString(got[:]); !strings.EqualFold(gotHex, want) {
		return fmt.Errorf(
			"контрольная сумма скачанного %s не совпадает (ожидалась %s, получена %s) — "+
				"повреждённая загрузка или подмена, бинарник не установлен",
			assetName, want, gotHex)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil {
		return fmt.Errorf("каталог кэша бинарников: %w", err)
	}
	tmp := destPath + ".tmp"
	if err := os.WriteFile(tmp, binBytes, 0o755); err != nil {
		return fmt.Errorf("запись %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("переименование %s: %w", tmp, err)
	}

	report("hub.releaseBinaryVerified", goos, goarch)
	return nil
}

// downloadUnitTemplate fetches deploy/netknownsthat.service straight from
// this project's own repository, pinned to the tag matching the hub's own
// version — the same file loadUnitTemplate reads from a local checkout when
// one exists, and the same unauthenticated raw.githubusercontent.com fetch
// README's own manual install instructions already use for a plain host.
// Reached only when loadUnitTemplate's local read failed, i.e. the hub has
// no source checkout to begin with — the same situation
// downloadReleaseBinary exists for.
func (m *Manager) downloadUnitTemplate(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/v%s/deploy/netknownsthat.service", m.cfg.HubReleaseRepo, m.version)
	data, err := fetchReleaseBytes(ctx, url)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// findSHA256 parses a `sha256sum`-style SHA256SUMS listing (one "<hex>
// <filename>" line per file — see release.yml's `sha256sum nkt-linux-* >
// SHA256SUMS`) for the line naming asset, returning its expected hash.
// Matched against the trailing field rather than the whole line, since
// sha256sum prefixes filenames with "*" when run in binary mode.
func findSHA256(sums []byte, asset string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(sums)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("в SHA256SUMS нет строки для %s", asset)
}

// fetchReleaseBytes GETs url and returns the full body, failing on a
// non-200 status with the status code in the message rather than trying to
// parse whatever error page (GitHub's 404 HTML, say) came back as if it
// were the expected content.
func fetchReleaseBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("загрузка %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: код %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
