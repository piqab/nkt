package monitor

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/store"
)

// Живой ping до 127.0.0.1 и до имени, которого нет (.invalid): сеть WSL
// отвечает на ping любого IP, поэтому недоступность — через имя.
func TestProbeICMP(t *testing.T) {
	if _, err := exec.LookPath("ping"); err != nil {
		t.Skip("нет ping")
	}
	p := &Prober{cfg: &config.Config{ProbeTimeout: time.Second}}
	if e := p.probeICMP(context.Background(), store.Target{Kind: "icmp", Host: "127.0.0.1"}); e != "" {
		t.Skipf("ping в этой среде не работает: %s", e)
	}
	if e := p.probeICMP(context.Background(), store.Target{Kind: "icmp", Host: "nkt-test.invalid"}); e == "" {
		t.Error("nkt-test.invalid ответил")
	}
}
