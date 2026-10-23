package script

import (
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
)

// «docker stack … up» без блока — поднять файл на хосте; с блоком — YAML
// до «end»; «down» блока не берёт; блок с командами сценария — ошибка.
func TestDockerStackBlock(t *testing.T) {
	issuesText := func(is []Issue) string {
		var out []string
		for _, i := range is {
			out = append(out, msgs.Localize(msgs.EN, i.Err))
		}
		return strings.Join(out, "; ")
	}
	// Сценарий ассистента: up без блока, дальше обычные команды.
	sc, is := Parse("on reg1-fr docker stack /srv/compose/shop/compose.yaml up\nwait url https://shop.example.com 200 5m\non reg1-fr service nginx reload\n", nil)
	if len(is) != 0 || len(sc.Steps) != 3 || sc.Steps[0].Block != "" || sc.Steps[1].Kind != KindWait {
		t.Fatalf("up without block: %s %+v", issuesText(is), sc.Steps)
	}
	// С блоком (комментарий перед YAML — тоже блок).
	sc, is = Parse("on web1 docker stack /srv/app/compose.yml up\n# app\nservices:\n  web:\n    image: nginx\nend\nwait web1 port 80 1m\n", nil)
	if len(is) != 0 || len(sc.Steps) != 2 || !strings.Contains(sc.Steps[0].Block, "image: nginx") || !strings.HasPrefix(sc.Steps[0].Block, "# app") {
		t.Fatalf("up with block: %s %+v", issuesText(is), sc.Steps)
	}
	// Блок с отступом в первой строке.
	if _, is = Parse("on web1 docker stack /srv/app/compose.yml up\n  services: {}\nend\n", nil); len(is) != 0 {
		t.Fatalf("indented block: %s", issuesText(is))
	}
	// down — без блока, даже если ниже YAML-подобное не идёт.
	sc, is = Parse("on web1 docker stack /srv/app/compose.yml down\non web1 docker stack /srv/app/compose.yml up\n", nil)
	if len(is) != 0 || len(sc.Steps) != 2 {
		t.Fatalf("down: %s", issuesText(is))
	}
	// «end» не на месте: блок проглотил команды — ошибка с номерами строк.
	_, is = Parse("on web1 docker stack /srv/app/compose.yml up\nservices: {}\nwait url https://x.example.com 200\non web1 service nginx reload\nend\n", nil)
	if len(is) != 1 || !strings.Contains(issuesText(is), "lines 3–4") {
		t.Fatalf("commands in block: %s", issuesText(is))
	}
	// Блок не закрыт — названа команда.
	_, is = Parse("on web1 docker stack /srv/app/compose.yml up\nservices: {}\n", nil)
	if len(is) != 1 || !strings.Contains(issuesText(is), "docker stack /srv/app/compose.yml up") {
		t.Fatalf("not closed: %s", issuesText(is))
	}
	// Блок compose — не YAML.
	_, is = Parse("on web1 docker stack /srv/app/compose.yml up\nservices: [\nend\n", nil)
	if len(is) != 1 || !strings.Contains(issuesText(is), "not YAML") {
		t.Fatalf("not yaml: %s", issuesText(is))
	}
	// Синтаксис в справке — блок в скобках (необязателен).
	if doc := docByKind(KindDockerStack); doc == nil || !strings.Contains(Localize(doc.Syntax, true), "[") {
		t.Fatalf("syntax: %+v", doc)
	}
}
