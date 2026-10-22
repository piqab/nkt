package svcdesc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// Встроенные описания разбираются без ошибок.
func TestBuiltinParse(t *testing.T) {
	e := NewEngine(nil, "", "")
	list := e.loadBuiltin()
	if len(list) != 16 {
		t.Fatalf("builtin: %d", len(list))
	}
	for _, l := range list {
		if l.Err != nil {
			t.Errorf("%s: %v", l.File, l.Err)
		}
	}
}

// Встроенные описания на тестовом хосте (testdata/host): адреса, пулы,
// находки, версии — ровно этот список.
func TestBuiltinGolden(t *testing.T) {
	c := collect.NewFixtures("testdata/host")
	e := NewEngine(c, HostDir, t.TempDir())
	res := e.Run(context.Background(), &model.Snapshot{})
	var got []string
	for _, ep := range res.Endpoints {
		got = append(got, fmt.Sprintf("ep %s %s/%s tls=%v %q", ep.Service, ep.Socket(), ep.Protocol, ep.TLS, ep.Label))
	}
	for _, u := range res.Upstreams {
		var srv []string
		for _, s := range u.Servers {
			srv = append(srv, s.Socket())
		}
		got = append(got, fmt.Sprintf("up %s %s %s", u.Service, u.Name, strings.Join(srv, ",")))
	}
	for _, f := range res.Findings {
		got = append(got, fmt.Sprintf("finding %s %s %s", f.Rule, f.Severity, f.Object))
	}
	for _, s := range res.Sources {
		if s.Version != "" {
			got = append(got, fmt.Sprintf("version %s %s", s.Name, s.Version))
		}
		for _, w := range s.Warnings {
			got = append(got, "warning "+s.Name+" "+w)
		}
	}
	sort.Strings(got)
	want := []string{
		`ep envoy 0.0.0.0:10000/tcp tls=false "Envoy ingress"`,
		`ep envoy 0.0.0.0:9901/tcp tls=false "Envoy admin"`,
		`ep mosquitto 10.0.0.2:8883/tcp tls=true "Mosquitto mqtt"`,
		`ep mosquitto :1883/tcp tls=false "Mosquitto mqtt"`,
		`ep mosquitto :9001/tcp tls=false "Mosquitto websockets"`,
		`ep postgresql *:5432/tcp tls=false "PostgreSQL 16/main"`,
		`ep rabbitmq :::15672/tcp tls=false "RabbitMQ http"`,
		`ep rabbitmq :::5671/tcp tls=true "RabbitMQ amqp/ssl"`,
		`ep rabbitmq :::5672/tcp tls=false "RabbitMQ amqp"`,
		`ep redis 0.0.0.0:6379/tcp tls=false "Redis"`,
		`ep redis 0.0.0.0:6380/tcp tls=true "Redis TLS"`,
		`ep traefik 127.0.0.1:53/udp tls=false "Traefik · dns"`,
		`ep traefik :443/tcp tls=true "Traefik · websecure"`,
		`ep traefik :80/tcp tls=false "Traefik · web"`,
		`finding svc.envoy.admin-public high admin`,
		`finding svc.mosquitto.anonymous-listener high 1883`,
		`finding svc.postgresql.hba-cleartext medium 192.168.1.0/24`,
		`finding svc.postgresql.hba-everyone medium 0.0.0.0/0`,
		`finding svc.postgresql.hba-trust critical 10.0.0.0/8`,
		`finding svc.postgresql.listen-all-no-ssl medium /etc/postgresql/16/main/postgresql.conf`,
		`finding svc.rabbitmq.guest-remote high /etc/rabbitmq/rabbitmq.conf`,
		`finding svc.rabbitmq.management-plain-public low 15672`,
		`finding svc.redis.no-auth-public high /etc/redis/redis.conf`,
		`finding svc.redis.protected-mode-off medium /etc/redis/redis.conf`,
		`finding svc.traefik.insecure-api high /etc/traefik/traefik.yml`,
		`up envoy backend 10.0.0.7:8000,10.0.0.8:8000`,
		`up traefik app 10.0.0.5:8080,10.0.0.6:8080`,
		`up traefik whoami-app@docker 172.18.0.4:80`,
		`version service:rabbitmq 3.13.7`,
		`version service:traefik 3.1.2`,
	}
	sort.Strings(want)
	units := map[string]string{}
	for _, d := range res.Detected {
		units[d.Name] = d.Unit
	}
	if len(units) != 6 || units["redis"] != "redis-server" || units["envoy"] != "" {
		t.Errorf("detected units: %v", units)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
