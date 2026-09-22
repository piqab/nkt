package aptcache

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCacheable(t *testing.T) {
	for p, want := range map[string]bool{
		"/debian/pool/main/n/nginx/nginx_1.24.0-2_amd64.deb":          true,
		"/debian/dists/bookworm/InRelease":                            false,
		"/debian/dists/bookworm/main/binary-amd64/Packages.xz":        false,
		"/debian/dists/bookworm/main/binary-amd64/by-hash/SHA256/abc": true,
		"/linux/debian/dists/bookworm/stable/binary-amd64/Packages":   false,
		"/something/tool.rpm":                                         true,
		"/index.html":                                                 false,
	} {
		if Cacheable(p) != want {
			t.Errorf("%s → %v", p, !want)
		}
	}
}

// Второй запрос того же .deb отдаётся из кэша, индексы идут насквозь,
// лимит вытесняет старое.
func TestProxyCachesPoolOnly(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		switch r.URL.Path {
		case "/debian/pool/main/a/a_1.deb":
			w.Write([]byte("deb-one"))
		case "/debian/pool/main/b/b_1.deb":
			w.Write([]byte("deb-two-bigger"))
		case "/debian/dists/x/InRelease":
			w.Header().Set("ETag", "v1")
			w.Write([]byte("release"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	dir := t.TempDir()
	c, err := New(dir, 12) // хватает на один .deb
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(c)
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	get := func(p string) (string, string) {
		resp, err := client.Get(upstream.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", p, resp.StatusCode, b)
		}
		return string(b), resp.Header.Get("X-Cache")
	}
	if body, xc := get("/debian/pool/main/a/a_1.deb"); body != "deb-one" || xc != "nkt" {
		t.Errorf("первый: %q %q", body, xc)
	}
	get("/debian/pool/main/a/a_1.deb")
	if upstreamHits != 1 {
		t.Errorf("второй запрос ушёл наверх: %d", upstreamHits)
	}
	if body, _ := get("/debian/dists/x/InRelease"); body != "release" {
		t.Errorf("индекс: %q", body)
	}
	get("/debian/dists/x/InRelease")
	if upstreamHits != 3 {
		t.Errorf("индекс должен идти насквозь каждый раз: %d", upstreamHits)
	}
	st := c.Stats()
	if st.Hits != 1 || st.Misses != 1 || st.Entries != 1 {
		t.Errorf("stats: %+v", st)
	}
	// Второй .deb больше лимита вместе с первым — первый вытесняется.
	get("/debian/pool/main/b/b_1.deb")
	if st := c.Stats(); st.Entries != 1 || st.SizeBytes != 14 {
		t.Errorf("после вытеснения: %+v", st)
	}
	resp, err := client.Get(upstream.URL + "/nope/pool/x.deb")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("404 наверху должен пройти как 404: %d", resp.StatusCode)
	}
}

// ?ns= зеркала registry входит в путь кэша и в адрес источника: «..» и
// «/» в нём — путь наружу из каталога кэша, поэтому принимается только
// имя хоста.
func TestRegistryMirrorRejectsBadNS(t *testing.T) {
	c, err := New(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	defer srv.Close()
	for _, ns := range []string{"../../etc", "a/b", "evil.example.com/..", "x y"} {
		resp, err := http.Get(srv.URL + "/v2/library/alpine/manifests/latest?ns=" + url.QueryEscape(ns))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("ns=%q: код %d, ждали 400", ns, resp.StatusCode)
		}
	}
	for _, ns := range []string{"ghcr.io", "registry.k8s.io", "quay.io", "localhost:5000", "docker.io"} {
		if !registryNSRe.MatchString(ns) {
			t.Errorf("ns=%q должен приниматься", ns)
		}
	}
}
