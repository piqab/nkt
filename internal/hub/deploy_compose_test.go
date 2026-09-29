package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/deploy"
)

func TestCollectComposeFiles(t *testing.T) {
	src := t.TempDir()
	write := func(p, s string) {
		full := filepath.Join(src, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(s), 0o644)
	}
	write("deploy/docker-compose.yml", "services:\n  web:\n    image: ghcr.io/org/app:{{nkt.tag}}\n")
	write("deploy/nginx.conf", "server {}\n")
	write("deploy/conf/a.ini", "a=1\n")
	write("deploy/conf/sub/b.ini", "b=2\n")
	write("other/x.txt", "x\n")
	write("deploy/logo.bin", "\xff\xfe\x00")
	c := &deploy.ComposeSpec{File: "deploy/docker-compose.yml", Project: "app", Hosts: []string{"h"}, Files: []string{"deploy/nginx.conf", "deploy/conf/"}}
	files, main, err := collectComposeFiles(src, c, deploy.Vars{Tag: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if main != "docker-compose.yml" || len(files) != 4 || !strings.Contains(files["docker-compose.yml"], "app:v1.2.3") ||
		files["conf/sub/b.ini"] != "b=2\n" || files["nginx.conf"] == "" {
		t.Fatalf("%q %v", main, files)
	}
	c.Files = []string{"other/x.txt"}
	if _, _, err := collectComposeFiles(src, c, deploy.Vars{}); err == nil {
		t.Fatal("file outside the compose dir accepted")
	}
	c.Files = []string{"deploy/logo.bin"}
	if _, _, err := collectComposeFiles(src, c, deploy.Vars{}); err == nil {
		t.Fatal("binary file accepted")
	}
	// Как в postmanlabs/httpbin: образ собирается из исходников.
	write("deploy/docker-compose.yml", "services:\n  httpbin:\n    build: '.'\n")
	c.Files = nil
	if _, _, err := collectComposeFiles(src, c, deploy.Vars{}); err == nil || !strings.Contains(err.Error(), "httpbin") {
		t.Fatalf("build-only service accepted: %v", err)
	}
}

// Пример из examples/httpbin должен оставаться выкладываемым.
func TestHTTPBinExample(t *testing.T) {
	c := &deploy.ComposeSpec{File: "deploy/docker-compose.yml", Project: "httpbin", Hosts: []string{"h"}}
	files, main, err := collectComposeFiles("../../examples/httpbin", c, deploy.Vars{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files[main], "mccutchen/go-httpbin:2.25.0") {
		t.Fatal(files[main])
	}
	pl, err := os.ReadFile("../../examples/httpbin/deploy/pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := deploy.ParseSpec(string(pl))
	if err != nil || spec.Compose == nil || spec.Compose.File != "examples/httpbin/deploy/docker-compose.yml" {
		t.Fatalf("%+v %v", spec, err)
	}
}

func TestComposeSpecValidation(t *testing.T) {
	ok := "repo: https://codeberg.org/me/app.git\nref: main\naction: compose\ncompose:\n  file: deploy/docker-compose.yml\n  project: app\n  hosts: [web1]\n  wait_timeout: 2m\n  site: app.example.com\n"
	spec, err := deploy.ParseSpec(ok)
	if err != nil || spec.Compose.Wait().Minutes() != 2 || !spec.Compose.PullImages() {
		t.Fatalf("%+v %v", spec, err)
	}
	for _, bad := range []string{
		strings.Replace(ok, "project: app", "project: App!", 1),
		strings.Replace(ok, "  hosts: [web1]\n", "", 1),
		strings.Replace(ok, "file: deploy/docker-compose.yml", "file: ../etc/passwd", 1),
		strings.Replace(ok, "wait_timeout: 2m", "wait_timeout: 5h", 1),
		strings.Replace(ok, "site: app.example.com", "site: 'bad site'", 1),
	} {
		if _, err := deploy.ParseSpec(bad); err == nil {
			t.Fatalf("accepted:\n%s", bad)
		}
	}
}
