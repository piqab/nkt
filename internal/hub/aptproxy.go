package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/aptcache"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Кэш пакетов: один на хаб, хостам отдаётся обратным пробросом порта по
// SSH — хост видит его на 127.0.0.1:<порт>, пока хаб держит сессию, и
// apt через Proxy-Auto-Detect ходит через кэш, а без него — напрямую.
// Хабу для этого не нужен публичный адрес, а хосту — новый открытый порт.

const aptCacheMaxKVKey = "aptcache_max_gb"

// aptProxyScanInterval — как часто хаб сверяет, каким хостам нужен
// проброс (включили/выключили флаг, хост появился или удалён).
const aptProxyScanInterval = 30 * time.Second

func (m *Manager) aptCacheDir() string { return filepath.Join(m.cfg.DataDir, "aptcache") }

// AptCache — кэш; заводится лениво, чтобы NewManager не трогал диск.
func (m *Manager) AptCache() *aptcache.Cache {
	m.aptCacheOnce.Do(func() {
		maxGB := m.cfg.HubAptCacheMaxGB
		if raw, ok, err := m.db.KVGet(context.Background(), aptCacheMaxKVKey); err == nil && ok {
			if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
				maxGB = n
			}
		}
		c, err := aptcache.New(m.aptCacheDir(), int64(maxGB)<<30)
		if err != nil {
			m.log.Error("apt cache unavailable", "error", err)
			return
		}
		m.aptCache = c
	})
	return m.aptCache
}

// SetAptCacheMaxGB сохраняет лимит и применяет его сразу.
func (m *Manager) SetAptCacheMaxGB(ctx context.Context, gb int) error {
	if gb < 0 {
		return msgs.Errorf("hub.aptCacheBadLimit")
	}
	if err := m.db.KVSet(ctx, aptCacheMaxKVKey, strconv.Itoa(gb)); err != nil {
		return err
	}
	if c := m.AptCache(); c != nil {
		c.SetMaxBytes(int64(gb) << 30)
	}
	return nil
}

// AptProxyConnected — держит ли хаб проброс на этот хост прямо сейчас.
func (m *Manager) AptProxyConnected(hostID int64) bool {
	m.aptProxyMu.Lock()
	defer m.aptProxyMu.Unlock()
	return m.aptProxyLive[hostID]
}

func (m *Manager) setAptProxyLive(hostID int64, live bool) {
	m.aptProxyMu.Lock()
	if m.aptProxyLive == nil {
		m.aptProxyLive = map[int64]bool{}
	}
	if live {
		m.aptProxyLive[hostID] = true
	} else {
		delete(m.aptProxyLive, hostID)
	}
	m.aptProxyMu.Unlock()
}

// maintainAptProxies — по образцу maintainTunnelDialers: на каждый хост с
// флагом — своя горутина с сессией, лишние гасятся.
func (m *Manager) maintainAptProxies(ctx context.Context) {
	if m.AptCache() == nil {
		return
	}
	running := map[int64]context.CancelFunc{}
	defer func() {
		for _, cancel := range running {
			cancel()
		}
	}()
	ticker := time.NewTicker(aptProxyScanInterval)
	defer ticker.Stop()
	for {
		if hosts, err := m.db.ListHosts(ctx); err == nil {
			want := map[int64]bool{}
			for _, h := range hosts {
				if h.AptViaHub && h.Status == store.HostStatusOnline {
					want[h.ID] = true
					if _, ok := running[h.ID]; !ok {
						hctx, cancel := context.WithCancel(ctx)
						running[h.ID] = cancel
						go m.runAptProxy(hctx, h.ID)
					}
				}
			}
			for id, cancel := range running {
				if !want[id] {
					cancel()
					delete(running, id)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runAptProxy держит на хосте слушающий порт, пока жива сессия; при
// обрыве переподключается с нарастающей паузой.
func (m *Manager) runAptProxy(ctx context.Context, hostID int64) {
	defer m.setAptProxyLive(hostID, false)
	backoff := 5 * time.Second
	for {
		err := m.serveAptProxyOnce(ctx, hostID)
		m.setAptProxyLive(hostID, false)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.log.Debug("apt proxy session ended", "host_id", hostID, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
		}
	}
}

func (m *Manager) serveAptProxyOnce(ctx context.Context, hostID int64) error {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return err
	}
	link, err := m.dialHost(ctx, host)
	if err != nil {
		return err
	}
	defer link.Close()
	// Обратный проброс: sshd хоста слушает 127.0.0.1:порт и передаёт
	// соединения сюда; их обслуживает сам кэш как HTTP-прокси.
	ln, err := link.client.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(m.cfg.HubAptCachePort)))
	if err != nil {
		return msgs.Errorf("hub.aptProxyListen", err)
	}
	defer ln.Close()
	m.setAptProxyLive(hostID, true)
	srv := &http.Server{Handler: m.AptCache(), ReadHeaderTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		_ = srv.Close()
		return nil
	case err := <-done:
		return err
	case <-linkClosed(link):
		_ = srv.Close()
		return fmt.Errorf("ssh session closed")
	}
}

// linkClosed сигналит, когда SSH-соединение умерло.
func linkClosed(link *sshLink) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		_ = link.client.Wait()
		close(ch)
	}()
	return ch
}

