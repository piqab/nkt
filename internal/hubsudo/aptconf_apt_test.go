package hubsudo

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Проверка против настоящего apt — если он в системе есть. Конфиг,
// который кладёт hub-sudo, обязан заставить apt спросить автоопределение
// прокси и для https-источника: с одной лишь http-строкой https-зеркала
// (deb.debian.org, зеркало из mirror+file) до скрипта не доходили совсем
// и качали мимо кэша хаба — молча, без ошибки. Заодно проверяется, что
// apt вызывает скрипт именно из файла конфигурации, а не только из «-o»,
// которым его пробуют руками.

// counter — слушатель, который считает соединения и сразу их закрывает:
// apt спотыкается о закрытый рукав мгновенно, вместо таймаута на
// недостижимый адрес. Сам слушающий порт не закрывается и не перевязывается
// — того самого флака с эфемерными портами здесь и нет.
func counter(t *testing.T) (port int, hits *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("локальный слушатель недоступен: ", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	hits = &atomic.Int64{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			_ = c.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, hits
}

func TestAptConfCoversHTTPSSources(t *testing.T) {
	aptGet, err := exec.LookPath("apt-get")
	if err != nil {
		t.Skip("apt-get в системе нет — поведение apt не проверяется")
	}
	proxyPort, proxyHits := counter(t) // «кэш хаба»: сюда apt идёт, если скрипт спросили
	repoPort, repoHits := counter(t)   // «зеркало»: сюда apt идёт напрямую

	h, sign := newHost(t, "hub-secret")
	if _, err := h.Execute(sign(Request{Op: OpAptProxy,
		Args: map[string]string{"enabled": "true", "port": strconv.Itoa(proxyPort)}})); err != nil {
		t.Fatal(err)
	}
	shipped, err := os.ReadFile(filepath.Join(h.Root, AptProxyConf))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`Acquire::http::Proxy-Auto-Detect "` + AptProxyDetect + `";`,
		`Acquire::https::Proxy-Auto-Detect "` + AptProxyDetect + `";`,
	} {
		if !strings.Contains(string(shipped), line) {
			t.Fatalf("конфиг apt обязан называть детект для каждой схемы, нет %q:\n%s", line, shipped)
		}
	}
	// apt исполняет скрипт по абсолютному пути, в песочнице — тот же
	// текст с путём внутри корня проверки.
	text := strings.ReplaceAll(string(shipped), AptProxyDetect, filepath.Join(h.Root, AptProxyDetect))

	t.Run("конфиг хаба — apt спрашивает детект и для https", func(t *testing.T) {
		conf := writeConf(t, text)
		deadline := runApt(t, aptGet, conf, repoPort)
		if !waitFor(t, func() bool { return proxyHits.Load() > 0 }, deadline) {
			t.Errorf("apt не спросил автоопределение для https-источника (на прокси %d, на зеркало %d)",
				proxyHits.Load(), repoHits.Load())
		}
	})

	// Прежний конфиг — одна http-строка: ровно та регрессия, из-за
	// которой https-пакеты не кэшировались.
	t.Run("одна http-строка — apt идёт мимо кэша", func(t *testing.T) {
		var onlyHTTP []string
		for _, line := range strings.Split(text, "\n") {
			if !strings.Contains(line, "Acquire::https::") {
				onlyHTTP = append(onlyHTTP, line)
			}
		}
		conf := writeConf(t, strings.Join(onlyHTTP, "\n"))
		proxyHits.Store(0) // предыдущий подтест спрашивал детект — считаем заново
		deadline := runApt(t, aptGet, conf, repoPort)
		if !waitFor(t, func() bool { return repoHits.Load() > 0 }, deadline) {
			t.Skip("apt не дошёл даже до зеркала — проверка не показательна")
		}
		if n := proxyHits.Load(); n > 0 {
			t.Errorf("apt спросил детект, хотя в конфиге только http-строка (%d соединений с прокси)", n)
		}
	})
}

func writeConf(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "99nkt-hub-proxy")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runApt гоняет apt-get update с заданным конфигом в песочнице: ни
// системные каталоги, ни наружу не трогаются. Возвращает срок, до которого
// имеет смысл ждать соединений — сам apt при этом может и ошибиться, это
// неважно: важен адрес, куда он пошёл.
func runApt(t *testing.T, aptGet, conf string, repoPort int) time.Time {
	t.Helper()
	dir := t.TempDir()
	etc := filepath.Join(dir, "etc")
	for _, p := range []string{etc, filepath.Join(etc, "trusted.gpg.d"), filepath.Join(dir, "lists"), filepath.Join(dir, "locks"), filepath.Join(dir, "st")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	list := "deb [trusted=yes] https://127.0.0.1:" + strconv.Itoa(repoPort) + "/debian trixie main\n"
	if err := os.WriteFile(filepath.Join(etc, "sources.list"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"-c", conf,
		"-o", "Dir::Etc=" + etc,
		"-o", "Dir::Etc::sourceparts=-",
		"-o", "Dir::State::lists=" + filepath.Join(dir, "lists"),
		"-o", "Dir::State::status=" + filepath.Join(dir, "st", "status"),
		"-o", "Dir::State::locks=" + filepath.Join(dir, "locks"),
		"-o", "Dir::Cache::pkgcache=",
		"-o", "Dir::Cache::srcpkgcache=",
		"-o", "Acquire::Retries=0",
		"-o", "Acquire::http::Timeout=3",
		"-o", "Acquire::https::Timeout=3",
		"update",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, aptGet, args...).CombinedOutput()
	t.Logf("apt-get update: %v\n%s", err, out)
	return time.Now().Add(5 * time.Second)
}

// waitFor ждёт условия, но не дольше срока, который вернул runApt.
func waitFor(t *testing.T, cond func() bool, deadline time.Time) bool {
	t.Helper()
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cond()
}
