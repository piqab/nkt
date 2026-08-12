package store

import (
	"context"
	"database/sql"
	"errors"
)

// Host status values.
const (
	HostStatusNew        = "new"
	HostStatusInstalling = "installing"
	HostStatusOnline     = "online"
	HostStatusError      = "error"
)

// SSH auth kinds.
const (
	HostAuthPassword = "password"
	HostAuthKey      = "key"
)

// Host is one VPS a hub instance manages over SSH.
type Host struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHAuthKind string `json:"ssh_auth_kind"`
	Arch        string `json:"arch"`
	Status      string `json:"status"`
	NktVersion  string `json:"nkt_version"`
	AdminUser   string `json:"admin_user,omitempty"`
	ErrorMsg    string `json:"error_msg,omitempty"`
	CreatedAt   string `json:"created_at"`
	LastSeenAt  string `json:"last_seen_at,omitempty"`

	// SecretEnc and AdminPasswordEnc are secretbox-encrypted and never
	// serialised to JSON — only the hub package that holds the master key
	// reads them.
	SecretEnc        []byte `json:"-"`
	AdminPasswordEnc []byte `json:"-"`
}

// CreateHost inserts a host with an already-encrypted SSH secret.
func (d *DB) CreateHost(ctx context.Context, name, addr string, sshPort int, sshUser, authKind string, secretEnc []byte) (int64, error) {
	if authKind != HostAuthPassword && authKind != HostAuthKey {
		return 0, errors.New("unknown ssh auth kind: " + authKind)
	}
	res, err := d.ExecContext(ctx,
		`INSERT INTO hosts(name, addr, ssh_port, ssh_user, ssh_auth_kind, secret_enc, status, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		name, addr, sshPort, sshUser, authKind, secretEnc, HostStatusNew, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const hostColumns = `id, name, addr, ssh_port, ssh_user, ssh_auth_kind, secret_enc,
	arch, status, nkt_version, admin_user, admin_password_enc, error_msg, created_at, last_seen_at`

func scanHost(row interface{ Scan(...any) error }) (Host, error) {
	var h Host
	var lastSeen sql.NullString
	var adminPasswordEnc []byte
	err := row.Scan(&h.ID, &h.Name, &h.Addr, &h.SSHPort, &h.SSHUser, &h.SSHAuthKind, &h.SecretEnc,
		&h.Arch, &h.Status, &h.NktVersion, &h.AdminUser, &adminPasswordEnc, &h.ErrorMsg, &h.CreatedAt, &lastSeen)
	if err != nil {
		return Host{}, err
	}
	h.AdminPasswordEnc = adminPasswordEnc
	h.LastSeenAt = lastSeen.String
	return h, nil
}

// HostByID looks a host up by its id.
func (d *DB) HostByID(ctx context.Context, id int64) (Host, error) {
	row := d.QueryRowContext(ctx, `SELECT `+hostColumns+` FROM hosts WHERE id = ?`, id)
	h, err := scanHost(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrNotFound
	}
	return h, err
}

// ListHosts returns every managed host, ordered by name.
func (d *DB) ListHosts(ctx context.Context) ([]Host, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+hostColumns+` FROM hosts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Host{}
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DeleteHost removes a host from the registry. It does not undo anything on
// the remote machine — uninstalling nkt there, if wanted, is a separate step.
func (d *DB) DeleteHost(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateHost changes a host's connection details — everything but its SSH
// secret, which SetHostSecret handles separately so editing a host's name or
// address never requires re-entering a credential that did not change.
func (d *DB) UpdateHost(ctx context.Context, id int64, name, addr string, sshPort int, sshUser, authKind string) error {
	if authKind != HostAuthPassword && authKind != HostAuthKey {
		return errors.New("unknown ssh auth kind: " + authKind)
	}
	res, err := d.ExecContext(ctx,
		`UPDATE hosts SET name = ?, addr = ?, ssh_port = ?, ssh_user = ?, ssh_auth_kind = ? WHERE id = ?`,
		name, addr, sshPort, sshUser, authKind, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostSecret replaces a host's encrypted SSH credential — used when
// editing a host with a new password or key, distinct from the auth_kind/
// secret_enc pair CreateHost sets at registration.
func (d *DB) SetHostSecret(ctx context.Context, id int64, authKind string, secretEnc []byte) error {
	if authKind != HostAuthPassword && authKind != HostAuthKey {
		return errors.New("unknown ssh auth kind: " + authKind)
	}
	res, err := d.ExecContext(ctx,
		`UPDATE hosts SET ssh_auth_kind = ?, secret_enc = ? WHERE id = ?`, authKind, secretEnc, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostStatus updates a host's install/health status and, for the 'error'
// status, the reason. errMsg is cleared for every other status.
func (d *DB) SetHostStatus(ctx context.Context, id int64, status, errMsg string) error {
	res, err := d.ExecContext(ctx, `UPDATE hosts SET status = ?, error_msg = ? WHERE id = ?`, status, errMsg, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostArch records the CPU/OS architecture detected for a host, e.g.
// "linux/amd64".
func (d *DB) SetHostArch(ctx context.Context, id int64, arch string) error {
	res, err := d.ExecContext(ctx, `UPDATE hosts SET arch = ? WHERE id = ?`, arch, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostVersion records the nkt build version installed on a host.
func (d *DB) SetHostVersion(ctx context.Context, id int64, version string) error {
	res, err := d.ExecContext(ctx, `UPDATE hosts SET nkt_version = ? WHERE id = ?`, version, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostAdmin stores the remote bootstrap admin account the hub logs in as
// when proxying requests — encrypted, unlike a session token, because a
// session expires and can only be renewed by logging in again.
func (d *DB) SetHostAdmin(ctx context.Context, id int64, adminUser string, adminPasswordEnc []byte) error {
	res, err := d.ExecContext(ctx,
		`UPDATE hosts SET admin_user = ?, admin_password_enc = ? WHERE id = ?`, adminUser, adminPasswordEnc, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchHostSeen records that the hub successfully reached a host just now.
func (d *DB) TouchHostSeen(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `UPDATE hosts SET last_seen_at = ? WHERE id = ?`, Now(), id)
	return err
}
