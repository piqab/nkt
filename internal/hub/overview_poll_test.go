package hub

import (
	"context"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// TestPollOverviewPopulatesFindings is the same real-sshd/real-nkt-subprocess
// setup TestManagerProxyRoundTrip uses, but exercises pollOnce/pollHost
// directly instead of Proxy: after one poll tick, the host's cached
// overview must carry the fixtures dataset's real findings counts, and
// killing the remote process must flip it to unreachable without wiping
// those counts — see pollHost's doc comment for why that's deliberate.
func TestPollOverviewPopulatesFindings(t *testing.T) {
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
		"NKT_ADDR="+remoteAPIAddr,
		"NKT_DATA_DIR="+remoteDataDir,
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD="+adminPassword,
		"NKT_COOKIE_SECURE=false",
		"NKT_SCHEDULER_ENABLED=false",
	)
	if err := remoteCmd.Start(); err != nil {
		t.Fatalf("start remote nkt: %v", err)
	}
	cleanedUp := false
	killRemote := func() {
		if cleanedUp {
			return
		}
		cleanedUp = true
		_ = remoteCmd.Process.Kill()
		_, _ = remoteCmd.Process.Wait()
	}
	t.Cleanup(killRemote)
	waitForLocalHTTP(t, "http://"+remoteAPIAddr+"/api/health")

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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	if _, _, _, ok := manager.Overview(hostID); ok {
		t.Fatalf("Overview before any poll tick should report ok=false")
	}

	manager.pollOnce(ctx)

	findings, reachable, lastPolledAt, ok := manager.Overview(hostID)
	if !ok {
		t.Fatalf("Overview after pollOnce: ok=false, want true")
	}
	if !reachable {
		t.Fatalf("Overview after pollOnce: reachable=false, want true")
	}
	if lastPolledAt.IsZero() {
		t.Errorf("Overview after pollOnce: lastPolledAt is zero")
	}
	if findings["critical"] == 0 && findings["high"] == 0 {
		t.Fatalf("Overview after pollOnce: findings = %+v, want the fixtures dataset's real critical/high counts", findings)
	}

	// Kill the remote and poll again: the host must flip to unreachable
	// without losing the findings counts pollOnce just cached — see
	// pollHost's doc comment.
	killRemote()
	manager.pollOnce(ctx)

	findingsAfter, reachableAfter, _, ok := manager.Overview(hostID)
	if !ok {
		t.Fatalf("Overview after the remote died: ok=false, want true (a stale cache entry, not none)")
	}
	if reachableAfter {
		t.Errorf("Overview after the remote died: reachable=true, want false")
	}
	if findingsAfter["critical"] != findings["critical"] || findingsAfter["high"] != findings["high"] {
		t.Errorf("Overview after the remote died: findings = %+v, want the last-known %+v preserved",
			findingsAfter, findings)
	}
}

// TestPollOverviewSkipsNonOnlineHosts confirms pollOnce never contacts a
// host that isn't store.HostStatusOnline (a "new"/"installing"/"error" host
// has no admin credential recorded yet, or is mid-install — either way,
// nothing to poll).
func TestPollOverviewSkipsNonOnlineHosts(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	_ = db

	m.pollOnce(ctx)

	if _, _, _, ok := m.Overview(id); ok {
		t.Errorf("Overview for a non-online host after pollOnce: ok=true, want false")
	}
}
