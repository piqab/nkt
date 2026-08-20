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

// Sudo status values — set as a side effect of whatever the last install/
// update actually observed (see internal/hub), not probed independently:
// stageFiles/activateService already need sudo when SSHUser isn't root, so
// their own success or failure already answers the question.
const (
	// SudoStatusUnknown means no install has run since this field was
	// added, or since the host's connection details last changed.
	SudoStatusUnknown          = ""
	SudoStatusRoot             = "root"
	SudoStatusNopasswd         = "nopasswd"
	SudoStatusPasswordRequired = "password_required"
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
	SudoStatus  string `json:"sudo_status,omitempty"`
	// TerminalEnabled is passed through as NKT_TERMINAL_ENABLED when the hub
	// (re)installs this host — see internal/hub/provision.go's renderEnv.
	// Off by default like the env var itself: opening a root shell on a
	// managed host is not something the hub should hand out just because a
	// host was added, so this needs its own explicit per-host opt-in.
	TerminalEnabled bool `json:"terminal_enabled"`
	// TunnelEnabled turns on the reverse-tunnel fallback channel (see
	// internal/tunnel and internal/hub/tunnel.go) — the host dials the hub
	// itself over WebSocket and keeps that connection ready so the hub can
	// still reach its dashboard/terminal if SSH becomes unreachable. Off by
	// default, same reasoning as TerminalEnabled: a new opt-in surface,
	// not something every host should get just by being added.
	TunnelEnabled bool   `json:"tunnel_enabled"`
	ErrorMsg      string `json:"error_msg,omitempty"`
	CreatedAt     string `json:"created_at"`
	LastSeenAt    string `json:"last_seen_at,omitempty"`

	// SecretEnc and AdminPasswordEnc are secretbox-encrypted and never
	// serialised to JSON — only the hub package that holds the master key
	// reads them. TunnelTokenHash is not a secret to recover (nothing ever
	// needs the raw token back, only to verify a presented one against it),
	// so it is a plain SHA-256 digest, not secretbox-encrypted — see
	// SetHostTunnelToken.
	SecretEnc        []byte `json:"-"`
	AdminPasswordEnc []byte `json:"-"`
	TunnelTokenHash  []byte `json:"-"`
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
	arch, status, nkt_version, admin_user, admin_password_enc, sudo_status, terminal_enabled,
	tunnel_enabled, tunnel_token_hash, error_msg, created_at, last_seen_at`

func scanHost(row interface{ Scan(...any) error }) (Host, error) {
	var h Host
	var lastSeen sql.NullString
	var adminPasswordEnc, tunnelTokenHash []byte
	err := row.Scan(&h.ID, &h.Name, &h.Addr, &h.SSHPort, &h.SSHUser, &h.SSHAuthKind, &h.SecretEnc,
		&h.Arch, &h.Status, &h.NktVersion, &h.AdminUser, &adminPasswordEnc, &h.SudoStatus, &h.TerminalEnabled,
		&h.TunnelEnabled, &tunnelTokenHash, &h.ErrorMsg, &h.CreatedAt, &lastSeen)
	if err != nil {
		return Host{}, err
	}
	h.AdminPasswordEnc = adminPasswordEnc
	h.TunnelTokenHash = tunnelTokenHash
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
	// A changed ssh_user in particular can invalidate a previously observed
	// sudo_status (root <-> non-root, or a different account entirely) —
	// clearing it here means the UI shows "неизвестно" instead of a status
	// that may no longer be true until the next install/update reobserves it.
	res, err := d.ExecContext(ctx,
		`UPDATE hosts SET name = ?, addr = ?, ssh_port = ?, ssh_user = ?, ssh_auth_kind = ?, sudo_status = ? WHERE id = ?`,
		name, addr, sshPort, sshUser, authKind, SudoStatusUnknown, id)
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

// SetHostSudoStatus records what the last install/update actually observed
// about sudo access for a non-root SSH user (or that none was needed,
// SudoStatusRoot) — see the SudoStatus* constants.
func (d *DB) SetHostSudoStatus(ctx context.Context, id int64, status string) error {
	switch status {
	case SudoStatusUnknown, SudoStatusRoot, SudoStatusNopasswd, SudoStatusPasswordRequired:
	default:
		return errors.New("unknown sudo status: " + status)
	}
	res, err := d.ExecContext(ctx, `UPDATE hosts SET sudo_status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostTerminalEnabled records whether the hub should pass
// NKT_TERMINAL_ENABLED=true to this host on its next install/update — see
// Host.TerminalEnabled.
func (d *DB) SetHostTerminalEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := d.ExecContext(ctx, `UPDATE hosts SET terminal_enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostTunnelEnabled records whether the hub should pass the reverse-
// tunnel env vars (see internal/hub/provision.go's renderEnv) to this host
// on its next install/update — see Host.TunnelEnabled. Does not by itself
// touch tunnel_token_hash: turning this on takes effect only once an
// install/update actually runs and calls SetHostTunnelToken with a freshly
// generated token.
func (d *DB) SetHostTunnelEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := d.ExecContext(ctx, `UPDATE hosts SET tunnel_enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostTunnelToken stores the SHA-256 digest of a freshly generated
// reverse-tunnel token — called once per install/update that has
// TunnelEnabled on, right after a new random token is generated and written
// into the host's own env file. Only the digest is kept: nothing on the hub
// side ever needs the raw token back, only to verify a value a connecting
// host presents against it (see internal/hub/tunnel.go), so there is
// nothing to decrypt and therefore no secretbox round trip needed here,
// unlike SecretEnc/AdminPasswordEnc.
func (d *DB) SetHostTunnelToken(ctx context.Context, id int64, tokenHash []byte) error {
	res, err := d.ExecContext(ctx, `UPDATE hosts SET tunnel_token_hash = ? WHERE id = ?`, tokenHash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetStuckInstalls marks every host still showing 'installing' as
// 'error' — called once when a hub starts up, since a status like that can
// only mean the process that was running the install died (crashed,
// restarted for an upgrade) with no goroutine left to ever finish it. Left
// alone, the host would stay 'installing' forever and its
// "переустановить"/cancel controls would have nothing real to act on.
func (d *DB) ResetStuckInstalls(ctx context.Context, message string) (int64, error) {
	res, err := d.ExecContext(ctx,
		`UPDATE hosts SET status = ?, error_msg = ? WHERE status = ?`,
		HostStatusError, message, HostStatusInstalling)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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
