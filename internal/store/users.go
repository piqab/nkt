package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound is returned when a lookup finds nothing.
var ErrNotFound = errors.New("not found")

// Role values.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// User is an account able to sign in to the dashboard.
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at,omitempty"`

	PasswordHash string `json:"-"`
}

// IsAdmin reports whether the user may change host state.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// CountUsers returns the number of accounts, used to decide bootstrapping.
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts an account with an already-hashed password.
func (d *DB) CreateUser(ctx context.Context, username, passwordHash, role string) (int64, error) {
	if role != RoleAdmin && role != RoleViewer {
		return 0, errors.New("unknown role: " + role)
	}
	res, err := d.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, role, created_at) VALUES(?, ?, ?, ?)`,
		username, passwordHash, role, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var lastLogin sql.NullString
	var disabled int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &disabled, &u.CreatedAt, &lastLogin)
	if err != nil {
		return User{}, err
	}
	u.Disabled = disabled != 0
	u.LastLoginAt = lastLogin.String
	return u, nil
}

const userColumns = `id, username, password_hash, role, disabled, created_at, last_login_at`

// UserByName looks an account up by its login.
func (d *DB) UserByName(ctx context.Context, username string) (User, error) {
	row := d.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE username = ?`, username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// ListUsers returns all accounts ordered by name.
func (d *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPasswordHash replaces an account's password and revokes its sessions.
func (d *DB) SetPasswordHash(ctx context.Context, username, hash string) error {
	res, err := d.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE username = ?`, hash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = d.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE username = ?)`, username)
	return err
}

// SetUserRole changes an account's role.
func (d *DB) SetUserRole(ctx context.Context, username, role string) error {
	if role != RoleAdmin && role != RoleViewer {
		return errors.New("unknown role: " + role)
	}
	res, err := d.ExecContext(ctx, `UPDATE users SET role = ? WHERE username = ?`, role, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserDisabled enables or disables an account, revoking sessions on disable.
func (d *DB) SetUserDisabled(ctx context.Context, username string, disabled bool) error {
	flag := 0
	if disabled {
		flag = 1
	}
	res, err := d.ExecContext(ctx, `UPDATE users SET disabled = ? WHERE username = ?`, flag, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if disabled {
		_, err = d.ExecContext(ctx,
			`DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE username = ?)`, username)
	}
	return err
}

// DeleteUser removes an account and its sessions.
func (d *DB) DeleteUser(ctx context.Context, username string) error {
	res, err := d.ExecContext(ctx, `DELETE FROM users WHERE username = ?`, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLogin records a successful sign-in.
func (d *DB) TouchLogin(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, Now(), id)
	return err
}

// --------------------------------------------------------------------- sessions

// CreateSession stores an opaque session token.
func (d *DB) CreateSession(ctx context.Context, token string, userID int64, expires time.Time, userAgent string) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, created_at, expires_at, user_agent) VALUES(?, ?, ?, ?, ?)`,
		token, userID, Now(), FormatTime(expires), userAgent)
	return err
}

// ResolveSession returns the user behind a token, deleting it when expired.
func (d *DB) ResolveSession(ctx context.Context, token string) (User, error) {
	var u User
	var expires string
	var disabled int
	err := d.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.role, u.disabled, s.expires_at
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token = ?`, token).Scan(&u.ID, &u.Username, &u.Role, &disabled, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if disabled != 0 {
		return User{}, ErrNotFound
	}
	if expires < Now() {
		_, _ = d.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
		return User{}, ErrNotFound
	}
	return u, nil
}

// DeleteSession signs a single session out.
func (d *DB) DeleteSession(ctx context.Context, token string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}
