package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/althq/netknownsthat/internal/auth"
)

// remoteAPIAddr is where a host's own nkt listens, per NKT_ADDR in
// renderEnv — loopback only, reachable exclusively through the SSH tunnel
// dialed below, never directly over the network.
const remoteAPIAddr = "127.0.0.1:8077"

// tunnelHTTPClient builds an http.Client whose every request travels through
// an already-established SSH connection to a host's own nkt API — dialing
// straight from the remote side of that connection, with no separate
// port-forward listener to manage. Reused by the install job's health check
// and login, and by Server's per-host reverse proxy (see server.go).
func tunnelHTTPClient(client *ssh.Client) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return client.Dial("tcp", remoteAPIAddr)
			},
		},
	}
}

// waitForHealth polls the remote nkt's /health endpoint through the tunnel
// until it answers or ctx is done — systemctl reporting the unit started
// does not guarantee the HTTP listener is bound yet.
func waitForHealth(ctx context.Context, client *ssh.Client) error {
	httpClient := tunnelHTTPClient(client)
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("сервис не ответил на /health: %w (последняя ошибка: %v)", ctx.Err(), lastErr)
			}
			return ctx.Err()
		default:
		}

		if err := probeHealth(ctx, httpClient); err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return nil
	}
}

func probeHealth(ctx context.Context, httpClient *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+remoteAPIAddr+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("код %d", resp.StatusCode)
	}
	return nil
}

// bootstrapLogin logs in to a freshly installed remote nkt as its bootstrap
// admin and returns the session cookie value, so the hub can later replay it
// when proxying requests — the human operator authenticates to the hub only
// once, never to each managed host individually.
func bootstrapLogin(ctx context.Context, client *ssh.Client, username, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+remoteAPIAddr+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tunnelHTTPClient(client).Do(req)
	if err != nil {
		return "", fmt.Errorf("запрос входа: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("вход не удался (код %d): %s", resp.StatusCode, string(b))
	}
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("ответ на вход не содержит cookie %s", auth.SessionCookie)
}
