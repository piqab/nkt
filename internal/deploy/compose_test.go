package deploy

import (
	"reflect"
	"testing"
)

func TestBuildOnlyServices(t *testing.T) {
	// docker-compose.yml из github.com/postmanlabs/httpbin.
	httpbin := "version: '2'\nservices:\n    httpbin:\n      build: '.'\n      ports:\n        - '80:80'\n"
	if got := BuildOnlyServices(httpbin); !reflect.DeepEqual(got, []string{"httpbin"}) {
		t.Fatal(got)
	}
	both := "services:\n  web:\n    build: .\n    image: ghcr.io/org/web:1\n  db:\n    image: postgres:16\n"
	if got := BuildOnlyServices(both); got != nil {
		t.Fatal(got)
	}
	if got := BuildOnlyServices("services: [broken"); got != nil {
		t.Fatal(got)
	}
}
