package hub

import (
	"testing"

	"github.com/piqab/nkt/internal/store"
)

func TestDeployedStackMoves(t *testing.T) {
	prev := &deployedStack{Project: "portainer", Hosts: []deployedHost{{ID: 1, Name: "db-1"}, {ID: 2, Name: "web-1"}}}
	targets := []targetHost{{ID: 2, Name: "web-1"}, {ID: 3, Name: "web-2"}}
	if got := stackMovesFrom(prev, targets); len(got) != 1 || got[0].Name != "db-1" {
		t.Fatalf("уходит с: %+v", got)
	}
	// Переименование — только там, где стек был.
	if got := replaceOn(prev, 2, "portainer2"); got != "portainer" {
		t.Fatalf("replace на web-1: %q", got)
	}
	if got := replaceOn(prev, 3, "portainer2"); got != "" {
		t.Fatalf("replace на новом хосте: %q", got)
	}
	if got := replaceOn(prev, 2, "portainer"); got != "" {
		t.Fatalf("то же имя — не замена: %q", got)
	}
	if stackMovesFrom(nil, targets) != nil || replaceOn(nil, 2, "x") != "" {
		t.Fatal("без прежнего состояния нечего убирать")
	}
}

func TestHostNameMatching(t *testing.T) {
	if !sameHostName(" Web-1 ", "web-1") || sameHostName("web-1", "web-11") {
		t.Fatal("сравнение имён")
	}
	hosts := []store.Host{{Name: "web-1"}, {Name: "web-2"}, {Name: "db-1"}}
	if got := similarHosts("web", hosts); len(got) != 2 {
		t.Fatalf("похожие: %v", got)
	}
	if got := similarHosts("zzz", hosts); len(got) != 3 {
		t.Fatalf("похожих нет — все: %v", got)
	}
}
