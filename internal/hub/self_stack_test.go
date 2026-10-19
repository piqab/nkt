package hub

import (
	"testing"

	"github.com/piqab/nkt/internal/api"
	"github.com/piqab/nkt/internal/model"
)

func TestHubProjectIn(t *testing.T) {
	list := []model.Container{
		{ID: "aaaaaaaaaaaa1111", Project: "shop"},
		{ID: "0123456789ab9999", Project: "nkt-hub-test"},
	}
	if got := hubProjectIn(list, "0123456789ab"); got != "nkt-hub-test" {
		t.Fatalf("got %q", got)
	}
	if got := hubProjectIn(list, "ffffffffffff"); got != "" {
		t.Fatalf("foreign: %q", got)
	}
	if got := hubProjectIn(list, ""); got != "" {
		t.Fatalf("empty id: %q", got)
	}
}

func TestOtherHubAgo(t *testing.T) {
	seen := []api.HubSeen{{ID: "aa", AgoS: 5}, {ID: "bb", AgoS: 40}, {ID: "cc", AgoS: 20}}
	if got := otherHubAgo(seen, "aa"); got != 20 {
		t.Fatalf("got %d", got)
	}
	if got := otherHubAgo(seen[:1], "aa"); got != -1 {
		t.Fatalf("alone: %d", got)
	}
	if got := otherHubOr(hostOverview{}); got != -1 {
		t.Fatalf("unknown: %d", got)
	}
}
