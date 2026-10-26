package examples

// Тесты плагинов каталога на подделках (каждый — deploy/test_plugin.py):
// их же гоняет CI перед сборкой каталога (.github/workflows/pages.yml).

import (
	"os/exec"
	"testing"
)

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

// Плагин Windows — против поддельного ssh: загрузчик PowerShell,
// ответы JSON, пароль не в аргументах.
func TestWindowsPluginPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	out, err := exec.Command(py, "-I", "windows/deploy/test_plugin.py").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
