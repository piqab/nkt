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
	"sync/atomic"
	"time"

	"github.com/piqab/nkt/internal/msgs"
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
//
// version is an explicit parameter rather than always m.version: ensureBinary
// passes the hub's own running version (the only thing it ever needs), but
// the hub's own self-update (checkAndApplyHubUpdate) needs exactly this same
// download-and-verify logic for whatever *newer* version versionCheckLoop
// last found — a different value than m.version by definition.
//
// progress (может быть nil) получает «N% (X из Y МБ)» по ходу скачивания
// — той же строкой-заменой, что и заливка на хост: иначе между
// «скачиваю…» и «скачан» журнал молчит всё время загрузки.
func (m *Manager) downloadReleaseBinary(ctx context.Context, goos, goarch, version, destPath string, report, progress func(key string, args ...any)) error {
	return m.downloadReleaseAsset(ctx, "nkt", goos, goarch, version, destPath, report, progress)
}

// Сроки скачивания с GitHub. У http.DefaultClient своего срока нет, и
// повисшее соединение (такое бывает у objects.githubusercontent.com, куда
// GitHub перенаправляет скачивание) ждало бы вечно: пока скачивание шло
// внутри HTTP-запроса, его обрывал срок запроса, а в задании — ничто.
var (
	// releaseIdleTimeout — сколько ждать следующей порции данных.
	releaseIdleTimeout = 60 * time.Second
	// releaseSmallFileTimeout — файл сумм и юнит целиком.
	releaseSmallFileTimeout = 30 * time.Second
)

// releaseDownloadAttempts — попыток скачать бинарник: оборванное или
// повисшее соединение с GitHub обычно проходит со второго раза.
const releaseDownloadAttempts = 2

func releaseBase(repo, version string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s", repo, version)
}

// releaseSum — ожидаемая sha256 файла assetName из SHA256SUMS релиза.
func (m *Manager) releaseSum(ctx context.Context, version, assetName string) (string, error) {
	sums, err := fetchReleaseBytes(ctx, releaseBase(m.cfg.HubReleaseRepo, version)+"/SHA256SUMS")
	if err != nil {
		return "", msgs.Errorf("hub.releaseVChecksums", version, err)
	}
	want, err := findSHA256(sums, assetName)
	if err != nil {
		return "", msgs.Errorf("hub.releaseV", version, err)
	}
	return want, nil
}

// fetchVerified скачивает файл релиза в destPath через временный файл и
// сверяет sha256 с want; обрыв или зависание — ещё одна попытка (о ней
// строка в report).
func (m *Manager) fetchVerified(ctx context.Context, version, assetName, want, destPath string, report, progress func(key string, args ...any)) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil {
		return msgs.Errorf("hub.binaryCacheDirectory", err)
	}
	tmp := destPath + ".tmp"
	url := releaseBase(m.cfg.HubReleaseRepo, version) + "/" + assetName
	var gotHex string
	var err error
	for attempt := 1; attempt <= releaseDownloadAttempts; attempt++ {
		gotHex, err = fetchReleaseFile(ctx, url, tmp, progress)
		if err == nil || ctx.Err() != nil {
			break
		}
		if attempt < releaseDownloadAttempts {
			report("hub.downloadRetry", err)
		}
	}
	if err != nil {
		_ = os.Remove(tmp)
		return msgs.Errorf("hub.releaseVBinary", version, err)
	}
	if !strings.EqualFold(gotHex, want) {
		_ = os.Remove(tmp)
		return msgs.Errorf("hub.checksumDownloadedDoesMatchExpected", assetName, want, gotHex)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return msgs.Errorf("hub.renaming", tmp, err)
	}
	return nil
}

