package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// Роли API-токена: чтение — состояние хостов, проблемы, оповещения,
// журналы заданий; администратор — ещё и действия (выкладка, бан IP,
// службы). Управлять хабом (учётки, токены, экспорт, обновление) токен
// не может ни с какой ролью.
const (
	TokenRoleRead  = "read"
	TokenRoleAdmin = "admin"
)

// APIToken — токен API хаба. Пустые Hosts и Groups — доступ ко всем
// хостам; иначе — только к перечисленным хостам и хостам групп.
type APIToken struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	KeyID     string   `json:"key_id"`
	Role      string   `json:"role"`
	Hosts     []int64  `json:"hosts"`
	Groups    []string `json:"groups"`
	IPs       []string `json:"ips"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	Author    string   `json:"author,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	LastUsed  string   `json:"last_used_at,omitempty"`
	LastIP    string   `json:"last_ip,omitempty"`

	SecretEnc []byte `json:"-"`
}

// Scoped — токен ограничен хостами или группами.
func (t APIToken) Scoped() bool { return len(t.Hosts) > 0 || len(t.Groups) > 0 }

// Expired — срок токена вышел.
func (t APIToken) Expired(now time.Time) bool {
	if t.ExpiresAt == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, t.ExpiresAt)
	return err != nil || !now.Before(exp)
}

// AllowsHost — хост (и его группа) в пределах токена.
func (t APIToken) AllowsHost(id int64, group string) bool {
	if !t.Scoped() {
		return true
	}
	return slices.Contains(t.Hosts, id) || (group != "" && slices.Contains(t.Groups, group))
}

const apiTokenCols = `id, name, key_id, secret_enc, role, hosts, groups_json, ips, expires_at, author, created_at, updated_at, last_used_at, last_ip`

func scanAPIToken(row interface{ Scan(...any) error }) (APIToken, error) {
	var t APIToken
	var hosts, groups, ips string
	err := row.Scan(&t.ID, &t.Name, &t.KeyID, &t.SecretEnc, &t.Role, &hosts, &groups, &ips, &t.ExpiresAt, &t.Author,
		&t.CreatedAt, &t.UpdatedAt, &t.LastUsed, &t.LastIP)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal([]byte(hosts), &t.Hosts)
	_ = json.Unmarshal([]byte(groups), &t.Groups)
	_ = json.Unmarshal([]byte(ips), &t.IPs)
	if t.Hosts == nil {
		t.Hosts = []int64{}
	}
	if t.Groups == nil {
		t.Groups = []string{}
	}
	if t.IPs == nil {
		t.IPs = []string{}
	}
	return t, nil
}

func jsonList[T any](v []T) string {
	if v == nil {
		return "[]"
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// CreateAPIToken заводит токен.
func (d *DB) CreateAPIToken(ctx context.Context, t APIToken) (int64, error) {
	now := FormatTime(time.Now())
	res, err := d.ExecContext(ctx, `INSERT INTO api_tokens(name, key_id, secret_enc, role, hosts, groups_json, ips, expires_at, author, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Name, t.KeyID, t.SecretEnc, t.Role, jsonList(t.Hosts), jsonList(t.Groups), jsonList(t.IPs), t.ExpiresAt, t.Author, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAPITokens — все токены, новые сверху.
func (d *DB) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// APITokenByID — токен по номеру.
func (d *DB) APITokenByID(ctx context.Context, id int64) (APIToken, error) {
	return scanAPIToken(d.QueryRowContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE id = ?`, id))
}

// APITokenByKeyID — токен по открытой части ключа.
func (d *DB) APITokenByKeyID(ctx context.Context, keyID string) (APIToken, error) {
	return scanAPIToken(d.QueryRowContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE key_id = ?`, keyID))
}

// UpdateAPIToken — имя, роль, пределы, адреса и срок.
func (d *DB) UpdateAPIToken(ctx context.Context, t APIToken) error {
	_, err := d.ExecContext(ctx, `UPDATE api_tokens SET name = ?, role = ?, hosts = ?, groups_json = ?, ips = ?, expires_at = ?, updated_at = ? WHERE id = ?`,
		t.Name, t.Role, jsonList(t.Hosts), jsonList(t.Groups), jsonList(t.IPs), t.ExpiresAt, FormatTime(time.Now()), t.ID)
	return err
}

// SetAPITokenSecret — новый секрет (старый перестаёт действовать сразу).
func (d *DB) SetAPITokenSecret(ctx context.Context, id int64, keyID string, secretEnc []byte) error {
	_, err := d.ExecContext(ctx, `UPDATE api_tokens SET key_id = ?, secret_enc = ?, updated_at = ? WHERE id = ?`,
		keyID, secretEnc, FormatTime(time.Now()), id)
	return err
}

// TouchAPIToken — когда и откуда токен использовали в последний раз.
func (d *DB) TouchAPIToken(ctx context.Context, id int64, ip string) error {
	_, err := d.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ?, last_ip = ? WHERE id = ?`, FormatTime(time.Now()), ip, id)
	return err
}

// DeleteAPIToken — отзыв токена.
func (d *DB) DeleteAPIToken(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	return err
}

// renameGroupInTokens — группа переименована: токены, ограниченные ею,
// остаются при ней.
func renameGroupInTokens(ctx context.Context, tx *sql.Tx, from, to string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, groups_json FROM api_tokens WHERE groups_json <> '[]'`)
	if err != nil {
		return err
	}
	type upd struct {
		id     int64
		groups string
	}
	var todo []upd
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var groups []string
		_ = json.Unmarshal([]byte(raw), &groups)
		if i := slices.Index(groups, from); i >= 0 {
			groups[i] = to
			todo = append(todo, upd{id, jsonList(slices.Compact(groups))})
		}
	}
	rows.Close()
	for _, u := range todo {
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET groups_json = ? WHERE id = ?`, u.groups, u.id); err != nil {
			return err
		}
	}
	return nil
}
