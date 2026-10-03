package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/api"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
)

func TestParseImageRef(t *testing.T) {
	for ref, want := range map[string]imageRef{
		"gitea/gitea:28.0":           {"registry-1.docker.io", "gitea/gitea", "28.0"},
		"postgres":                   {"registry-1.docker.io", "library/postgres", "latest"},
		"docker.io/library/nginx:1":  {"registry-1.docker.io", "library/nginx", "1"},
		"ghcr.io/acme/app:2.1.0":     {"ghcr.io", "acme/app", "2.1.0"},
		"localhost:5000/team/svc:v1": {"localhost:5000", "team/svc", "v1"},
		"quay.io/minio/minio@sha256:" + strings.Repeat("a", 64): {"quay.io", "minio/minio", "sha256:" + strings.Repeat("a", 64)},
	} {
		got, err := parseImageRef(ref)
		if err != nil || got != want {
			t.Errorf("parseImageRef(%q) = %+v, %v; want %+v", ref, got, err, want)
		}
	}
	for _, bad := range []string{"Bad/Upper", "a/b:bad tag", "evil.com/../x", "x@sha256:zz"} {
		if _, err := parseImageRef(bad); err == nil {
			t.Errorf("parseImageRef(%q) accepted", bad)
		}
	}
}

// fakeRegistry — registry с анонимным токеном: список платформ → манифест
// amd64 → конфигурация с EXPOSE 22 и 3000.
func fakeRegistry(t *testing.T) *httptest.Server {
	cfgDigest := "sha256:" + strings.Repeat("c", 64)
	amdDigest := "sha256:" + strings.Repeat("a", 64)
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.URL.Query().Get("scope") != "repository:team/gitea:pull" {
				t.Errorf("scope %q", r.URL.Query().Get("scope"))
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v2/team/gitea/manifests/28.0":
			_, _ = w.Write([]byte(`{"manifests":[{"digest":"sha256:` + strings.Repeat("b", 64) + `","platform":{"architecture":"arm64","os":"linux"}},{"digest":"` + amdDigest + `","platform":{"architecture":"amd64","os":"linux"}}]}`))
		case "/v2/team/gitea/manifests/" + amdDigest:
			_, _ = w.Write([]byte(`{"config":{"digest":"` + cfgDigest + `"}}`))
		case "/v2/team/gitea/blobs/" + cfgDigest:
			_, _ = w.Write([]byte(`{"config":{"ExposedPorts":{"22/tcp":{},"3000/tcp":{}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImageExposedPortsFromRegistry(t *testing.T) {
	srv := fakeRegistry(t)
	host := strings.TrimPrefix(srv.URL, "https://")
	ports, err := imageExposedPorts(context.Background(), srv.Client(), host+"/team/gitea:28.0", "amd64")
	if err != nil || !slices.Equal(ports, []int{22, 3000}) {
		t.Fatalf("ports %v, %v", ports, err)
	}
	if _, err := imageExposedPorts(context.Background(), srv.Client(), host+"/team/gitea:28.0", "riscv64"); err == nil {
		t.Fatal("no riscv64 manifest, but no error")
	}
}

type runnerFunc func(ctx context.Context, jc *jobs.Context) error

func (f runnerFunc) Run(ctx context.Context, jc *jobs.Context) error { return f(ctx, jc) }

// Порт сайта: образ на хосте не скачан (порты только из compose) —
// берутся из registry; registry не ответил — предупреждение, не ошибка;
// порты из скачанного образа без site.port — ошибка.
func TestLogSitePortSources(t *testing.T) {
	srv := fakeRegistry(t)
	host := strings.TrimPrefix(srv.URL, "https://")
	_, db := newTestManager(t)
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	spec := &deploy.ComposeSpec{Project: "gitea", Site: &deploy.SiteSpec{Domains: []string{"git.example.com"}, Service: "gitea", Port: 3000}}
	run := func(res composeCheck, client *http.Client) (int, []string) {
		var problems int
		jm.Register("test.siteport", runnerFunc(func(ctx context.Context, jc *jobs.Context) error {
			problems, _ = logSitePort(ctx, jc, spec, res, client)
			return nil
		}))
		ctx := context.Background()
		id, err := jm.Start(ctx, jobs.Spec{Kind: "test.siteport", Title: "t", Steps: 1})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			j, _ := db.JobByID(ctx, id)
			if j.Done() || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		lines, _ := db.JobLog(ctx, id, 0, 50)
		var keys []string
		for _, l := range lines {
			keys = append(keys, l.Key)
		}
		return problems, keys
	}
	composeOnly := composeCheck{SiteChecked: true, SiteFound: true, SitePorts: []int{22}, SiteImage: host + "/team/gitea:28.0", SitePortsFrom: api.PortsFromCompose, HostArch: "amd64"}

	if n, keys := run(composeOnly, srv.Client()); n != 0 || !slices.Contains(keys, "deploy.drySitePortRegistry") || !slices.Contains(keys, "deploy.drySitePortOK") {
		t.Fatalf("registry: problems %d, log %v", n, keys)
	}
	// Registry недоступен (клиент не доверяет сертификату) — предупреждение.
	if n, keys := run(composeOnly, &http.Client{Timeout: 5 * time.Second}); n != 0 || !slices.Contains(keys, "deploy.drySitePortComposeOnly") {
		t.Fatalf("no registry: problems %d, log %v", n, keys)
	}
	fromImage := composeOnly
	fromImage.SitePortsFrom = api.PortsFromImage
	if n, keys := run(fromImage, srv.Client()); n != 1 || !slices.Contains(keys, "deploy.drySitePortBadOne") {
		t.Fatalf("image: problems %d, log %v", n, keys)
	}
}

func TestQuietSSH(t *testing.T) {
	in := "git ls-remote: Warning: Permanently added '[127.0.0.1]:2299' (ED25519) to the list of known hosts.\r\nalex@127.0.0.1: Permission denied (publickey).\r\nfatal: Could not read"
	if got := quietSSH(in); got != "git ls-remote:\nalex@127.0.0.1: Permission denied (publickey).\nfatal: Could not read" {
		t.Fatalf("%q", got)
	}
}
