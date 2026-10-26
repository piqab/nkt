package control

import (
	"context"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// Размеры машины: диск только растёт — у запущенной blockresize, у
// выключенной qemu-img; уменьшение — отказ без команд; имена снимков —
// без опций virsh.
func TestLibvirtResizeAndSnapshots(t *testing.T) {
	base, db := libvirtSetup(t)
	var calls []string
	state := "running"
	esc := func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		line := strings.Join(argv, " ")
		calls = append(calls, line)
		res := collect.CommandResult{Argv: argv}
		switch {
		case strings.Contains(line, " domstate "):
			res.Stdout = state + "\n"
		case strings.HasPrefix(line, "qemu-img info"):
			res.Stdout = `{"virtual-size": 10737418240}`
		}
		return res, nil
	}
	m := NewLibvirtManager(base.cfg, base.c, db, base.scanner, esc)
	ctx := context.Background()

	if _, err := m.Resize(ctx, "t", "app1", 0, 0, 5); err == nil || !strings.Contains(err.Error(), "10") {
		t.Fatalf("shrink: %v", err)
	}
	calls = nil
	res, err := m.Resize(ctx, "t", "app1", 2, 1024, 20)
	if err != nil || !res.RestartNeeded {
		t.Fatalf("resize: %+v %v", res, err)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"setvcpus app1 2 --config --maximum", "setmaxmem app1 1048576 --config", "blockresize app1 /var/lib/libvirt/images/app1.qcow2 --size 20G"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q:\n%s", want, joined)
		}
	}
	state, calls = "shut off", nil
	if res, err := m.Resize(ctx, "t", "app1", 0, 0, 30); err != nil || res.RestartNeeded {
		t.Fatalf("stopped resize: %+v %v", res, err)
	}
	if j := strings.Join(calls, "\n"); !strings.Contains(j, "qemu-img resize /var/lib/libvirt/images/app1.qcow2 30G") || strings.Contains(j, "blockresize") {
		t.Fatalf("offline disk:\n%s", j)
	}
	for _, bad := range []string{"--atomic", "a b", "", "../x"} {
		if err := m.CreateSnapshot(ctx, "t", "app1", bad); err == nil {
			t.Errorf("snapshot name %q accepted", bad)
		}
	}
	if err := m.CreateSnapshot(ctx, "t", "app1", "before-upgrade"); err != nil {
		t.Fatal(err)
	}
}
