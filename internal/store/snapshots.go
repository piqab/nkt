package store

import (
	"context"
	"database/sql"
	"errors"
)

// SnapshotMeta describes a stored inventory scan without its payload.
type SnapshotMeta struct {
	ID     int64  `json:"id"`
	TS     string `json:"ts"`
	Digest string `json:"digest"`
	Size   int    `json:"size"`
}

// SaveSnapshot stores an inventory scan. When the digest matches the previous
// snapshot nothing is written and the existing id is returned, so the history
// only contains genuine changes.
func (d *DB) SaveSnapshot(ctx context.Context, digest, payload string) (int64, bool, error) {
	var lastID int64
	var lastDigest string
	err := d.QueryRowContext(ctx, `SELECT id, digest FROM snapshots ORDER BY id DESC LIMIT 1`).
		Scan(&lastID, &lastDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if lastDigest == digest && lastID != 0 {
		return lastID, false, nil
	}
	res, err := d.ExecContext(ctx,
		`INSERT INTO snapshots(ts, digest, payload) VALUES(?, ?, ?)`, Now(), digest, payload)
	if err != nil {
		return 0, false, err
	}
	id, err := res.LastInsertId()
	return id, true, err
}

// LatestSnapshot returns the most recent stored inventory payload.
func (d *DB) LatestSnapshot(ctx context.Context) (string, string, error) {
	var ts, payload string
	err := d.QueryRowContext(ctx, `SELECT ts, payload FROM snapshots ORDER BY id DESC LIMIT 1`).Scan(&ts, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return ts, payload, err
}

// ListSnapshots returns snapshot metadata, newest first.
func (d *DB) ListSnapshots(ctx context.Context, limit int) ([]SnapshotMeta, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.QueryContext(ctx,
		`SELECT id, ts, digest, LENGTH(payload) FROM snapshots ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SnapshotMeta{}
	for rows.Next() {
		var m SnapshotMeta
		if err := rows.Scan(&m.ID, &m.TS, &m.Digest, &m.Size); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SnapshotByID returns a stored inventory payload.
func (d *DB) SnapshotByID(ctx context.Context, id int64) (SnapshotMeta, string, error) {
	var m SnapshotMeta
	var payload string
	err := d.QueryRowContext(ctx,
		`SELECT id, ts, digest, LENGTH(payload), payload FROM snapshots WHERE id = ?`, id).
		Scan(&m.ID, &m.TS, &m.Digest, &m.Size, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return m, "", ErrNotFound
	}
	return m, payload, err
}

// LatestSnapshotMeta отдаёт номер и время самого свежего снимка.
func (d *DB) LatestSnapshotMeta(ctx context.Context) (SnapshotMeta, error) {
	var m SnapshotMeta
	err := d.QueryRowContext(ctx,
		`SELECT id, ts, digest, LENGTH(payload) FROM snapshots ORDER BY id DESC LIMIT 1`).
		Scan(&m.ID, &m.TS, &m.Digest, &m.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// SnapshotBefore отдаёт последний снимок, сделанный раньше данного, —
// «то, как было до этого». Нужен, когда отметки «я это видел» ещё нет:
// сравнивать с самим собой бессмысленно, а с предыдущим — уже осмысленно.
func (d *DB) SnapshotBefore(ctx context.Context, id int64) (SnapshotMeta, string, error) {
	var m SnapshotMeta
	var payload string
	err := d.QueryRowContext(ctx,
		`SELECT id, ts, digest, LENGTH(payload), payload FROM snapshots WHERE id < ? ORDER BY id DESC LIMIT 1`, id).
		Scan(&m.ID, &m.TS, &m.Digest, &m.Size, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return m, "", ErrNotFound
	}
	return m, payload, err
}

// PruneSnapshots оставляет последние keep снимков.
//
// Снимок пишется при каждом изменении состояния, а состояние на живом
// сервере меняется часто: без чистки таблица растёт, пока не станет
// больше всего остального в базе.
func (d *DB) PruneSnapshots(ctx context.Context, keep int) error {
	if keep <= 0 {
		keep = 200
	}
	_, err := d.ExecContext(ctx, `
		DELETE FROM snapshots WHERE id NOT IN (
			SELECT id FROM snapshots ORDER BY id DESC LIMIT ?
		)`, keep)
	return err
}
