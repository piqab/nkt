package svcdesc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const traefikDesc = `name: traefik
title: Traefik
unit: traefik
sources:
  - id: static
    file: /etc/traefik/traefik.yml
  - id: dynamic
    file: /etc/traefik/dynamic/*.yml
    optional: true
version:
  from: static
  value: "$.version | default(unknown)"
extract:
  endpoints:
    - from: static
      each: $.entryPoints.*
      fields:
        address: "@.address | host"
        port: "@.address | port"
        tls: "@.http.tls | exists"
  upstreams:
    - from: dynamic
      each: $.http.services.*
      fields:
        name: "@key"
      servers:
        each: "@.loadBalancer.servers[*]"
        fields:
          url: "@.url"
rules:
  - id: insecure-api
    from: static
    when: "@.api.insecure == true"
    severity: high
    title:
      ru: "Панель Traefik без защиты ({{@file}})"
      en: "Traefik dashboard is unprotected ({{@file}})"
    suggestion: "api.insecure: false"
`

const appDesc = `name: myapp
detect:
  any:
    - port: 9000
sources:
  - id: status
    command: [myapp, status, --json]
  - id: api
    http: http://127.0.0.1:9000/api/listeners
    interval: 1m
extract:
  endpoints:
    - from: status
      each: $.listen
      fields:
        port: "@.port"
        address: "@.addr"
    - from: api
      each: $[?@.enabled == true]
      fields:
        port: "@.port"
        protocol: udp
`

