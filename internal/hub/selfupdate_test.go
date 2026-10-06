package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// toServer отправляет все запросы (github.com, raw.githubusercontent.com)
// на подменный сервер, сохраняя путь.
type toServer struct{ base *url.URL }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = t.base.Scheme, t.base.Host
	return http.DefaultTransport.RoundTrip(r)
}

func fakeGitHub(t *testing.T, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	old := http.DefaultClient.Transport
	http.DefaultClient.Transport = toServer{u}
	t.Cleanup(func() { http.DefaultClient.Transport = old })
}

// stepLog собирает ход обновления, как журнал задания.
type stepLog struct {
	mu    sync.Mutex
	keys  []string
	steps []int
}

func (l *stepLog) Log(key string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
}

func (l *stepLog) StepKey(n, _ int, _ string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = append(l.steps, n)
}

func (l *stepLog) has(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range l.keys {
		if k == key {
			return true
		}
	}
	return false
}

// Обновление хаба пишет в журнал все четыре шага; повторное — берёт
// бинарник из кэша по совпавшей сумме; установка запускается сценарием,
// который в тесте не выполняется.
func TestSelfUpdateSteps(t *testing.T) {
	m, _ := newTestManager(t)
	m.cfg.DataDir = t.TempDir()
	m.cfg.HubReleaseRepo = "piqab/nkt"
	asset := fmt.Sprintf("nkt-%s-%s", runtime.GOOS, runtime.GOARCH)
	bin := []byte(strings.Repeat("ELF", 50_000))
	sum := sha256.Sum256(bin)
	var binHits atomic.Int32
	fakeGitHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/v9.9.9/SHA256SUMS"):
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		case strings.HasSuffix(r.URL.Path, "/v9.9.9/"+asset):
			binHits.Add(1)
			_, _ = w.Write(bin)
		case strings.HasSuffix(r.URL.Path, "/v9.9.9/deploy/netknownsthat-hub.service"):
			fmt.Fprint(w, "[Service]\nExecStart=/usr/local/bin/nkt hub\n")
		default:
			http.NotFound(w, r)
		}
	}))
	var script string
	old := startSelfUpdateScript
	startSelfUpdateScript = func(s string) error { script = s; return nil }
	t.Cleanup(func() { startSelfUpdateScript = old })

	log := &stepLog{}
	if err := m.applyVersion(t.Context(), "9.9.9", log); err != nil {
		t.Fatalf("обновление: %v", err)
	}
	for _, k := range []string{"hub.selfUpdateFromTo", "hub.selfUpdateStepSumsLine", "hub.selfUpdateSumFound",
		"hub.selfUpdateStepBinaryLine", "hub.releaseBinaryVerified", "hub.selfUpdateStepUnitLine",
		"hub.selfUpdateStepInstallLine", "hub.selfUpdateRestarting"} {
		if !log.has(k) {
			t.Errorf("нет строки %s в журнале: %v", k, log.keys)
		}
	}
	if fmt.Sprint(log.steps) != "[1 2 3 4]" {
		t.Errorf("шаги: %v", log.steps)
	}
	if !strings.Contains(script, "systemctl restart netknownsthat-hub") || !strings.Contains(script, asset+"-9.9.9") {
		t.Errorf("сценарий установки: %q", script)
	}

	again := &stepLog{}
	if err := m.applyVersion(t.Context(), "9.9.9", again); err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if !again.has("hub.selfUpdateFromCache") || binHits.Load() != 1 {
		t.Errorf("повтор качал заново: %d раз, %v", binHits.Load(), again.keys)
	}
}

// Сервер, который отдал половину файла и замолчал: скачивание
// заканчивается ошибкой «остановилось на 50%» через срок ожидания (после
// второй попытки), а не ждёт вечно; если вторая попытка проходит —
// файл скачан.
func TestReleaseDownloadStall(t *testing.T) {
	old := releaseIdleTimeout
	releaseIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { releaseIdleTimeout = old })

	m, _ := newTestManager(t)
	m.cfg.HubReleaseRepo = "piqab/nkt"
	body := []byte(strings.Repeat("x", 1000))
	sum := sha256.Sum256(body)
	var hits atomic.Int32
	var healAfter atomic.Int32
	healAfter.Store(100)
	fakeGitHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Content-Length", "1000")
		if n > healAfter.Load() {
			_, _ = w.Write(body)
			return
		}
		_, _ = w.Write(body[:500])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))

	log := &stepLog{}
	dest := t.TempDir() + "/nkt"
	start := time.Now()
	err := m.fetchVerified(t.Context(), "9.9.9", "nkt-linux-amd64", hex.EncodeToString(sum[:]), dest, log.Log, nil)
	if err == nil || !strings.Contains(err.Error(), "50%") {
		t.Fatalf("зависший сервер: %v", err)
	}
	if time.Since(start) > 5*time.Second || hits.Load() != 2 || !log.has("hub.downloadRetry") {
		t.Errorf("попытки: %d за %v, %v", hits.Load(), time.Since(start), log.keys)
	}

	hits.Store(0)
	healAfter.Store(1)
	if err := m.fetchVerified(t.Context(), "9.9.9", "nkt-linux-amd64", hex.EncodeToString(sum[:]), dest, log.Log, nil); err != nil {
		t.Errorf("вторая попытка должна пройти: %v", err)
	}
}
