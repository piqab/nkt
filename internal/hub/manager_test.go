package hub

import (
	"context"
	"path/filepath"
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
