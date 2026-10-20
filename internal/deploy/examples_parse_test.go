package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Все примеры из examples/ — правильные описания; блок env читается,
// начало — предупреждение о стороннем коде.
func TestExamplePipelinesParse(t *testing.T) {
	files, _ := filepath.Glob("../../examples/*/pipeline*.yaml")
	more, _ := filepath.Glob("../../examples/*/deploy/pipeline.yaml")
	files = append(files, more...)
	if len(files) < 8 {
		t.Fatalf("examples: %v", files)
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		text := string(raw)
		if _, err := ParseSpec(text); err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if _, err := EnvBlock(text); err != nil {
			t.Errorf("%s: env: %v", f, err)
		}
		if !strings.Contains(f, "hello-app") && !strings.Contains(f, "nkt-hub-test") && !strings.HasPrefix(text, "# WARNING") {
			t.Errorf("%s: no WARNING on top", f)
		}
	}
}

// max_body в сайте конвейера: "0" и число с k/m/g — да, прочее — нет.
func TestSiteMaxBody(t *testing.T) {
	base := "repo: https://example.com/r.git\nref: main\naction: compose\ncompose:\n  file: docker-compose.yml\n  project: s3\n  hosts: [web1]\n  site:\n    domains: [s3.example.com]\n    service: s3\n    port: 3900\n"
	for v, ok := range map[string]bool{`"0"`: true, "0": true, "512m": true, `"1;evil"`: false, "-1": false} {
		spec, err := ParseSpec(base + "    max_body: " + v + "\n")
		if (err == nil) != ok {
			t.Errorf("%s: %v", v, err)
		}
		if ok && err == nil && spec.Compose.Site.MaxBody == "" {
			t.Errorf("%s: пусто", v)
		}
	}
}
