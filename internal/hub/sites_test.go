package hub

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
	"io"
	"log/slog"
	"math/big"
	"net/http"
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
	jm.Register(KindSiteRemove, NewSiteRemoveRunner(srv))
	jm.Register(KindPipelineRemove, NewPipelineRemoveRunner(srv))

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
	if pl, _ := db.PipelineByID(ctx, pid); pl.EnvSHA != envSHA("TOKEN=abc\n") {
		t.Fatalf("env sha not remembered: %q", pl.EnvSHA)
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

// После бана на всех хостах сводка машины хаба — свежая: список
// забаненных на хабе не ждёт следующего опроса.
func TestF2BFleetRefreshesSummary(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	srv.jobs.Register(KindF2BFleet, NewF2BFleetRunner(srv))
	ctx := context.Background()
	before := srv.localScanner.Latest()
	id, err := srv.jobs.Start(ctx, jobs.Spec{Kind: KindF2BFleet, Queue: "f2b", Author: "admin", Steps: 1,
		Params: F2BFleetParams{Action: "ban", IPs: []string{"203.0.113.9"}, HostIDs: []int64{localHostID}}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	var j store.Job
	for time.Now().Before(deadline) {
		if j, _ = db.JobByID(ctx, id); j.Status == store.JobSucceeded || j.Status == store.JobFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if j.Status != store.JobSucceeded {
		t.Fatalf("fleet: %+v\n%s", j, jobLogText(t, ctx, db, id))
	}
	if srv.localScanner.Latest() == before {
		t.Fatal("summary not refreshed")
	}
}

// Сайт из блока site: конвейера — после стека, привязан к конвейеру;
// повторная выкладка без изменений сайта его не перенастраивает.
func TestDeployComposeWithSite(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(repo, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	content := "repo: " + repo + "\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: shop\n  hosts: [localhost]\n" +
		"  site:\n    domains: [shop.example.com]\n    service: web\n    port: 80\n    proxy: nginx\n"
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	pid, err := db.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: content, HookID: "hook-site-1", HookSecret: secret, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := db.PipelineByID(ctx, pid)
	deployOnce := func() string {
		t.Helper()
		d, err := srv.startDeployment(ctx, pl, store.Deployment{Trigger: "manual", Author: "admin"}, false)
		if err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		got := waitDeployment(t, wctx, db, d.ID)
		log := jobLogText(t, ctx, db, d.JobID)
		if got.Status != store.DeploySucceeded {
			t.Fatalf("deployment: %+v\n%s", got, log)
		}
		return log
	}
	log := deployOnce()
	sites, _ := db.ListSites(ctx)
	if len(sites) != 1 || sites[0].PipelineID != pid || sites[0].Status != store.SiteOK || sites[0].Stack != "shop" {
		t.Fatalf("site: %+v\n%s", sites, log)
	}
	if conf, err := os.ReadFile(filepath.Join(root, "etc", "nginx", "conf.d", "nkt-shop.example.com.conf")); err != nil || !strings.Contains(string(conf), "proxy_pass") {
		t.Fatalf("nginx conf: %v\n%s", err, log)
	}
	before := sites[0].UpdatedAt
	time.Sleep(1100 * time.Millisecond)
	log = deployOnce()
	sites, _ = db.ListSites(ctx)
	if len(sites) != 1 || sites[0].UpdatedAt != before || strings.Contains(log, "127.0.0.1:18000") {
		t.Fatalf("unchanged site was set up again: %+v\n%s", sites, log)
	}

	// Сухой прогон сайта: настроенный — «только проверка HTTPS».
	id, err := srv.jobs.Start(ctx, jobs.Spec{Kind: KindDeploy, Queue: "deploy:dryrun", Author: "admin", Steps: 3,
		Params: DeployParams{DryRun: true, PipelineID: pid, Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := db.JobByID(ctx, id); j.Status == store.JobSucceeded || j.Status == store.JobFailed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dl := jobLogText(t, ctx, db, id); !strings.Contains(dl, "shop.example.com") || !strings.Contains(dl, "HTTPS") {
		t.Fatalf("dry run site:\n%s", dl)
	}
}

// Упавшая выкладка не повторяется опросом: ждёт нового коммита.
func TestPollSkipsFailedCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(repo, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	// Хост, которого нет, — выкладка падает.
	content := "repo: " + repo + "\nref: main\npoll: 1m\naction: compose\ncompose:\n  file: compose.yaml\n  project: app\n  hosts: [nope]\n"
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	pid, err := db.CreatePipeline(ctx, store.Pipeline{Name: "app", Content: content, HookID: "hook-poll-1", HookSecret: secret, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.SetPipelineDeployed(ctx, pid, strings.Repeat("a", 40), "")
	pl, _ := db.PipelineByID(ctx, pid)
	spec, _ := deploy.ParseSpec(content)
	count := func() int {
		ds, _ := db.Deployments(ctx, pid, 50)
		return len(ds)
	}
	wait := func() store.Deployment {
		ds, _ := db.Deployments(ctx, pid, 1)
		wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return waitDeployment(t, wctx, db, ds[0].ID)
	}
	srv.pollRepo(ctx, pl, spec)
	if count() != 1 {
		t.Fatal("poll did not deploy the new commit")
	}
	d := wait()
	if d.Status != store.DeployFailed || !strings.Contains(jobLogText(t, ctx, db, d.JobID), "poll") && !strings.Contains(jobLogText(t, ctx, db, d.JobID), "Опрос") {
		t.Fatalf("expected a failure with the no-retry note: %+v\n%s", d, jobLogText(t, ctx, db, d.JobID))
	}
	pl, _ = db.PipelineByID(ctx, pid)
	if pl.FailedCommit != d.Commit || pl.FailedCommit == "" {
		t.Fatalf("failed commit not remembered: %+v", pl)
	}
	srv.pollRepo(ctx, pl, spec)
	if count() != 1 {
		t.Fatal("poll retried the failed commit")
	}
	testGit(t, repo, "commit", "-q", "--allow-empty", "-m", "two")
	srv.pollRepo(ctx, pl, spec)
	if count() != 2 {
		t.Fatal("poll ignored a new commit")
	}
	wait()
}

// Версии .env: каждая смена — версия, разница — по именам, возврат.
func TestPipelineEnvVersions(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	old, _ := secretbox.Encrypt(srv.hub.key, []byte("A=1\n"))
	pid, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "env", Content: "x", HookID: "hook-env-1", HookSecret: secret, Author: "admin"})
	_ = db.SetPipelineEnv(ctx, pid, old) // заданный до истории
	pl, _ := db.PipelineByID(ctx, pid)
	env := "A=1\nB=2\n"
	if err := srv.setPipelineEnv(ctx, pl, "admin", "edit", &env); err != nil {
		t.Fatal(err)
	}
	env2 := "# c\nexport B=3\nC=4\n"
	pl, _ = db.PipelineByID(ctx, pid)
	if err := srv.setPipelineEnv(ctx, pl, "admin", "edit", &env2); err != nil {
		t.Fatal(err)
	}
	vs, _ := db.EnvVersions(ctx, pid, 10)
	if len(vs) != 3 {
		t.Fatalf("versions: %d", len(vs))
	}
	cur, _ := srv.decryptEnv(vs[0].EnvEnc)
	prev, _ := srv.decryptEnv(vs[1].EnvEnc)
	added, removed, changed := envNameDiff(prev, cur)
	if strings.Join(added, ",") != "C" || strings.Join(removed, ",") != "A" || strings.Join(changed, ",") != "B" {
		t.Fatalf("diff: %v %v %v", added, removed, changed)
	}
	// Выкладка помнит версию; возврат первой версии — новая версия.
	pl, _ = db.PipelineByID(ctx, pid)
	if got := srv.ensureEnvVersion(ctx, pl); got != vs[0].ID {
		t.Fatalf("current version %d, want %d", got, vs[0].ID)
	}
	if err := srv.restoreEnvVersion(ctx, pl, vs[2], "admin", "back"); err != nil {
		t.Fatal(err)
	}
	pl, _ = db.PipelineByID(ctx, pid)
	raw, _ := secretbox.Decrypt(srv.hub.key, pl.EnvEnc)
	if string(raw) != "A=1\n" || db.LatestEnvVersion(ctx, pid) <= vs[0].ID {
		t.Fatalf("restore: %q", raw)
	}
}

// waitJobDone — задание хаба завершилось (любым исходом).
func waitJobDone(t *testing.T, db *store.DB, id int64) store.Job {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := db.JobByID(context.Background(), id)
		if j.Status != store.JobQueued && j.Status != store.JobRunning {
			return j
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return store.Job{}
}

// Удаление сайта — заданием: прокси, публикация сервиса, запись.
func TestSiteRemoveJob(t *testing.T) {
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	id, _ := db.SaveSite(ctx, store.Site{Domains: []string{"shop.example.com"}, HostID: localHostID, Proxy: "nginx",
		Stack: "shop", Service: "web", ContainerPort: 80, OpenFirewall: true, Author: "admin"})
	jobID, _ := srv.startSiteSetup(ctx, "admin", id, "shop.example.com", false)
	if j := waitJobDone(t, db, jobID); j.Status != store.JobSucceeded {
		t.Fatalf("setup: %+v", j)
	}
	ov := filepath.Join(root, "srv", "compose", "shop", "compose.nkt.yml")
	if b, _ := os.ReadFile(ov); !strings.Contains(string(b), "18000") {
		t.Fatalf("override: %s", b)
	}
	jid, err := srv.jobs.Start(ctx, jobs.Spec{Kind: KindSiteRemove, Queue: "site:x", Author: "admin", Steps: 1, Params: SiteRemoveParams{SiteID: id}})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJobDone(t, db, jid); j.Status != store.JobSucceeded {
		t.Fatalf("remove: %+v\n%s", j, jobLogText(t, ctx, db, jid))
	}
	if _, err := db.SiteByID(ctx, id); err == nil {
		t.Fatal("site record left")
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "nginx", "conf.d", "nkt-shop.example.com.conf")); !os.IsNotExist(err) {
		t.Fatal("nginx conf left")
	}
	if _, err := os.Stat(ov); !os.IsNotExist(err) {
		b, _ := os.ReadFile(ov)
		t.Fatalf("publication left: %s", b)
	}
}

// Удаление конвейера compose: сайт, стек, запись; сбой — «не завершено».
func TestPipelineRemoveJob(t *testing.T) {
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	content := "repo: https://codeberg.org/me/shop.git\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: shop\n  hosts: [localhost, gone]\n"
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	pid, err := db.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: content, HookID: "hook-rm-1", HookSecret: secret, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := db.SaveSite(ctx, store.Site{Domains: []string{"shop.example.com"}, HostID: localHostID, Proxy: "nginx",
		Stack: "shop", Service: "web", ContainerPort: 80, PipelineID: pid, Author: "admin"})
	jobID, _ := srv.startSiteSetup(ctx, "admin", sid, "shop.example.com", false)
	waitJobDone(t, db, jobID)
	jid, err := srv.jobs.Start(ctx, jobs.Spec{Kind: KindPipelineRemove, Queue: "deploy:x", Author: "admin", Steps: 3,
		Params: PipelineRemoveParams{PipelineID: pid}})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJobDone(t, db, jid)
	log := jobLogText(t, ctx, db, jid)
	if j.Status != store.JobSucceeded || !strings.Contains(log, "gone") {
		t.Fatalf("remove: %+v\n%s", j, log)
	}
	if _, err := db.PipelineByID(ctx, pid); err == nil {
		t.Fatal("pipeline record left")
	}
	if _, err := db.SiteByID(ctx, sid); err == nil {
		t.Fatal("site record left")
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "nginx", "conf.d", "nkt-shop.example.com.conf")); !os.IsNotExist(err) {
		t.Fatal("nginx conf left")
	}

	// Хост не работает — удаление не завершено, конвейер остаётся.
	if _, err := db.CreateHost(ctx, "down1", "192.0.2.1", 22, "root", "password", []byte("x")); err != nil {
		t.Fatal(err)
	}
	pid2, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "shop2", Content: strings.Replace(content, "[localhost, gone]", "[down1]", 1), HookID: "hook-rm-2", HookSecret: secret, Author: "admin"})
	jid, _ = srv.jobs.Start(ctx, jobs.Spec{Kind: KindPipelineRemove, Queue: "deploy:y", Author: "admin", Steps: 3,
		Params: PipelineRemoveParams{PipelineID: pid2}})
	if j := waitJobDone(t, db, jid); j.Status != store.JobFailed {
		t.Fatalf("offline host accepted: %+v\n%s", j, jobLogText(t, ctx, db, jid))
	}
	pl, err := db.PipelineByID(ctx, pid2)
	if err != nil || !strings.Contains(pl.Removal, "error") {
		t.Fatalf("removal state: %+v %v", pl.Removal, err)
	}
}

// Старый хост не знает новых полей запроса — хаб повторяет без них.
func TestComposeOldHostFallback(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	inner := srv.local
	srv.local = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/compose/stacks/") {
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), "site_port") || strings.Contains(string(raw), "env_sha") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"некорректное тело запроса: json: unknown field \"site_port\""}`))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		inner.ServeHTTP(w, r)
	})
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(repo, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	content := "repo: " + repo + "\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: shop\n  hosts: [localhost]\n" +
		"  site:\n    domains: [shop.example.com]\n    service: web\n    port: 80\n"
	id, err := srv.jobs.Start(ctx, jobs.Spec{Kind: KindDeploy, Queue: "deploy:dryrun", Author: "admin", Steps: 3,
		Params: DeployParams{DryRun: true, Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJobDone(t, db, id)
	log := jobLogText(t, ctx, db, id)
	if j.Status != store.JobSucceeded || !strings.Contains(log, "site_service") {
		t.Fatalf("old host: %+v\n%s", j, log)
	}
}

// Нет certbot — настройка сайта ставит его (здесь установка недоступна:
// фикстуры) и говорит, что поставить вручную.
func TestSiteSetupInstallsCertbot(t *testing.T) {
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	idxPath := filepath.Join(root, ".commands", "index.json")
	raw, _ := os.ReadFile(idxPath)
	var idx map[string]any
	_ = json.Unmarshal(raw, &idx)
	idx["commands"] = append([]any{map[string]any{"match": []string{"sh", "-c", "command -v certbot"}, "exit_code": 1}}, idx["commands"].([]any)...)
	raw, _ = json.Marshal(idx)
	_ = os.WriteFile(idxPath, raw, 0o644)
	id, _ := db.SaveSite(ctx, store.Site{Domains: []string{"shop.example.com"}, HostID: localHostID, Proxy: "nginx",
		Stack: "shop", Service: "web", ContainerPort: 80, Author: "admin"})
	jobID, err := srv.startSiteSetup(ctx, "admin", id, "shop.example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	j := waitJobDone(t, db, jobID)
	log := jobLogText(t, ctx, db, jobID)
	// Годный сертификат в фикстурах есть — без certbot сайт настроится,
	// а в журнале — попытка установки и предупреждение.
	if j.Status != store.JobSucceeded || !strings.Contains(log, "certbot не установлен") {
		t.Fatalf("certbot install path: %+v\n%s", j, log)
	}
}
