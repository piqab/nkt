package hub

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

func newTestManager(t *testing.T) (*Manager, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return NewManager(&config.Config{}, db, key, "test"), db
}

// TestResolveAdminCredentialIsStableAcrossReinstalls reproduces the bug a
// reinstall used to hit: bootstrapLogin failing with "неверный логин или
// пароль" because a retry generated a fresh password that never matched
// whatever the remote's own accounts table was actually bootstrapped with
// on an earlier, partially-successful attempt. resolveAdminCredential must
// persist a generated password immediately and reuse it on every later
// call for the same host, never handing back two different passwords.
func TestResolveAdminCredentialIsStableAcrossReinstalls(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "ssh-pw")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	host, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	user1, pw1, err := m.resolveAdminCredential(ctx, id, host)
	if err != nil {
		t.Fatalf("resolveAdminCredential (first call): %v", err)
	}
	if user1 == "" || pw1 == "" {
		t.Fatalf("resolveAdminCredential returned empty user/password: %q/%q", user1, pw1)
	}

	// Simulate a reinstall: re-read the host row (now carrying whatever
	// resolveAdminCredential just persisted) exactly as install() does on
	// every call, and resolve again.
	host, err = db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID (reload): %v", err)
	}
	user2, pw2, err := m.resolveAdminCredential(ctx, id, host)
	if err != nil {
		t.Fatalf("resolveAdminCredential (second call): %v", err)
	}
	if user2 != user1 || pw2 != pw1 {
		t.Errorf("resolveAdminCredential is not stable across calls: got (%q,%q) then (%q,%q)",
			user1, pw1, user2, pw2)
	}
}

func TestAddHostRejectsBadKey(t *testing.T) {
	m, _ := newTestManager(t)
	_, err := m.AddHost(context.Background(), "h1", "10.0.0.1", 22, "root", store.HostAuthKey, "not a key")
	if err == nil {
		t.Fatal("expected AddHost to reject an unparsable private key")
	}
}

func TestUpdateHostRenameKeepsSecret(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "old-name", "10.0.0.1", 22, "root", store.HostAuthPassword, "s3cret")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	before, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	if err := m.UpdateHost(ctx, id, "new-name", "10.0.0.2", 2222, "admin", store.HostAuthPassword, ""); err != nil {
		t.Fatalf("UpdateHost: %v", err)
	}

	after, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID after update: %v", err)
	}
	if after.Name != "new-name" || after.Addr != "10.0.0.2" || after.SSHPort != 2222 || after.SSHUser != "admin" {
		t.Fatalf("UpdateHost did not apply the new fields: %+v", after)
	}
	if string(after.SecretEnc) != string(before.SecretEnc) {
		t.Error("UpdateHost with an empty secret must not touch the stored credential")
	}
}

func TestUpdateHostReplacesSecret(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "old-password")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	before, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	if err := m.UpdateHost(ctx, id, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "new-password"); err != nil {
		t.Fatalf("UpdateHost: %v", err)
	}

	after, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID after update: %v", err)
	}
	if string(after.SecretEnc) == string(before.SecretEnc) {
		t.Error("UpdateHost with a new secret must replace the stored credential")
	}
}

func TestAddHostGeneratedProducesAWorkingKeyPair(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, authorizedKey, err := m.AddHostGenerated(ctx, "h1", "10.0.0.1", 22, "root")
	if err != nil {
		t.Fatalf("AddHostGenerated: %v", err)
	}
	if authorizedKey == "" {
		t.Fatal("AddHostGenerated returned an empty authorized_keys line")
	}
	if !strings.HasPrefix(authorizedKey, "ssh-ed25519 ") {
		t.Errorf("authorized_keys line has an unexpected format: %q", authorizedKey)
	}

	host, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if host.SSHAuthKind != store.HostAuthKey {
		t.Errorf("stored auth kind = %q, want %q", host.SSHAuthKind, store.HostAuthKey)
	}

	// PublicKeyLine must derive exactly the same line from the stored
	// (encrypted) private key — it is the mechanism a caller uses to
	// re-fetch the key later, so it has to agree with what AddHostGenerated
	// already handed back once.
	got, err := m.PublicKeyLine(ctx, id)
	if err != nil {
		t.Fatalf("PublicKeyLine: %v", err)
	}
	if got != authorizedKey {
		t.Errorf("PublicKeyLine() = %q, want %q", got, authorizedKey)
	}
}

func TestPublicKeyLineRejectsPasswordHost(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if _, err := m.PublicKeyLine(ctx, id); err == nil {
		t.Fatal("expected PublicKeyLine to reject a password-auth host")
	}
}

func TestUpdateHostGeneratedRotatesKey(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, firstKey, err := m.AddHostGenerated(ctx, "h1", "10.0.0.1", 22, "root")
	if err != nil {
		t.Fatalf("AddHostGenerated: %v", err)
	}
	before, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	secondKey, err := m.UpdateHostGenerated(ctx, id, "h1", "10.0.0.1", 22, "root")
	if err != nil {
		t.Fatalf("UpdateHostGenerated: %v", err)
	}
	if secondKey == firstKey {
		t.Error("UpdateHostGenerated must produce a fresh key, not repeat the old one")
	}

	after, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID after update: %v", err)
	}
	if string(after.SecretEnc) == string(before.SecretEnc) {
		t.Error("UpdateHostGenerated must replace the stored credential")
	}
}

func TestUpdateHostRejectsBadKey(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	err = m.UpdateHost(ctx, id, "h1", "10.0.0.1", 22, "root", store.HostAuthKey, "not a key")
	if err == nil {
		t.Fatal("expected UpdateHost to reject an unparsable private key")
	}
}
