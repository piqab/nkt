package examples

import (
	"os/exec"
	"strings"
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
	for _, k := range []string{"forgejo", "forgejo-fly", "forgejo-runner", "technitium", "technitium-secondary", "docker-mailserver", "litellm"} {
		if !keys[k] {
			t.Errorf("no example %s in %v", k, keys)
		}
	}
}

func TestPluginPackages(t *testing.T) {
	list := PluginPackages()
	keys := []string{}
	for _, p := range list {
		keys = append(keys, p.Key)
	}
	if strings.Join(keys, ",") != "cloudflare,docker-mailserver,litellm,technitium" || list[3].Run != "python3" || len(list[3].Content) < 1000 {
		t.Fatalf("packages: %v", keys)
	}
	if !list[1].Docker || list[0].Docker || list[2].Docker || list[3].Docker {
		t.Fatal("docker flags")
	}
	if list[2].Port != 9210 || len(list[2].Settings) != 2 || !list[2].Settings[0].Secret {
		t.Fatalf("litellm: %+v", list[2].Settings)
	}
	list = list[3:]
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

// Плагин LiteLLM — против поддельного API LiteLLM (Python): ключ — один раз
// или в .env конвейера, в журнал службы не попадает.
func TestLiteLLMPluginPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	out, err := exec.Command(py, "-I", "litellm/deploy/test_plugin.py").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
