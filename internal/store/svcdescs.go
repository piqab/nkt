package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// SvcDesc — описание сервиса хаба (YAML internal/svcdesc) с историей.
type SvcDesc struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Content   string `json:"content,omitempty"`
	Note      string `json:"note,omitempty"`
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SvcDescVersion — прошлая редакция сценария.
type SvcDescVersion struct {
	ID      int64  `json:"id"`
	DescID  int64  `json:"desc_id"`
	TS      string `json:"ts"`
	Author  string `json:"author,omitempty"`
	Note    string `json:"note,omitempty"`
	Content string `json:"content,omitempty"`
	// Size — длина текста в байтах (только в списке редакций).
	Size int64 `json:"size,omitempty"`
}

// CreateSvcDesc заводит описание вместе с первой редакцией.
func (db *DB) CreateSvcDesc(ctx context.Context, s SvcDesc) (int64, error) {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO svc_descs (name, content, note, author, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, s.Name, s.Content, s.Note, s.Author, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO svc_desc_versions (desc_id, ts, author, note, content)
		VALUES (?, ?, ?, ?, ?)`, id, now, s.Author, msgs.Tc(ctx, "store.profileCreated"), s.Content); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdateSvcDesc меняет описание, сохраняя прошлую редакцию.
func (db *DB) UpdateSvcDesc(ctx context.Context, s SvcDesc) error {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE svc_descs SET name = ?, content = ?, note = ?, author = ?, updated_at = ? WHERE id = ?`,
		s.Name, s.Content, s.Note, s.Author, now, s.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO svc_desc_versions (desc_id, ts, author, note, content)
		VALUES (?, ?, ?, ?, ?)`, s.ID, now, s.Author, s.Note, s.Content); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSvcDesc убирает описание вместе с историей.
func (db *DB) DeleteSvcDesc(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM svc_descs WHERE id = ?`, id)
	return err
}

// SvcDescByID отдаёт описание с текстом.
func (db *DB) SvcDescByID(ctx context.Context, id int64) (SvcDesc, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, name, content, note, author, created_at, updated_at FROM svc_descs WHERE id = ?`, id)
	var s SvcDesc
	err := row.Scan(&s.ID, &s.Name, &s.Content, &s.Note, &s.Author, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SvcDesc{}, ErrNotFound
	}
	return s, err
}

// ListSvcDescs отдаёт описания без текста.
func (db *DB) ListSvcDescs(ctx context.Context) ([]SvcDesc, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, note, author, created_at, updated_at FROM svc_descs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SvcDesc{}
	for rows.Next() {
		var s SvcDesc
		if err := rows.Scan(&s.ID, &s.Name, &s.Note, &s.Author, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SvcDescVersions отдаёт историю правок без текста.
func (db *DB) SvcDescVersions(ctx context.Context, descID int64, limit int) ([]SvcDescVersion, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, desc_id, ts, author, note, COALESCE(length(CAST(content AS BLOB)), 0) FROM svc_desc_versions
		WHERE desc_id = ? ORDER BY id DESC LIMIT ?`, descID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SvcDescVersion{}
	for rows.Next() {
		var v SvcDescVersion
		if err := rows.Scan(&v.ID, &v.DescID, &v.TS, &v.Author, &v.Note, &v.Size); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SvcDescVersion отдаёт одну редакцию целиком.
func (db *DB) SvcDescVersion(ctx context.Context, id int64) (SvcDescVersion, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, desc_id, ts, author, note, content FROM svc_desc_versions WHERE id = ?`, id)
	var v SvcDescVersion
	err := row.Scan(&v.ID, &v.DescID, &v.TS, &v.Author, &v.Note, &v.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return SvcDescVersion{}, ErrNotFound
	}
	return v, err
}

// AllSvcDescs — все описания с текстом (доставка на хосты, экспорт).
func (db *DB) AllSvcDescs(ctx context.Context) ([]SvcDesc, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, content, note, author, created_at, updated_at FROM svc_descs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SvcDesc{}
	for rows.Next() {
		var s SvcDesc
		if err := rows.Scan(&s.ID, &s.Name, &s.Content, &s.Note, &s.Author, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SvcDescByName — описание по имени.
func (db *DB) SvcDescByName(ctx context.Context, name string) (SvcDesc, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, name, content, note, author, created_at, updated_at FROM svc_descs WHERE name = ?`, name)
	var s SvcDesc
	err := row.Scan(&s.ID, &s.Name, &s.Content, &s.Note, &s.Author, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SvcDesc{}, ErrNotFound
	}
	return s, err
}
