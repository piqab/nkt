package examples

import (
	"os/exec"
	"testing"
)

func TestPipelines(t *testing.T) {
	list := Pipelines("en")
	keys := map[string]bool{}
	for _, p := range list {
		keys[p.Key] = true
		if p.Title == "" || p.Content == "" {
			t.Errorf("%s: no title or content", p.Key)
		}
	}
	for _, p := range list {
		for _, r := range p.Title {
			if r >= 'а' && r <= 'я' {
				t.Errorf("%s: Russian title %q", p.Key, p.Title)
				break
			}
		}
	}
	for _, k := range []string{"forgejo", "forgejo-fly", "forgejo-runner", "technitium", "technitium-secondary", "docker-mailserver"} {
		if !keys[k] {
			t.Errorf("no example %s in %v", k, keys)
		}
	}
}

func TestPluginPackages(t *testing.T) {
	list := PluginPackages()
	if len(list) != 3 || list[0].Key != "cloudflare" || list[1].Key != "docker-mailserver" || list[2].Key != "technitium" || list[2].Run != "python3" || len(list[2].Content) < 1000 {
		t.Fatalf("packages: %+v", list)
	}
	if !list[1].Docker || list[0].Docker || list[2].Docker {
		t.Fatalf("docker flags: %v %v %v", list[0].Docker, list[1].Docker, list[2].Docker)
	}
	list = list[2:]
	secret := false
	for _, s := range list[0].Settings {
		if s.Env == "DNS_API_TOKEN" && s.Secret && s.Required {
			secret = true
		}
		if s.Title["ru"] == "" || s.Title["en"] == "" {
			t.Errorf("%s: no title", s.Env)
		}
	}
	if !secret {
		t.Fatal("no DNS_API_TOKEN secret setting")
	}
}

// Плагин Cloudflare — против поддельного API Cloudflare (Python, без сети).
func TestCloudflarePluginPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	out, err := exec.Command(py, "-I", "cloudflare/deploy/test_plugin.py").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Плагин почты — против поддельного docker (Python): пароли не попадают
// в argv.
func TestMailPluginPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	out, err := exec.Command(py, "-I", "docker-mailserver/deploy/test_plugin.py").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
