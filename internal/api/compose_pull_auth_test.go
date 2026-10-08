package api

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/config"
)

// Ключ registry на время pull: config.json с правами 0600 в своём каталоге,
// docker.io — под ключом Docker Hub; чужой каталог из параметров задания
// не принимается; кривой ключ — отказ.
func TestPullAuth(t *testing.T) {
	s := &Server{cfg: &config.Config{DataDir: t.TempDir()}}
	dir, err := s.writePullAuth(composeRegistryAuth{Host: "ghcr.io", User: "piqab", Token: "ghp_x"})
	if err != nil {
		t.Fatal(err)
	}
	if s.pullAuthDir(dir) != dir || s.pullAuthDir("/etc") != "" || s.pullAuthDir(filepath.Join(dir, "x")) != "" {
		t.Errorf("каталог из параметров: %q", s.pullAuthDir(dir))
	}
	st, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config.json: %v %v", st, err)
	}
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	_ = json.Unmarshal(raw, &cfg)
	if got, _ := base64.StdEncoding.DecodeString(cfg.Auths["ghcr.io"].Auth); string(got) != "piqab:ghp_x" {
		t.Errorf("auth: %q", got)
	}
	hub, err := s.writePullAuth(composeRegistryAuth{Host: "docker.io", User: "u", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(hub, "config.json"))
	cfg.Auths = nil
	_ = json.Unmarshal(raw, &cfg)
	if _, ok := cfg.Auths["https://index.docker.io/v1/"]; !ok {
		t.Errorf("Docker Hub: %s", raw)
	}
	for _, bad := range []composeRegistryAuth{
		{Host: "ghcr.io", User: "a:b", Token: "t"},
		{Host: "ghcr.io/evil", User: "u", Token: "t"},
		{Host: "ghcr.io", User: "u", Token: "t\nx"},
		{Host: "ghcr.io", User: "", Token: "t"},
	} {
		if _, err := s.writePullAuth(bad); err == nil {
			t.Errorf("принят: %+v", bad)
		}
	}
}
