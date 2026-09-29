package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// sitesServer — сервер на копии фикстур с ответами docker compose и
// настоящим менеджером заданий.
func sitesServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "fixtures", "host"))); err != nil {
		t.Fatal(err)
	}
	idxPath := filepath.Join(root, ".commands", "index.json")
	raw, _ := os.ReadFile(idxPath)
	var idx map[string]any
	_ = json.Unmarshal(raw, &idx)
	cmds := idx["commands"].([]any)
	cmds = append(cmds, map[string]any{"match": []string{"docker", "compose"},
		"stdout": `{"services":{"web":{"ports":[]},"db":{"ports":[{"target":5432,"published":"5432","host_ip":"0.0.0.0"}]}}}`})
	idx["commands"] = cmds
	raw, _ = json.Marshal(idx)
	_ = os.WriteFile(idxPath, raw, 0o644)

	// Действующий сертификат на shop.example.com — выпуск не нужен.
	live := filepath.Join(root, "etc", "letsencrypt", "live", "shop.example.com")
	_ = os.MkdirAll(live, 0o755)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "shop.example.com"},
		DNSNames: []string{"shop.example.com"}, NotBefore: time.Now(), NotAfter: time.Now().AddDate(0, 0, 80)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	_ = os.WriteFile(filepath.Join(live, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	kb, _ := x509.MarshalECPrivateKey(key)
	_ = os.WriteFile(filepath.Join(live, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)

	c := collect.NewFixtures(root)
	cfg := &config.Config{Mode: config.ModeFixtures, DataDir: t.TempDir(), NginxRoot: "/etc/nginx", NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyRoot: "/etc/haproxy", HAProxyMainConf: "/etc/haproxy/haproxy.cfg", CaddyRoot: "/etc/caddy", CaddyMainConfig: "/etc/caddy/Caddyfile",
		Fail2banRoot: "/etc/fail2ban", CommandTimeout: 5 * time.Second, CertbotTimeout: time.Minute}
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := control.NewServiceManager(cfg, c, db)
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	s := &Server{cfg: cfg, db: db, scanner: scanner, services: svc, configs: control.NewConfigManager(cfg, c, db, scanner, svc),
		certs: control.NewCertManager(cfg, c, db, svc, scanner, nil), firewall: control.NewFirewallManager(cfg, c, db),
		firewalld: control.NewFirewalldManager(cfg, c, db), jobs: jm, sessions: map[string]*updateSession{}}
	jm.Register(KindComposeDeploy, &composeDeployRunner{s})
	jm.Register(KindSiteApply, &siteRunner{s})
	return s, root
}

func adminReq(method, url string, body any) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, url, &buf)
	return r.WithContext(auth.WithUser(r.Context(), store.User{Username: "admin", Role: store.RoleAdmin}))
}

func waitJob(t *testing.T, s *Server, id int64) store.Job {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		j, err := s.db.JobByID(context.Background(), id)
		if err == nil && j.Status != store.JobQueued && j.Status != store.JobRunning {
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return store.Job{}
}

func jobText(t *testing.T, s *Server, id int64) string {
	lines, _ := s.db.JobLog(context.Background(), id, 0, 1000)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

func TestComposeDeployAndSite(t *testing.T) {
	s, root := sitesServer(t)
	env := "DB_PASSWORD=secret\n"
	rec := httptest.NewRecorder()
	s.handleComposeDeploy(rec, adminReq("POST", "/compose/stacks/deploy", map[string]any{
		"project": "shop", "file": "compose.yaml", "pull": true, "wait_timeout": 30, "env": env,
		"files": map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx\n", "conf/app.ini": "a=1\n"},
	}))
	var out struct {
		JobID int64 `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out.JobID == 0 {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body)
	}
	if j := waitJob(t, s, out.JobID); j.Status != store.JobSucceeded {
		t.Fatalf("compose job: %+v\n%s", j, jobText(t, s, out.JobID))
	}
	if b, _ := os.ReadFile(filepath.Join(root, "srv", "compose", "shop", "conf", "app.ini")); string(b) != "a=1\n" {
		t.Fatalf("nested file: %q", b)
	}
	if st, err := os.Stat(filepath.Join(root, "srv", "compose", "shop", ".env")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf(".env: %v %v", st, err)
	}
	for _, bad := range []map[string]string{{"compose.yaml": "x", "../evil": "x"}, {"compose.yaml": "x", ".env": "x"}} {
		rec := httptest.NewRecorder()
		s.handleComposeDeploy(rec, adminReq("POST", "/compose/stacks/deploy", map[string]any{"project": "shop", "file": "compose.yaml", "files": bad}))
		if rec.Code != 400 {
			t.Fatalf("bad files accepted: %v %d", bad, rec.Code)
		}
	}

	rec = httptest.NewRecorder()
	s.handleSitePreflight(rec, adminReq("GET", "/sites/preflight", nil))
	var pre struct {
		Proxies []SiteProxyState `json:"proxies"`
		Stacks  []SiteStack      `json:"stacks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pre)
	found := false
	for _, st := range pre.Stacks {
		if st.Name == "shop" && len(st.Services) == 2 {
			found = true
		}
	}
	if !found || len(pre.Proxies) != 3 {
		t.Fatalf("preflight: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	s.handleSiteApply(rec, adminReq("POST", "/sites/apply", map[string]any{
		"domains": []string{"shop.example.com"}, "proxy": "nginx", "stack": "shop", "service": "web", "container_port": 80,
	}))
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 {
		t.Fatalf("site apply: %d %s", rec.Code, rec.Body)
	}
	if j := waitJob(t, s, out.JobID); j.Status != store.JobSucceeded {
		t.Fatalf("site job: %+v\n%s", j, jobText(t, s, out.JobID))
	}
	conf, err := os.ReadFile(filepath.Join(root, "etc", "nginx", "conf.d", "nkt-shop.example.com.conf"))
	if err != nil || !strings.Contains(string(conf), "proxy_pass http://127.0.0.1:18000;") || !strings.Contains(string(conf), "live/shop.example.com/fullchain.pem") {
		t.Fatalf("nginx conf: %v\n%s\n%s", err, conf, jobText(t, s, out.JobID))
	}
	ov, _ := os.ReadFile(filepath.Join(root, "srv", "compose", "shop", "compose.nkt.yml"))
	if !strings.Contains(string(ov), "127.0.0.1:18000:80") {
		t.Fatalf("override: %s", ov)
	}
	if sites := s.hostSites(context.Background()); len(sites) != 1 || sites[0].Upstream != "127.0.0.1:18000" || sites[0].Lineage != "shop.example.com" {
		t.Fatalf("registry: %+v", sites)
	}
	rec = httptest.NewRecorder()
	s.handleSiteRemove(rec, adminReq("POST", "/sites/remove", map[string]string{"domain": "shop.example.com"}))
	if rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "nginx", "conf.d", "nkt-shop.example.com.conf")); err == nil {
		t.Fatal("nginx conf not removed")
	}
}
