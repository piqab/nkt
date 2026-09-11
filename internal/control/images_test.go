package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/inventory"
)

// The reference goes into a URL path against the Docker socket, so a value
// carrying path or query characters would address something other than the
// image meant.
func TestCheckImageRef(t *testing.T) {
	allowed := []string{
		"nginx",
		"nginx:1.25",
		"ghcr.io/owner/app:v2",
		"sha256:0123456789abcdef",
		"0123456789ab",
		"registry.example.com:5000/team/app:latest",
		"app@sha256:0123456789abcdef",
	}
	for _, ref := range allowed {
		if err := checkImageRef(ref); err != nil {
			t.Errorf("checkImageRef(%q) = %v, want nil", ref, err)
		}
	}

	rejected := []string{
		"",
		"-f",                       // reads as a flag, not a name
		"nginx latest",             // whitespace
		"../../etc/passwd",         // climbs the API path
		"nginx/../../containers/x", // ditto, hidden mid-reference
		"nginx?force=1",            // smuggles a query parameter
		"nginx#tag",
		"nginx;rm -rf /",
	}
	for _, ref := range rejected {
		if err := checkImageRef(ref); err == nil {
			t.Errorf("checkImageRef(%q) = nil, want an error", ref)
		}
	}
}

// dockerFake answers /images/json with a canned body — the shape Docker
// actually returns, including the "<none>:<none>" an untagged image gets.
type dockerFake struct {
	collect.Collector
	body []byte
}

func (d dockerFake) DockerAPI(context.Context, string, string, []byte) ([]byte, int, error) {
	return d.body, 200, nil
}

func TestImageListParsesDockerOutput(t *testing.T) {
	body := []byte(`[
	  {"Id":"sha256:aaa","RepoTags":["nginx:1.25","nginx:latest"],"Size":142000000,"Created":1700000000},
	  {"Id":"sha256:bbb","RepoTags":["<none>:<none>"],"Size":900000,"Created":1700000100},
	  {"Id":"sha256:ccc","RepoTags":null,"Size":500,"Created":1700000200}
	]`)

	scanner := inventory.New(&config.Config{}, dockerFake{body: body}, nil)
	m := NewImageManager(dockerFake{body: body}, scanner, "/tmp/backups")

	images, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(images) != 3 {
		t.Fatalf("got %d images, want 3", len(images))
	}

	if got := images[0].Tags; len(got) != 2 || got[0] != "nginx:1.25" {
		t.Errorf("tags = %v", got)
	}
	if images[0].Dangling {
		t.Error("a tagged image must not be reported as dangling")
	}
	// Both of these are untagged: Docker spells it "<none>:<none>" in one
	// case and null in the other, and both mean the same thing.
	for _, i := range []int{1, 2} {
		if !images[i].Dangling {
			t.Errorf("image %d should be dangling, tags=%v", i, images[i].Tags)
		}
		if len(images[i].Tags) != 0 {
			t.Errorf("image %d should have no tags, got %v", i, images[i].Tags)
		}
	}
	if images[0].Created == "" {
		t.Error("created timestamp did not decode")
	}
}

// Образ без тега уходил в браузер как "tags": null, и первый же такой
// образ ронял страницу целиком: «Cannot read properties of null (reading
// 'length')». Проверяется не длина среза (у nil она тоже 0), а именно
// то, что попадает в JSON.
func TestImageListNeverEmitsNullTags(t *testing.T) {
	body := []byte(`[{"Id":"sha256:bbb","RepoTags":null,"Size":900000,"Created":1700000100}]`)
	scanner := inventory.New(&config.Config{}, dockerFake{body: body}, nil)
	m := NewImageManager(dockerFake{body: body}, scanner, "/tmp/backups")

	images, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	raw, err := json.Marshal(images)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"tags":[]`) {
		t.Errorf("в JSON нет пустого массива тегов: %s", raw)
	}
	if strings.Contains(string(raw), "null") {
		t.Errorf("в списке образов есть null: %s", raw)
	}
}