// Файлы на хосте. Скрипт — bash: /dev/tcp есть только у него, а nc на
// хосте может не быть. Ответ DIRECT при закрытом порте — штатное
// поведение apt: без хаба пакеты качаются как обычно.
const (
	aptProxyDetectPath = "/usr/local/bin/nkt-apt-proxy"
	aptProxyConfPath   = "/etc/apt/apt.conf.d/99nkt-hub-proxy"
)

func aptProxyDetectScript(port int) string {
	return fmt.Sprintf(`#!/bin/bash
# nkt: apt через кэш пакетов хаба, если хаб сейчас держит проброс порта.
if (exec 3<>/dev/tcp/127.0.0.1/%d) 2>/dev/null; then
  exec 3>&-
  echo "http://127.0.0.1:%d"
else
  echo DIRECT
fi
`, port, port)
}

const aptProxyConf = `// nkt: кэш пакетов хаба; скрипт отвечает DIRECT, когда хаб не подключён.
Acquire::http::Proxy-Auto-Detect "` + aptProxyDetectPath + `";
`

// ApplyAptProxy включает или выключает apt через хаб на хосте: кладёт или
// убирает конфиг и запоминает флаг.
func (m *Manager) ApplyAptProxy(ctx context.Context, hostID int64, enabled bool) error {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return err
	}
	link, err := m.dialHost(ctx, host)
	if err != nil {
		return err
	}
	defer link.Close()
	if err := configureAptProxy(link.client, host.SSHUser, m.cfg.HubAptCachePort, enabled); err != nil {
		return err
	}
	return m.db.SetHostAptViaHub(ctx, hostID, enabled)
}

// configureAptProxy — сама раскладка файлов по открытому соединению;
// вызывается и из установки nkt на хост.
func configureAptProxy(client *ssh.Client, sshUser string, port int, enabled bool) error {
	sudo := ""
	if sshUser != "root" {
		sudo = "sudo -n "
	}
	if !enabled {
		if out, err := runRemote(client, sudo+"rm -f "+aptProxyConfPath+" "+aptProxyDetectPath); err != nil {
			return msgs.Errorf("hub.aptProxyConfigure", err, out)
		}
		return nil
	}
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return msgs.Errorf("hub.openingSFTP", err)
	}
	defer sftpClient.Close()
	tmpDir := fmt.Sprintf("/tmp/nkt-aptproxy-%d", time.Now().UnixNano())
	defer func() { _, _ = runRemote(client, "rm -rf "+tmpDir) }()
	if err := uploadBytes(sftpClient, []byte(aptProxyDetectScript(port)), tmpDir+"/nkt-apt-proxy", 0o755); err != nil {
		return msgs.Errorf("hub.aptProxyConfigure", err, "")
	}
	if err := uploadBytes(sftpClient, []byte(aptProxyConf), tmpDir+"/99nkt-hub-proxy", 0o644); err != nil {
		return msgs.Errorf("hub.aptProxyConfigure", err, "")
	}
	if err := installRemoteFile(client, sshUser, tmpDir+"/nkt-apt-proxy", aptProxyDetectPath, 0o755); err != nil {
		return err
	}
	if err := installRemoteFile(client, sshUser, tmpDir+"/99nkt-hub-proxy", aptProxyConfPath, 0o644); err != nil {
		return err
	}
	return nil
}

// aptCacheStatsJSON — для карточки в «О системе».
func (m *Manager) aptCacheStatsJSON() map[string]any {
	c := m.AptCache()
	if c == nil {
		return map[string]any{"available": false}
	}
	st := c.Stats()
	raw, _ := json.Marshal(st)
	out := map[string]any{"available": true}
	_ = json.Unmarshal(raw, &out)
	m.aptProxyMu.Lock()
	out["connected_hosts"] = len(m.aptProxyLive)
	m.aptProxyMu.Unlock()
	return out
}
