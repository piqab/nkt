package aptcache

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Живой тест зеркала и артефактов: Docker Hub (с токеном), quay.io, GitHub.
func TestRegistryMirrorLive(t *testing.T) {
	if os.Getenv("NKT_TEST_LIVE_REGISTRY") != "1" {
		t.Skip("set NKT_TEST_LIVE_REGISTRY=1")
	}
	c, err := New(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	defer srv.Close()
	get := func(p, accept string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+p, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, body
	}
	resp, _ := get("/v2/", "")
	if resp.StatusCode != 200 || resp.Header.Get("Docker-Distribution-API-Version") == "" {
		t.Fatalf("/v2/: %d", resp.StatusCode)
	}
	const accept = "application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.v2+json"
	for _, tc := range []struct{ path, name string }{
		{"/v2/alpine/manifests/latest?ns=docker.io", "docker.io alpine"},
		{"/v2/cilium/cilium/manifests/v1.16.5?ns=quay.io", "quay.io cilium"},
		{"/v2/pause/manifests/3.10?ns=registry.k8s.io", "registry.k8s.io pause"},
	} {
		resp, body := get(tc.path, accept)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", tc.name, resp.StatusCode, body)
		}
		digest := resp.Header.Get("Docker-Content-Digest")
		var m struct {
			Manifests []struct {
				Digest   string                            `json:"digest"`
				Platform struct{ Architecture, OS string } `json:"platform"`
			} `json:"manifests"`
			Layers []struct{ Digest string } `json:"layers"`
			Config struct{ Digest string }   `json:"config"`
		}
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("%s: manifest json: %v", tc.name, err)
		}
		t.Logf("%s: %s %s, %d платформ", tc.name, resp.Header.Get("Content-Type"), digest, len(m.Manifests))
		// Повтор — из кэша.
		resp2, _ := get(tc.path, accept)
		if resp2.Header.Get("X-Cache") != "nkt" {
			t.Errorf("%s: second request not from cache", tc.name)
		}
		// Манифест платформы и небольшой блоб (config).
		for _, pm := range m.Manifests {
			if pm.Platform.Architecture == "amd64" && pm.Platform.OS == "linux" {
				base := tc.path[:strings.Index(tc.path, "/manifests/")]
				ns := tc.path[strings.Index(tc.path, "?"):]
				r3, b3 := get(base+"/manifests/"+pm.Digest+ns, "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
				if r3.StatusCode != 200 {
					t.Fatalf("%s: platform manifest %d %s", tc.name, r3.StatusCode, b3)
				}
				var pmf struct{ Config struct{ Digest string } }
				_ = json.Unmarshal(b3, &pmf)
				r4, b4 := get(base+"/blobs/"+pmf.Config.Digest+ns, "")
				if r4.StatusCode != 200 || len(b4) == 0 || r4.Header.Get("Docker-Content-Digest") != pmf.Config.Digest {
					t.Fatalf("%s: config blob %d", tc.name, r4.StatusCode)
				}
				t.Logf("%s: config blob %d байт", tc.name, len(b4))
				break
			}
		}
	}
	// Артефакт с GitHub: неизменяемая ссылка с версией.
	r, b := get("/nkt/artifact?url=https://github.com/piqab/nkt/releases/download/v1.10.36/SHA256SUMS", "")
	if r.StatusCode != 200 || !strings.Contains(string(b), "nkt-linux-amd64") {
		t.Fatalf("artifact: %d %s", r.StatusCode, b)
	}
	r, _ = get("/nkt/artifact?url=https://github.com/piqab/nkt/releases/download/v1.10.36/SHA256SUMS", "")
	if r.Header.Get("X-Cache") != "nkt" {
		t.Errorf("artifact second request not from cache")
	}
	r, b = get("/nkt/artifact?url=https://get.k3s.io", "")
	if r.StatusCode != 200 || !strings.Contains(string(b), "#!/bin/sh") {
		t.Fatalf("get.k3s.io: %d", r.StatusCode)
	}
	st := c.Stats()
	t.Logf("stats: %+v", st)
}
