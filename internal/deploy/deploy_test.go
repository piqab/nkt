package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSpec(t *testing.T) {
	ok := []string{
		"repo: https://github.com/org/app.git\nref: main\naction: manifest\nmanifests: [deploy/k8s.yaml]\nclusters: [prod]\n",
		"repo: git@github.com:org/app.git\ntags: \"v*\"\naction: script\nscript: deploy/run.nkt\npoll: 5m\n",
		"repo: https://gitlab.com/g/app.git\nref: main\naction: helm\ngroup: prod\nhelm:\n  repo_name: bitnami\n  repo_url: https://charts.bitnami.com/bitnami\n  chart: nginx\n  release: web\n  namespace: web\n  values: deploy/values.yaml\n  tag_key: image.tag\nregistry: ghcr.io/org/app\nregistry_tags: '^v\\d+$'\n",
	}
	for _, c := range ok {
		if _, err := ParseSpec(c); err != nil {
			t.Errorf("%v\n%s", err, c)
		}
	}
	bad := []string{
		"repo: https://u:p@github.com/org/app.git\nref: main\naction: script\nscript: a.nkt\n",
		"repo: file:///etc\nref: main\naction: script\nscript: a.nkt\n",
		"repo: https://github.com/o/a.git\nref: --upload-pack=x\naction: script\nscript: a.nkt\n",
		"repo: https://github.com/o/a.git\nref: main\naction: script\nscript: ../../etc/passwd\n",
		"repo: https://github.com/o/a.git\nref: main\naction: manifest\nmanifests: [a.yaml]\n",
		"repo: https://github.com/o/a.git\nref: main\naction: script\nscript: a.nkt\npoll: 10s\n",
		"repo: https://github.com/o/a.git\nref: main\naction: script\nscript: a.nkt\nunknown: 1\n",
		"repo: https://github.com/o/a.git; rm -rf /\nref: main\naction: script\nscript: a.nkt\n",
	}
	for _, c := range bad {
		if _, err := ParseSpec(c); err == nil {
			t.Errorf("принято:\n%s", c)
		}
	}
	if !MatchTag("v*", "v1.2.3") || MatchTag("v*", "release-1") || !MatchTag("release-?", "release-1") {
		t.Error("MatchTag")
	}
	if TemplateFor("en") == TemplateFor("ru") {
		t.Error("шаблоны одинаковые")
	}
	if _, err := ParseSpec(TemplateFor("ru")); err != nil {
		t.Errorf("шаблон ru: %v", err)
	}
	if _, err := ParseSpec(TemplateFor("en")); err != nil {
		t.Errorf("шаблон en: %v", err)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) string {
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

func TestGitRemoteAndCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	allowLocalRepos = true
	defer func() { allowLocalRepos = false }()
	repo := t.TempDir()
	gitCmd(t, repo, "init", "-q", "-b", "main")
	_ = os.MkdirAll(filepath.Join(repo, "deploy"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "deploy", "k8s.yaml"), []byte("image: app:{{nkt.tag}}\n"), 0o644)
	gitCmd(t, repo, "add", ".")
	gitCmd(t, repo, "commit", "-q", "-m", "one")
	first := gitCmd(t, repo, "rev-parse", "HEAD")
	gitCmd(t, repo, "tag", "-a", "v1.0.0", "-m", "v1")
	_ = os.WriteFile(filepath.Join(repo, "deploy", "k8s.yaml"), []byte("image: app:{{nkt.tag}}\nreplicas: 2\n"), 0o644)
	gitCmd(t, repo, "commit", "-q", "-am", "two")
	second := gitCmd(t, repo, "rev-parse", "HEAD")

	g := Git{Dir: t.TempDir()}
	refs, err := g.Remote(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if refs["refs/heads/main"] != second || refs["refs/tags/v1.0.0"] != first {
		t.Fatalf("refs: %v", refs)
	}
	dest := filepath.Join(g.Dir, "src")
	if err := g.Checkout(context.Background(), repo, "v1.0.0", first, dest); err != nil {
		t.Fatal(err)
	}
	text, err := ReadFile(dest, "deploy/k8s.yaml")
	if err != nil || strings.Contains(text, "replicas") {
		t.Fatalf("старый коммит: %q %v", text, err)
	}
	if got := (Vars{Tag: "v1.0.0"}).Substitute(text); got != "image: app:v1.0.0\n" {
		t.Errorf("подстановка: %q", got)
	}
	if _, err := ReadFile(dest, "../x"); err == nil {
		t.Error("выход из каталога")
	}
	_ = os.Symlink("/etc/hostname", filepath.Join(dest, "link"))
	if _, err := ReadFile(dest, "link"); err == nil {
		t.Error("ссылка наружу прочитана")
	}
	if err := g.Checkout(context.Background(), repo, "main", second, dest); err != nil {
		t.Fatal(err)
	}
	if text, _ := ReadFile(dest, "deploy/k8s.yaml"); !strings.Contains(text, "replicas") {
		t.Error("новый коммит не взят")
	}
}
