package hub

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	gopath "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// База сигнатур ClamAV — ~250 МБ, и качать её на каждый хост с
// database.clamav.net накладно (а с десятка хостов одного адреса — ещё и
// повод для блокировки у их CDN). Хаб держит одну копию и раздаёт хостам
// по SSH, тем же путём, что заливает бинарник nkt. Копия обновляется по
// If-Modified-Since — daily.cvd меняется каждый день, main.cvd почти
// никогда, так что суточное обновление обходится в десятки мегабайт.
//
// В отличие от базы trivy, эта не качается сама при старте: она нужна
// только тем, кто пользуется ClamAV. Первое обновление — кнопкой в «О
// системе»; после этого цикл держит её свежей.

// clamDBFiles — что составляет базу. Порядок — порядок заливки.
var clamDBFiles = []string{"main.cvd", "daily.cvd", "bytecode.cvd"}

const clamDBMirror = "https://database.clamav.net/"

// KindClamDBPush — задание хаба: залить базу на хост.
const KindClamDBPush = "clamdb.push"

// ClamDBInfo — состояние копии на хабе.
type ClamDBInfo struct {
	Available  bool
	UpdatedAt  time.Time
	SizeBytes  int64
	Refreshing bool
	Progress   string
	Error      string
}

func (m *Manager) clamDBDir() string { return filepath.Join(m.cfg.DataDir, "clamdb") }

// ClamDBStatus — что лежит в кэше; без обращений в сеть.
func (m *Manager) ClamDBStatus() ClamDBInfo {
	info := ClamDBInfo{}
	var newest time.Time
	present := 0
	for _, f := range clamDBFiles {
		st, err := os.Stat(filepath.Join(m.clamDBDir(), f))
		if err != nil {
			continue
		}
		present++
		info.SizeBytes += st.Size()
		if st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	// bytecode.cvd не обязателен; база — это main + daily.
	info.Available = present >= 2
	info.UpdatedAt = newest
	m.clamDBMu.Lock()
	info.Refreshing, info.Progress, info.Error = m.clamDBRefreshing, m.clamDBProgress, m.clamDBErr
	m.clamDBMu.Unlock()
	return info
}

// RefreshClamDB скачивает то, что изменилось на зеркале.
func (m *Manager) RefreshClamDB(ctx context.Context) error {
	m.clamDBMu.Lock()
	if m.clamDBRefreshing {
		m.clamDBMu.Unlock()
		return nil
	}
	m.clamDBRefreshing, m.clamDBErr = true, ""
	m.clamDBMu.Unlock()
	report := func(msg string) {
		m.clamDBMu.Lock()
		m.clamDBProgress = msg
		m.clamDBMu.Unlock()
	}
	err := m.refreshClamDB(ctx, report)
	m.clamDBMu.Lock()
	m.clamDBRefreshing, m.clamDBProgress = false, ""
	if err != nil {
		m.clamDBErr = err.Error()
	}
	m.clamDBMu.Unlock()
	return err
}

func (m *Manager) refreshClamDB(ctx context.Context, report func(string)) error {
	dir := m.clamDBDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	for _, name := range clamDBFiles {
		dest := filepath.Join(dir, name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, clamDBMirror+name, nil)
		if err != nil {
			return err
		}
		// Зеркало отдаёт файлы только своим клиентам: freshclam и
		// cvdupdate — официальному инструменту для частных зеркал, чем хаб
		// здесь и является. Его User-Agent — «CVDUPDATE/версия (uuid)»,
		// uuid постоянный на установку: по нему зеркало считает частоту
		// обращений. If-Modified-Since — чтобы не качать то, что не
		// менялось; этого же просит и сам ClamAV.
		req.Header.Set("User-Agent", "CVDUPDATE/1.1.3 ("+m.clamDBClientID()+")")
		if st, err := os.Stat(dest); err == nil {
			req.Header.Set("If-Modified-Since", st.ModTime().UTC().Format(http.TimeFormat))
		}
		report(msgs.Tc(ctx, "hub.clamDBDownloading", name))
		resp, err := client.Do(req)
		if err != nil {
			return msgs.Errorf("hub.clamDBDownload", name, err)
		}
		func() {
			defer resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusNotModified:
				return
			case http.StatusOK:
			default:
				err = msgs.Errorf("hub.clamDBDownloadCode", name, resp.StatusCode)
				return
			}
			tmp := dest + ".part"
			f, ferr := os.Create(tmp)
			if ferr != nil {
				err = ferr
				return
			}
			if _, cerr := io.Copy(f, resp.Body); cerr != nil {
				f.Close()
				os.Remove(tmp)
				err = msgs.Errorf("hub.clamDBDownload", name, cerr)
				return
			}
			f.Close()
			if t, perr := http.ParseTime(resp.Header.Get("Last-Modified")); perr == nil {
				_ = os.Chtimes(tmp, t, t)
			}
			err = os.Rename(tmp, dest)
		}()
		if err != nil {
			// bytecode.cvd — необязательный: без него clamscan работает.
			if name == "bytecode.cvd" {
				m.log.Warn("clamav bytecode.cvd", "error", err)
				continue
			}
			return err
		}
	}
	return nil
}

