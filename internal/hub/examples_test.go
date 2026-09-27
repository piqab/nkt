package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/script"
)

// Пример examples/hello-app должен оставаться рабочим: описания
// конвейеров, манифест и сценарий проходят те же проверки, что в хабе.
func TestHelloAppExample(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "hello-app", "deploy")
	for _, f := range []string{"pipeline-k8s.yaml", "pipeline-helm.yaml", "pipeline-host.yaml"} {
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
	wf, _ := os.ReadFile(filepath.Join(dir, "..", ".github", "workflows", "build.yml"))
	var workflow map[string]any
	if err := yaml.Unmarshal(wf, &workflow); err != nil || workflow["jobs"] == nil {
		t.Errorf("build.yml: %v", err)
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
