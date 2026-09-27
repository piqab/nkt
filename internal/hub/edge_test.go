package hub

import (
	"os"
	"testing"
)

// deploy/nkt-edge.service — тот же юнит, что ставит хаб (для ручной установки).
func TestEdgeUnitFileMatches(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/nkt-edge.service")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != EdgeUnit {
		t.Error("deploy/nkt-edge.service расходится с EdgeUnit")
	}
}
