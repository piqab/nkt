package api

import (
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/model"
)

// wg syncconf приводит интерфейс точно к файлу, а закрытого ключа в файле
// нет — сразу за ним ключ ставится заново, иначе туннель молча глохнет.
func TestFlyHostRefreshRestoresKey(t *testing.T) {
	i := strings.Index(flyHostRefresh, "wg syncconf")
	j := strings.Index(flyHostRefresh, `wg set "$1" private-key "$7"`)
	if i < 0 || j < i {
		t.Fatalf("private key is not re-set after syncconf:\n%s", flyHostRefresh)
	}
}

func TestFlyTargetIP(t *testing.T) {
	snap := &model.Snapshot{
		Container: []model.Container{{Name: "httpbin", Networks: []model.ContainerNetwork{{IPAddress: "172.19.0.7"}}}},
		VMs:       []model.VirtualMachine{{Name: "game", Networks: []model.VMNetIface{{IP: "fe80::1"}, {IP: "192.168.122.45"}}}},
		LXD:       []model.LXDInstance{{Name: "dns", IPv4: []string{"10.90.10.15"}}},
	}
	for _, c := range []struct{ kind, ref, want string }{
		{"container", "httpbin", "172.19.0.7"}, {"vm", "game", "192.168.122.45"}, {"lxd", "dns", "10.90.10.15"},
		{"container", "gone", ""}, {"host", "", ""}, {"addr", "10.0.0.1", ""},
	} {
		if got := flyTargetIP(snap, c.kind, c.ref); got != c.want {
			t.Errorf("%s %s: %q", c.kind, c.ref, got)
		}
	}
}
