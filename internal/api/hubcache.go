package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/config"
)

// Кэш хаба на этом хосте: пока хаб держит проброс порта (галочка «apt
// через хаб»), на 127.0.0.1:<порт> отвечает его кэширующий прокси —
// пакеты, файлы по ссылке (/nkt/artifact) и зеркало registry (/v2/).
// Признак тот же, что у apt: открыт ли порт. Всё, что хост качает из
// интернета для кластеров и машин, при открытом порте идёт через хаб и
// оседает там; при закрытом — напрямую, как раньше.

// HubCacheURL — адрес кэша хаба или пусто. Порт должен не просто быть
// открыт, а отвечать как наш кэш: на 3142 может сидеть и apt-cacher-ng.
func HubCacheURL(cfg *config.Config) string {
	if cfg == nil || cfg.HubAptCachePort <= 0 || cfg.Mode != config.ModeLocal {
		return ""
	}
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.HubAptCachePort)
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return ""
	}
	c.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/nkt/ping")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "nkt-cache" {
		return ""
	}
	return "http://" + addr
}

func (s *Server) hubCacheURL() string { return HubCacheURL(s.cfg) }
