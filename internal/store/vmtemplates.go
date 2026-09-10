package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// VMTemplate — заготовка машины: имя набора и описание целиком.
//
// Spec хранится одним куском JSON по той же причине, что и профиль
// текстом: набор полей меняется вместе с формой создания, и каждая
//新 настройка не должна тянуть за собой миграцию таблицы.
type VMTemplate struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SaveVMTemplate заводит шаблон или переписывает одноимённый.
func (db *DB) SaveVMTemplate(ctx context.Context, t VMTemplate) (int64, error) {
	now := FormatTime(time.Now())
	if _, err := db.ExecContext(ctx, `
		INSERT INTO vm_templates (name, spec, author, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET spec = excluded.spec, author = excluded.author,
		                                updated_at = excluded.updated_at`,
		t.Name, t.Spec, t.Author, now, now); err != nil {
		return 0, err
	}
	// Идентификатор читается запросом, а не берётся из LastInsertId:
	// после ON CONFLICT DO UPDATE вставки не было, и последний rowid
	// соединения относится к чему-то другому — на перезаписи он врал.
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM vm_templates WHERE name = ?`, t.Name).Scan(&id)
	return id, err
}

// ListVMTemplates отдаёт шаблоны по имени.
func (db *DB) ListVMTemplates(ctx context.Context) ([]VMTemplate, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, spec, author, created_at, updated_at FROM vm_templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VMTemplate{}
	for rows.Next() {
		var t VMTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.Spec, &t.Author, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// VMTemplateByID отдаёт один шаблон.
func (db *DB) VMTemplateByID(ctx context.Context, id int64) (VMTemplate, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, name, spec, author, created_at, updated_at FROM vm_templates WHERE id = ?`, id)
	var t VMTemplate
	err := row.Scan(&t.ID, &t.Name, &t.Spec, &t.Author, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return VMTemplate{}, ErrNotFound
	}
	return t, err
}

// DeleteVMTemplate убирает шаблон.
func (db *DB) DeleteVMTemplate(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM vm_templates WHERE id = ?`, id)
	return err
}
