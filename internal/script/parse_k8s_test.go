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
		// ключ без значения последним словом — ошибка, а не выход за границы
		"fly route hello https names",
		"fly route mc tcp port",
		"fly route hello https names a.example.com tls",
		"fly route hello https names a.example.com check",
		"fly route hello https names a.example.com targets cluster lab port",
		"fly route mc tcp port 25565 target web1 vm mc",
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

// Сервис из кластера наружу: без значений параметры становятся
// плейсхолдерами и проверка проходит с пустыми Refs, со значениями —
// кластер берётся из Refs; ingress, манифест и маршрут несут подставленное.
func TestK8sFlyServiceExample(t *testing.T) {
	sc, issues := Parse(K8sFlyServiceExample, nil)
	if len(issues) > 0 {
		t.Fatalf("строка %d: %v", issues[0].Line, issues[0].Err)
	}
	if issues := Check(sc, Refs{}); len(issues) > 0 {
		t.Fatalf("check: строка %d: %v", issues[0].Line, issues[0].Err)
	}
	vals := map[string]string{"param:CLUSTER": "lab", "param:DOMAIN": "shop.example.com"}
	sc, issues = Parse(K8sFlyServiceExample, vals)
	if len(issues) > 0 {
		t.Fatalf("строка %d: %v", issues[0].Line, issues[0].Err)
	}
	if issues := Check(sc, Refs{Clusters: map[string]bool{"lab": true}}); len(issues) > 0 {
		t.Fatalf("check: строка %d: %v", issues[0].Line, issues[0].Err)
	}
	var ingress, apply, route *Step
	for i := range sc.Steps {
		switch sc.Steps[i].Kind {
		case KindK8sIngress:
			ingress = &sc.Steps[i]
		case KindK8sApply:
			apply = &sc.Steps[i]
		case KindFlyRoute:
			route = &sc.Steps[i]
		}
	}
	if ingress == nil || ingress.Name != "lab" || ingress.Args["port"] != "8080" {
		t.Fatalf("ingress: %+v", ingress)
	}
	if apply == nil || apply.Name != "lab" || !strings.Contains(apply.Block, "host: shop.example.com") || !strings.Contains(apply.Block, "ingressClassName: cilium") {
		t.Fatalf("apply: %+v", apply)
	}
	if route == nil || route.Action != "https" || route.Args["cluster"] != "lab" || route.Args["target_port"] != "8080" || route.Args["tls"] != "gateway" {
		t.Fatalf("route: %+v", route)
	}
	raw, err := os.ReadFile("../../examples/scripts/k8s-fly-service.nkt")
	if err != nil || string(raw) != K8sFlyServiceExample {
		t.Error("examples/scripts/k8s-fly-service.nkt разошёлся с K8sFlyServiceExample")
	}
}

func TestK8sCreateAddon(t *testing.T) {
	sc, issues := Parse("on hv1 k8s create lab nodes 1+2 addon longhorn=1.12.1\non hv1 k8s create lab2 addon longhorn", nil)
	if len(issues) > 0 {
		t.Fatalf("%v", issues[0].Err)
	}
	if sc.Steps[0].Args["addon"] != "longhorn=1.12.1" || sc.Steps[1].Args["addon"] != "longhorn" {
		t.Fatalf("%+v", sc.Steps)
	}
	for _, bad := range []string{"addon rook", "addon longhorn=latest", "addon longhorn=1.12"} {
		if _, issues := Parse("on hv1 k8s create lab "+bad, nil); len(issues) == 0 {
			t.Errorf("%s accepted", bad)
		}
	}
}

