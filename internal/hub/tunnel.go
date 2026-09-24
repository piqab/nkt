package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/piqab/nkt/internal/auth"
)

// defaultHostAPIPort — порт собственного API nkt на хосте по умолчанию
// (NKT_HUB_HOST_API_PORT у хаба, api_port в записи хоста — если на нём
// 8077 занят). Loopback: снаружи он недоступен, хаб ходит туда только
// через туннель (проброс по SSH или резервный канал, см. dialFunc).
const defaultHostAPIPort = 8077

// hostAPIAddr — адрес API конкретного хоста для туннеля.
func (m *Manager) hostAPIAddr(ctx context.Context, hostID int64) string {
	m.connsMu.Lock()
	port, ok := m.apiPorts[hostID]
	m.connsMu.Unlock()
	if !ok {
		port = m.cfg.HubHostAPIPort
		if host, err := m.db.HostByID(ctx, hostID); err == nil && host.APIPort > 0 {
			port = host.APIPort
		}
		if port <= 0 {
			port = defaultHostAPIPort
		}
		m.connsMu.Lock()
		m.apiPorts[hostID] = port
		m.connsMu.Unlock()
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// hostAPIAddrFor — то же по уже прочитанной записи, без обращения к базе.
func (m *Manager) hostAPIAddrFor(host store.Host) string {
	port := host.APIPort
	if port <= 0 {
		port = m.cfg.HubHostAPIPort
	}
	if port <= 0 {
		port = defaultHostAPIPort
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// dialFunc opens a connection to a host's own nkt API — satisfied
// identically by *ssh.Client.Dial (the primary path: SSH port-forwarding)
// and by a fallback reverse-tunnel session's Open method wrapped to match
// this shape (see proxy.go's dialerFor and relay.go's relayDial). Every
// helper in this file that used to take a concrete *ssh.Client takes this
// instead, so the install-time health check/login and Server's per-host
// reverse proxy (see server.go) work unchanged over either channel.
type dialFunc func(network, addr string) (net.Conn, error)

// tunnelHTTPClient builds an http.Client whose every request travels
// through dial to a host's own nkt API — no separate port-forward listener
// to manage either way. Reused by the install job's health check and
// login, and by Server's per-host reverse proxy (see server.go).
func tunnelHTTPClient(dial dialFunc, addr string) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return dial("tcp", addr)
			},
		},
	}
}

// waitForHealth polls the remote nkt's /health endpoint through the tunnel
// until it answers or ctx is done — systemctl reporting the unit started
// does not guarantee the HTTP listener is bound yet.
func waitForHealth(ctx context.Context, dial dialFunc, addr string) error {
	httpClient := tunnelHTTPClient(dial, addr)
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return msgs.Errorf("hub.serviceDidAnswerHealthLast", ctx.Err(), lastErr)
			}
			return ctx.Err()
		default:
		}

		if err := probeHealth(ctx, httpClient, addr); err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return nil
	}
}

func probeHealth(ctx context.Context, httpClient *http.Client, addr string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return msgs.Errorf("hub.code", resp.StatusCode)
	}
	return nil
}

// bootstrapLogin logs in to a freshly installed remote nkt as its bootstrap
// admin and returns the session cookie value, so the hub can later replay it
// when proxying requests — the human operator authenticates to the hub only
// once, never to each managed host individually.
func bootstrapLogin(ctx context.Context, dial dialFunc, addr, username, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tunnelHTTPClient(dial, addr).Do(req)
	if err != nil {
		return "", msgs.Errorf("hub.loginRequest", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", msgs.Errorf("hub.loginFailedCode", resp.StatusCode, string(b))
	}
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie {
			return c.Value, nil
		}
	}
	return "", msgs.Errorf("hub.loginResponseHasCookie", auth.SessionCookie)
}
