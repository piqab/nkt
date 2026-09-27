package hub

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/script"
)

// Пример examples/hello-app должен оставаться рабочим: описания
// конвейеров, манифест и сценарий проходят те же проверки, что в хабе.
func TestHelloAppExample(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "hello-app", "deploy")
	for _, f := range []string{"pipeline-k8s.yaml", "pipeline-helm.yaml", "pipeline-host.yaml", "pipeline-registry.yaml"} {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := deploy.ParseSpec(string(raw)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	man, _ := os.ReadFile(filepath.Join(dir, "k8s.yaml"))
	content := (deploy.Vars{Tag: "abc123"}).Substitute(string(man))
	if docs, err := k8s.ParseManifest(content); err != nil || len(docs) != 4 {
		t.Errorf("k8s.yaml: %v %v", docs, err)
	}
	if strings.Contains(content, "{{nkt.") {
		t.Error("подстановка в k8s.yaml")
	}
	values, _ := os.ReadFile(filepath.Join(dir, "values.yaml"))
	out, err := SetYAMLKey(string(values), "image.tag", "v1.0.0")
	if err != nil || !strings.Contains(out, "tag: v1.0.0") {
		t.Errorf("values.yaml: %v\n%s", err, out)
	}
	for _, f := range []string{".github/workflows/build.yml", ".gitea/workflows/build.yml"} {
		wf, _ := os.ReadFile(filepath.Join(dir, "..", f))
		var workflow map[string]any
		if err := yaml.Unmarshal(wf, &workflow); err != nil || workflow["jobs"] == nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	gl, _ := os.ReadFile(filepath.Join(dir, "..", ".gitlab-ci.yml"))
	var gitlab map[string]any
	if err := yaml.Unmarshal(gl, &gitlab); err != nil || gitlab["deploy"] == nil || gitlab["build"] == nil {
		t.Errorf(".gitlab-ci.yml: %v", err)
	}
	nkt, _ := os.ReadFile(filepath.Join(dir, "deploy.nkt"))
	sc, issues := script.Parse(string(nkt), map[string]string{"param:TAG": "v1.0.0"})
	if len(issues) > 0 {
		t.Fatalf("deploy.nkt: %v", issues)
	}
	if !strings.Contains(sc.Steps[0].Block, "hello-app:v1.0.0") {
		t.Errorf("deploy.nkt: тег не подставлен:\n%s", sc.Steps[0].Block)
	}
}

// scripts/nkt-hook.sh из примера подписывает запрос так, что хаб его
// принимает: тот же разбор подписи и решение, что у настоящего вебхука.
func TestHelloAppHookScript(t *testing.T) {
	for _, bin := range []string{"sh", "openssl", "curl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " не найден")
		}
	}
	const secret = "example-secret"
	got := make(chan deploy.HookEvent, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ev, err := deploy.VerifyHook(r.Header, body, secret, time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		got <- ev
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	commit := strings.Repeat("ab", 20)
	cmd := exec.Command("sh", filepath.Join("..", "..", "examples", "hello-app", "scripts", "nkt-hook.sh"), "v1.2.3", commit, "main")
	cmd.Env = append(os.Environ(), "NKT_HOOK_URL="+srv.URL+"/hooks/x", "NKT_HOOK_SECRET="+secret)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nkt-hook.sh: %v\n%s", err, out)
	}
	ev := <-got
	spec, err := deploy.ParseSpec("repo: https://example.com/a.git\nref: main\naction: script\nscript: deploy/deploy.nkt\n")
	if err != nil {
		t.Fatal(err)
	}
	d := deploy.Decide(spec, ev)
	if !d.Deploy || d.Ref != "main" || d.Tag != "v1.2.3" || d.Commit != commit {
		t.Errorf("решение: %+v (событие %+v)", d, ev)
	}
}
