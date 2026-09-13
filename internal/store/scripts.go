package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Script — сценарий хаба: текст на языке internal/script, хранится и
// версионируется так же, как профиль.
type Script struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color,omitempty"`
	Content   string `json:"content,omitempty"`
	Note      string `json:"note,omitempty"`
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ScriptVersion — прошлая редакция сценария.
type ScriptVersion struct {
	ID       int64  `json:"id"`
	ScriptID int64  `json:"script_id"`
	TS       string `json:"ts"`
	Author   string `json:"author,omitempty"`
	Note     string `json:"note,omitempty"`
	Content  string `json:"content,omitempty"`
}

// CreateScript заводит сценарий вместе с первой редакцией.
func (db *DB) CreateScript(ctx context.Context, s Script) (int64, error) {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO scripts (name, color, content, note, author, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, s.Name, s.Color, s.Content, s.Note, s.Author, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO script_versions (script_id, ts, author, note, content)
		VALUES (?, ?, ?, ?, ?)`, id, now, s.Author, msgs.Tc(ctx, "store.profileCreated"), s.Content); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdateScript меняет сценарий, сохраняя прошлую редакцию.
func (db *DB) UpdateScript(ctx context.Context, s Script) error {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE scripts SET name = ?, color = ?, content = ?, note = ?, author = ?, updated_at = ? WHERE id = ?`,
		s.Name, s.Color, s.Content, s.Note, s.Author, now, s.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO script_versions (script_id, ts, author, note, content)
		VALUES (?, ?, ?, ?, ?)`, s.ID, now, s.Author, s.Note, s.Content); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteScript убирает сценарий вместе с историей.
func (db *DB) DeleteScript(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM scripts WHERE id = ?`, id)
	return err
}

// ScriptByID отдаёт сценарий с текстом.
func (db *DB) ScriptByID(ctx context.Context, id int64) (Script, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, name, color, content, note, author, created_at, updated_at FROM scripts WHERE id = ?`, id)
	var s Script
	err := row.Scan(&s.ID, &s.Name, &s.Color, &s.Content, &s.Note, &s.Author, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Script{}, ErrNotFound
	}
	return s, err
}

// ListScripts отдаёт сценарии без текста.
func (db *DB) ListScripts(ctx context.Context) ([]Script, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, color, note, author, created_at, updated_at FROM scripts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Script{}
	for rows.Next() {
		var s Script
		if err := rows.Scan(&s.ID, &s.Name, &s.Color, &s.Note, &s.Author, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ScriptVersions отдаёт историю правок без текста.
func (db *DB) ScriptVersions(ctx context.Context, scriptID int64, limit int) ([]ScriptVersion, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, script_id, ts, author, note FROM script_versions
		WHERE script_id = ? ORDER BY id DESC LIMIT ?`, scriptID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScriptVersion{}
	for rows.Next() {
		var v ScriptVersion
		if err := rows.Scan(&v.ID, &v.ScriptID, &v.TS, &v.Author, &v.Note); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ScriptVersion отдаёт одну редакцию целиком.
func (db *DB) ScriptVersion(ctx context.Context, id int64) (ScriptVersion, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, script_id, ts, author, note, content FROM script_versions WHERE id = ?`, id)
	var v ScriptVersion
	err := row.Scan(&v.ID, &v.ScriptID, &v.TS, &v.Author, &v.Note, &v.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return ScriptVersion{}, ErrNotFound
	}
	return v, err
}
