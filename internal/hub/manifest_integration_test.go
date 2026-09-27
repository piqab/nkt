package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Один YAML в кластер: хаб через настоящий SSH-туннель спрашивает у
// control plane (nkt в fixtures) kubectl diff и делает kubectl apply,
// применение сохраняется редакцией с итогом по кластерам.
func TestManifestApplyRoundTrip(t *testing.T) {
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)

	repoRoot := findRepoRoot(t)
	nktBin := filepath.Join(t.TempDir(), "nkt")
	buildCmd := exec.Command("go", "build", "-o", nktBin, "./cmd/nkt")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build nkt for the test: %v\n%s", err, out)
	}
	const adminPassword = "integration-test-password-1234"
	remoteCmd := exec.Command(nktBin)
	remoteCmd.Dir = repoRoot
	remoteCmd.Env = append(os.Environ(),
		"NKT_MODE=fixtures",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_DATA_DIR="+t.TempDir(),
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
		t.Fatal(err)
	}
	me, err := osuser.Current()
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := secretbox.Encrypt(key, clientKeyPEM)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hostID, err := db.CreateHost(ctx, "cp1", sshAddr, sshPort, me.Username, store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatal(err)
	}
	adminEnc, _ := secretbox.Encrypt(key, []byte(adminPassword))
	if err := db.SetHostAdmin(ctx, hostID, "admin", adminEnc); err != nil {
		t.Fatal(err)
	}
	if err := db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
		t.Fatal(err)
	}
	clusterID, err := db.CreateCluster(ctx, store.Cluster{Name: "lab", HostID: hostID, Flavor: "k3s", Topology: "single", Status: store.ClusterReady})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetHostCluster(ctx, hostID, clusterID, "control-plane"); err != nil {
		t.Fatal(err)
	}

	m := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	s := &Server{hub: m, db: db}
	call := func(h http.HandlerFunc, body any, out any) int {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/", bytes.NewReader(raw)).WithContext(ctx)
		rec := httptest.NewRecorder()
		h(rec, req)
		if out != nil {
			_ = json.Unmarshal(rec.Body.Bytes(), out)
		}
		return rec.Code
	}
	manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cluster-info\n  namespace: shop\ndata:\n  owner: ops\n"
	var diff struct {
		Results []ManifestResult `json:"results"`
	}
	if code := call(s.handleManifestDiff, map[string]any{"content": manifest, "clusters": []int64{clusterID, 9999}}, &diff); code != 200 {
		t.Fatalf("diff: %d", code)
	}
	if len(diff.Results) != 2 || diff.Results[0].Error != "" || !strings.Contains(diff.Results[0].Diff, "replicas") || diff.Results[1].Error == "" {
		t.Fatalf("diff: %+v", diff.Results)
	}
	var applied struct {
		Results   []ManifestResult `json:"results"`
		VersionID int64            `json:"version_id"`
	}
	if code := call(s.handleManifestApply, map[string]any{"name": "cluster-info", "content": manifest, "clusters": []int64{clusterID}}, &applied); code != 200 {
		t.Fatalf("apply: %d", code)
	}
	if len(applied.Results) != 1 || applied.Results[0].Error != "" || !strings.Contains(applied.Results[0].Output, "configured") || applied.VersionID == 0 {
		t.Fatalf("apply: %+v", applied)
	}
	v, err := db.ManifestVersion(ctx, applied.VersionID)
	if err != nil || v.Content != manifest || !strings.Contains(v.Results, `"cluster":"lab"`) {
		t.Fatalf("версия: %+v %v", v, err)
	}
	if code := call(s.handleManifestApply, map[string]any{"name": "x", "content": "kind: Broken\n", "clusters": []int64{clusterID}}, nil); code != 400 {
		t.Errorf("битый манифест: %d", code)
	}
}
