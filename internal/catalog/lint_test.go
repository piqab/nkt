package catalog_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/catalog"
	"github.com/piqab/nkt/internal/catalog/catalogtest"
)

// Каждый плагин каталога (и заготовка examples/_template) проходит lint:
// то же, что CI проверяет перед сборкой каталога.
func TestLintExamples(t *testing.T) {
	_, err := exec.LookPath("python3")
	live := err == nil
	dirs, _ := filepath.Glob(filepath.Join(catalogtest.ExamplesDir(), "*", "deploy", "nkt-plugin.json"))
	samples, _ := filepath.Glob(filepath.Join(catalogtest.ExamplesDir(), "_samples", "*", "deploy", "nkt-plugin.json"))
	dirs = append(dirs, samples...)
	if len(dirs) < 9 {
		t.Fatalf("plugins: %v", dirs)
	}
	for _, f := range dirs {
		dir := filepath.Dir(filepath.Dir(f))
		if p := catalog.Lint(dir, live); len(p) > 0 {
			t.Errorf("%s:\n  %s", filepath.Base(dir), strings.Join(p, "\n  "))
		}
	}
}

// lint ловит опасное и недоделанное.
func TestLintCatches(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "deploy"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "deploy", "nkt-plugin.json"), []byte(`{"name":"bad","version":"1.0","min_nkt":"1.16.0","protocol":1,
		"title":{"ru":"Плохой"},"description":{"ru":"x","en":"x"},"run":"python3","file":"nkt-plugin.py","port":80,
		"settings":[{"env":"API_TOKEN","title":{"ru":"Токен","en":"Token"}}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "deploy", "nkt-plugin.py"), []byte("VERSION = \"1.0\"\nimport subprocess, os\nsubprocess.run(f\"ls {x}\", shell=True)\nos.system(cmd)\neval(text)\n# eval(in a comment is fine)\n"), 0o644)
	got := strings.Join(catalog.Lint(dir, false), "\n")
	for _, want := range []string{"no deploy/test_plugin.py", "no README.md", "version \"1.0\"", "title needs ru and en", "port 80",
		"API_TOKEN looks like a secret", "shell=True", "subprocess with a command string", "os.system", "eval/exec", "hmac.compare_digest", "X-NKT-Lang"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "eval/exec") != 1 {
		t.Errorf("comment counted:\n%s", got)
	}
}
