package hub

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// TestManagerProxyWebSocketRoundTrip is the one path TestManagerProxyRoundTrip
// never exercised: a WebSocket upgrade going through Manager.Proxy — the hub
// dialing a real host over a real SSH tunnel, exactly the route the browser
// actually uses to reach a managed host's own /api/terminal/ws (or
// /api/updates/ws). httputil.ReverseProxy's WebSocket support hinges on
// http.Transport recognising a 101 response and handing back the raw
// connection for hijacking — that machinery is well documented to work over
// an ordinary dialed net.Conn, but was never actually proven here over one
// obtained via ssh.Client.Dial specifically. A real PTY, a real `echo`, a
// real answer read back off the WebSocket is the only thing that actually
// settles whether it does.
func TestManagerProxyWebSocketRoundTrip(t *testing.T) {
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)

	repoRoot := findRepoRoot(t)
	nktBin := filepath.Join(t.TempDir(), "nkt")
	buildCmd := exec.Command("go", "build", "-o", nktBin, "./cmd/nkt")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build nkt for the test: %v\n%s", err, out)
	}

	// ModeLocal, not fixtures: the terminal endpoint refuses outright in
	// ModeFixtures (see handleTerminalWS) — this test is specifically about
	// the WebSocket upgrade surviving the proxy, so it needs the real thing
	// actually willing to serve it.
	const adminPassword = "integration-test-password-1234"
	remoteDataDir := t.TempDir()
	remoteCmd := exec.Command(nktBin)
	remoteCmd.Dir = repoRoot
	remoteCmd.Env = append(os.Environ(),
		"NKT_MODE=local",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_DATA_DIR="+remoteDataDir,
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD="+adminPassword,
		"NKT_COOKIE_SECURE=false",
		"NKT_SCHEDULER_ENABLED=false",
		"NKT_TERMINAL_ENABLED=true",
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
	me, err := osuser.Current()
	if err != nil {
		t.Fatalf("os/user.Current: %v", err)
	}
	secretEnc, err := secretbox.Encrypt(key, clientKeyPEM)
	if err != nil {
		t.Fatalf("encrypt ssh key: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	hostID, err := db.CreateHost(ctx, "test-host", sshAddr, sshPort, me.Username, store.HostAuthKey, secretEnc)
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

	manager := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))

	// A real network listener, not httptest.NewRecorder(): the WS upgrade
	// needs a real http.Hijacker-backed connection on the client side of
	// this server, which a ResponseRecorder cannot provide.
	ts := httptest.NewServer(manager.Proxy(hostID))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/terminal/ws"
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial through the hub proxy: %v", err)
	}
	defer conn.CloseNow()

	const marker = "nkt-proxy-ws-roundtrip-ok"
	if err := conn.Write(dialCtx, websocket.MessageBinary, []byte("echo "+marker+"\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out strings.Builder
	found := false
	for !found {
		msgType, data, err := conn.Read(dialCtx)
		if err != nil {
			t.Fatalf("read (collected so far: %q): %v", out.String(), err)
		}
		if msgType != websocket.MessageBinary {
			continue
		}
		out.Write(data)
		if strings.Contains(out.String(), marker) {
			found = true
		}
	}

	conn.Close(websocket.StatusNormalClosure, "test done")
}
