package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/msgs"
)

// Доставка бинарника на хост: с хаба по SFTP или хост качает его сам с
// GitHub Releases — что быстрее. Хост рядом с GitHub и далеко от хаба
// скачает 16 МБ за секунды, тогда как заливка с хаба через полмира
// тянется десятки секунд; бывает и наоборот (хост без интернета, GitHub
// закрыт). Поэтому перед первой доставкой — короткая проба обоих
// путей, итог запоминается на хост (hosts.binary_via) и в следующий раз
// проба не повторяется; провал выбранного пути сбрасывает выбор и
// возвращает к SFTP в том же задании.
//
// С GitHub хост берёт ровно тот же файл, что лежит в кэше хаба:
// releaseDelivery сверяет sha256 локального бинарника с SHA256SUMS
// релиза, и при расхождении (сборка из исходников, версия без релиза)
// остаётся только SFTP.

const (
	BinaryViaGitHub = "github"
	BinaryViaSFTP   = "sftp"

	probeBytes       = 1 << 20
	probeTimeout     = 8 * time.Second
	releaseSumsTTL   = 10 * time.Minute
	hostDownloadWait = 15 * time.Minute
)

// binaryDelivery — ассет релиза, байт в байт совпадающий с локальным
// файлом.
type binaryDelivery struct {
	URL    string
	SHA256 string
	Size   int64
}

// binarySource — что заливать и как: локальный файл, его копия на
// GitHub (если есть), запомненный способ и куда записать выбранный.
type binarySource struct {
	LocalPath string
	Release   *binaryDelivery
	Via       string
	OnVia     func(via string)
}

type releaseSumsEntry struct {
	data []byte
	err  error
	at   time.Time
}

// releaseSums — SHA256SUMS релиза с кэшем: один запрос на версию, а не
// на каждый хост; 404 тоже кэшируется, но недолго — релиз могут
// выложить позже.
func (m *Manager) releaseSums(ctx context.Context, version string) ([]byte, error) {
	m.sumsMu.Lock()
	defer m.sumsMu.Unlock()
	if m.sumsCache == nil {
		m.sumsCache = map[string]releaseSumsEntry{}
	}
	if e, ok := m.sumsCache[version]; ok && time.Since(e.at) < releaseSumsTTL {
		return e.data, e.err
	}
	base := fmt.Sprintf("https://github.com/%s/releases/download/v%s", m.cfg.HubReleaseRepo, version)
	data, err := fetchReleaseBytes(ctx, base+"/SHA256SUMS")
	m.sumsCache[version] = releaseSumsEntry{data: data, err: err, at: time.Now()}
	return data, err
}

// releaseDelivery — ассет GitHub Releases для goos/goarch текущей версии,
// если он совпадает с localPath; иначе nil (только SFTP).
func (m *Manager) releaseDelivery(ctx context.Context, goos, goarch, localPath string) *binaryDelivery {
	if m.cfg.HubReleaseRepo == "" {
		return nil
	}
	sums, err := m.releaseSums(ctx, m.version)
	if err != nil {
		return nil
	}
	asset := fmt.Sprintf("nkt-%s-%s", goos, goarch)
	want, err := findSHA256(sums, asset)
	if err != nil {
		return nil
	}
	f, err := os.Open(localPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		return nil
	}
	return &binaryDelivery{URL: fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", m.cfg.HubReleaseRepo, m.version, asset), SHA256: want, Size: size}
}

// probeDelivery меряет оба пути: первый мегабайт ассета с хоста через
// curl и мегабайт по SFTP с хаба. GitHub берётся, когда он заметно
// быстрее — при равных SFTP предсказуемее.
func probeDelivery(client *ssh.Client, sftpClient *sftp.Client, tmpDir string, d *binaryDelivery, report func(key string, args ...any)) string {
	report("hub.probingDelivery")
	cmd := fmt.Sprintf("command -v curl >/dev/null 2>&1 || { echo nocurl; exit 0; }; curl -sSL -r 0-%d -o /dev/null --max-time %d -w '%%{speed_download}' %s 2>&1",
		probeBytes-1, int(probeTimeout.Seconds()), shellQuote(d.URL))
	out, err := runRemote(client, cmd)
	out = strings.TrimSpace(out)
	if err != nil || out == "nocurl" {
		reason := out
		if err != nil {
			reason = lastLines(out+"\n"+err.Error(), 1)
		}
		report("hub.probeGitHubUnavailable", reason)
		return BinaryViaSFTP
	}
	gh, _ := strconv.ParseFloat(strings.Fields(out)[len(strings.Fields(out))-1], 64)
	if gh <= 0 {
		report("hub.probeGitHubUnavailable", out)
		return BinaryViaSFTP
	}
	started := time.Now()
	if err := uploadBytes(sftpClient, make([]byte, probeBytes), tmpDir+"/probe", 0o644); err != nil {
		return BinaryViaSFTP
	}
	sftpSpeed := float64(probeBytes) / time.Since(started).Seconds()
	const mb = 1 << 20
	if gh > 1.5*sftpSpeed {
		report("hub.probeChooseGitHub", gh/mb, sftpSpeed/mb)
		return BinaryViaGitHub
	}
	report("hub.probeChooseSFTP", gh/mb, sftpSpeed/mb)
	return BinaryViaSFTP
}

// downloadOnHost качает ассет на хосте curl'ом в dst, показывая прогресс
// по росту файла, и сверяет sha256sum.
func downloadOnHost(client *ssh.Client, d *binaryDelivery, dst string, progress func(key string, args ...any)) error {
	done := make(chan error, 1)
	go func() {
		out, err := runRemote(client, fmt.Sprintf("curl -fsSL --max-time %d -o %s %s 2>&1", int(hostDownloadWait.Seconds()), dst, shellQuote(d.URL)))
		if err != nil {
			done <- fmt.Errorf("%s", lastLines(strings.TrimSpace(out+"\n"+err.Error()), 1))
			return
		}
		done <- nil
	}()
	ticker := time.NewTicker(progressReportInterval)
	defer ticker.Stop()
	deadline := time.After(hostDownloadWait + time.Minute)
	for {
		select {
		case err := <-done:
			if err != nil {
				return err
			}
			progress("hub.downloadingOnHost", 100, float64(d.Size)/(1<<20), float64(d.Size)/(1<<20))
			out, err := runRemote(client, "sha256sum "+dst)
			if err != nil {
				return fmt.Errorf("sha256sum: %v", err)
			}
			got := strings.Fields(out)
			if len(got) == 0 || !strings.EqualFold(got[0], d.SHA256) {
				return msgs.Errorf("hub.checksumDownloadedDoesMatchExpected", d.URL, d.SHA256, strings.Join(got, " "))
			}
			return nil
		case <-ticker.C:
			if out, err := runRemote(client, "stat -c %s "+dst+" 2>/dev/null"); err == nil {
				if n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil && d.Size > 0 {
					progress("hub.downloadingOnHost", int(n*100/d.Size), float64(n)/(1<<20), float64(d.Size)/(1<<20))
				}
			}
		case <-deadline:
			return fmt.Errorf("timeout")
		}
	}
}
