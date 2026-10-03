package deploy

import (
	"os"
	"strings"
	"testing"
)

// Пример Forgejo из examples/: описание конвейера разбирается, а
// переопределения (ports, env_keys) ложатся на compose-файл примера.
func TestForgejoExample(t *testing.T) {
	spec, err := os.ReadFile("../../examples/forgejo/pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSpec(string(spec))
	if err != nil {
		t.Fatal(err)
	}
	c := s.Compose
	if c == nil || c.Project != "forgejo" || c.Site == nil || c.Site.Service != "forgejo" || c.Site.Port != 3000 {
		t.Fatalf("spec: %+v", c)
	}
	if keys := strings.Join(c.AllEnvKeys(), ","); !strings.Contains(keys, "FORGEJO_ADMIN_PASSWORD") {
		t.Fatalf("env keys: %s", keys)
	}
	file, err := os.ReadFile("../../" + c.File)
	if err != nil {
		t.Fatal(err)
	}
	out, err := OverrideServices(string(file), c.Images, c.Ports, c.EnvKeys)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"0.0.0.0:2222:2222", "forgejo-admin", "FORGEJO__security__INSTALL_LOCK"} {
		if !strings.Contains(out, want) {
			t.Errorf("override lacks %q", want)
		}
	}
}
