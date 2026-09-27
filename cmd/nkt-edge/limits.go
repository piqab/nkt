package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// limiter — не больше rate запросов в минуту с одного адреса.
type limiter struct {
	rate int
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter(rate int) *limiter { return &limiter{rate: rate, hits: map[string][]time.Time{}} }

func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.rate {
		l.hits[ip] = recent
		return false
	}
	l.hits[ip] = append(recent, now)
	if len(l.hits) > 10000 {
		// Память не растёт от перебора адресов: старые записи — прочь.
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > time.Minute {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// githubNets — адреса, с которых GitHub шлёт вебхуки (api.github.com/meta).
type githubNets struct {
	mu   sync.RWMutex
	nets []*net.IPNet
}

func (g *githubNets) refreshLoop() {
	for {
		if err := g.refresh(); err != nil {
			log.Printf("github meta: %v", err)
		}
		time.Sleep(6 * time.Hour)
	}
}

func (g *githubNets) refresh() error {
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Get("https://api.github.com/meta")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var meta struct {
		Hooks []string `json:"hooks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return err
	}
	var nets []*net.IPNet
	for _, c := range meta.Hooks {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}
	g.mu.Lock()
	g.nets = nets
	g.mu.Unlock()
	return nil
}

func (g *githubNets) contains(ip net.IP) bool {
	if ip == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, n := range g.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
