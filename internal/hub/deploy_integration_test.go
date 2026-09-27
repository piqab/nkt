package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func waitDeployment(t *testing.T, ctx context.Context, db *store.DB, id int64) store.Deployment {
	t.Helper()
	for {
		d, _ := db.DeploymentByID(ctx, id)
		if d.Status == store.DeploySucceeded || d.Status == store.DeployFailed || ctx.Err() != nil {
			return d
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Конвейер «manifest»: хаб берёт коммит из репозитория, подставляет тег и
// делает kubectl apply в кластере через настоящий туннель; откат выкладывает
// прежний коммит.
func TestDeployManifestRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	db, key, _, clusterID, ctx := fixtureClusterHub(t, "lab-cp-1")
	_ = db.SetClusterStatus(ctx, clusterID, store.ClusterReady, "")
	cfg := &config.Config{DataDir: t.TempDir()}
	m := NewManager(cfg, db, key, "test", slog.New(slog.DiscardHandler))
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	s.jobs.Register(KindDeploy, NewDeployRunner(s))

	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.MkdirAll(filepath.Join(repo, "deploy"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "deploy", "cm.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\n  namespace: shop\ndata:\n  version: \"{{nkt.tag}}\"\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	first := testGit(t, repo, "rev-parse", "HEAD")

	content := "repo: " + repo + "\nref: main\naction: manifest\nmanifests: [deploy/cm.yaml]\nclusters: [lab]\n"
	secret, _ := secretbox.Encrypt(key, []byte("s"))
	pid, err := db.CreatePipeline(ctx, store.Pipeline{Name: "app", Content: content, HookID: "h1", HookSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := db.PipelineByID(ctx, pid)
	d, err := s.startDeployment(ctx, pl, store.Deployment{Trigger: "manual", Tag: "v1.0.0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := waitDeployment(t, ctx, db, d.ID)
	if got.Status != store.DeploySucceeded || got.Commit != first {
		log := jobLogText(t, ctx, db, d.JobID)
		t.Fatalf("выкладка: %+v\n%s", got, log)
	}
	pl, _ = db.PipelineByID(ctx, pid)
	if pl.LastCommit != first || pl.LastTag != "v1.0.0" {
		t.Errorf("последнее выложенное: %+v", pl)
	}
	man, _ := db.ListManifests(ctx)
	if len(man) != 1 || man[0].Name != "pipeline: app" {
		t.Fatalf("библиотека манифестов: %+v", man)
	}
	full, _ := db.ManifestByID(ctx, man[0].ID)
	if !strings.Contains(full.Content, `version: "v1.0.0"`) {
		t.Errorf("подстановка тега: %s", full.Content)
	}

	// Второй коммит и откат на первый.
	_ = os.WriteFile(filepath.Join(repo, "deploy", "cm.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\n  namespace: shop\ndata:\n  version: two\n"), 0o644)
	testGit(t, repo, "commit", "-q", "-am", "two")
	d2, _ := s.startDeployment(ctx, pl, store.Deployment{Trigger: "manual"}, false)
	if got := waitDeployment(t, ctx, db, d2.ID); got.Status != store.DeploySucceeded || got.Commit == first {
		t.Fatalf("вторая: %+v", got)
	}
	d3, _ := s.startDeployment(ctx, pl, store.Deployment{Trigger: "rollback", Commit: first, Tag: "v1.0.0"}, false)
	if got := waitDeployment(t, ctx, db, d3.ID); got.Status != store.DeploySucceeded || got.Commit != first {
		t.Fatalf("откат: %+v", got)
	}

	// Кластер, которого нет, — выкладка падает с понятной ошибкой.
	bad := "repo: " + repo + "\nref: main\naction: manifest\nmanifests: [deploy/cm.yaml]\nclusters: [nope]\n"
	_ = db.UpdatePipelineContent(ctx, pid, bad, "t", "")
	pl, _ = db.PipelineByID(ctx, pid)
	d4, _ := s.startDeployment(ctx, pl, store.Deployment{Trigger: "manual"}, false)
	if got := waitDeployment(t, ctx, db, d4.ID); got.Status != store.DeployFailed || got.Error == "" {
		t.Fatalf("без кластеров: %+v", got)
	}
}

// Вебхук GitHub запускает выкладку; повтор той же доставки, чужая подпись
// и другая ветка — нет. Опрос находит новый коммит сам.
func TestDeployHookAndPoll(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	db, key, _, clusterID, ctx := fixtureClusterHub(t, "lab-cp-1")
	_ = db.SetClusterStatus(ctx, clusterID, store.ClusterReady, "")
	m := NewManager(&config.Config{DataDir: t.TempDir()}, db, key, "test", slog.New(slog.DiscardHandler))
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	s.jobs.Register(KindDeploy, NewDeployRunner(s))

	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(repo, "cm.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\n  namespace: shop\ndata:\n  c: \"{{nkt.commit}}\"\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	head := testGit(t, repo, "rev-parse", "HEAD")
	secret, _ := secretbox.Encrypt(key, []byte("hook-secret"))
	content := "repo: " + repo + "\nref: main\naction: manifest\nmanifests: [cm.yaml]\nclusters: [lab]\npoll: 1m\n"
	pid, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "app", Content: content, HookID: "abc123", HookSecret: secret})

	router := chi.NewRouter()
	router.Post("/api/hub/hooks/{hook}", s.handleHook)
	send := func(body, secret, delivery string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/hub/hooks/abc123", strings.NewReader(body)).WithContext(ctx)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-GitHub-Event", "push")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	push := `{"ref":"refs/heads/main","after":"` + head + `"}`
	if rec := send(push, "wrong", "d1"); rec.Code != 401 {
		t.Fatalf("чужая подпись: %d", rec.Code)
	}
	rec := send(push, "hook-secret", "d2")
	if rec.Code != 202 {
		t.Fatalf("вебхук: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(push, "hook-secret", "d2"); rec.Code != 401 {
		t.Errorf("повтор доставки: %d", rec.Code)
	}
	if rec := send(`{"ref":"refs/heads/dev","after":"`+head+`"}`, "hook-secret", "d3"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "ignored") {
		t.Errorf("другая ветка: %d %s", rec.Code, rec.Body.String())
	}
	ds, _ := db.Deployments(ctx, pid, 10)
	if len(ds) != 1 || ds[0].Trigger != "webhook" {
		t.Fatalf("выкладки: %+v", ds)
	}
	if got := waitDeployment(t, ctx, db, ds[0].ID); got.Status != store.DeploySucceeded || got.Commit != head {
		t.Fatalf("выкладка по вебхуку: %+v", got)
	}

	// Опрос: новый коммит — новая выкладка; тот же — ничего.
	w := &pipelineWatch{}
	s.checkPipelines(ctx, w, time.Now())
	if ds, _ := db.Deployments(ctx, pid, 10); len(ds) != 1 {
		t.Fatalf("опрос без изменений выложил: %+v", ds)
	}
	_ = os.WriteFile(filepath.Join(repo, "cm.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app\n  namespace: shop\ndata:\n  v: \"2\"\n"), 0o644)
	testGit(t, repo, "commit", "-q", "-am", "two")
	s.checkPipelines(ctx, w, time.Now().Add(2*time.Minute))
	ds, _ = db.Deployments(ctx, pid, 10)
	if len(ds) != 2 || ds[0].Trigger != "poll" {
		t.Fatalf("опрос: %+v", ds)
	}
	if got := waitDeployment(t, ctx, db, ds[0].ID); got.Status != store.DeploySucceeded || got.Commit == head {
		t.Fatalf("выкладка по опросу: %+v", got)
	}
}
