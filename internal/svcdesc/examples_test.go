package svcdesc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// Примеры из справки «Свои сервисы» → «Ещё примеры» (testdata/examples):
// каждый работает на тестовом хосте ровно так, как написано, и текст
// примера в справке (ru и en) совпадает с файлом.
func TestDocExamples(t *testing.T) {
	files, _ := filepath.Glob("testdata/examples/*.yaml")
	if len(files) != 6 {
		t.Fatalf("examples: %d", len(files))
	}
	var list []Loaded
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		list = append(list, Loaded{Desc: d, Origin: OriginHost, File: f})
		for _, page := range []string{"../../site/guide/service-descriptions.md", "../../site/en/guide/service-descriptions.md"} {
			doc, err := os.ReadFile(page)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(doc), "```yaml\n"+string(data)+"```") {
				t.Errorf("%s: the example differs from %s", page, f)
			}
		}
	}
	e := NewEngine(collect.NewFixtures("testdata/examples/host"), "", t.TempDir())
	snap := &model.Snapshot{Listeners: []model.Listener{{Protocol: "tcp", Port: 15672}}}
	res := e.RunLoaded(context.Background(), snap, list)
	var got []string
	for _, ep := range res.Endpoints {
		got = append(got, fmt.Sprintf("ep %s %s/%s tls=%v %q", ep.Service, ep.Socket(), ep.Protocol, ep.TLS, ep.Label))
	}
	for _, f := range res.Findings {
		got = append(got, fmt.Sprintf("finding %s %s %s", f.Rule, f.Severity, f.Object))
	}
	for _, s := range res.Sources {
		if s.Version != "" {
			got = append(got, "version "+s.Name+" "+s.Version)
		}
		for _, w := range s.Warnings {
			got = append(got, "warning "+s.Name+" "+w)
		}
	}
	sort.Strings(got)
	want := []string{
		`ep kafka :9092/tcp tls=false "Kafka PLAINTEXT"`,
		`ep kafka :9093/tcp tls=true "Kafka SSL"`,
		`ep memcached :11211/tcp tls=false "memcached"`,
		`ep mysql 0.0.0.0:3306/tcp tls=false "MySQL · mysqld"`,
		`ep tomcat :8080/tcp tls=false "Tomcat HTTP/1.1"`,
		`ep tomcat :8443/tcp tls=true "Tomcat org.apache.coyote.http11.Http11NioProtocol"`,
		`ep tomcat 0.0.0.0:8009/tcp tls=false "Tomcat AJP/1.3"`,
		`finding svc.consul.member-down medium node-2`,
		`finding svc.kafka.plaintext-all medium PLAINTEXT://:9092`,
		`finding svc.memcached.listen-all high 11211`,
		`finding svc.mysql.listen-all medium mysqld`,
		`finding svc.rabbitmq-api.many-queues low rabbitmq-api`,
		`finding svc.tomcat.ajp-no-secret high 8009`,
		`version service:consul 1.18.1`,
		`version service:rabbitmq-api 3.13.7`,
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
