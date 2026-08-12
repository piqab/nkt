// Package store is the SQLite persistence layer.
//
// A single file database is plenty for one host: write volume is a handful of
// rows per probe interval. The pool is capped at one connection so a writer can
// never collide with another writer; WAL keeps reads fast anyway.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin','viewer')),
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    last_login_at TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    user_agent TEXT
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS audit_log (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    ts       TEXT NOT NULL,
    username TEXT NOT NULL,
    action   TEXT NOT NULL,
    target   TEXT,
    result   TEXT NOT NULL,
    detail   TEXT
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts DESC);

-- Probe targets are mostly derived from the parsed inventory, but an operator
-- can pin extra ones by hand (source='manual'); those survive a re-scan.
CREATE TABLE IF NOT EXISTS targets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    key        TEXT NOT NULL UNIQUE,
    label      TEXT NOT NULL,
    kind       TEXT NOT NULL,           -- http | https | tcp
    host       TEXT NOT NULL,
    port        INTEGER NOT NULL,
    path        TEXT NOT NULL DEFAULT '',
    host_header TEXT NOT NULL DEFAULT '', -- vhost sent with HTTP probes
    source     TEXT NOT NULL,           -- nginx | haproxy | docker | manual
    service    TEXT NOT NULL DEFAULT '',
    node_id    TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    first_seen TEXT NOT NULL,
    last_seen  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS probe_results (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    target_id   INTEGER NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    ts          TEXT NOT NULL,
    ok          INTEGER NOT NULL,
    latency_ms  REAL,
    status_code INTEGER,
    error       TEXT
);
CREATE INDEX IF NOT EXISTS idx_probe_target_ts ON probe_results(target_id, ts DESC);
CREATE INDEX IF NOT EXISTS idx_probe_ts ON probe_results(ts DESC);

-- Generic time series: firewall counters, container stats, access-log rates.
CREATE TABLE IF NOT EXISTS metric_samples (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    ts      TEXT NOT NULL,
    source  TEXT NOT NULL,              -- iptables | docker | nginx_log | haproxy_log
    subject TEXT NOT NULL,              -- container name, rule id, vhost...
    metric  TEXT NOT NULL,              -- bytes | packets | cpu_pct | requests | ...
    value   REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_metric_lookup ON metric_samples(source, metric, subject, ts DESC);
CREATE INDEX IF NOT EXISTS idx_metric_ts ON metric_samples(ts DESC);

-- Monotonic counters need their previous reading to become a rate.
CREATE TABLE IF NOT EXISTS counter_state (
    key   TEXT PRIMARY KEY,
    value REAL NOT NULL,
    ts    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS config_versions (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    path      TEXT NOT NULL,
    service   TEXT NOT NULL,
    ts        TEXT NOT NULL,
    author    TEXT NOT NULL,
    action    TEXT NOT NULL,            -- observed | edit | rollback
    note      TEXT NOT NULL DEFAULT '',
    size      INTEGER NOT NULL,
    sha256    TEXT NOT NULL,
    blob_name TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_versions_path ON config_versions(path, ts DESC);

-- Inventory snapshots, kept so the UI can show what changed on the host.
CREATE TABLE IF NOT EXISTS snapshots (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    ts       TEXT NOT NULL,
    digest   TEXT NOT NULL,
    payload  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_ts ON snapshots(ts DESC);

CREATE TABLE IF NOT EXISTS kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Remote hosts a hub instance manages over SSH. Only present/used in
-- NKT_MODE=hub; a plain single-host nkt never touches this table.
CREATE TABLE IF NOT EXISTS hosts (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT NOT NULL,
    addr               TEXT NOT NULL,
    ssh_port           INTEGER NOT NULL DEFAULT 22,
    ssh_user           TEXT NOT NULL,
    ssh_auth_kind      TEXT NOT NULL CHECK (ssh_auth_kind IN ('password','key')),
    secret_enc         BLOB NOT NULL,          -- SSH password or private key, secretbox-encrypted
    arch               TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new','installing','online','error')),
    nkt_version        TEXT NOT NULL DEFAULT '',
    admin_user         TEXT NOT NULL DEFAULT '',
    admin_password_enc BLOB,                   -- remote bootstrap admin password, secretbox-encrypted;
                                                -- used to re-login when a proxied session expires
    sudo_status        TEXT NOT NULL DEFAULT '' CHECK (sudo_status IN ('','root','nopasswd','password_required')),
    error_msg          TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    last_seen_at       TEXT
);
CREATE INDEX IF NOT EXISTS idx_hosts_name ON hosts(name);
`

// DB wraps the SQLite handle.
type DB struct {
	*sql.DB
	path string
}

// Open creates (or opens) the database and applies the schema.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
		filepath.ToSlash(path))
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// One connection removes every SQLITE_BUSY race; the workload is tiny.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	if _, err := sqlDB.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{DB: sqlDB, path: path}, nil
}

// Path returns the database file location.
func (d *DB) Path() string { return d.path }

// Now is the canonical timestamp format used across every table: RFC3339 in UTC,
// which sorts lexicographically and therefore works with plain string ranges.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// FormatTime renders a time in the canonical storage format.
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// KVGet reads a scalar setting.
func (d *DB) KVGet(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := d.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	switch {
	case err == sql.ErrNoRows:
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return v, true, nil
}

// KVSet writes a scalar setting.
func (d *DB) KVSet(ctx context.Context, key, value string) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO kv(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// PurgeResult reports how many rows retention removed.
type PurgeResult struct {
	Probes    int64 `json:"probes"`
	Metrics   int64 `json:"metrics"`
	Sessions  int64 `json:"sessions"`
	Snapshots int64 `json:"snapshots"`
}

// Purge drops time series older than the retention window, expired sessions and
// all but the most recent inventory snapshots.
func (d *DB) Purge(ctx context.Context, retention time.Duration) (PurgeResult, error) {
	var out PurgeResult
	cutoff := FormatTime(time.Now().Add(-retention))

	rows := func(res sql.Result) int64 {
		n, err := res.RowsAffected()
		if err != nil {
			return 0
		}
		return n
	}

	res, err := d.ExecContext(ctx, `DELETE FROM probe_results WHERE ts < ?`, cutoff)
	if err != nil {
		return out, err
	}
	out.Probes = rows(res)

	if res, err = d.ExecContext(ctx, `DELETE FROM metric_samples WHERE ts < ?`, cutoff); err != nil {
		return out, err
	}
	out.Metrics = rows(res)

	if res, err = d.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, Now()); err != nil {
		return out, err
	}
	out.Sessions = rows(res)

	if res, err = d.ExecContext(ctx,
		`DELETE FROM snapshots WHERE id NOT IN (SELECT id FROM snapshots ORDER BY id DESC LIMIT 50)`); err != nil {
		return out, err
	}
	out.Snapshots = rows(res)

	return out, nil
}
