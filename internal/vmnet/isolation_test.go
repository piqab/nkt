package vmnet

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

func TestIsolatedName(t *testing.T) {
	if IsolatedName(42) != "nkt-t42" || !IsIsolated("nkt-t42") {
		t.Fatal("name")
	}
	for _, n := range []string{"default", "nkt-t", "nkt-tx", "nkt-t1a", "lab-nkt-t1"} {
		if IsIsolated(n) {
			t.Errorf("%s: isolated", n)
		}
	}
}

// Правила: к хосту — только DNS, DHCP и ping; в частные сети и IPv6 —
// нет; внутрь сети — только ответы; без мостов — таблица снимается.
func TestIsolationRules(t *testing.T) {
	empty := IsolationRules(nil)
	if !strings.Contains(empty, "delete table inet nkt_isolation") || strings.Contains(empty, "chain") {
		t.Fatalf("empty: %s", empty)
	}
	rules := IsolationRules([]string{"virbr5", "virbr7"})
	for _, want := range []string{
		`elements = { "virbr5", "virbr7" }`,
		"192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10",
		"iifname @isolated udp dport { 53, 67 } accept",
		"iifname @isolated drop",
		`iifname "virbr5" oifname "virbr5" accept`,
		"iifname @isolated meta nfproto ipv6 drop",
		"iifname @isolated ip daddr @private4 ct state new,invalid,untracked drop",
		"oifname @isolated ct status dnat accept",
		"oifname @isolated ct state new,invalid,untracked drop",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("no %q in:\n%s", want, rules)
		}
	}
	// Синтаксис — у настоящего nft в своём сетевом пространстве, если он
	// и unshare без прав есть.
	if _, err := exec.LookPath("nft"); err != nil {
		return
	}
	path := filepath.Join(t.TempDir(), "r.nft")
	_ = os.WriteFile(path, []byte(rules), 0o600)
	out, err := exec.Command("unshare", "-rn", "nft", "-c", "-f", path).CombinedOutput()
	if err != nil && strings.Contains(string(out), "Operation not permitted") {
		t.Skip("no user namespaces")
	}
	if err != nil {
		t.Fatalf("nft -c: %v\n%s", err, out)
	}
}

// ApplyIsolation: мосты — из описаний сетей токенов, чужие сети не
// трогает; nft нет — ошибка, а не машина без изоляции.
func TestApplyIsolation(t *testing.T) {
	var applied string
	nft := true
	run := func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		switch strings.Join(argv[:2], " ") {
		case "virsh net-list":
			return collect.CommandResult{Stdout: "default\nnkt-t3\nnkt-t9\n"}, nil
		case "virsh net-dumpxml":
			br := map[string]string{"default": "virbr0", "nkt-t3": "virbr3", "nkt-t9": "virbr9"}[argv[2]]
			return collect.CommandResult{Stdout: "<network><name>" + argv[2] + "</name><forward mode='nat'/><bridge name='" + br + "'/></network>"}, nil
		case "sh -c":
			if !nft {
				return collect.CommandResult{ExitCode: 1}, nil
			}
			return collect.CommandResult{Stdout: "/usr/sbin/nft\n"}, nil
		case "lxc network":
			return collect.CommandResult{Stdout: "lxdbr0,bridge,YES,,\nnkt-t4,bridge,YES,,\nnkt-t7,macvlan,YES,,\n"}, nil
		case "nft -f":
			b, _ := os.ReadFile(argv[2])
			applied = string(b)
			return collect.CommandResult{}, nil
		}
		t.Fatalf("unexpected %v", argv)
		return collect.CommandResult{}, nil
	}
	m := NewManager(run, t.TempDir())
	bridges, err := m.ApplyIsolation(context.Background())
	if err != nil || strings.Join(bridges, ",") != "nkt-t4,virbr3,virbr9" {
		t.Fatalf("%v %v", bridges, err)
	}
	if !strings.Contains(applied, `"nkt-t4", "virbr3", "virbr9"`) || strings.Contains(applied, `"virbr0"`) || strings.Contains(applied, "lxdbr0") {
		t.Fatalf("rules: %s", applied)
	}
	// Почта: разрешение по имени сети — мост в mail_ok; снятие — пусто.
	if err := m.SetMailAllowed(context.Background(), "nkt-t9", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(applied, "set mail_ok {\n\t\ttype ifname\n\t\telements = { \"virbr9\" }") {
		t.Fatalf("mail: %s", applied)
	}
	_ = m.SetMailAllowed(context.Background(), "nkt-t9", false)
	if strings.Contains(applied, `"virbr9" }`+"\n\t}\n\tchain") || strings.Contains(applied, "elements = { \"virbr9\" }") {
		t.Fatalf("mail not removed: %s", applied)
	}
	if err := m.SetMailAllowed(context.Background(), "default", true); err == nil {
		t.Fatal("mail for a non-token network")
	}
	nft = false
	if _, err := m.ApplyIsolation(context.Background()); err == nil {
		t.Fatal("applied without nft")
	}
}

// Живой трафик: машина токена и «локальная сеть» в сетевых пространствах
// имён, правила — настоящие. Нужны unshare без прав, ip, nft и python3,
// поэтому только по NKT_NETNS=1.
func TestIsolationTraffic(t *testing.T) {
	if os.Getenv("NKT_NETNS") != "1" {
		t.Skip("NKT_NETNS=1")
	}
	rules := filepath.Join(t.TempDir(), "r.nft")
	_ = os.WriteFile(rules, []byte(IsolationRules([]string{"virbr5", "virbr6"}, "virbr6")), 0o644)
	out, err := exec.Command("timeout", "60", "unshare", "-rn", "--mount", "bash", "testdata/netns_check.sh", rules).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines := 0
	for _, line := range strings.Split(string(out), "\n") {
		got, want, ok := strings.Cut(line, " want ")
		if !ok {
			continue
		}
		lines++
		if !strings.HasSuffix(strings.TrimSpace(got), want) {
			t.Errorf("%s", line)
		}
	}
	if lines != 15 {
		t.Fatalf("checks: %d\n%s", lines, out)
	}
}
