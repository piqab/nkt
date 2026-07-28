package parse

import (
	"context"
	"testing"
)

func TestHAProxyParsesFixture(t *testing.T) {
	res := HAProxy(context.Background(), fixtureCollector(t), "/etc/haproxy/haproxy.cfg")
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}

	ports := map[int]string{}
	for _, e := range res.Endpoints {
		ports[e.Port] = e.Label
	}
	for port, want := range map[int]string{5432: "fe_postgres", 8090: "fe_http", 9001: "stats"} {
		if got := ports[port]; got != want {
			t.Errorf("порт %d: label=%q, ожидалось %q (все: %v)", port, got, want, ports)
		}
	}

	// The stats listener is bound to every interface and has no credentials.
	for _, e := range res.Endpoints {
		if e.Port != 9001 {
			continue
		}
		if !e.Public() {
			t.Errorf("stats должен быть виден как публичный, адрес=%s", e.Address)
		}
		if e.Extra["stats"] != "enabled" || e.Extra["stats_auth"] != "none" {
			t.Errorf("stats: ожидались флаги enabled/none, получено %v", e.Extra)
		}
	}

	up := findUpstream(res.Upstreams, "be_app")
	if up == nil {
		t.Fatalf("backend be_app не найден, есть: %d шт.", len(res.Upstreams))
	}
	if up.Algorithm != "leastconn" {
		t.Errorf("be_app: balance=%q, ожидался leastconn", up.Algorithm)
	}
	if up.Health != "httpchk GET /healthz" {
		t.Errorf("be_app: health=%q", up.Health)
	}
	if len(up.Servers) != 2 {
		t.Fatalf("be_app: серверов %d, ожидалось 2", len(up.Servers))
	}
	// Neither app server carries "check" — that is the point of this fixture.
	for _, s := range up.Servers {
		if s.Checked {
			t.Errorf("be_app/%s не должен иметь check", s.Name)
		}
	}

	pg := findUpstream(res.Upstreams, "be_postgres")
	if pg == nil {
		t.Fatal("backend be_postgres не найден")
	}
	if !pg.Servers[0].Checked {
		t.Error("be_postgres/pg1 должен иметь check")
	}
	if !pg.Servers[1].Backup {
		t.Error("be_postgres/pg2 должен быть backup")
	}
	if pg.Health != "pgsql-check user haproxy" {
		t.Errorf("be_postgres: health=%q", pg.Health)
	}

	// A listen section is both an endpoint and a pool.
	if findUpstream(res.Upstreams, "stats") != nil {
		t.Log("listen stats без server-строк корректно не стал upstream")
	}
}
