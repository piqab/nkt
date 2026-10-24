package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

// Пример раннера Forgejo с DinD разбирается и проходит проверки
// выкладки: описание, compose без сборки из исходников, файлы внутри
// стека.
func TestForgejoRunnerExample(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "examples", "forgejo-runner", "pipeline.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ParseSpec(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Action != ActionCompose || spec.Compose == nil || spec.Compose.Project != "forgejo-runner" {
		t.Fatalf("spec: %+v", spec)
	}
	compose, err := os.ReadFile(filepath.Join(root, spec.Compose.File))
	if err != nil {
		t.Fatal(err)
	}
	if names := BuildOnlyServices(string(compose)); len(names) > 0 {
		t.Fatalf("build-only services: %v", names)
	}
	for _, f := range spec.Compose.Files {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Fatalf("file %s: %v", f, err)
		}
	}
}
