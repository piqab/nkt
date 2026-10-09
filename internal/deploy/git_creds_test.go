package deploy

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Настоящий git по HTTP (git http-backend): у каждого репозитория свой
// токен, git спрашивает его у askpass с адресом — ключ подбирается по
// адресу; чужой токен не подходит.
func gitHTTPServer(t *testing.T, root string, tokens map[string]string) *httptest.Server {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git не установлен")
	}
	backend := &cgi.Handler{Path: gitPath, Args: []string{"http-backend"},
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), ".git", 2)[0]
		if want, ok := tokens[repo]; ok {
			if _, pass, ok := r.BasicAuth(); !ok || pass != want {
				w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		backend.ServeHTTP(w, r)
	}))
}

func bareRepo(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	work := t.TempDir()
	gitCmd(t, work, "init", "-q", "-b", "main")
	for p, c := range files {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(work, p)), 0o755)
		_ = os.WriteFile(filepath.Join(work, p), []byte(c), 0o644)
	}
	gitCmd(t, work, "add", ".")
	gitCmd(t, work, "commit", "-q", "-m", "init")
	bare := filepath.Join(root, name+".git")
	_ = os.MkdirAll(filepath.Dir(bare), 0o755)
	gitCmd(t, root, "clone", "-q", "--bare", work, bare)
	return gitCmd(t, work, "rev-parse", "HEAD")
}

func TestGitCredsByURL(t *testing.T) {
	root := t.TempDir()
	bareRepo(t, root, "team/app", map[string]string{"a.txt": "a"})
	bareRepo(t, root, "vendor/lib", map[string]string{"b.txt": "b"})
	srv := gitHTTPServer(t, root, map[string]string{"team/app": "tok-app", "vendor/lib": "tok-lib"})
	defer srv.Close()
	app, lib := srv.URL+"/team/app.git", srv.URL+"/vendor/lib.git"
	host := strings.TrimPrefix(srv.URL, "http://")
	ctx := context.Background()

	g := Git{Dir: t.TempDir(), Cred: Cred{Token: "tok-app"},
		Extra: []RepoCred{{Prefix: host + "/vendor/", Cred: Cred{Token: "tok-lib"}}}}
	if _, err := g.Remote(ctx, app); err != nil {
		t.Fatalf("основной репозиторий: %v", err)
	}
	// Ключ по префиксу (основной — у основного репозитория, здесь его нет).
	other := Git{Dir: t.TempDir(), Extra: g.Extra}
	if _, err := other.Remote(ctx, lib); err != nil {
		t.Fatalf("репозиторий по префиксу: %v", err)
	}
	// Без ключа на vendor/ — отказ.
	other.Extra = []RepoCred{{Prefix: host + "/team/", Cred: Cred{Token: "tok-app"}}}
	if _, err := other.Remote(ctx, lib); err == nil {
		t.Fatal("репозиторий открылся ключом другого префикса")
	}
	for in, want := range map[string]string{
		"https://GitHub.com/org/app.git": "github.com/org/app", "git@github.com:org/app.git": "github.com/org/app",
		"ssh://git@git.example.com:2222/org/app": "git.example.com/org/app", "github.com/vendor": "github.com/vendor",
	} {
		if got := RepoKey(in); got != want {
			t.Errorf("RepoKey(%s) = %s, ждали %s", in, got, want)
		}
	}
}

// Submodule из другого закрытого репозитория: выкладка подтягивает его
// своим ключом (по префиксу), основной ключ туда не уходит.
func TestCheckoutSubmodules(t *testing.T) {
	root := t.TempDir()
	libSHA := bareRepo(t, root, "vendor/lib", map[string]string{"lib.txt": "from lib"})
	srv := gitHTTPServer(t, root, map[string]string{"team/app": "tok-app", "vendor/lib": "tok-lib"})
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	work := t.TempDir()
	gitCmd(t, work, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(work, ".gitmodules"), []byte("[submodule \"lib\"]\n\tpath = lib\n\turl = ../../vendor/lib.git\n"), 0o644)
	_ = os.WriteFile(filepath.Join(work, "app.txt"), []byte("app"), 0o644)
	gitCmd(t, work, "add", ".")
	gitCmd(t, work, "update-index", "--add", "--cacheinfo", "160000,"+libSHA+",lib")
	gitCmd(t, work, "commit", "-q", "-m", "with submodule")
	sha := gitCmd(t, work, "rev-parse", "HEAD")
	bare := filepath.Join(root, "team", "app.git")
	_ = os.MkdirAll(filepath.Dir(bare), 0o755)
	gitCmd(t, root, "clone", "-q", "--bare", work, bare)

	app := srv.URL + "/team/app.git"
	dest := filepath.Join(t.TempDir(), "src")
	g := Git{Dir: t.TempDir(), Cred: Cred{Token: "tok-app"}, Extra: []RepoCred{{Prefix: host + "/vendor/", Cred: Cred{Token: "tok-lib"}}}}
	if err := g.Checkout(context.Background(), app, "main", sha, dest); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "lib", "lib.txt")); err != nil || string(b) != "from lib" {
		t.Fatalf("submodule: %q %v", b, err)
	}
	g.Extra = nil
	if err := g.Checkout(context.Background(), app, "main", sha, filepath.Join(t.TempDir(), "src")); err == nil {
		t.Fatal("submodule без своего ключа подтянулся")
	}
}
