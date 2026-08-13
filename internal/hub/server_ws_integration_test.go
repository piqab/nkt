package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// TestHubTerminalWebSocketThroughFullRouter is the test TestManagerProxyWebSocketRoundTrip
// could not be: that test dials manager.Proxy(hostID) directly, wrapped in
// its own httptest.NewServer, bypassing Server.Handler() — and with it, the
// hub's own middleware chain, RequireAuth/RequireAdmin, and the real
// /api/auth/login flow — entirely. This one goes through the actual router
// with a real login and a real session cookie, the same path the browser
// takes, closing that coverage gap. (It was written to check a specific
// hypothesis — that the hub's blanket middleware.Timeout broke the WS
// upgrade the same way an equivalent bug once did in internal/api/server.go
// — reverting that route split and re-running this test still passed, so
// that hypothesis was wrong: this chi version's Timeout only wraps the
// request context, not the ResponseWriter, and never touches Hijack. The
// test earns its keep anyway as the only exercise of this exact path.)
func TestHubTerminalWebSocketThroughFullRouter(t *testing.T) {
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)

	repoRoot := findRepoRoot(t)
	nktBin := filepath.Join(t.TempDir(), "nkt")
	buildCmd := exec.Command("go", "build", "-o", nktBin, "./cmd/nkt")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build nkt for the test: %v\n%s", err, out)
	}

	const hostAdminPassword = "integration-test-password-1234"
	remoteDataDir := t.TempDir()
	remoteCmd := exec.Command(nktBin)
	remoteCmd.Dir = repoRoot
	remoteCmd.Env = append(os.Environ(),
		"NKT_MODE=local",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_DATA_DIR="+remoteDataDir,
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD="+hostAdminPassword,
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

	hubCfg := &config.Config{AllowMutations: true, SessionTTL: time.Hour, CookieSecure: false}
	authSvc := auth.NewService(db, hubCfg)
	hash, err := auth.HashPassword("hub-admin-password-1234")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := db.CreateUser(context.Background(), "hubadmin", hash, store.RoleAdmin); err != nil {
		t.Fatalf("create hub admin: %v", err)
	}

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
	adminPasswordEnc, err := secretbox.Encrypt(key, []byte(hostAdminPassword))
	if err != nil {
		t.Fatalf("encrypt host admin password: %v", err)
	}
	if err := db.SetHostAdmin(ctx, hostID, "admin", adminPasswordEnc); err != nil {
		t.Fatalf("SetHostAdmin: %v", err)
	}
	if err := db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}

	manager := NewManager(hubCfg, db, key, "test")

	srv := New(Deps{Cfg: hubCfg, DB: db, Auth: authSvc, Hub: manager, Log: slog.Default()})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	loginBody, _ := json.Marshal(map[string]string{"username": "hubadmin", "password": "hub-admin-password-1234"})
	res, err := client.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", res.StatusCode)
	}

	var cookieHeader string
	for _, c := range jar.Cookies(res.Request.URL) {
		if c.Name == auth.SessionCookie {
			cookieHeader = c.Name + "=" + c.Value
		}
	}
	if cookieHeader == "" {
		t.Fatal("no session cookie captured after login")
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/hosts/" + strconv.FormatInt(hostID, 10) + "/terminal/ws"
	header := http.Header{}
	header.Set("Cookie", cookieHeader)
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("websocket.Dial through the hub's real router: %v", err)
	}
	defer conn.CloseNow()

	const marker = "nkt-hub-full-router-ws-ok"
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