func TestEngine(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	write(t, root, "etc/traefik/traefik.yml", "version: 3.1\nentryPoints:\n  web:\n    address: \":80\"\n  websecure:\n    address: \"127.0.0.1:443\"\n    http:\n      tls: {}\napi:\n  insecure: true\n")
	write(t, root, "etc/traefik/dynamic/app.yml", "http:\n  services:\n    app:\n      loadBalancer:\n        servers:\n          - url: http://10.0.0.5:8080\n          - url: http://10.0.0.6\n")
	write(t, root, "etc/netknownsthat/services.d/traefik.yaml", traefikDesc)
	write(t, root, "etc/netknownsthat/services.d/myapp.yaml", appDesc)
	write(t, root, ".commands/index.json", `{"commands":[{"match":["myapp","status","--json"],"stdout":"{\"listen\":[{\"port\":9000,\"addr\":\"0.0.0.0\"}]}"}]}`)
	write(t, root, ".http/index.json", `{"http://127.0.0.1:9000/api/listeners":"listeners.json"}`)
	write(t, root, ".http/listeners.json", `[{"port":9001,"enabled":true},{"port":9002,"enabled":false}]`)
	// Два описания берут один файл: старший priority — владелец.
	write(t, root, "etc/netknownsthat/services.d/other.yaml", "name: other\npriority: 80\nsources:\n  - id: f\n    file: /etc/traefik/traefik.yml\n")

	c := collect.NewFixtures(root)
	e := NewEngine(c, HostDir, data)
	var trace []string
	e.Trace = func(s string) { trace = append(trace, s) }
	snap := &model.Snapshot{Listeners: []model.Listener{{Protocol: "tcp", Port: 9000}}}
	res := e.Run(context.Background(), snap)

	eps := map[string]model.Endpoint{}
	for _, ep := range res.Endpoints {
		eps[ep.Service+" "+ep.Socket()+"/"+ep.Protocol] = ep
	}
	if ep, ok := eps["traefik :80/tcp"]; !ok || ep.TLS || ep.Label != "Traefik · web" || ep.File != "/etc/traefik/traefik.yml" {
		t.Errorf("web: %+v (all %v)", ep, eps)
	}
	if ep, ok := eps["traefik 127.0.0.1:443/tcp"]; !ok || !ep.TLS {
		t.Errorf("websecure: %+v", ep)
	}
	if _, ok := eps["myapp 0.0.0.0:9000/tcp"]; !ok {
		t.Errorf("command endpoint: %v", eps)
	}
	if _, ok := eps["myapp :9001/udp"]; !ok || len(eps) != 4 {
		t.Errorf("http endpoint: %v", eps)
	}
	if len(res.Upstreams) != 1 || res.Upstreams[0].Name != "app" || len(res.Upstreams[0].Servers) != 2 ||
		res.Upstreams[0].Servers[1].Host != "10.0.0.6" || res.Upstreams[0].Servers[1].Port != 80 {
		t.Errorf("upstreams: %+v", res.Upstreams)
	}
	var finding, conflict *model.Finding
	for i, f := range res.Findings {
		switch f.Rule {
		case "svc.traefik.insecure-api":
			finding = &res.Findings[i]
		case "svcdesc-file-conflict":
			conflict = &res.Findings[i]
		}
	}
	if finding == nil || finding.Severity != "high" || finding.Title != "Панель Traefik без защиты (/etc/traefik/traefik.yml)" ||
		msgs.T(msgs.EN, finding.TitleKey, finding.TitleArgs...) != "Traefik dashboard is unprotected (/etc/traefik/traefik.yml)" {
		t.Errorf("finding: %+v", finding)
	}
	if conflict == nil || conflict.Service != "other" || conflict.Object != "/etc/traefik/traefik.yml" {
		t.Errorf("conflict: %+v", conflict)
	}
	owners := map[string]string{}
	for _, f := range res.Files {
		owners[f.Path] = f.Service
	}
	if owners["/etc/traefik/traefik.yml"] != "other" || owners["/etc/traefik/dynamic/app.yml"] != "traefik" {
		t.Errorf("files: %v", owners)
	}
	var version string
	for _, s := range res.Sources {
		if s.Name == "service:traefik" {
			version = s.Version
		}
	}
	if version != "3.1" {
		t.Errorf("version: %q", version)
	}
	if !strings.Contains(strings.Join(trace, "\n"), "$.entryPoints.* → 2") {
		t.Errorf("trace:\n%s", strings.Join(trace, "\n"))
	}

	// Описание сломали — работает прошлая исправная версия с предупреждением.
	write(t, root, "etc/netknownsthat/services.d/myapp.yaml", "name: myapp\nsources: [\n")
	res = e.Run(context.Background(), snap)
	var st model.SourceStatus
	for _, s := range res.Sources {
		if s.Name == "service:myapp" {
			st = s
		}
	}
	if !st.Available || len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "прошлая исправная") {
		t.Errorf("fallback: %+v", st)
	}

	// Выключить по имени.
	write(t, root, "etc/netknownsthat/services.d/zz-off.yaml", "name: other\ndisabled: true\n")
	res = e.Run(context.Background(), snap)
	for _, s := range res.Sources {
		if s.Name == "service:other" {
			t.Errorf("disabled description still runs")
		}
	}
	// Нет порта 9000 — myapp не найден, источники не читаются.
	res = e.Run(context.Background(), &model.Snapshot{})
	for _, ep := range res.Endpoints {
		if ep.Service == "myapp" {
			t.Errorf("myapp without detect: %+v", ep)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"native name":    "name: nginx\nsources: [{id: a, file: /x.yml}]\n",
		"no sources":     "name: a\n",
		"relative file":  "name: a\nsources: [{id: a, file: x.yml}]\n",
		"no format":      "name: a\nsources: [{id: a, file: /x.weird}]\n",
		"two kinds":      "name: a\nsources: [{id: a, file: /x.yml, http: 'http://x'}]\n",
		"shell":          "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, command: [bash, -c, 'rm -rf /']}]\n",
		"shell path":     "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, command: [/usr/bin/sudo, ls]}]\n",
		"creds in url":   "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, http: 'http://u:p@x/'}]\n",
		"auth header":    "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, http: 'http://x/', headers: {Authorization: x}}]\n",
		"need detect":    "name: a\nsources: [{id: a, http: 'http://x/'}]\n",
		"unknown key":    "name: a\nsoruces: []\n",
		"bad path":       "name: a\nsources: [{id: a, file: /x.yml}]\nextract: {endpoints: [{each: '$.[', fields: {port: 1}}]}\n",
		"no port":        "name: a\nsources: [{id: a, file: /x.yml}]\nextract: {endpoints: [{fields: {address: x}}]}\n",
		"unknown source": "name: a\nsources: [{id: a, file: /x.yml}]\nextract: {endpoints: [{from: b, fields: {port: 1}}]}\n",
		"bad severity":   "name: a\nsources: [{id: a, file: /x.yml}]\nrules: [{id: r, severity: huge, title: t, when: 'true'}]\n",
		"probe two":      "name: a\ndetect: {any: [{port: 1, file: /x}]}\nsources: [{id: a, file: /x.yml}]\n",
		"bad binary":     "name: a\ndetect: {any: [{binary: 'x; rm'}]}\nsources: [{id: a, file: /x.yml}]\n",
		"shadow":         "name: a\nsources: [{id: a, file: /etc/shadow, format: text}]\n",
		"host key":       "name: a\nsources: [{id: a, file: /etc/ssh/ssh_host_ed25519_key, format: text}]\n",
		"nkt env":        "name: a\nsources: [{id: a, file: /etc/netknownsthat/nkt.env}]\n",
		"user key":       "name: a\nsources: [{id: a, file: /home/u/.ssh/config, format: text}]\n",
	}
	for name, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
