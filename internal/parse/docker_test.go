package parse

import (
	"context"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// TestDockerNeverReturnsNilSlices guards against a real production crash: a
// host with no docker socket and no compose files must still get real empty
// slices back, not nil — encoding/json marshals a nil slice as `null` (none
// of these fields have `omitempty`), and the frontend crashes calling
// .filter/.map on `null`. An empty fixtures root exercises exactly that.
func TestDockerNeverReturnsNilSlices(t *testing.T) {
	res := Docker(context.Background(), collect.NewFixtures(t.TempDir()), nil)
	if res.Containers == nil {
		t.Error("Containers = nil, ожидался непустой (даже если пустой) срез")
	}
	if res.Networks == nil {
		t.Error("Networks = nil, ожидался непустой (даже если пустой) срез")
	}
	if res.Endpoints == nil {
		t.Error("Endpoints = nil, ожидался непустой (даже если пустой) срез")
	}
	if res.Files == nil {
		t.Error("Files = nil, ожидался непустой (даже если пустой) срез")
	}
}

func TestDockerMergesComposeAndEngine(t *testing.T) {
	res := Docker(context.Background(), fixtureCollector(t), []string{"/srv/docker/docker-compose.yml"})
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}

	byName := map[string]model.Container{}
	for _, ct := range res.Containers {
		byName[ct.Name] = ct
	}
	for _, want := range []string{"acme-app", "acme-api", "acme-redis", "acme-postgres", "acme-grafana", "acme-prometheus", "acme-minio"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("контейнер %s не найден, есть: %v", want, keys(byName))
		}
	}

	app := byName["acme-app"]
	if !app.Declared || !app.Running {
		t.Errorf("acme-app должен быть и описан, и запущен: declared=%v running=%v", app.Declared, app.Running)
	}
	if app.Restart != "unless-stopped" {
		t.Errorf("acme-app: restart=%q (из compose)", app.Restart)
	}
	if len(app.Networks) == 0 || app.Networks[0].IPAddress == "" {
		t.Errorf("acme-app: IP из движка не подхватился: %+v", app.Networks)
	}

	redis := byName["acme-redis"]
	if len(redis.Ports) != 1 {
		t.Fatalf("acme-redis: портов %d, ожидался 1", len(redis.Ports))
	}
	if !redis.Ports[0].PublicallyBound() {
		t.Errorf("acme-redis: порт 6379 должен считаться открытым наружу: %+v", redis.Ports[0])
	}

	pg := byName["acme-postgres"]
	if len(pg.Ports) != 0 {
		t.Errorf("acme-postgres не публикует портов, получено %+v", pg.Ports)
	}

	minio := byName["acme-minio"]
	if minio.State != "restarting" {
		t.Errorf("acme-minio: state=%q, ожидалось restarting", minio.State)
	}

	// Published ports become endpoints; the internal-only database does not.
	ports := map[int]bool{}
	for _, e := range res.Endpoints {
		ports[e.Port] = true
		if e.Service != model.ServiceDocker {
			t.Errorf("endpoint %s: service=%q", e.ID, e.Service)
		}
	}
	for _, want := range []int{8080, 8081, 6379, 3000, 9090} {
		if !ports[want] {
			t.Errorf("endpoint для порта %d не создан (есть: %v)", want, ports)
		}
	}
	if ports[5432] {
		t.Error("postgres не публикует 5432 — endpoint не должен создаваться")
	}

	netNames := map[string]bool{}
	for _, n := range res.Networks {
		netNames[n.Name] = true
	}
	for _, want := range []string{"acme_backend", "acme_monitoring", "bridge"} {
		if !netNames[want] {
			t.Errorf("сеть %s не найдена: %v", want, netNames)
		}
	}
}

func TestParseComposePort(t *testing.T) {
	cases := []struct {
		in   string
		ip   string
		host int
		ctr  int
		prot string
	}{
		{"3000", "", 0, 3000, "tcp"},
		{"8080:80", "", 8080, 80, "tcp"},
		{"127.0.0.1:8080:80", "127.0.0.1", 8080, 80, "tcp"},
		{"5353:53/udp", "", 5353, 53, "udp"},
		{"8000-8010:8000-8010", "", 8000, 8000, "tcp"},
	}
	for _, c := range cases {
		got, ok := parseComposePort(c.in)
		if !ok {
			t.Errorf("%q: не разобрано", c.in)
			continue
		}
		if got.HostIP != c.ip || got.HostPort != c.host || got.ContainerPort != c.ctr || got.Protocol != c.prot {
			t.Errorf("%q -> %+v, ожидалось ip=%s host=%d ctr=%d proto=%s",
				c.in, got, c.ip, c.host, c.ctr, c.prot)
		}
	}
}

func keys(m map[string]model.Container) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPrimaryComposeFile covers the fix for a real "файл не найден" report:
// a stack brought up with several -f files gets a comma-joined label
// (com.docker.compose.project.config_files), and the whole joined string is
// never a real path on disk — "редактировать конфиг" must open the first
// (base) file, not the raw label.
func TestPrimaryComposeFile(t *testing.T) {
	cases := []struct {
		label string
		want  string
	}{
		{"/srv/docker/docker-compose.yml", "/srv/docker/docker-compose.yml"},
		{"/srv/docker/docker-compose.yml,/srv/docker/docker-compose.override.yml", "/srv/docker/docker-compose.yml"},
		{"", ""},
	}
	for _, c := range cases {
		if got := primaryComposeFile(c.label); got != c.want {
			t.Errorf("primaryComposeFile(%q) = %q, want %q", c.label, got, c.want)
		}
	}
}
