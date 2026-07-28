package store

import (
	"context"
	"database/sql"
	"errors"
)

// Version actions.
const (
	ActionObserved = "observed" // captured by a scan, not written by us
	ActionEdit     = "edit"
	ActionRollback = "rollback"
)

// ConfigVersion indexes one stored revision of a managed config file. The bytes
// themselves live in the history directory under BlobName.
type ConfigVersion struct {
	ID       int64  `json:"id"`
	Path     string `json:"path"`
	Service  string `json:"service"`
	TS       string `json:"ts"`
	Author   string `json:"author"`
	Action   string `json:"action"`
	Note     string `json:"note"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	BlobName string `json:"-"`
}

// AddVersion records a new revision.
func (d *DB) AddVersion(ctx context.Context, v ConfigVersion) (int64, error) {
	res, err := d.ExecContext(ctx,
		`INSERT INTO config_versions(path, service, ts, author, action, note, size, sha256, blob_name)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.Path, v.Service, Now(), v.Author, v.Action, v.Note, v.Size, v.SHA256, v.BlobName)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const versionColumns = `id, path, service, ts, author, action, note, size, sha256, blob_name`

func scanVersion(row interface{ Scan(...any) error }) (ConfigVersion, error) {
	var v ConfigVersion
	err := row.Scan(&v.ID, &v.Path, &v.Service, &v.TS, &v.Author, &v.Action, &v.Note, &v.Size, &v.SHA256, &v.BlobName)
	return v, err
}

// ListVersions returns revisions of one path (or all paths when empty), newest first.
func (d *DB) ListVersions(ctx context.Context, path string, limit int) ([]ConfigVersion, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if path == "" {
		rows, err = d.QueryContext(ctx, `SELECT `+versionColumns+` FROM config_versions ORDER BY id DESC LIMIT ?`, limit)
	} else {
		rows, err = d.QueryContext(ctx, `SELECT `+versionColumns+` FROM config_versions WHERE path = ? ORDER BY id DESC LIMIT ?`, path, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ConfigVersion{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VersionByID fetches a single revision.
func (d *DB) VersionByID(ctx context.Context, id int64) (ConfigVersion, error) {
	v, err := scanVersion(d.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM config_versions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ConfigVersion{}, ErrNotFound
	}
	return v, err
}

// LatestVersion returns the most recent revision of a path.
func (d *DB) LatestVersion(ctx context.Context, path string) (ConfigVersion, error) {
	v, err := scanVersion(d.QueryRowContext(ctx,
		`SELECT `+versionColumns+` FROM config_versions WHERE path = ? ORDER BY id DESC LIMIT 1`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return ConfigVersion{}, ErrNotFound
	}
	return v, err
}
