package deploy

import (
	"strings"
	"testing"
)

const envSpec = `repo: https://github.com/org/app.git
ref: main
action: compose
compose:
  file: deploy/docker-compose.yml
  project: app
  hosts: [web1]
  env:
    AUTH_DOMAIN: auth.example.com   # имя сайта
    ADMIN_SECRET: generate:hex32
    DB_URL: "postgres://app:${DB_PASS}@db/app"
    QUOTED: 'it''s # not a comment'
    JWT: "@secret"
    EMPTY: ""
`

func TestEnvBlockStrip(t *testing.T) {
	if _, err := ParseSpec(envSpec); err != nil {
		t.Fatalf("parse: %v", err)
	}
	es, err := EnvBlock(envSpec)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 6 || es[1].Generate() != "hex32" || !es[4].Stored() || es[2].Value != "postgres://app:${DB_PASS}@db/app" || es[3].Value != "it's # not a comment" {
		t.Fatalf("entries: %+v", es)
	}
	out := StripEnvValues(envSpec, es)
	for _, want := range []string{
		`    AUTH_DOMAIN: "@secret"   # имя сайта`,
		`    ADMIN_SECRET: "@secret"`,
		`    DB_URL: "@secret"`,
		`    QUOTED: "@secret"`,
		`    JWT: "@secret"`,
		`    EMPTY: "@secret"`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Fatalf("no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "example.com") || strings.Contains(out, "hex32") || strings.Contains(out, "not a comment") {
		t.Fatalf("value left:\n%s", out)
	}
	// Остальной текст — как был.
	if !strings.HasPrefix(out, "repo: https://github.com/org/app.git\nref: main\n") {
		t.Fatal(out)
	}
	if _, err := ParseSpec(out); err != nil {
		t.Fatalf("stripped parse: %v", err)
	}
	if es2, _ := EnvBlock(out); HasEnvValues(es2) {
		t.Fatal("still has values")
	}
}

func TestEnvBlockErrors(t *testing.T) {
	bad := map[string]string{
		"flow":   "  env: {A: b}\n",
		"multi":  "  env:\n    A: |\n      x\n",
		"gen":    "  env:\n    A: generate:nope\n",
		"badKey": "  env:\n    1A: x\n",
	}
	base := "repo: https://github.com/org/app.git\nref: main\naction: compose\ncompose:\n  file: d.yml\n  project: app\n  hosts: [w]\n"
	for name, tail := range bad {
		_, err1 := ParseSpec(base + tail)
		_, err2 := EnvBlock(base + tail)
		if err1 == nil && err2 == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGenerateAndMerge(t *testing.T) {
	for k, n := range map[string]int{"hex16": 32, "hex32": 64, "password": 24} {
		if v := GenerateEnvValue(k); len(v) != n || strings.ContainsAny(v, "@:/#'\"$ ") {
			t.Errorf("%s: %q", k, v)
		}
	}
	if GenerateEnvValue("hex32") == GenerateEnvValue("hex32") {
		t.Fatal("not random")
	}
	old := "# мой .env\nA=1\nexport B=2\n"
	got := MergeEnv(old, []string{"C", "B", "D"}, map[string]string{"B": "two words", "C": "x", "D": "u:${A}@h"})
	want := "# мой .env\nA=1\nB='two words'\nC=x\nD=\"u:${A}@h\"\n"
	if got != want {
		t.Fatalf("merge:\n%q\nwant\n%q", got, want)
	}
}

func TestConfigsRenderMount(t *testing.T) {
	spec := `repo: https://github.com/org/app.git
ref: main
action: compose
compose:
  file: deploy/docker-compose.yml
  project: app
  hosts: [web1]
  configs:
    web:
      - target: /etc/nginx/conf.d/app.conf
        content: |
          server { server_name ${DOMAIN}; location / { proxy_set_header Host $host; } }
          # $${LITERAL}
      - target: /etc/app/x.yaml
        file: deploy/x.yaml
        mode: "0440"
        uid: 1000
`
	s, err := ParseSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.Compose.ConfigFiles(func(p string) (string, error) { return "key: ${SECRET}\n", nil })
	if err != nil || len(files) != 2 {
		t.Fatalf("%v %+v", err, files)
	}
	if files[0].Path != ".nkt-configs/web-1-app.conf" || files[0].Mode != 0o444 || files[1].Mode != 0o440 || *files[1].UID != 1000 {
		t.Fatalf("%+v", files)
	}
	if v := ConfigVars(files[0].Content); strings.Join(v, ",") != "DOMAIN" {
		t.Fatalf("vars %v", v)
	}
	got := RenderConfig(files[0].Content, map[string]string{"DOMAIN": "a.example.com", "host": "NO"})
	if !strings.Contains(got, "server_name a.example.com;") || !strings.Contains(got, "Host $host;") || !strings.Contains(got, "# ${LITERAL}") {
		t.Fatalf("render:\n%s", got)
	}
	compose := "services:\n  web:\n    image: nginx\n    labels:\n      - a=b\n"
	out, err := MountConfigs(compose, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"./.nkt-configs/web-1-app.conf:/etc/nginx/conf.d/app.conf:ro", "./.nkt-configs/web-2-x.yaml:/etc/app/x.yaml:ro", "nkt.config.web-1-app.conf="} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in\n%s", want, out)
		}
	}
	if _, err := MountConfigs("services:\n  db:\n    image: x\n", files); err == nil {
		t.Fatal("missing service accepted")
	}
	// Источник — ровно один.
	if _, err := ParseSpec(strings.Replace(spec, "        file: deploy/x.yaml\n", "", 1)); err == nil {
		t.Fatal("config without source accepted")
	}
}

func TestConfigSecretHints(t *testing.T) {
	c := ComposeSpec{Configs: map[string][]ConfigSpec{
		"a": {{Target: "/x", Content: "password = hunter2secret\n"}},
		"b": {{Target: "/y", Content: "password = \"${DB_PASS}\"\n"}},
	}}
	if h := c.ConfigSecretHints(); len(h) != 1 || h[0] != "a:/x" {
		t.Fatalf("%v", h)
	}
}
