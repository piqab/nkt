package script

import (
	"os"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/deploy"
)

// Команды выкладок: «pipeline … end», «deploy», «migrate», «wait url».
func TestParseDeployCommands(t *testing.T) {
	text := strings.Join([]string{
		"pipeline hello",
		"repo: https://example.com/r.git",
		"ref: main",
		"action: compose",
		"compose:",
		"  file: docker-compose.yml",
		"  project: hello",
		"  hosts: [web1]",
		"end",
		"deploy hello 20m",
		"migrate web1 hello-app-1 to web2 switch-fly switch-pipeline",
		"wait url https://hello.example.com/healthz 204 2m",
	}, "\n")
	sc, issues := Parse(text, nil)
	if len(issues) > 0 {
		t.Fatalf("%v", issues[0].Err)
	}
	if len(sc.Steps) != 4 {
		t.Fatalf("%+v", sc.Steps)
	}
	p, d, m, w := sc.Steps[0], sc.Steps[1], sc.Steps[2], sc.Steps[3]
	if p.Kind != KindPipeline || p.Name != "hello" || !strings.Contains(p.Block, "project: hello") {
		t.Errorf("pipeline: %+v", p)
	}
	if d.Kind != KindDeploy || d.Name != "hello" || d.Args["timeout"] != "20m" {
		t.Errorf("deploy: %+v", d)
	}
	if m.Kind != KindMigrate || m.Host != "web1" || m.Name != "hello-app-1" || m.Args["to"] != "web2" || m.Args["switch-fly"] != "true" || m.Args["switch-pipeline"] != "true" || m.Args["clean-leftovers"] != "" {
		t.Errorf("migrate: %+v", m)
	}
	if w.Kind != KindWait || w.Action != "url" || w.Host != "" || w.Args["code"] != "204" || w.Args["timeout"] != "2m" {
		t.Errorf("wait url: %+v", w)
	}
	// Проверка ссылок: deploy — конвейера из «pipeline» выше достаточно,
	// migrate — оба хоста должны быть известны.
	issues = Check(sc, Refs{Hosts: map[string]bool{"web1": true}})
	if len(issues) != 1 || !strings.Contains(issues[0].Err.Error(), "web2") {
		t.Errorf("check: %v", issues)
	}
	for _, bad := range []string{
		"deploy",
		"deploy hello soon",
		"migrate web1 app to web1",
		"migrate web1 app web2",
		"migrate web1 app to web2 now",
		"wait url ftp://example.com/",
		"wait url https://user:pw@example.com/",
		"pipeline",
	} {
		if _, issues := Parse(bad, nil); len(issues) == 0 {
			t.Errorf("%q разобрано без ошибки", bad)
		}
	}
	// deploy незнакомого конвейера — ошибка проверки.
	sc, _ = Parse("deploy nope", nil)
	if issues := Check(sc, Refs{}); len(issues) != 1 {
		t.Errorf("unknown pipeline: %v", issues)
	}
}

// Пример Forgejo: без значений параметров проверка проходит (имена хостов
// из параметров сверяются при запуске), а со значениями описание
// конвейера — правильное описание выкладки с маршрутами Traffic Fly.
func TestForgejoFlyExample(t *testing.T) {
	sc, issues := Parse(ForgejoFlyExample, nil)
	if len(issues) > 0 {
		t.Fatalf("строка %d: %v", issues[0].Line, issues[0].Err)
	}
	if issues := Check(sc, Refs{}); len(issues) > 0 {
		t.Fatalf("check: строка %d: %v", issues[0].Line, issues[0].Err)
	}
	vals := map[string]string{"param:FROM": "web1", "param:TO": "web2", "param:DOMAIN": "git.example.com"}
	sc, issues = Parse(ForgejoFlyExample, vals)
	if len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	if issues := Check(sc, Refs{Hosts: map[string]bool{"web1": true, "web2": true}}); len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	var block string
	for _, st := range sc.Steps {
		if st.Kind == KindPipeline {
			block = st.Block
		}
	}
	spec, err := deploy.ParseSpec(block)
	if err != nil {
		t.Fatal(err)
	}
	c := spec.Compose
	if c == nil || len(c.Hosts) != 1 || c.Hosts[0] != "web1" || len(c.Fly) != 2 || c.Fly[0].Names[0] != "git.example.com" {
		t.Fatalf("%+v", c)
	}
	// Тот же хост в «migrate» со значениями — ошибка.
	vals["param:TO"] = "web1"
	if _, issues := Parse(ForgejoFlyExample, vals); len(issues) == 0 {
		t.Error("перенос на тот же хост разобран без ошибки")
	}
}

// Файл примера в репозитории и пример в справке — один и тот же текст.
func TestForgejoFlyExampleFile(t *testing.T) {
	raw, err := os.ReadFile("../../examples/scripts/forgejo-fly.nkt")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != ForgejoFlyExample {
		t.Error("examples/scripts/forgejo-fly.nkt разошёлся с ForgejoFlyExample")
	}
}

// deploy ИМЯ start|stop [ДЛИТЕЛЬНОСТЬ] — запуск и остановка выложенного.
func TestParseDeployPower(t *testing.T) {
	for line, want := range map[string][2]string{
		"deploy shop stop":      {"stop", "30m"},
		"deploy shop start 10m": {"start", "10m"},
		"deploy shop 20m":       {"", "20m"},
	} {
		s, err := Parse(line, nil)
		if err != nil || len(s.Steps) != 1 || s.Steps[0].Kind != KindDeploy || s.Steps[0].Args["action"] != want[0] || s.Steps[0].Args["timeout"] != want[1] {
			t.Errorf("%s: %+v %v", line, s, err)
		}
	}
	for _, bad := range []string{"deploy shop restart", "deploy shop stop 1m extra", "deploy shop 1m stop"} {
		if _, err := Parse(bad, nil); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}
