package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
	"github.com/althq/netknownsthat/internal/tunnel"
)

// TestProxyFallsBackToRelayWhenSSHUnreachable is the reverse-tunnel
// counterpart to TestManagerProxyRoundTrip: a real nkt binary (fixtures
// mode) reached through Manager.Proxy — except this host's SSH address
// deliberately refuses connections (nothing listens on it), the way a
// blocked or misconfigured inbound port 22 looks in practice, and the
// ONLY way to it is a real internal/tunnel.Client dialing a real hub TLS
// server. Success here proves the whole path end to end: dialerFor's
// SSH-then-relay fallback, the WebSocket+yamux transport, token auth, and
// Manager.Proxy/cookieFor working unmodified over it.
func TestProxyFallsBackToRelayWhenSSHUnreachable(t *testing.T) {
	repoRoot := findRepoRoot(t)
	nktBin := filepath.Join(t.TempDir(), "nkt")
	buildCmd := exec.Command("go", "build", "-o", nktBin, "./cmd/nkt")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build nkt for the test: %v\n%s", err, out)
	}

	const adminPassword = "integration-test-password-1234"
	remoteDataDir := t.TempDir()
	remoteCmd := exec.Command(nktBin)
	remoteCmd.Dir = repoRoot // fixtures mode reads ./fixtures/host by default
	remoteCmd.Env = append(os.Environ(),
		"NKT_MODE=fixtures",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_DATA_DIR="+remoteDataDir,
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD="+adminPassword,
		"NKT_COOKIE_SECURE=false",
		"NKT_SCHEDULER_ENABLED=false",
	)
	if err := remoteCmd.Start(); err != nil {
		t.Fatalf("start remote nkt: %v", err)
	}
	t.Cleanup(func() {
		_ = remoteCmd.Process.Kill()
		_, _ = remoteCmd.Process.Wait()
	})
	waitForLocalHTTP(t, "http://127.0.0.1:8077/api/health")

	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open hub store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Port 1 — nothing binds a privileged port like that in a test
	// sandbox, so this fails fast with connection refused, standing in
	// for "SSH is unreachable" without needing to actually firewall
	// anything.
	secretEnc, err := secretbox.Encrypt(key, []byte("unused-ssh-secret"))
	if err != nil {
		t.Fatalf("encrypt ssh secret: %v", err)
	}
	hostID, err := db.CreateHost(ctx, "test-host", "127.0.0.1", 1, "root", store.HostAuthPassword, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	adminPasswordEnc, err := secretbox.Encrypt(key, []byte(adminPassword))
	if err != nil {
		t.Fatalf("encrypt admin password: %v", err)
	}
	if err := db.SetHostAdmin(ctx, hostID, "admin", adminPasswordEnc); err != nil {
		t.Fatalf("SetHostAdmin: %v", err)
	}
	if err := db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}
	const token = "relay-integration-test-token"
	if err := db.SetHostTunnelToken(ctx, hostID, tunnel.TokenHash(token)); err != nil {
		t.Fatalf("SetHostTunnelToken: %v", err)
	}

	cfg := &config.Config{SessionTTL: time.Hour}
	manager := NewManager(cfg, db, key, "test")
	authSvc := auth.NewService(db, cfg)
	srv := New(Deps{Cfg: cfg, DB: db, Auth: authSvc, Hub: manager, Log: slog.New(slog.DiscardHandler)})

	hubSrv := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(hubSrv.Close)
	certSum := sha256.Sum256(hubSrv.Certificate().Raw)

	tunnelCtx, stopTunnel := context.WithCancel(context.Background())
	t.Cleanup(stopTunnel)
	go tunnel.Run(tunnelCtx, tunnel.ClientConfig{
		HubAddr:          hubSrv.Listener.Addr().String(),
		HostID:           strconv.FormatInt(hostID, 10),
		Token:            token,
		PinnedCertSHA256: hex.EncodeToString(certSum[:]),
		LocalAddr:        "127.0.0.1:8077", // the real fixtures nkt above
		Log:              slog.New(slog.DiscardHandler),
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := manager.relayDial(hostID); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tunnel client never registered its relay session with the hub")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Confirm SSH genuinely doesn't work here — otherwise a passing test
	// below wouldn't actually prove the relay fallback did anything.
	if _, err := manager.clientFor(ctx, hostID); err == nil {
		t.Fatal("test setup bug: clientFor unexpectedly succeeded against a port nothing listens on")
	}

	// /api/health needs no session — proves the relay transport itself
	// works (WebSocket + yamux + token auth, byte-for-byte).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	manager.Proxy(hostID).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health through the relay fallback: status %d, body %s", rec.Code, rec.Body.String())
	}

	// /api/auth/me needs the session cookie cookieFor/bootstrapLogin
	// obtains over the SAME relay dialFunc — proves the login-and-replay
	// path also works unmodified over this channel.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	manager.Proxy(hostID).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/me through the relay fallback: status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"admin"`) {
		t.Errorf("GET /api/auth/me through the relay fallback: expected the admin username in body, got %s", rec.Body.String())
	}
}

// TestHandleTunnelRejectsBadAuth confirms the WebSocket upgrade endpoint
// itself refuses an unknown host id, a host with no tunnel token
// configured, and a wrong token — each without ever reaching
// websocket.Accept (a 401/400 plain HTTP response, not an upgraded
// connection that then gets closed).
func TestHandleTunnelRejectsBadAuth(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	hostID, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	cfg := &config.Config{SessionTTL: time.Hour}
	authSvc := auth.NewService(db, cfg)
	srv := New(Deps{Cfg: cfg, DB: db, Auth: authSvc, Hub: m, Log: slog.New(slog.DiscardHandler)})

	t.Run("unknown host id", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tunnel.Path, nil)
		req.Header.Set(tunnel.HeaderHostID, "999999")
		req.Header.Set(tunnel.HeaderToken, "whatever")
		srv.handleTunnel(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("tunnel never configured for this host", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tunnel.Path, nil)
		req.Header.Set(tunnel.HeaderHostID, strconv.FormatInt(hostID, 10))
		req.Header.Set(tunnel.HeaderToken, "whatever")
		srv.handleTunnel(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		if err := db.SetHostTunnelToken(ctx, hostID, tunnel.TokenHash("the-real-token")); err != nil {
			t.Fatalf("SetHostTunnelToken: %v", err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tunnel.Path, nil)
		req.Header.Set(tunnel.HeaderHostID, strconv.FormatInt(hostID, 10))
		req.Header.Set(tunnel.HeaderToken, "not-the-real-token")
		srv.handleTunnel(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("missing headers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tunnel.Path, nil)
		srv.handleTunnel(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}
