package api

import (
	"slices"
	"testing"
)

func TestOneShotsDone(t *testing.T) {
	cfg := `{"services":{"forgejo":{"restart":"unless-stopped"},"db":{"restart":"unless-stopped"},"forgejo-admin":{"restart":"no"}}}`
	ok := []composePSEntry{
		{Service: "forgejo", State: "running", Health: "healthy"},
		{Service: "db", State: "running", Health: "healthy"},
		{Service: "forgejo-admin", State: "exited", ExitCode: 0},
	}
	if got := oneShotsDone(cfg, ok); !slices.Equal(got, []string{"forgejo-admin"}) {
		t.Fatalf("got %v", got)
	}
	failed := slices.Clone(ok)
	failed[2].ExitCode = 1
	if got := oneShotsDone(cfg, failed); got != nil {
		t.Fatalf("failed one-shot accepted: %v", got)
	}
	unhealthy := slices.Clone(ok)
	unhealthy[0].Health = "unhealthy"
	if got := oneShotsDone(cfg, unhealthy); got != nil {
		t.Fatalf("unhealthy accepted: %v", got)
	}
	// Долгоживущий сервис, который вышел с кодом 0, — не разовый.
	exited := slices.Clone(ok)
	exited[1].State, exited[1].Health = "exited", ""
	if got := oneShotsDone(cfg, exited); got != nil {
		t.Fatalf("exited long-running accepted: %v", got)
	}
	if got := oneShotsDone(cfg, ok[:2]); got != nil {
		t.Fatalf("nothing one-shot, got %v", got)
	}
}
