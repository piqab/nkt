package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestExportImportHostsRoundTrip covers the backup/restore path end to end
// on real SQLite databases: export everything CreateHost/SetHostAdmin/
// SetHostSudoStatus/SetHostTerminalEnabled can set on a host, import that
// export into a second, empty database, and confirm every field — most
// importantly the encrypted blobs, which this whole feature exists to
// carry through unexamined — survived intact.
func TestExportImportHostsRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatalf("Open src: %v", err)
	}
	defer src.Close()

	id, err := src.CreateHost(ctx, "h1", "10.0.0.1", 22, "root", HostAuthKey, []byte("cipher-secret"))
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	if err := src.SetHostAdmin(ctx, id, "admin", []byte("cipher-admin-pw")); err != nil {
		t.Fatalf("SetHostAdmin: %v", err)
	}
	if err := src.SetHostSudoStatus(ctx, id, SudoStatusNopasswd); err != nil {
		t.Fatalf("SetHostSudoStatus: %v", err)
	}
	if err := src.SetHostTerminalEnabled(ctx, id, true); err != nil {
		t.Fatalf("SetHostTerminalEnabled: %v", err)
	}
	if err := src.SetHostTunnelEnabled(ctx, id, true); err != nil {
		t.Fatalf("SetHostTunnelEnabled: %v", err)
	}
	if err := src.SetHostTunnelToken(ctx, id, []byte("cipher-tunnel-token")); err != nil {
		t.Fatalf("SetHostTunnelToken: %v", err)
	}
	if err := src.SetHostArch(ctx, id, "linux/amd64"); err != nil {
		t.Fatalf("SetHostArch: %v", err)
	}
	if err := src.SetHostVersion(ctx, id, "1.5.7"); err != nil {
		t.Fatalf("SetHostVersion: %v", err)
	}

	export, err := src.ExportHosts(ctx)
	if err != nil {
		t.Fatalf("ExportHosts: %v", err)
	}
	if export.Version != ExportFormatVersion {
		t.Errorf("export.Version = %d, want %d", export.Version, ExportFormatVersion)
	}
	if len(export.Hosts) != 1 {
		t.Fatalf("export.Hosts = %+v, want exactly one host", export.Hosts)
	}

	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	defer dst.Close()

	imported, errs := dst.ImportHosts(ctx, export)
	if len(errs) != 0 {
		t.Fatalf("ImportHosts errs = %v, want none", errs)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}

	got, err := dst.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListHosts = %+v, want one host", got)
	}
	h := got[0]
	if h.Name != "h1" || h.Addr != "10.0.0.1" || h.SSHUser != "root" || h.SSHAuthKind != HostAuthKey {
		t.Errorf("basic fields did not survive the round trip: %+v", h)
	}
	if string(h.SecretEnc) != "cipher-secret" {
		t.Errorf("SecretEnc = %q, want %q (ciphertext must round-trip byte for byte)", h.SecretEnc, "cipher-secret")
	}
	if string(h.AdminPasswordEnc) != "cipher-admin-pw" {
		t.Errorf("AdminPasswordEnc = %q, want %q", h.AdminPasswordEnc, "cipher-admin-pw")
	}
	if h.AdminUser != "admin" {
		t.Errorf("AdminUser = %q, want %q", h.AdminUser, "admin")
	}
	if h.SudoStatus != SudoStatusNopasswd {
		t.Errorf("SudoStatus = %q, want %q", h.SudoStatus, SudoStatusNopasswd)
	}
	if !h.TerminalEnabled {
		t.Error("TerminalEnabled = false, want true")
	}
	// The reverse-tunnel fallback is the one thing most worth carrying over
	// intact: a hub migrating to a new address/location — exactly what
	// export/import is for — is the single most likely time for SSH itself
	// to stop reaching a host (a firewall/security group allowlisting only
	// the old hub's IP), which is precisely the case this fallback exists
	// for. Dropping it silently here would leave a migrated host stuck with
	// no fallback exactly when it's needed most.
	if !h.TunnelEnabled {
		t.Error("TunnelEnabled = false, want true")
	}
	if string(h.TunnelTokenEnc) != "cipher-tunnel-token" {
		t.Errorf("TunnelTokenEnc = %q, want %q (ciphertext must round-trip byte for byte)", h.TunnelTokenEnc, "cipher-tunnel-token")
	}
	// TunnelCertSHA256 deliberately does NOT travel — see HostExport's own
	// doc comment: the new hub must pin its own trust-on-first-use
	// fingerprint on its first connection, not inherit one from whichever
	// hub exported this.
	if h.TunnelCertSHA256 != nil {
		t.Errorf("TunnelCertSHA256 = %x, want nil (must re-pin fresh on the new hub, not inherit the old one)", h.TunnelCertSHA256)
	}
	if h.Arch != "linux/amd64" || h.NktVersion != "1.5.7" {
		t.Errorf("Arch/NktVersion did not survive: %+v", h)
	}
}

// TestImportHostsPartialFailureKeepsGoing confirms one malformed entry
// (an invalid ssh_auth_kind here — the CHECK constraint schema.go already
// enforces) doesn't abort an otherwise-valid import: this is meant to
// tolerate a hand-edited or partially-corrupted export file.
func TestImportHostsPartialFailureKeepsGoing(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	export := HubExport{
		Version: ExportFormatVersion,
		Hosts: []HostExport{
			{Name: "good", Addr: "10.0.0.1", SSHUser: "root", SSHAuthKind: HostAuthPassword, SecretEnc: []byte("s"), Status: HostStatusNew, CreatedAt: Now()},
			{Name: "bad", Addr: "10.0.0.2", SSHUser: "root", SSHAuthKind: "not-a-real-kind", SecretEnc: []byte("s"), CreatedAt: Now()},
		},
	}

	imported, errs := db.ImportHosts(ctx, export)
	if imported != 1 {
		t.Errorf("imported = %d, want 1 (the good entry)", imported)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one error for the bad entry", errs)
	}

	got, err := db.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Errorf("ListHosts = %+v, want only the good host", got)
	}
}

func TestDecodeHubExportRejectsWrongVersion(t *testing.T) {
	_, err := DecodeHubExport([]byte(`{"version": 999, "hosts": []}`))
	if err == nil {
		t.Fatal("expected an error for an unsupported export version")
	}
}

func TestDecodeHubExportRejectsGarbage(t *testing.T) {
	_, err := DecodeHubExport([]byte(`not json at all`))
	if err == nil {
		t.Fatal("expected an error for a non-JSON file")
	}
}