// clamDBClientID — постоянный идентификатор этого хаба для зеркала;
// создаётся при первом обращении и хранится рядом с базой.
func (m *Manager) clamDBClientID() string {
	path := filepath.Join(m.clamDBDir(), "client-id")
	if raw, err := os.ReadFile(path); err == nil && len(raw) >= 32 {
		return strings.TrimSpace(string(raw))
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
	return id
}

// clamDBRefreshLoop держит копию свежей — только если она уже заведена
// кнопкой: без явного желания оператора хаб 250 МБ не качает.
func (m *Manager) clamDBRefreshLoop(ctx context.Context) {
	interval := m.cfg.HubClamDBRefreshInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.ClamDBStatus().Available {
				_ = m.RefreshClamDB(ctx)
			}
		}
	}
}

// ClamDBPushParams — вход задания заливки.
type ClamDBPushParams struct {
	HostID int64 `json:"host_id"`
}

// ClamDBPushRunner заливает базу на хост по SSH и кладёт её в
// /var/lib/clamav — туда, где её ждёт clamscan.
type ClamDBPushRunner struct{ m *Manager }

// NewClamDBPushRunner строит исполнителя.
func NewClamDBPushRunner(m *Manager) *ClamDBPushRunner { return &ClamDBPushRunner{m: m} }

// Run выполняет заливку.
func (r *ClamDBPushRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ClamDBPushParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	if !r.m.ClamDBStatus().Available {
		return msgs.Errorf("hub.clamDBNotCached")
	}
	host, err := r.m.db.HostByID(ctx, p.HostID)
	if err != nil {
		return err
	}
	jc.Step(1, 3, msgs.Tc(ctx, "hub.clamDBStepConnect", host.Name))
	link, err := r.m.dialHost(ctx, host)
	if err != nil {
		return err
	}
	defer link.Close()
	// Отмена задания: ни SFTP, ни удалённая команда контекста не знают —
	// закрытое SSH-соединение обрывает и заливку, и установку, а
	// проверка ctx между шагами превращает обрыв в честное «отменено».
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = link.Close()
		case <-stop:
		}
	}()
	sftpClient, err := sftp.NewClient(link.client)
	if err != nil {
		return msgs.Errorf("hub.openingSFTP", err)
	}
	defer sftpClient.Close()

	tmpDir := fmt.Sprintf("/tmp/nkt-clamdb-%d", time.Now().UnixNano())
	defer func() { _, _ = runRemote(link.client, "rm -rf "+tmpDir) }()
	jc.Step(2, 3, msgs.Tc(ctx, "hub.clamDBStepUpload"))
	for _, name := range clamDBFiles {
		src := filepath.Join(r.m.clamDBDir(), name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		// Прогресс uploadFile сформулирован про бинарник — здесь свой
		// текст с именем файла, проценты те же.
		if err := uploadFile(sftpClient, src, gopath.Join(tmpDir, name), 0o644, func(_ string, args ...any) {
			jc.Log("hub.clamDBUploading", append([]any{name}, args...)...)
		}); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return msgs.Errorf("hub.clamDBUpload", name, err)
		}
	}
	jc.Step(3, 3, msgs.Tc(ctx, "hub.clamDBStepInstall"))
	// Файлы принадлежат clamav, чтобы freshclam потом мог их обновлять;
	// служба на время замены остановлена — иначе она может как раз в
	// этот момент писать в те же файлы.
	script := fmt.Sprintf("set -e; systemctl stop clamav-freshclam 2>/dev/null || true; "+
		"install -d -m 755 /var/lib/clamav; for f in %s/*.cvd; do install -m 644 \"$f\" /var/lib/clamav/; done; "+
		"chown clamav:clamav /var/lib/clamav/*.cvd 2>/dev/null || true; systemctl start clamav-freshclam 2>/dev/null || true", tmpDir)
	cmd := "sh -c " + shellQuote(script)
	if host.SSHUser != "root" {
		cmd = "sudo -n " + cmd
	}
	if out, err := runRemote(link.client, cmd); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return msgs.Errorf("hub.clamDBInstall", err, out)
	}
	jc.Log("hub.clamDBDone", host.Name)
	return nil
}