// downloadReleaseAsset — то же для любой программы релиза (prefix —
// «nkt» или «nkt-edge»): файл <prefix>-<os>-<arch> со сверкой SHA256SUMS.
func (m *Manager) downloadReleaseAsset(ctx context.Context, prefix, goos, goarch, version, destPath string, report, progress func(key string, args ...any)) error {
	assetName := fmt.Sprintf("%s-%s-%s", prefix, goos, goarch)
	report("hub.downloadingReleaseBinary", goos, goarch, version)
	want, err := m.releaseSum(ctx, version, assetName)
	if err != nil {
		return err
	}
	if err := m.fetchVerified(ctx, version, assetName, want, destPath, report, progress); err != nil {
		return err
	}
	report("hub.releaseBinaryVerified", goos, goarch)
	return nil
}

// idleReader отодвигает срок ожидания на каждой пришедшей порции и
// считает прочитанное — для сообщения, на скольких процентах встало.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
	read  int64
}

func (i *idleReader) Read(b []byte) (int, error) {
	n, err := i.r.Read(b)
	if n > 0 {
		i.read += int64(n)
		i.timer.Reset(releaseIdleTimeout)
	}
	return n, err
}

// fetchReleaseFile скачивает url в файл dest (0755), считая sha256 по
// ходу; progress получает проценты, когда сервер сообщил размер. Если
// releaseIdleTimeout не приходит ни байта (и до ответа сервера тоже), —
// ошибка «скачивание остановилось на N%», а не вечное ожидание.
func fetchReleaseFile(ctx context.Context, url, dest string, progress func(key string, args ...any)) (sha256Hex string, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	timer := time.AfterFunc(releaseIdleTimeout, func() {
		stalled.Store(true)
		cancel()
	})
	defer timer.Stop()
	idle := &idleReader{timer: timer}
	var total int64
	stallErr := func(err error) error {
		if !stalled.Load() {
			return err
		}
		pct := int64(0)
		if total > 0 {
			pct = idle.read * 100 / total
		}
		return msgs.Errorf("hub.downloadStalled", pct, int(releaseIdleTimeout.Seconds()))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", stallErr(msgs.Errorf("hub.downloading", url, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", msgs.Errorf("hub.code2", url, resp.StatusCode)
	}
	total = resp.ContentLength
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", msgs.Errorf("control.writing", dest, err)
	}
	idle.r = resp.Body
	var body io.Reader = idle
	var pr *progressReader
	if progress != nil && resp.ContentLength > 0 {
		pr = &progressReader{r: idle, total: resp.ContentLength, report: progress, key: "hub.downloadingBinaryProgress"}
		body = pr
	}
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, sum), body); err != nil {
		_ = out.Close()
		return "", stallErr(msgs.Errorf("hub.downloading", url, err))
	}
	if err := out.Close(); err != nil {
		return "", msgs.Errorf("control.writing", dest, err)
	}
	if pr != nil {
		pr.reportNow()
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// fetchReleaseBytes GETs url and returns the full body, failing on a
// non-200 status with the status code in the message rather than trying to
// parse whatever error page (GitHub's 404 HTML, say) came back as if it
// were the expected content.
// Целиком не дольше releaseSmallFileTimeout.
func fetchReleaseBytes(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, releaseSmallFileTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, msgs.Errorf("hub.downloading", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, msgs.Errorf("hub.code2", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// downloadUnitTemplate fetches deploy/<unitFile> straight from this
// project's own repository, pinned to the tag matching version — the same
// file loadUnitTemplate reads from a local checkout when one exists, and
// the same unauthenticated raw.githubusercontent.com fetch README's own
// manual install instructions already use for a plain host. Reached only
// when loadUnitTemplate's local read failed, i.e. the hub has no source
// checkout to begin with — the same situation downloadReleaseBinary exists
// for. unitFile is "netknownsthat.service" for a managed host's unit
// (loadUnitTemplate's own use) or "netknownsthat-hub.service" for the
// hub's own (checkAndApplyHubUpdate) — both live side by side under
// deploy/ in the same repository.
func (m *Manager) downloadUnitTemplate(ctx context.Context, version, unitFile string) (string, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/v%s/deploy/%s", m.cfg.HubReleaseRepo, version, unitFile)
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
	return "", msgs.Errorf("hub.sha256sumsHasLine", asset)
}
