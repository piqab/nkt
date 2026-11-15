package hub

import (
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
)

// TestPresetScriptRoundTrip — набор полной формы (размеры строк, lb с
// haproxy, список дополнений, внутренний путь, порты, values Cilium)
// превращается в сценарий, который разбирается и проходит проверку без
// замечаний; старый набор с VIP-парой — с WARNING и одной lb.
func TestPresetScriptRoundTrip(t *testing.T) {
	form := `{
	  "name": "prod", "flavor": "kubeadm", "k8s_version": "1.34",
	  "cilium": true, "kpr": true,
	  "addons": {"longhorn": {"on": true, "version": "1.12.1"}, "ingress-nginx": {"on": true, "version": ""}, "kyverno": {"on": false, "version": ""}},
	  "network": "wireguard",
	  "rows": [
	    {"host": "hv1", "role": "control-plane", "kind": "vm", "count": 3, "vcpus": 4, "memory_mb": 8192, "disk_gb": 60, "image_id": "debian-13", "network": "", "bridge": "", "nic": ""},
	    {"host": "hv2", "role": "worker", "kind": "vm", "count": 2, "vcpus": 8, "memory_mb": 16384, "disk_gb": 200, "image_id": "debian-13", "network": "", "bridge": "", "nic": ""}
	  ],
	  "endpoints": {"hv1": "10.0.0.5", "hv2": "10.0.0.6"},
	  "expose": true, "ports": {"api": 6443, "http": 80, "https": 443},
	  "endpoint": "10.77.1.1:6443", "gateway_addr": "89.111.153.12", "allow": "203.0.113.0/24, 198.51.100.7",
	  "endpoint_internal": true,
	  "lb_enabled": true, "lb_count": 1,
	  "lb_rows": [{"host": "hv3", "role": "load-balancer", "kind": "vm", "count": 1, "vcpus": 1, "memory_mb": 1024, "disk_gb": 10, "image_id": "debian-13"}],
	  "lb_haproxy": {"connect": 5000, "client": 50000, "server": 50000, "maxConn": 2000, "statsPort": 8404, "algorithm": "source"},
	  "cilium_values": "cni:\n  binPath: /usr/lib/cni"
	}`
	text, err := presetScript(form, msgs.RU)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"on hv1 k8s create prod flavor kubeadm",
		`nodes "hv1: cp 3 cpu 4 mem 8192 disk 60 image debian-13, endpoint 10.0.0.5; hv2: w 2 cpu 8 mem 16384 disk 200 image debian-13, endpoint 10.0.0.6; hv3: lb cpu 1 mem 1024 disk 10 image debian-13"`,
		"network wireguard", "version 1.34", "cni cilium", "no-kube-proxy",
		"endpoint 10.77.1.1:6443", "gateway 89.111.153.12", "internal",
		"allow 203.0.113.0/24,198.51.100.7", "addons ingress-nginx,longhorn=1.12.1",
		"haproxy connect=5000,client=50000,server=50000,maxconn=2000,stats=8404,algo=source",
		"expose api 6443 http 80 https 443",
		"cni:\n  binPath: /usr/lib/cni\nend",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("нет %q:\n%s", want, text)
		}
	}
	sc, issues := script.Parse(text, nil)
	if len(issues) > 0 {
		t.Fatalf("разбор, строка %d: %v\n%s", issues[0].Line, issues[0].Err, text)
	}
	if issues := script.Check(sc, script.Refs{Hosts: map[string]bool{"hv1": true, "hv2": true, "hv3": true}}); len(issues) != 0 {
		t.Fatalf("проверка: %v\n%s", issues, text)
	}
	if st := sc.Steps[0]; st.Args["internal"] != "true" || st.Args["addons"] != "ingress-nginx,longhorn=1.12.1" ||
		!strings.Contains(st.Block, "binPath") {
		t.Errorf("step: %+v", st)
	}

	// Старый набор с парой балансировщиков и VIP: WARNING в шапке, одна
	// lb-запись; текст всё равно разбирается.
	legacy := `{"name":"old","rows":[{"host":"hv1","role":"control-plane","kind":"vm","count":1}],"lb_enabled":true,"lb_count":2,"lb_vip":"10.0.0.99","lb_rows":[{"host":"hv1","role":"load-balancer","kind":"vm","count":2}]}`
	text, err = presetScript(legacy, msgs.RU)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "WARNING") || !strings.Contains(text, "lb 2") {
		t.Errorf("legacy:\n%s", text)
	}
	if _, issues := script.Parse(text, nil); len(issues) != 0 {
		t.Errorf("legacy-разбор: %v\n%s", issues, text)
	}

	// Без cilium-поля (старый набор) — cilium включён по умолчанию формы.
	old := `{"name":"old2","rows":[{"host":"hv1","role":"control-plane","kind":"vm","count":1}],"longhorn":{"on":true,"version":"1.10.0"}}`
	text, err = presetScript(old, msgs.RU)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "cni cilium") || !strings.Contains(text, "addons longhorn=1.10.0") {
		t.Errorf("old:\n%s", text)
	}

	if _, err := presetScript(`{"name":"x"}`, msgs.RU); err == nil {
		t.Error("пустой набор принят")
	}
	if _, err := presetScript(`broken`, msgs.RU); err == nil {
		t.Error("битый JSON принят")
	}
}
