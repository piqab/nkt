package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSetHostTerminalEnabled covers the flag that decides whether the hub
// passes NKT_TERMINAL_ENABLED=true when it (re)installs a host — off by
// default (matching the env var's own default), settable per host, and
// read back correctly through the same hostColumns/scanHost path every
// other host field goes through.
func TestSetHostTerminalEnabled(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	id, err := db.CreateHost(ctx, "h1", "10.0.0.1", 22, "root", HostAuthPassword, []byte("secret"))
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}

	got, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if got.TerminalEnabled {
		t.Error("TerminalEnabled on a freshly created host = true, want false (off by default)")
	}

	if err := db.SetHostTerminalEnabled(ctx, id, true); err != nil {
		t.Fatalf("SetHostTerminalEnabled(true): %v", err)
	}
	got, err = db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if !got.TerminalEnabled {
		t.Error("TerminalEnabled after SetHostTerminalEnabled(true) = false, want true")
	}

	if err := db.SetHostTerminalEnabled(ctx, id, false); err != nil {
		t.Fatalf("SetHostTerminalEnabled(false): %v", err)
	}
	got, err = db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if got.TerminalEnabled {
		t.Error("TerminalEnabled after SetHostTerminalEnabled(false) = true, want false")
	}

	if err := db.SetHostTerminalEnabled(ctx, 999999, true); err != ErrNotFound {
		t.Errorf("SetHostTerminalEnabled on unknown id: err = %v, want ErrNotFound", err)
	}
}
