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
		if !strings.Contains(f, "hello-app") && !strings.HasPrefix(text, "# WARNING") {
			t.Errorf("%s: no WARNING on top", f)
		}
	}
}
