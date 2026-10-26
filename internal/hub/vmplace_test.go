package hub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Автовыбор хоста: только хосты в сети, не машины; без KVM, без памяти
// или диска — мимо; из подходящих — с наибольшей свободной памятью;
// токену — в пределах его хостов и потолков на хост; никто не подошёл —
// причины по каждому.
func TestPickVMHost(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	mk := func(name string) int64 {
		id, _ := db.CreateHost(ctx, name, "10.0.0."+name[len(name)-1:], 22, "root", store.HostAuthKey, []byte("x"))
		_ = db.SetHostStatus(ctx, id, "online", "")
		return id
	}
	big, small, nokvm, full := mk("h1"), mk("h2"), mk("h3"), mk("h4")
	off, _ := db.CreateHost(ctx, "h5", "10.0.0.5", 22, "root", store.HostAuthKey, []byte("x"))
	vm := mk("h6")
	_ = db.SetHostParent(ctx, vm, big)
	const gb = int64(1) << 30
	pf := map[int64]vmPreflight{
		big:   {KVM: true, LibvirtdActive: true, MemAvailable: 16 * gb, ImagesFree: 100 * gb},
		small: {KVM: true, LibvirtdActive: true, MemAvailable: 4 * gb, ImagesFree: 100 * gb},
		nokvm: {KVM: false, LibvirtdActive: true, MemAvailable: 64 * gb, ImagesFree: 500 * gb},
		full:  {KVM: true, LibvirtdActive: true, MemAvailable: 32 * gb, ImagesFree: 5 * gb},
	}
	asked := map[int64]bool{}
	saved := placePreflight
	t.Cleanup(func() { placePreflight = saved })
	placePreflight = func(_ *Server, _ context.Context, _ string, id int64, out *vmPreflight) error {
		asked[id] = true
		p, ok := pf[id]
		if !ok {
			return errors.New("unreachable")
		}
		*out = p
		return nil
	}
	spec := vmcreate.Spec{Name: "app1", VCPUs: 2, MemoryMB: 2048, DiskGB: 20}
	h, err := srv.pickVMHost(ctx, spec)
	if err != nil || h.ID != big {
		t.Fatalf("pick: %v %v", h.Name, err)
	}
	if asked[off] || asked[vm] {
		t.Fatal("asked an offline host or a machine")
	}
	// Памяти хватает только у h1 и h3 (без KVM), диска — не у h4.
	spec.MemoryMB = 8192
	if h, err := srv.pickVMHost(ctx, spec); err != nil || h.ID != big {
		t.Fatalf("big machine: %v %v", h.Name, err)
	}
	spec.MemoryMB = 20000
	_, err = srv.pickVMHost(ctx, spec)
	if err == nil || !strings.Contains(err.Error(), "h3") || !strings.Contains(err.Error(), "h4") {
		t.Fatalf("nothing fits: %v", err)
	}
	// Токен с пределом по хостам: из h2 и h4 подходит только h2.
	tok := store.APIToken{ID: 1, Name: "client", Role: store.TokenRoleCustom, Hosts: []int64{small, full}, Perms: store.TokenPerms{Actions: []string{store.TokenActVMManage}}}
	spec.MemoryMB = 1024
	if h, err := srv.pickVMHost(withToken(ctx, tok), spec); err != nil || h.ID != small {
		t.Fatalf("token pick: %v %v", h.Name, err)
	}
	// Потолок на хост: на h2 у токена уже занято — подходящих нет.
	tok.Perms.Limits.HostMemMB = 1500
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: 1, Kind: store.UsageVM, HostID: small, Ref: "old", MemMB: 1024})
	if _, err := srv.pickVMHost(withToken(ctx, tok), spec); err == nil {
		t.Fatal("host limit ignored")
	}
}
