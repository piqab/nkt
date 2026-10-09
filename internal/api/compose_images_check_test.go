package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
)

// imagesCollector: на хосте есть только образы из local и каталоги из dirs.
type imagesCollector struct {
	collect.Collector
	local map[string]bool
	dirs  map[string]bool
}

func (f imagesCollector) RunTimeout(_ context.Context, _ time.Duration, name string, args ...string) (collect.CommandResult, error) {
	if len(args) >= 2 && args[0] == "image" && args[1] == "inspect" && f.local[args[len(args)-1]] {
		return collect.CommandResult{Stdout: "sha256:1"}, nil
	}
	return collect.CommandResult{ExitCode: 1}, nil
}

func (f imagesCollector) Exists(p string) bool { return f.dirs[p] }

func TestComposeImageIssues(t *testing.T) {
	// compose config --format json стека videolog: образ из .env, pull_policy
	// по умолчанию missing, build из каталога выше стека.
	cfg := `{"name":"videolog-station","services":{
	  "station":{"image":"rgstr.example.com/test/videolog/videolog-station:latest","pull_policy":"missing",
	    "build":{"context":"/srv/compose/.nkt-check/x/../../..","dockerfile":"build/station.Dockerfile"}},
	  "web":{"image":"http://rgstr.example.com/test/web:1"},
	  "worker":{"image":"/test/worker:1"},
	  "cron":{"image":"rgstr.example.com/test/cron:1","pull_policy":"never"},
	  "db":{"image":"postgres:16"},
	  "local":{"build":"/srv/compose/.nkt-check/x/app","pull_policy":"build"}}}`
	svcs := parseComposeServices(cfg)
	if len(svcs) != 6 || svcs[3].Name != "station" || !svcs[3].Build || svcs[3].PullPolicy != "missing" {
		t.Fatalf("%+v", svcs)
	}
	c := imagesCollector{
		local: map[string]bool{"rgstr.example.com/test/videolog/videolog-station:latest": true},
		dirs:  map[string]bool{"/srv/compose/.nkt-check/x/app": true},
	}
	issues, bad := composeImageIssues(context.Background(), c, "docker", "/srv/compose/.nkt-check/x", svcs)
	var got []string
	for _, is := range issues {
		got = append(got, is.Service+":"+is.Kind+":"+is.Context)
	}
	want := "cron:never: station:stale: station:build_outside:../../.. web:scheme: worker:nohost:"
	if strings.Join(got, " ") != want {
		t.Fatalf("%s\nждали %s", strings.Join(got, " "), want)
	}
	if !bad["/test/worker:1"] || !bad["http://rgstr.example.com/test/web:1"] || bad["postgres:16"] {
		t.Fatal(bad)
	}
	if parseComposeServices("services:\n  a: {}\n") != nil {
		t.Fatal("YAML вместо JSON — пусто")
	}
}
