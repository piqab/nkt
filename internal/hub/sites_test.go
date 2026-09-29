package hub

import (
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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/api"
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Сайт на машине хаба целиком: задание хаба → встроенный API (копия
// фикстур) → задание хоста (публикация сервиса стека, существующий
// сертификат, nginx conf.d) → состояние сайта.
// localFixtureHub — хаб, у которого машина хаба («localhost») — копия
// фикстур со встроенным API и общим менеджером заданий.
func localFixtureHub(t *testing.T) (*Server, *store.DB, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "fixtures", "host"))); err != nil {
		t.Fatal(err)
	}
	idxPath := filepath.Join(root, ".commands", "index.json")
	raw, _ := os.ReadFile(idxPath)
	var idx map[string]any
	_ = json.Unmarshal(raw, &idx)
	idx["commands"] = append(idx["commands"].([]any), map[string]any{"match": []string{"docker", "compose"},
		"stdout": `{"services":{"web":{"ports":[]}}}`})
	raw, _ = json.Marshal(idx)
	_ = os.WriteFile(idxPath, raw, 0o644)
	stack := filepath.Join(root, "srv", "compose", "shop")
	_ = os.MkdirAll(stack, 0o755)
	_ = os.WriteFile(filepath.Join(stack, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0o644)
	live := filepath.Join(root, "etc", "letsencrypt", "live", "shop.example.com")
	_ = os.MkdirAll(live, 0o755)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "shop.example.com"},
		DNSNames: []string{"shop.example.com"}, NotBefore: time.Now(), NotAfter: time.Now().AddDate(0, 0, 80)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	_ = os.WriteFile(filepath.Join(live, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	kb, _ := x509.MarshalECPrivateKey(key)
	_ = os.WriteFile(filepath.Join(live, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)

	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{Mode: config.ModeFixtures, FixturesRoot: root, DataDir: t.TempDir(),
		NginxRoot: "/etc/nginx", NginxMainConfig: "/etc/nginx/nginx.conf", HAProxyRoot: "/etc/haproxy", HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		CaddyRoot: "/etc/caddy", CaddyMainConfig: "/etc/caddy/Caddyfile", CommandTimeout: 5 * time.Second, CertbotTimeout: time.Minute,
		AllowMutations: true, SessionTTL: time.Hour}
	authSvc := auth.NewService(db, cfg)
	hash, _ := auth.HashPassword("admin-password-1234")
	if _, err := db.CreateUser(context.Background(), "admin", hash, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	collector := collect.NewFixtures(root)
	scanner := inventory.New(cfg, collector, db)
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	services := control.NewServiceManager(cfg, collector, db)
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	localAPI := api.New(api.Deps{
		Cfg: cfg, DB: db, Auth: authSvc, Scanner: scanner, Services: services,
		Configs:   control.NewConfigManager(cfg, collector, db, scanner, services),
		Firewall:  control.NewFirewallManager(cfg, collector, db),
		Firewalld: control.NewFirewalldManager(cfg, collector, db),
		Certs:     control.NewCertManager(cfg, collector, db, services, scanner, nil),
		Jobs:      jm, Log: slog.New(slog.DiscardHandler), Version: "test",
	})
	secret, _ := secretbox.GenerateKey()
	manager := NewManager(cfg, db, secret, "test", slog.New(slog.DiscardHandler))
	srv := New(Deps{Cfg: cfg, DB: db, Auth: authSvc, Hub: manager, Local: localAPI.Handler(), LocalScanner: scanner,
		Log: slog.New(slog.DiscardHandler), Jobs: jm})
	jm.Register(KindSiteSetup, NewSiteSetupRunner(srv))
	jm.Register(KindDeploy, NewDeployRunner(srv))

	return srv, db, root
}

func TestSiteSetupOnLocal(t *testing.T) {
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	id, err := db.SaveSite(ctx, store.Site{Domains: []string{"shop.example.com"}, HostID: localHostID, Proxy: "nginx",
		Stack: "shop", Service: "web", ContainerPort: 80, OpenFirewall: true, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := srv.startSiteSetup(ctx, "admin", id, "shop.example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	var job store.Job
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		job, _ = db.JobByID(ctx, jobID)
		if job.Status != store.JobQueued && job.Status != store.JobRunning {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	lines, _ := db.JobLog(ctx, jobID, 0, 1000)
	var log strings.Builder
	for _, l := range lines {
		log.WriteString(l.Text + "\n")
	}
	if job.Status != store.JobSucceeded {
		t.Fatalf("job %s: %s\n%s", job.Status, job.Error, log.String())
	}
	if !strings.Contains(log.String(), "127.0.0.1:18000") {
		t.Fatalf("host job log not relayed:\n%s", log.String())
	}
	conf, err := os.ReadFile(filepath.Join(root, "etc", "nginx", "conf.d", "nkt-shop.example.com.conf"))
	if err != nil || !strings.Contains(string(conf), "proxy_pass http://127.0.0.1:18000;") {
		t.Fatalf("nginx conf: %v %s", err, conf)
	}
	st, _ := db.SiteByID(ctx, id)
	if st.Status != store.SiteOK || len(st.Check) == 0 {
		t.Fatalf("site: %+v", st)
	}
}

// Конвейер compose на машину хаба: файлы стека из репозитория (с
// подстановкой тега) и .env из секрета конвейера — в /srv/compose/<стек>,
// задание хоста поднимает стек.
func TestDeployComposeOnLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.MkdirAll(filepath.Join(repo, "deploy", "conf"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "deploy", "docker-compose.yml"), []byte("services:\n  web:\n    image: ghcr.io/org/app:{{nkt.tag}}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "deploy", "conf", "app.ini"), []byte("x=1\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	content := "repo: " + repo + "\nref: main\naction: compose\ncompose:\n  file: deploy/docker-compose.yml\n  project: app\n  hosts: [localhost]\n  files: [deploy/conf/]\n  wait_timeout: 1m\n"
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	pid, err := db.CreatePipeline(ctx, store.Pipeline{Name: "app", Content: content, HookID: "hook-compose-1", HookSecret: secret, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	envEnc, _ := secretbox.Encrypt(srv.hub.key, []byte("TOKEN=abc\n"))
	_ = db.SetPipelineEnv(ctx, pid, envEnc)
	pl, _ := db.PipelineByID(ctx, pid)
	// Как по вебхуку: автор выкладки — не учётная запись.
	d, err := srv.startDeployment(ctx, pl, store.Deployment{Trigger: "webhook", Tag: "v2.0.0", Author: "github"}, false)
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	got := waitDeployment(t, wctx, db, d.ID)
	if got.Status != store.DeploySucceeded {
		t.Fatalf("deployment: %+v\n%s", got, jobLogText(t, ctx, db, d.JobID))
	}
	compose, _ := os.ReadFile(filepath.Join(root, "srv", "compose", "app", "docker-compose.yml"))
	ini, _ := os.ReadFile(filepath.Join(root, "srv", "compose", "app", "conf", "app.ini"))
	env, _ := os.ReadFile(filepath.Join(root, "srv", "compose", "app", ".env"))
	if !strings.Contains(string(compose), "app:v2.0.0") || string(ini) != "x=1\n" || string(env) != "TOKEN=abc\n" {
		t.Fatalf("stack files: %q %q %q", compose, ini, env)
	}
}

// Сухой прогон: журнал говорит, что было бы, а стек на хосте не тронут.
func TestDryRunComposeOnLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.MkdirAll(filepath.Join(repo, "deploy"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "deploy", "docker-compose.yml"), []byte("services:\n  web:\n    image: ghcr.io/org/app:{{nkt.tag}}\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	content := "repo: " + repo + "\nref: main\naction: compose\ncompose:\n  file: deploy/docker-compose.yml\n  project: dry\n  hosts: [localhost]\n"
	run := func(content string) (store.Job, string) {
		t.Helper()
		id, err := srv.jobs.Start(ctx, jobs.Spec{Kind: KindDeploy, Queue: "deploy:dryrun", Author: "admin", Steps: 3,
			Params: DeployParams{DryRun: true, Content: content}})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			j, _ := db.JobByID(ctx, id)
			if j.Status == store.JobSucceeded || j.Status == store.JobFailed {
				return j, jobLogText(t, ctx, db, id)
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("dry run did not finish")
		return store.Job{}, ""
	}
	j, log := run(content)
	if j.Status != store.JobSucceeded || !strings.Contains(log, "dry") {
		t.Fatalf("dry run: %+v\n%s", j, log)
	}
	if _, err := os.Stat(filepath.Join(root, "srv", "compose", "dry")); !os.IsNotExist(err) {
		t.Fatalf("dry run touched the host: %v", err)
	}
	// Неизвестный хост — ошибка до хостов; не compose — отказ.
	if j, log := run(strings.Replace(content, "[localhost]", "[nope]", 1)); j.Status != store.JobFailed {
		t.Fatalf("unknown host accepted:\n%s", log)
	}
	if j, _ := run("repo: " + repo + "\nref: main\naction: script\nscript: {run: 'true'}\n"); j.Status != store.JobFailed {
		t.Fatal("non-compose dry run accepted")
	}
}
