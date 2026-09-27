package hub

import (
	"os"
	"os/exec"
	"testing"
)

// deploy/nkt-edge.service — тот же юнит, что ставит хаб (для ручной установки).
func TestEdgeUnitFileMatches(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/nkt-edge.service")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != EdgeUnit {
		t.Error("deploy/nkt-edge.service расходится с EdgeUnit")
	}
}

// deploy/nkt-edge-certbot-hook.sh — тот же deploy-hook, что ставит хаб.
func TestEdgeCertbotHookFileMatches(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/nkt-edge-certbot-hook.sh")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != EdgeCertbotHook {
		t.Error("deploy/nkt-edge-certbot-hook.sh расходится с EdgeCertbotHook")
	}
	if out, err := exec.Command("sh", "-n", "../../deploy/nkt-edge-certbot-hook.sh").CombinedOutput(); err != nil {
		t.Errorf("синтаксис hook: %v %s", err, out)
	}
}

// Имя указывает на VPS, если хоть один его адрес — адрес VPS.
func TestEdgeDNSMatch(t *testing.T) {
	var host []string
	for _, a := range []string{"83.136.232.148", "10.0.0.5", "83.136.232.148", "garbage", "2001:db8::1"} {
		host = appendIP(host, a)
	}
	if len(host) != 3 {
		t.Fatalf("%v", host)
	}
	if !ipsOverlap([]string{"83.136.232.148"}, host) || ipsOverlap([]string{"203.0.113.9"}, host) || ipsOverlap(nil, host) {
		t.Error("сравнение адресов")
	}
	if !ipsOverlap(appendIP(nil, "2001:0db8:0000::1"), host) {
		t.Error("нормализация IPv6")
	}
}
