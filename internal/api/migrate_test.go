package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Одиночный контейнер → compose: образ, порты, тома (external), restart,
// окружение — в .env, а не в compose-файле.
func TestStandaloneCompose(t *testing.T) {
	var c migrateInspect
	raw := `{"Name":"/web","Image":"sha256:abc","Config":{"Image":"nginx:1.27","Env":["PATH=/usr/bin","SECRET=s3 cret"],"Cmd":["nginx","-g","daemon off;"],"Labels":{"app":"x","com.docker.compose.project":"zz"}},
	"HostConfig":{"PortBindings":{"80/tcp":[{"HostIp":"","HostPort":"8080"}],"443/tcp":[{"HostIp":"127.0.0.1","HostPort":"8443"}]},"RestartPolicy":{"Name":"no"}},
	"Mounts":[{"Type":"volume","Name":"webdata","Destination":"/usr/share/nginx/html","RW":true},{"Type":"bind","Source":"/srv/web/conf","Destination":"/etc/nginx/conf.d","RW":false}]}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	compose, env, err := standaloneCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Image   string            `yaml:"image"`
			Restart string            `yaml:"restart"`
			Ports   []string          `yaml:"ports"`
			Volumes []string          `yaml:"volumes"`
			EnvFile []string          `yaml:"env_file"`
			Labels  map[string]string `yaml:"labels"`
		} `yaml:"services"`
		Volumes map[string]struct {
			External bool   `yaml:"external"`
			Name     string `yaml:"name"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		t.Fatalf("%v\n%s", err, compose)
	}
	w := doc.Services["web"]
	if w.Image != "nginx:1.27" || w.Restart != "unless-stopped" || strings.Join(w.Ports, ",") != "127.0.0.1:8443:443/tcp,8080:80/tcp" ||
		strings.Join(w.Volumes, ",") != "webdata:/usr/share/nginx/html,/srv/web/conf:/etc/nginx/conf.d:ro" || len(w.EnvFile) != 1 || w.Labels["app"] != "x" || w.Labels["com.docker.compose.project"] != "" {
		t.Fatalf("service: %+v\n%s", w, compose)
	}
	if !doc.Volumes["webdata"].External || doc.Volumes["webdata"].Name != "webdata" {
		t.Fatalf("volumes: %+v", doc.Volumes)
	}
	if strings.Contains(compose, "s3 cret") || !strings.Contains(env, "SECRET='s3 cret'") {
		t.Fatalf("env: %q\n%s", env, compose)
	}
}

// Манифест с другого хоста: системные пути и чужие имена файлов — отказ.
func TestReadManifestRejects(t *testing.T) {
	s := &Server{}
	base := t.TempDir()
	_ = base
	cases := []migrateManifest{
		{Plan: MigratePlan{Project: "p"}, Files: []migrateFile{{Name: "path-1.tgz", Kind: "path", Path: "/etc/cron.d"}}},
		{Plan: MigratePlan{Project: "p"}, Files: []migrateFile{{Name: "../x", Kind: "image"}}},
		{Plan: MigratePlan{Project: "p", WorkingDir: "/root/app"}},
		{Plan: MigratePlan{Project: "p", ConfigFiles: []string{"/usr/x.yml"}}},
	}
	for i, m := range cases {
		dir := filepath.Join(base, "migrate", "00000000000000"+strings.Repeat("0", 1)+string(rune('a'+i)))
		_ = os.MkdirAll(dir, 0o700)
		raw, _ := json.Marshal(m)
		_ = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600)
		if _, _, err := s.readManifestIn(base, filepath.Base(dir)); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if migrateSafePath("/home/app/data") == false || migrateSafePath("/var/lib/docker/volumes/x") || migrateSafePath("/srv/../etc") {
		t.Fatal("migrateSafePath")
	}
}
