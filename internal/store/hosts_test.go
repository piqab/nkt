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

// TestSetHostTunnelEnabledAndToken covers the reverse-tunnel opt-in flag and
// its paired encrypted token — same off-by-default/per-host shape as
// TestSetHostTerminalEnabled, plus SetHostTunnelToken (stores whatever
// ciphertext the caller already encrypted — this package doesn't touch the
// plaintext at all, see its own doc comment).
func TestSetHostTunnelEnabledAndToken(t *testing.T) {
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
	if got.TunnelEnabled {
		t.Error("TunnelEnabled on a freshly created host = true, want false (off by default)")
	}
	if got.TunnelTokenEnc != nil {
		t.Errorf("TunnelTokenEnc on a freshly created host = %x, want nil", got.TunnelTokenEnc)
	}

	if err := db.SetHostTunnelEnabled(ctx, id, true); err != nil {
		t.Fatalf("SetHostTunnelEnabled(true): %v", err)
	}
	enc := []byte{1, 2, 3, 4}
	if err := db.SetHostTunnelToken(ctx, id, enc); err != nil {
		t.Fatalf("SetHostTunnelToken: %v", err)
	}

	got, err = db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if !got.TunnelEnabled {
		t.Error("TunnelEnabled after SetHostTunnelEnabled(true) = false, want true")
	}
	if string(got.TunnelTokenEnc) != string(enc) {
		t.Errorf("TunnelTokenEnc = %x, want %x", got.TunnelTokenEnc, enc)
	}

	if err := db.SetHostTunnelEnabled(ctx, id, false); err != nil {
		t.Fatalf("SetHostTunnelEnabled(false): %v", err)
	}
	got, err = db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if got.TunnelEnabled {
		t.Error("TunnelEnabled after SetHostTunnelEnabled(false) = true, want false")
	}
	// Turning the flag off does not itself clear a previously stored token
	// — only a fresh install/update (which calls SetHostTunnelToken
	// again) changes it, same as TerminalEnabled leaves no matching secret
	// to clear in the first place.
	if string(got.TunnelTokenEnc) != string(enc) {
		t.Errorf("TunnelTokenEnc after disabling = %x, want unchanged %x", got.TunnelTokenEnc, enc)
	}

	if err := db.SetHostTunnelEnabled(ctx, 999999, true); err != ErrNotFound {
		t.Errorf("SetHostTunnelEnabled on unknown id: err = %v, want ErrNotFound", err)
	}
	if err := db.SetHostTunnelToken(ctx, 999999, enc); err != ErrNotFound {
		t.Errorf("SetHostTunnelToken on unknown id: err = %v, want ErrNotFound", err)
	}
}
