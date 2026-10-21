package script

import (
	"os"
	"strings"
	"testing"
)

// Команды готового кластера и маршрутов Traffic Fly.
func TestParseK8sFlyCommands(t *testing.T) {
	text := strings.Join([]string{
		"k8s lab ingress cilium port 9000",
		"k8s lab apply",
		"apiVersion: v1",
		"kind: Namespace",
		"metadata:",
		"  name: demo",
		"end",
		"k8s lab wait deployment hello namespace demo 3m",
		"fly route hello https names a.example.com,B.example.com tls gateway check http path /healthz targets cluster lab port 8080",
		"fly route mc tcp port 25565 target web1 container mc-1 25565 target web2 host 25565",
		"fly apply",
	}, "\n")
	sc, issues := Parse(text, nil)
	if len(issues) > 0 {
		t.Fatalf("строка %d: %v", issues[0].Line, issues[0].Err)
	}
	if len(sc.Steps) != 6 {
		t.Fatalf("%+v", sc.Steps)
	}
	in, ap, w, r1, r2, fa := sc.Steps[0], sc.Steps[1], sc.Steps[2], sc.Steps[3], sc.Steps[4], sc.Steps[5]
	if in.Kind != KindK8sIngress || in.Name != "lab" || in.Args["port"] != "9000" {
		t.Errorf("ingress: %+v", in)
	}
	if ap.Kind != KindK8sApply || !strings.Contains(ap.Block, "kind: Namespace") {
		t.Errorf("apply: %+v", ap)
	}
	if w.Kind != KindK8sWait || w.Args["deployment"] != "hello" || w.Args["namespace"] != "demo" || w.Args["timeout"] != "3m" {
		t.Errorf("wait: %+v", w)
	}
	if r1.Kind != KindFlyRoute || r1.Action != "https" || strings.Join(r1.List, " ") != "a.example.com b.example.com" || r1.Args["tls"] != "gateway" ||
		r1.Args["check"] != "http" || r1.Args["path"] != "/healthz" || r1.Args["cluster"] != "lab" || r1.Args["target_port"] != "8080" {
		t.Errorf("route https: %+v", r1)
	}
	ts := r2.FlyTargets()
	if r2.Args["port"] != "25565" || len(ts) != 2 || ts[0] != (FlyTarget{Host: "web1", Kind: "container", Ref: "mc-1", Port: 25565}) || ts[1] != (FlyTarget{Host: "web2", Kind: "host", Port: 25565}) {
		t.Errorf("route tcp: %+v %+v", r2, ts)
	}
	if fa.Kind != KindFlyApply {
		t.Errorf("fly apply: %+v", fa)
	}
	// Кластер lab неизвестен, хосты web1/web2 — тоже.
	if issues := Check(sc, Refs{}); len(issues) != 6 {
		t.Errorf("check: %d %v", len(issues), issues)
	}
	if issues := Check(sc, Refs{Clusters: map[string]bool{"lab": true}, Hosts: map[string]bool{"web1": true, "web2": true}}); len(issues) != 0 {
		t.Errorf("check: %v", issues)
	}
	for _, bad := range []string{
		"k8s lab ingress traefik",
		"k8s lab ingress cilium port 80",
		"k8s lab apply now",
		"k8s lab wait pod hello",
		"fly route hello https tls gateway targets cluster lab port 8080",
		"fly route mc tcp target web1 container mc 25565",
		"fly route hello https names a.example.com",
		"fly route hello https names a.example.com targets cluster lab port 8080 target web1 vm x 80",
		"fly route hello https names a.example.com check ping targets cluster lab port 8080",
		"fly route hello ftp names a.example.com targets cluster lab port 8080",
		"fly route mc tcp port 25565 target web1 vm mc 99999",
	} {
		if _, issues := Parse(bad, nil); len(issues) == 0 {
			t.Errorf("%q разобрано без ошибки", bad)
		}
	}
}

// Сценарий кластера: без параметров проверка проходит, со значениями —
// кластер из «k8s create» известен дальше по сценарию.
func TestK8sFirstPodExample(t *testing.T) {
	sc, issues := Parse(K8sFirstPodExample, nil)
	if len(issues) > 0 {
		t.Fatalf("строка %d: %v", issues[0].Line, issues[0].Err)
	}
	if issues := Check(sc, Refs{}); len(issues) > 0 {
		t.Fatalf("check: строка %d: %v", issues[0].Line, issues[0].Err)
	}
	vals := map[string]string{"param:HOST1": "hv1", "param:HOST2": "hv2", "param:DOMAIN": "hello.example.com"}
	sc, issues = Parse(K8sFirstPodExample, vals)
	if len(issues) > 0 {
		t.Fatalf("строка %d: %v", issues[0].Line, issues[0].Err)
	}
	if issues := Check(sc, Refs{Hosts: map[string]bool{"hv1": true, "hv2": true}}); len(issues) > 0 {
		t.Fatalf("check: строка %d: %v", issues[0].Line, issues[0].Err)
	}
	var create, apply *Step
	for i := range sc.Steps {
		switch sc.Steps[i].Kind {
		case KindK8sCreate:
			create = &sc.Steps[i]
		case KindK8sApply:
			apply = &sc.Steps[i]
		}
	}
	if create == nil || create.Args["flavor"] != "kubeadm" || create.Args["cni"] != "cilium" || create.Args["no-kube-proxy"] != "true" || create.Args["network"] != "wireguard" || create.Args["http"] != "0" {
		t.Fatalf("create: %+v", create)
	}
	if apply == nil || !strings.Contains(apply.Block, "host: hello.example.com") || !strings.Contains(apply.Block, "ingressClassName: cilium") {
		t.Fatalf("apply: %+v", apply)
	}
	raw, err := os.ReadFile("../../examples/scripts/k8s-first-pod.nkt")
	if err != nil || string(raw) != K8sFirstPodExample {
		t.Error("examples/scripts/k8s-first-pod.nkt разошёлся с K8sFirstPodExample")
	}
}
