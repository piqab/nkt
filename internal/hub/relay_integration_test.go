package hub

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
	"github.com/althq/netknownsthat/internal/tlscert"
	"github.com/althq/netknownsthat/internal/tunnel"
)

// TestProxyFallsBackToRelayWhenSSHUnreachable is the reverse-tunnel
// counterpart to TestManagerProxyRoundTrip: a real nkt binary (fixtures
// mode) reached through Manager.Proxy — except this host's SSH address
// deliberately refuses connections (nothing listens on it), the way a
// blocked or misconfigured inbound port 22 looks in practice, and the ONLY
// way to it is the hub's own tunnelDialOnce dialing a real internal/tunnel
// listener. Success here proves the whole path end to end: dialerFor's
// SSH-then-relay fallback, the TLS+token+yamux transport, and
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

	// The host-side tunnel listener — a real internal/tunnel.Run, exactly
	// what cmd/nkt/main.go's runServer starts on a managed host with
	// TunnelEnabled on, piping accepted streams to the fixtures nkt above.
	const token = "relay-integration-test-token"
	tunnelAddr := startTestTunnelListener(t, token, "127.0.0.1:8077")
	_, tunnelPortStr, err := net.SplitHostPort(tunnelAddr)
	if err != nil {
		t.Fatalf("split tunnel listener addr: %v", err)
	}
	tunnelPort, err := strconv.Atoi(tunnelPortStr)
	if err != nil {
		t.Fatalf("parse tunnel listener port: %v", err)
	}

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
	if err := db.SetHostTunnelEnabled(ctx, hostID, true); err != nil {
		t.Fatalf("SetHostTunnelEnabled: %v", err)
	}
	tokenEnc, err := secretbox.Encrypt(key, []byte(token))
	if err != nil {
		t.Fatalf("encrypt tunnel token: %v", err)
	}
	if err := db.SetHostTunnelToken(ctx, hostID, tokenEnc); err != nil {
		t.Fatalf("SetHostTunnelToken: %v", err)
	}

	cfg := &config.Config{SessionTTL: time.Hour, HubTunnelPort: tunnelPort}
	manager := NewManager(cfg, db, key, "test", slog.New(slog.DiscardHandler))

	dialerCtx, stopDialer := context.WithCancel(context.Background())
	t.Cleanup(stopDialer)
	go manager.runTunnelDialer(dialerCtx, hostID)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := manager.relayDial(hostID); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tunnel dialer never registered a relay session with the hub")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Confirm SSH genuinely doesn't work here — otherwise a passing test
	// below wouldn't actually prove the relay fallback did anything.
	if _, err := manager.clientFor(ctx, hostID); err == nil {
		t.Fatal("test setup bug: clientFor unexpectedly succeeded against a port nothing listens on")
	}

	// /api/health needs no session — proves the relay transport itself
	// works (TLS + token auth + yamux, byte-for-byte).
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

// startTestTunnelListener starts a real internal/tunnel.Run listener on an
// ephemeral port and returns its address once it's actually accepting
// connections.
func startTestTunnelListener(t *testing.T, token, localAddr string) string {
	t.Helper()

	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := tlscert.EnsureSelfSigned(certFile, keyFile, []string{"nkt-tunnel-test"}); err != nil {
		t.Fatalf("EnsureSelfSigned: %v", err)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick a free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = tunnel.Run(ctx, tunnel.ListenerConfig{
			ListenAddr: addr,
			Token:      token,
			TLSCert:    cert,
			LocalAddr:  localAddr,
			Log:        slog.New(slog.DiscardHandler),
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tunnel listener at %s never came up", addr)
	return ""
}

// TestTunnelDialOncePinsCertOnFirstConnectAndRejectsLaterMismatch exercises
// verifyPinnedTunnelCert (tunnelpin.go) against two real internal/tunnel
// listeners, each with its own genuinely distinct self-signed certificate
// (startTestTunnelListener generates a fresh keypair every call) — the
// integration-level counterpart to TestVerifyPinnedTunnelCert's pure-value
// checks: proves the pin actually gets persisted through a real TLS
// handshake+DB round trip, and that a second host reachable at the same
// address but presenting a different certificate is refused rather than
// silently trusted the way plain InsecureSkipVerify used to.
func TestTunnelDialOncePinsCertOnFirstConnectAndRejectsLaterMismatch(t *testing.T) {
	const token = "pin-test-token"

	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open hub store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	secretEnc, _ := secretbox.Encrypt(key, []byte("unused-ssh-secret"))
	hostID, err := db.CreateHost(context.Background(), "pin-test-host", "127.0.0.1", 1, "root", store.HostAuthPassword, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	tokenEnc, _ := secretbox.Encrypt(key, []byte(token))
	if err := db.SetHostTunnelToken(context.Background(), hostID, tokenEnc); err != nil {
		t.Fatalf("SetHostTunnelToken: %v", err)
	}

	// First listener: a fresh cert, nothing pinned yet — tunnelDialOnce
	// must accept it (trust-on-first-use) and record its fingerprint.
	addrA := startTestTunnelListener(t, token, "127.0.0.1:8077")
	_, portAStr, _ := net.SplitHostPort(addrA)
	portA, _ := strconv.Atoi(portAStr)

	managerA := NewManager(&config.Config{SessionTTL: time.Hour, HubTunnelPort: portA}, db, key, "test", slog.New(slog.DiscardHandler))
	ctxA, cancelA := context.WithTimeout(context.Background(), 5*time.Second)
	connected, err := managerA.tunnelDialOnce(ctxA, hostID)
	cancelA()
	if err != nil {
		t.Fatalf("tunnelDialOnce against the first listener: %v", err)
	}
	if !connected {
		t.Fatal("tunnelDialOnce against the first listener: connected = false, want true")
	}

	host, err := db.HostByID(context.Background(), hostID)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if len(host.TunnelCertSHA256) == 0 {
		t.Fatal("host.TunnelCertSHA256 is empty after the first successful dial, want a pinned fingerprint")
	}
	pinned := host.TunnelCertSHA256

	// Second listener at a different port with its own distinct
	// certificate — stands in for the same host address later presenting
	// a different certificate (an on-path attacker, or a reinstall that
	// forgot to reset the pin). tunnelDialOnce must refuse it outright.
	addrB := startTestTunnelListener(t, token, "127.0.0.1:8077")
	_, portBStr, _ := net.SplitHostPort(addrB)
	portB, _ := strconv.Atoi(portBStr)

	managerB := NewManager(&config.Config{SessionTTL: time.Hour, HubTunnelPort: portB}, db, key, "test", slog.New(slog.DiscardHandler))
	ctxB, cancelB := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = managerB.tunnelDialOnce(ctxB, hostID)
	cancelB()
	var mismatch *tunnelCertMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("tunnelDialOnce against the second (differently-certed) listener: err = %v, want a *tunnelCertMismatchError (and want it to fail fast, not via ctx timeout)", err)
	}

	host, err = db.HostByID(context.Background(), hostID)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if !bytes.Equal(host.TunnelCertSHA256, pinned) {
		t.Errorf("host.TunnelCertSHA256 changed after a rejected dial: got %x, want it to stay %x", host.TunnelCertSHA256, pinned)
	}
}
