package hub

import (
	"context"
	"os"
	osuser "os/user"
	"testing"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Перенесённый хост без nkt становится «не установлен» (с нуля, без
// версии), а не остаётся «в сети» со старой версией из файла.
func TestCheckImportedHostWithoutNkt(t *testing.T) {
	if _, err := os.Stat(remoteBinPath); err == nil {
		t.Skip("nkt is installed on this machine")
	}
	addr, port, keyPEM := startTestSSHD(t)
	me, _ := osuser.Current()
	m, db := newTestManager(t)
	ctx := context.Background()
	enc, _ := secretbox.Encrypt(m.key, keyPEM)
	id, err := db.CreateHost(ctx, "moved", addr, port, me.Username, store.HostAuthKey, enc)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.SetHostStatus(ctx, id, store.HostStatusOnline, "")
	_ = db.SetHostVersion(ctx, id, "1.11.100")
	h, _ := db.HostByID(ctx, id)
	m.checkImportedHost(ctx, h)
	h, _ = db.HostByID(ctx, id)
	if h.Status != store.HostStatusNew || h.NktVersion != "" {
		t.Fatalf("status %q version %q", h.Status, h.NktVersion)
	}
}
