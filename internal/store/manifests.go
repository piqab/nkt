package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Manifest — манифест Kubernetes хаба, который применяется сразу к
// нескольким кластерам. Каждое применение — редакция (ManifestVersion)
// с итогом по кластерам.
type Manifest struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Content   string `json:"content,omitempty"`
	Note      string `json:"note,omitempty"`
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ManifestVersion — одно применение манифеста.
type ManifestVersion struct {
	ID         int64  `json:"id"`
	ManifestID int64  `json:"manifest_id"`
	TS         string `json:"ts"`
	Author     string `json:"author,omitempty"`
	Note       string `json:"note,omitempty"`
	Content    string `json:"content,omitempty"`
	// Results — JSON: итог по каждому кластеру.
	Results string `json:"results,omitempty"`
}

// SaveManifestVersion записывает применение: манифест по имени
// создаётся или обновляется, редакция добавляется. Возвращает id
// манифеста и редакции.
func (db *DB) SaveManifestVersion(ctx context.Context, m Manifest, results string) (int64, int64, error) {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM k8s_manifests WHERE name = ?`, m.Name).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.ExecContext(ctx, `
			INSERT INTO k8s_manifests (name, content, note, author, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, m.Name, m.Content, m.Note, m.Author, now, now)
		if err != nil {
			return 0, 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, 0, err
		}
	case err != nil:
		return 0, 0, err
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE k8s_manifests SET content = ?, note = ?, author = ?, updated_at = ? WHERE id = ?`,
			m.Content, m.Note, m.Author, now, id); err != nil {
			return 0, 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO k8s_manifest_versions (manifest_id, ts, author, note, content, results)
		VALUES (?, ?, ?, ?, ?, ?)`, id, now, m.Author, m.Note, m.Content, results)
	if err != nil {
		return 0, 0, err
	}
	vid, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	return id, vid, tx.Commit()
}

// ListManifests — манифесты без текста, свежие сверху.
func (db *DB) ListManifests(ctx context.Context) ([]Manifest, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, note, author, created_at, updated_at FROM k8s_manifests ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Manifest{}
	for rows.Next() {
		var m Manifest
		if err := rows.Scan(&m.ID, &m.Name, &m.Note, &m.Author, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ManifestByID — манифест с текстом последней редакции.
func (db *DB) ManifestByID(ctx context.Context, id int64) (Manifest, error) {
	var m Manifest
	err := db.QueryRowContext(ctx, `SELECT id, name, content, note, author, created_at, updated_at FROM k8s_manifests WHERE id = ?`, id).
		Scan(&m.ID, &m.Name, &m.Content, &m.Note, &m.Author, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Manifest{}, ErrNotFound
	}
	return m, err
}

// DeleteManifest убирает манифест с историей.
func (db *DB) DeleteManifest(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM k8s_manifests WHERE id = ?`, id)
	return err
}

// ManifestVersions — применения манифеста без текста, свежие сверху.
func (db *DB) ManifestVersions(ctx context.Context, manifestID int64, limit int) ([]ManifestVersion, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, manifest_id, ts, author, note, results FROM k8s_manifest_versions
		WHERE manifest_id = ? ORDER BY id DESC LIMIT ?`, manifestID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManifestVersion{}
	for rows.Next() {
		var v ManifestVersion
		if err := rows.Scan(&v.ID, &v.ManifestID, &v.TS, &v.Author, &v.Note, &v.Results); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ManifestVersion — одна редакция целиком.
func (db *DB) ManifestVersion(ctx context.Context, id int64) (ManifestVersion, error) {
	var v ManifestVersion
	err := db.QueryRowContext(ctx, `SELECT id, manifest_id, ts, author, note, content, results FROM k8s_manifest_versions WHERE id = ?`, id).
		Scan(&v.ID, &v.ManifestID, &v.TS, &v.Author, &v.Note, &v.Content, &v.Results)
	if errors.Is(err, sql.ErrNoRows) {
		return ManifestVersion{}, ErrNotFound
	}
	return v, err
}
