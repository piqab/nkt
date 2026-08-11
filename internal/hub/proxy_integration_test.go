package hub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// TestManagerProxyRoundTrip is the closest thing to an end-to-end test of
// the hub's proxy without a second real machine: it runs an actual nkt
// binary (fixtures mode, so no root/real host needed) and reaches it only
// through Manager.Proxy — dialSSH, the tunnel and bootstrapLogin exactly as
// production would, just co-located on one box for the test. It does not
// exercise StartInstall's systemctl step, which genuinely needs a second
// machine — see the plan's own manual verification step for that.
func TestManagerProxyRoundTrip(t *testing.T) {
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)

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

	// Build the host registry entry the proxy needs, exactly as StartInstall
	// would have left it after a successful install.
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

	manager := NewManager(&config.Config{}, db, key, "test")

	// /api/health needs no session — proves the tunnel itself works.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	manager.Proxy(hostID).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health through proxy: status %d, body %s", rec.Code, rec.Body.String())
	}

	// /api/auth/me needs the session cookie Manager.Proxy injects via
	// cookieFor/bootstrapLogin — proves the login-and-replay path works.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	manager.Proxy(hostID).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/me through proxy: status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"admin"`) {
		t.Errorf("GET /api/auth/me through proxy: expected the admin username in body, got %s", rec.Body.String())
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	// internal/hub -> repo root
	return filepath.Join(wd, "..", "..")
}

func waitForLocalHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never answered: %v", url, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
