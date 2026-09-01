package hub

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// TestImportHostsWithEmbeddedKeyReencrypts is the real point of the whole
// feature: two hubs with genuinely DIFFERENT master keys (newTestManager
// generates a fresh one each call, so m1.key != m2.key here) — export from
// m1 with the key embedded, import into m2, and the secret must decrypt
// under m2's own key afterward without m2 ever being told m1's key
// permanently. If the two keys matched by coincidence this test would
// prove nothing, so it also asserts they don't.
func TestImportHostsWithEmbeddedKeyReencrypts(t *testing.T) {
	m1, db1 := newTestManager(t)
	m2, db2 := newTestManager(t)
	ctx := context.Background()

	if string(m1.key) == string(m2.key) {
		t.Fatal("test setup bug: the two managers ended up with the same key")
	}

	id, err := m1.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "correct horse battery staple", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	adminPwEnc, err := secretbox.Encrypt(m1.key, []byte("admin-pw"))
	if err != nil {
		t.Fatalf("encrypt admin password: %v", err)
	}
	if err := db1.SetHostAdmin(ctx, id, "admin", adminPwEnc); err != nil {
		t.Fatalf("SetHostAdmin: %v", err)
	}
	tunnelTokenEnc, err := secretbox.Encrypt(m1.key, []byte("tunnel-token"))
	if err != nil {
		t.Fatalf("encrypt tunnel token: %v", err)
	}
	if err := db1.SetHostTunnelToken(ctx, id, tunnelTokenEnc); err != nil {
		t.Fatalf("SetHostTunnelToken: %v", err)
	}

	export, err := m1.ExportHosts(ctx, true)
	if err != nil {
		t.Fatalf("ExportHosts: %v", err)
	}
	if export.MasterKey == "" {
		t.Fatal("ExportHosts(ctx, true) did not embed a master key")
	}

	imported, errs := m2.ImportHosts(ctx, export)
	if len(errs) != 0 {
		t.Fatalf("ImportHosts errs = %v, want none", errs)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}

	hosts, err := db2.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("ListHosts = %+v, want one host", hosts)
	}

	secretPlain, err := secretbox.Decrypt(m2.key, hosts[0].SecretEnc)
	if err != nil {
		t.Fatalf("decrypting the imported SSH secret with m2's own key: %v", err)
	}
	if string(secretPlain) != "correct horse battery staple" {
		t.Errorf("decrypted secret = %q, want the original", secretPlain)
	}
	pwPlain, err := secretbox.Decrypt(m2.key, hosts[0].AdminPasswordEnc)
	if err != nil {
		t.Fatalf("decrypting the imported admin password with m2's own key: %v", err)
	}
	if string(pwPlain) != "admin-pw" {
		t.Errorf("decrypted admin password = %q, want %q", pwPlain, "admin-pw")
	}
	// Regression coverage for the gap this test exists to close: the tunnel
	// token was originally left out of reencryptHostSecrets entirely, so it
	// stayed encrypted under m1's key — m2's own tunnelDialOnce would then
	// fail to decrypt it on every single dial, forever, with the reverse-
	// tunnel fallback never coming up as a result.
	tokenPlain, err := secretbox.Decrypt(m2.key, hosts[0].TunnelTokenEnc)
	if err != nil {
		t.Fatalf("decrypting the imported tunnel token with m2's own key: %v", err)
	}
	if string(tokenPlain) != "tunnel-token" {
		t.Errorf("decrypted tunnel token = %q, want %q", tokenPlain, "tunnel-token")
	}

	// The whole point: db2's copy of the host must not still require m1's
	// key. Confirm it does NOT decrypt under it (would only coincidentally
	// pass if re-encryption silently hadn't happened for some other reason).
	if _, err := secretbox.Decrypt(m1.key, hosts[0].SecretEnc); err == nil {
		t.Error("imported secret still decrypts under the exporting hub's key — re-encryption did not happen")
	}
	if _, err := secretbox.Decrypt(m1.key, hosts[0].TunnelTokenEnc); err == nil {
		t.Error("imported tunnel token still decrypts under the exporting hub's key — re-encryption did not happen")
	}

	// db1 (the exporter) must be untouched by any of this.
	original, err := db1.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID on the exporting hub: %v", err)
	}
	if _, err := secretbox.Decrypt(m1.key, original.SecretEnc); err != nil {
		t.Errorf("the exporting hub's own copy no longer decrypts under its own key: %v", err)
	}
}

// TestImportHostsRejectsBadEmbeddedKey confirms a corrupted/garbage
// embedded key fails the whole import cleanly (a key that doesn't even
// base64-decode can't possibly belong to the export) rather than silently
// falling back to storing undecryptable ciphertext.
func TestImportHostsRejectsBadEmbeddedKey(t *testing.T) {
	m2, _ := newTestManager(t)
	ctx := context.Background()

	export := store.HubExport{
		Version:   store.ExportFormatVersion,
		MasterKey: "not valid base64!!!",
		Hosts: []store.HostExport{
			{Name: "h1", Addr: "10.0.0.1", SSHUser: "root", SSHAuthKind: store.HostAuthPassword, SecretEnc: []byte("x"), Status: store.HostStatusNew, CreatedAt: store.Now()},
		},
	}

	imported, errs := m2.ImportHosts(ctx, export)
	if imported != 0 {
		t.Errorf("imported = %d, want 0", imported)
	}
	if len(errs) == 0 {
		t.Fatal("expected an error for the unparseable embedded key")
	}
}

// TestImportHostsDropsEntryUndecryptableWithEmbeddedKey confirms that when
// a key IS embedded but a specific host's ciphertext doesn't actually
// decrypt under it (a mismatched/corrupted export), that one host is
// dropped and reported rather than imported with garbage secrets.
func TestImportHostsDropsEntryUndecryptableWithEmbeddedKey(t *testing.T) {
	m1, _ := newTestManager(t)
	m2, db2 := newTestManager(t)
	ctx := context.Background()

	// Encrypted with a THIRD key, not m1's — decrypting under the embedded
	// key (m1's) must fail.
	thirdKey, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	wrongCiphertext, err := secretbox.Encrypt(thirdKey, []byte("wrong-key-entirely"))
	if err != nil {
		t.Fatalf("encrypt with third key: %v", err)
	}

	export := store.HubExport{
		Version:   store.ExportFormatVersion,
		MasterKey: base64.StdEncoding.EncodeToString(m1.key),
		Hosts: []store.HostExport{
			{Name: "h1", Addr: "10.0.0.1", SSHUser: "root", SSHAuthKind: store.HostAuthPassword,
				SecretEnc: wrongCiphertext, Status: store.HostStatusNew, CreatedAt: store.Now()},
		},
	}

	imported, errs := m2.ImportHosts(ctx, export)
	if imported != 0 {
		t.Errorf("imported = %d, want 0", imported)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one", errs)
	}

	hosts, err := db2.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(hosts) != 0 {
		t.Errorf("ListHosts = %+v, want none imported", hosts)
	}
}
