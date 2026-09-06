package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// TestConfigDiffThroughProxy covers editing a config on a managed host
// through the hub end to end — read, write, list versions, diff — over a
// real SSH tunnel to a real nkt.
//
// Written to check a report that the diff showed nothing after saving
// through the hub. It does not: the proxied path is fine, and the empty
// result came from asking for the diff of the newest version, which by
// definition equals the file on disk (see ConfigManager.Diff). Kept because
// nothing else exercised config editing across the tunnel.
func TestConfigDiffThroughProxy(t *testing.T) {
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)

	repoRoot := findRepoRoot(t)
	nktBin := filepath.Join(t.TempDir(), "nkt")
	buildCmd := exec.Command("go", "build", "-o", nktBin, "./cmd/nkt")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build nkt: %v\n%s", err, out)
	}

	const adminPassword = "integration-test-password-1234"
	// The remote writes into the repo's fixtures, so give it a private copy.
	fixtures := t.TempDir()
	if out, err := exec.Command("cp", "-r", filepath.Join(repoRoot, "fixtures", "host"),
		filepath.Join(fixtures, "host")).CombinedOutput(); err != nil {
		t.Fatalf("copy fixtures: %v\n%s", err, out)
	}

	remoteCmd := exec.Command(nktBin)
	remoteCmd.Dir = repoRoot
	remoteCmd.Env = append(os.Environ(),
		"NKT_MODE=fixtures",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_DATA_DIR="+t.TempDir(),
		"NKT_FIXTURES_ROOT="+filepath.Join(fixtures, "host"),
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
	me, err := osuser.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	secretEnc, err := secretbox.Encrypt(key, clientKeyPEM)
	if err != nil {
		t.Fatalf("encrypt ssh key: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	hostID, err := db.CreateHost(ctx, "test-host", sshAddr, sshPort, me.Username, store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	adminEnc, err := secretbox.Encrypt(key, []byte(adminPassword))
	if err != nil {
		t.Fatalf("encrypt admin password: %v", err)
	}
	if err := db.SetHostAdmin(ctx, hostID, "admin", adminEnc); err != nil {
		t.Fatalf("SetHostAdmin: %v", err)
	}
	if err := db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}

	manager := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	proxy := manager.Proxy(hostID)

	do := func(method, path, body string) (int, string) {
		t.Helper()
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// 1. Pick a config file and read it.
	code, listBody := do(http.MethodGet, "/api/configs", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/configs: %d %s", code, listBody)
	}
	var list struct {
		Files []struct {
			Path     string `json:"path"`
			Editable bool   `json:"editable"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(listBody), &list); err != nil {
		t.Fatalf("decode configs: %v", err)
	}
	var target string
	for _, f := range list.Files {
		if f.Editable {
			target = f.Path
			break
		}
	}
	if target == "" {
		t.Fatalf("no editable config in fixtures")
	}
	t.Logf("target config: %s", target)

	code, readBody := do(http.MethodGet, "/api/configs/file?path="+target, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/configs/file: %d %s", code, readBody)
	}
	var file struct {
		Content string `json:"content"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(readBody), &file); err != nil {
		t.Fatalf("decode file: %v", err)
	}

	// 2. Versions before the edit — note the newest id.
	versionsOf := func() []struct {
		ID     int64  `json:"id"`
		Action string `json:"action"`
	} {
		t.Helper()
		code, body := do(http.MethodGet, "/api/configs/versions?path="+target, "")
		if code != http.StatusOK {
			t.Fatalf("GET /api/configs/versions: %d %s", code, body)
		}
		var v struct {
			Versions []struct {
				ID     int64  `json:"id"`
				Action string `json:"action"`
			} `json:"versions"`
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("decode versions: %v", err)
		}
		return v.Versions
	}

	// 3. Write a change.
	payload, _ := json.Marshal(map[string]any{
		"path":            target,
		"content":         file.Content + "\n# added through the hub\n",
		"note":            "repro",
		"apply":           false,
		"expected_sha256": file.SHA256,
	})
	code, writeBody := do(http.MethodPut, "/api/configs/file", string(payload))
	if code != http.StatusOK {
		t.Fatalf("PUT /api/configs/file: %d %s", code, writeBody)
	}
	t.Logf("write result: %s", writeBody)

	after := versionsOf()
	if len(after) < 2 {
		t.Fatalf("expected at least two versions after an edit, got %d: %+v", len(after), after)
	}
	t.Logf("versions after edit: %+v", after)

	// 4. The diff of the version that preceded the edit must show it.
	// Versions come back newest first, so [1] is the state before the write.
	previous := after[1].ID
	code, diffBody := do(http.MethodGet,
		"/api/configs/versions/"+strconv.FormatInt(previous, 10)+"/diff", "")
	if code != http.StatusOK {
		t.Fatalf("GET diff: %d %s", code, diffBody)
	}
	var diff struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal([]byte(diffBody), &diff); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	t.Logf("diff of version %d:\n%s", previous, diff.Diff)

	if strings.TrimSpace(diff.Diff) == "" {
		t.Fatalf("diff of the pre-edit version is empty through the proxy — reproduced the report")
	}
	if !strings.Contains(diff.Diff, "added through the hub") {
		t.Errorf("diff does not mention the change:\n%s", diff.Diff)
	}
}
