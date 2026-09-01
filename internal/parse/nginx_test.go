package parse

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

func fixtureCollector(t *testing.T) collect.Collector {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("resolve fixtures root: %v", err)
	}
	return collect.NewFixtures(root)
}

func TestNginxParsesFixtureTree(t *testing.T) {
	res := Nginx(context.Background(), fixtureCollector(t), "/etc/nginx/nginx.conf")
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}
	if len(res.Files) < 5 {
		t.Errorf("ожидалось не меньше 5 файлов (nginx.conf + include), получено %d: %v",
			len(res.Files), fileNames(res.Files))
	}

	byName := map[string]bool{}
	for _, u := range res.Upstreams {
		byName[u.Name] = true
	}
	for _, want := range []string{"app_backend", "api_backend", "static_backend"} {
		if !byName[want] {
			t.Errorf("upstream %s не найден, есть: %v", want, byName)
		}
	}

	app := findUpstream(res.Upstreams, "app_backend")
	if app == nil {
		t.Fatal("upstream app_backend отсутствует")
	}
	if len(app.Servers) != 2 {
		t.Fatalf("app_backend: ожидалось 2 сервера, получено %d", len(app.Servers))
	}
	if app.Servers[0].Host != "127.0.0.1" || app.Servers[0].Port != 8080 {
		t.Errorf("app_backend[0] = %s, ожидалось 127.0.0.1:8080", app.Servers[0].Socket())
	}
	if app.Algorithm != "least_conn" {
		t.Errorf("app_backend: алгоритм = %q, ожидался least_conn", app.Algorithm)
	}

	// Ports declared across the fixture tree.
	ports := map[int]int{}
	tlsOn := map[int]bool{}
	for _, e := range res.Endpoints {
		ports[e.Port]++
		if e.TLS {
			tlsOn[e.Port] = true
		}
	}
	for _, want := range []int{80, 443, 8443} {
		if ports[want] == 0 {
			t.Errorf("не найден endpoint на порту %d (найдено: %v)", want, ports)
		}
	}
	if !tlsOn[443] {
		t.Error("endpoint на 443 должен быть помечен как TLS")
	}
	if tlsOn[80] {
		t.Error("endpoint на 80 не должен быть помечен как TLS")
	}

	// metrics.example.com proxies to a bare address, not to a named upstream.
	var metrics *endpointRef
	for i := range res.Endpoints {
		for _, n := range res.Endpoints[i].Names {
			if n == "metrics.example.com" {
				metrics = &endpointRef{idx: i}
			}
		}
	}
	if metrics == nil {
		t.Fatal("server metrics.example.com не найден")
	}
	ep := res.Endpoints[metrics.idx]
	if len(ep.Routes) == 0 || ep.Routes[0].TargetKind != "address" {
		t.Errorf("metrics.example.com: ожидался маршрут на адрес, получено %+v", ep.Routes)
	}
}

type endpointRef struct{ idx int }

func findUpstream(list []model.Upstream, name string) *model.Upstream {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}
