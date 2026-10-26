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
	if strings.Join(keys, ",") != "cloudflare,connector,docker-mailserver,litellm,mikrotik,technitium,yandex-cloud" || list[5].Run != "python3" || len(list[5].Content) < 1000 {
		t.Fatalf("packages: %v", keys)
	}
	for i, p := range list {
		if p.Docker != (p.Key == "docker-mailserver") {
			t.Fatalf("docker flag of %s (%d)", p.Key, i)
		}
	}
	if list[3].Port != 9210 || len(list[3].Settings) != 2 || !list[3].Settings[0].Secret {
		t.Fatalf("litellm: %+v", list[3].Settings)
	}
	// MikroTik и connector: учётки устройств — у хаба (в хостах), своих
	// настроек нет.
	if list[4].Port != 9220 || len(list[4].Settings) != 0 || list[1].Port != 9230 || len(list[1].Settings) != 0 {
		t.Fatalf("mikrotik / connector: %+v %+v", list[4], list[1])
	}
	if yc := list[6]; yc.Port != 9240 || len(yc.Settings) != 3 || !yc.Settings[0].Secret || !yc.Settings[1].Required {
		t.Fatalf("yandex-cloud: %+v", yc.Settings)
	}
	list = list[5:6]
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

// Плагин MikroTik — против поддельного REST RouterOS 7 по HTTPS: отпечаток
// сертификата, находки, конфигурация, действия, команды.
func TestMikroTikPluginPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	out, err := exec.Command(py, "-I", "mikrotik/deploy/test_plugin.py").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Плагин connector — против поддельных ssh, Keenetic и SNMP-агента:
// профили, ключ хоста, пароль не в аргументах.
func TestConnectorPluginPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	out, err := exec.Command(py, "-I", "connector/deploy/test_plugin.py").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Плагин Yandex Cloud — против поддельного API: подпись JWT (PS256)
// проверяет настоящий openssl, ВМ и как хосты, DNS для хаба.
func TestYandexCloudPluginPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	out, err := exec.Command(py, "-I", "yandex-cloud/deploy/test_plugin.py").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