// «k8s create … internal» с блоком values для Cilium и «k8s КЛАСТЕР helm» —
// релиз с values-блоком и set-параметрами.
func TestParseK8sHelmAndInternal(t *testing.T) {
	text := strings.Join([]string{
		"on hv1 k8s create lab flavor kubeadm cni cilium no-kube-proxy internal gateway fly1",
		"  cni:",
		"    binPath: /usr/lib/cni",
		"end",
		"k8s lab helm cache chart redis repo https://charts.bitnami.com/bitnami version 20.1.4 namespace shop set replica.replicaCount=3,image.tag=7.2",
		"k8s lab helm op chart rgstroperator repo oci://ghcr.io/piqab/charts",
		"  key: value",
		"end",
	}, "\n")
	sc, issues := Parse(text, nil)
	if len(issues) > 0 {
		t.Fatalf("строка %d: %v", issues[0].Line, issues[0].Err)
	}
	if len(sc.Steps) != 3 {
		t.Fatalf("%+v", sc.Steps)
	}
	cr, h1, h2 := sc.Steps[0], sc.Steps[1], sc.Steps[2]
	if cr.Kind != KindK8sCreate || cr.Args["internal"] != "true" || cr.Args["cni"] != "cilium" ||
		cr.Args["no-kube-proxy"] != "true" || cr.Args["gateway"] != "fly1" ||
		!strings.Contains(cr.Block, "binPath: /usr/lib/cni") {
		t.Errorf("create: %+v", cr)
	}
	if h1.Kind != KindK8sHelm || h1.Name != "lab" || h1.Action != "cache" || h1.Args["chart"] != "redis" ||
		h1.Args["repo"] != "https://charts.bitnami.com/bitnami" || h1.Args["version"] != "20.1.4" ||
		h1.Args["namespace"] != "shop" || h1.Args["set"] != "replica.replicaCount=3,image.tag=7.2" || h1.Block != "" {
		t.Errorf("helm: %+v", h1)
	}
	if h2.Kind != KindK8sHelm || h2.Action != "op" || h2.Args["repo"] != "oci://ghcr.io/piqab/charts" ||
		!strings.Contains(h2.Block, "key: value") {
		t.Errorf("helm oci: %+v", h2)
	}
	// create выше по тексту заводит кластер lab — helm-шаги.check это видит.
	if issues := Check(sc, Refs{Hosts: map[string]bool{"hv1": true}}); len(issues) != 0 {
		t.Errorf("check: %v", issues)
	}
	if issues := Check(sc, Refs{}); len(issues) != 1 {
		t.Errorf("check без хоста: %v", issues)
	}
	// Блок values без cni cilium — ошибка проверки.
	bad, issues := Parse("on hv1 k8s create lab\n  a: b\nend", nil)
	if len(issues) != 0 {
		t.Fatalf("разбор: %v", issues)
	}
	if issues := Check(bad, Refs{Hosts: map[string]bool{"hv1": true}}); len(issues) != 1 {
		t.Errorf("блок без cilium: %v", issues)
	}
	for _, bad := range []string{
		"k8s lab helm cache",
		"k8s lab helm",
		"k8s lab helm cache chart redis namespace \"a b\"",
		"k8s lab helm cache chart redis badkey x",
	} {
		if _, issues := Parse(bad, nil); len(issues) == 0 {
			t.Errorf("принято: %s", bad)
		}
	}
}

// «k8s create» с addons-списком, haproxy и prepare — разбор и отказы.
func TestParseK8sCreateFlags(t *testing.T) {
	sc, issues := Parse(`on hv1 k8s create lab flavor kubeadm nodes "hv1: cp 1; hv2: w 1, lb cpu 1 mem 1024 disk 10 image debian-13" network bridge bridge br0 addons longhorn=1.12.1,ingress-nginx haproxy connect=5000,algo=roundrobin prepare expose api 6443`, nil)
	if len(issues) > 0 {
		t.Fatalf("строка %d: %v", issues[0].Line, issues[0].Err)
	}
	st := sc.Steps[0]
	if st.Args["addons"] != "longhorn=1.12.1,ingress-nginx" || st.Args["haproxy"] != "connect=5000,algo=roundrobin" ||
		st.Args["prepare"] != "true" || st.Args["flavor"] != "kubeadm" {
		t.Errorf("args: %+v", st.Args)
	}
	for text, want := range map[string]string{
		`on hv1 k8s create lab addons longhorn addon longhorn`: "badK8sAddonsBoth",
		`on hv1 k8s create lab addons "bad name"`:              "badK8sAddons",
		`on hv1 k8s create lab haproxy connect=x`:              "badK8sHaproxy",
		`on hv1 k8s create lab haproxy algo=rr`:                "badK8sHaproxy",
		`on hv1 k8s create lab haproxy unknown=1`:              "badK8sHaproxy",
		`on hv1 k8s create lab haproxy connect`:                "badK8sHaproxy",
	} {
		_, issues := Parse(text, nil)
		if len(issues) == 0 || !strings.Contains(issueKey(issues[0]), want) {
			t.Errorf("%q: %v, ожидалось %s", text, issues, want)
		}
	}
}
