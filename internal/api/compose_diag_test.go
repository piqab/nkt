package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
)

func TestComposeUpCause(t *testing.T) {
	base := errors.New("up failed")
	for _, tc := range []struct {
		out     string
		entries []composePSEntry
		want    string
	}{
		{"Error response from daemon: driver failed programming external connectivity on endpoint httpbin-httpbin-1 (x): Bind for 127.0.0.1:8080 failed: port is already allocated", nil, "127.0.0.1:8080"},
		{"Error response from daemon: failed to set up container networking: listen tcp4 127.0.0.1:8080: bind: address already in use", nil, "127.0.0.1:8080"},
		{"exec /usr/bin/gunicorn: exec format error", nil, "архитектуры"},
		// Docker Desktop (WSL).
		{"Error response from daemon: ports are not available: exposing port TCP 127.0.0.1:8080 -> 127.0.0.1:0: /forwards/expose returned unexpected status: 500", nil, "127.0.0.1:8080"},
		{"container httpbin-httpbin-1 exited (1)", []composePSEntry{{Service: "httpbin", State: "exited", ExitCode: 1}}, "httpbin"},
		{"", []composePSEntry{{Service: "db", State: "running", Health: "unhealthy"}}, "healthcheck"},
	} {
		got := msgs.Localize(msgs.RU, composeUpCause(tc.out, tc.entries, base))
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%q → %q, want %q", tc.out, got, tc.want)
		}
	}
	if got := composeUpCause("something else", nil, base); got != base {
		t.Fatalf("unknown cause replaced: %v", got)
	}
}

func TestManifestArchesAndPS(t *testing.T) {
	list := `{"manifests":[{"platform":{"architecture":"amd64","os":"linux"}},{"platform":{"architecture":"arm64","os":"linux"}},{"platform":{"architecture":"unknown","os":"unknown"}}]}`
	if got := manifestArches(list); strings.Join(got, ",") != "amd64,arm64" {
		t.Fatal(got)
	}
	if got := manifestArches(`{"config":{}}`); got != nil {
		t.Fatal(got)
	}
	lines := `{"Name":"a-web-1","Service":"web","State":"running","Publishers":[{"URL":"127.0.0.1","PublishedPort":8080,"TargetPort":80,"Protocol":"tcp"},{"URL":"::1","PublishedPort":8080,"TargetPort":80,"Protocol":"tcp"}]}
{"Name":"a-db-1","Service":"db","State":"exited","ExitCode":1}`
	ps := composePS(lines)
	if len(ps) != 2 || len(ps[0].Publishers) != 2 || ps[1].ExitCode != 1 {
		t.Fatalf("%+v", ps)
	}
	if ps := composePS("[" + strings.ReplaceAll(lines, "}\n{", "},{") + "]"); len(ps) != 2 {
		t.Fatalf("array form: %+v", ps)
	}
}
