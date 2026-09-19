package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ClusterPreset — сохранённая форма «Новый кластер на нескольких хостах»:
// имя набора и поля формы одним куском JSON (как шаблон машины: набор
// полей меняется вместе с формой, миграция таблицы не нужна).
type ClusterPreset struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Form      string `json:"form"`
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SaveClusterPreset заводит набор или переписывает одноимённый.
func (db *DB) SaveClusterPreset(ctx context.Context, p ClusterPreset) (int64, error) {
	now := FormatTime(time.Now())
	if _, err := db.ExecContext(ctx, `
		INSERT INTO cluster_presets (name, form, author, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET form = excluded.form, author = excluded.author,
		                                updated_at = excluded.updated_at`,
		p.Name, p.Form, p.Author, now, now); err != nil {
		return 0, err
	}
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM cluster_presets WHERE name = ?`, p.Name).Scan(&id)
	return id, err
}

// ListClusterPresets — наборы по имени.
func (db *DB) ListClusterPresets(ctx context.Context) ([]ClusterPreset, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, form, author, created_at, updated_at FROM cluster_presets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClusterPreset{}
	for rows.Next() {
		var p ClusterPreset
		if err := rows.Scan(&p.ID, &p.Name, &p.Form, &p.Author, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClusterPresetByID — один набор.
func (db *DB) ClusterPresetByID(ctx context.Context, id int64) (ClusterPreset, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, name, form, author, created_at, updated_at FROM cluster_presets WHERE id = ?`, id)
	var p ClusterPreset
	err := row.Scan(&p.ID, &p.Name, &p.Form, &p.Author, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ClusterPreset{}, ErrNotFound
	}
	return p, err
}

// DeleteClusterPreset убирает набор.
func (db *DB) DeleteClusterPreset(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM cluster_presets WHERE id = ?`, id)
	return err
}
