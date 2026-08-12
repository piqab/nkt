package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestOpenAddsMissingHostsColumns reproduces a real failure: a hosts table
// created by an older build of this package (before admin_user/
// admin_password_enc/sudo_status existed) doesn't get those columns just
// because schema's CREATE TABLE IF NOT EXISTS changed — that statement is a
// no-op against a table that's already there. Open must retrofit them via
// addMissingColumns, or every read/write through a column this old a
// database doesn't have fails with "no such column".
func TestOpenAddsMissingHostsColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	// Simulate a database created before this package's schema grew any of
	// the migrated columns — deliberately not going through Open/schema.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	const oldHosts = `
CREATE TABLE hosts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    addr          TEXT NOT NULL,
    ssh_port      INTEGER NOT NULL DEFAULT 22,
    ssh_user      TEXT NOT NULL,
    ssh_auth_kind TEXT NOT NULL,
    secret_enc    BLOB NOT NULL,
    arch          TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'new',
    nkt_version   TEXT NOT NULL DEFAULT '',
    error_msg     TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    last_seen_at  TEXT
);`
	if _, err := raw.Exec(oldHosts); err != nil {
		t.Fatalf("create old hosts table: %v", err)
	}
	if _, err := raw.Exec(
		`INSERT INTO hosts(name, addr, ssh_user, ssh_auth_kind, secret_enc, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"old-host", "10.0.0.1", "root", HostAuthPassword, []byte("secret"), Now(),
	); err != nil {
		t.Fatalf("insert pre-existing row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}

	// The real thing under test: opening this pre-existing database through
	// the package's own Open must not error, and every later column must
	// end up usable.
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	hosts, err := db.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts (reads admin_user/admin_password_enc/sudo_status): %v", err)
	}
	if len(hosts) != 1 || hosts[0].Name != "old-host" {
		t.Fatalf("ListHosts = %+v, want the pre-existing row intact", hosts)
	}
	if hosts[0].SudoStatus != "" {
		t.Errorf("SudoStatus on a migrated old row = %q, want the column's own default ''", hosts[0].SudoStatus)
	}

	if err := db.SetHostSudoStatus(ctx, hosts[0].ID, SudoStatusNopasswd); err != nil {
		t.Fatalf("SetHostSudoStatus on a migrated column: %v", err)
	}
	got, err := db.HostByID(ctx, hosts[0].ID)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if got.SudoStatus != SudoStatusNopasswd {
		t.Errorf("SudoStatus after SetHostSudoStatus = %q, want %q", got.SudoStatus, SudoStatusNopasswd)
	}
}

// TestOpenIsIdempotentForMigrations confirms addMissingColumns doesn't
// error the second time it runs against an already-migrated database — the
// normal case, since every restart calls Open again.
func TestOpenIsIdempotentForMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nkt.db")
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("Open (second, must not fail re-adding existing columns): %v", err)
	}
	defer db2.Close()
}
