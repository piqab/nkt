package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/piqab/nkt/internal/msgs"
	"time"
)

// Profile — описание желаемого состояния, как его хранит nkt.
//
// Содержимое лежит одним куском YAML, а не разложенным по таблицам:
// профиль правят целиком, выгружают в git целиком и оттуда же
// возвращают. Разбирает его internal/profile — хранилищу знать его
// устройство незачем.
type Profile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Color — цвет профиля (#rrggbb) для строк хостов, созданных по нему.
	Color     string `json:"color,omitempty"`
	Content   string `json:"content,omitempty"`
	Note      string `json:"note,omitempty"`
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ProfileVersion — прошлая редакция профиля. История своя, а не общая с
// конфигурациями: там версии файлов на диске, здесь — описания.
type ProfileVersion struct {
	ID        int64  `json:"id"`
	ProfileID int64  `json:"profile_id"`
	TS        string `json:"ts"`
	Author    string `json:"author,omitempty"`
	Note      string `json:"note,omitempty"`
	Content   string `json:"content,omitempty"`
}

// CreateProfile заводит профиль вместе с первой редакцией.
func (db *DB) CreateProfile(ctx context.Context, p Profile) (int64, error) {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO profiles (name, color, content, note, author, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, p.Name, p.Color, p.Content, p.Note, p.Author, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO profile_versions (profile_id, ts, author, note, content)
		VALUES (?, ?, ?, ?, ?)`, id, now, p.Author, msgs.Tc(ctx, "store.profileCreated"), p.Content); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdateProfile меняет профиль, сохраняя прошлую редакцию в истории.
func (db *DB) UpdateProfile(ctx context.Context, p Profile) error {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE profiles SET name = ?, color = ?, content = ?, note = ?, author = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Color, p.Content, p.Note, p.Author, now, p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO profile_versions (profile_id, ts, author, note, content)
		VALUES (?, ?, ?, ?, ?)`, p.ID, now, p.Author, p.Note, p.Content); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteProfile убирает профиль вместе с историей.
func (db *DB) DeleteProfile(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM profiles WHERE id = ?`, id)
	return err
}

// ProfileByID отдаёт профиль с содержимым.
func (db *DB) ProfileByID(ctx context.Context, id int64) (Profile, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, name, color, content, note, author, created_at, updated_at FROM profiles WHERE id = ?`, id)
	var p Profile
	err := row.Scan(&p.ID, &p.Name, &p.Color, &p.Content, &p.Note, &p.Author, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

// ListProfiles отдаёт профили без содержимого — список бывает длинным, а
// текст в нём не показывают.
func (db *DB) ListProfiles(ctx context.Context) ([]Profile, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, color, note, author, created_at, updated_at FROM profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		if err := rows.Scan(&p.ID, &p.Name, &p.Color, &p.Note, &p.Author, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProfileVersions отдаёт историю правок без содержимого.
func (db *DB) ProfileVersions(ctx context.Context, profileID int64, limit int) ([]ProfileVersion, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, profile_id, ts, author, note FROM profile_versions
		WHERE profile_id = ? ORDER BY id DESC LIMIT ?`, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProfileVersion{}
	for rows.Next() {
		var v ProfileVersion
		if err := rows.Scan(&v.ID, &v.ProfileID, &v.TS, &v.Author, &v.Note); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ProfileVersion отдаёт одну редакцию целиком — для сравнения и отката.
func (db *DB) ProfileVersion(ctx context.Context, id int64) (ProfileVersion, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, profile_id, ts, author, note, content FROM profile_versions WHERE id = ?`, id)
	var v ProfileVersion
	err := row.Scan(&v.ID, &v.ProfileID, &v.TS, &v.Author, &v.Note, &v.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileVersion{}, ErrNotFound
	}
	return v, err
}
