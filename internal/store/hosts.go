package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
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
	// internal/tunnel and internal/hub/tunneldial.go) — the hub dials out
	// to this host and keeps that connection ready so it can still reach
	// the dashboard/terminal if SSH becomes unreachable. Off by default,
	// same reasoning as TerminalEnabled: a new opt-in surface, not
	// something every host should get just by being added.
	TunnelEnabled bool   `json:"tunnel_enabled"`
	ErrorMsg      string `json:"error_msg,omitempty"`
	// Group — произвольная группа в списке хостов («прод», «клиент А»).
	// Пустая строка означает «Без группы»: такой раздел показывается в
	// конце списка, а не прячется — хост без группы не должен исчезать.
	Group string `json:"group"`
	// ParentID — хост, на котором работает эта машина; 0 у обычных
	// хостов. Машина не живёт отдельно от своего сервера: она
	// показывается под ним и переезжает между группами только вместе с
	// ним.
	ParentID int64 `json:"parent_id,omitempty"`
	// ProfileID — профиль, по которому хост был создан (машина, заведённая
	// хабом с профилем). 0 — создан не по профилю или до того, как хаб
	// стал это запоминать. Только цвет строки, никакого повторного
	// применения.
	ProfileID  int64  `json:"profile_id,omitempty"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at,omitempty"`

	// SecretEnc, AdminPasswordEnc and TunnelTokenEnc are secretbox-encrypted
	// and never serialised to JSON — only the hub package that holds the
	// master key reads them. TunnelTokenEnc is encrypted, not just hashed
	// like the first iteration of this feature: the hub is now the side
	// that *presents* the token on every reconnect (see
	// internal/hub/tunneldial.go), not just the side that verifies one, so
	// it needs the raw value back — see SetHostTunnelToken.
	SecretEnc        []byte `json:"-"`
	AdminPasswordEnc []byte `json:"-"`
	TunnelTokenEnc   []byte `json:"-"`
	// TunnelCertSHA256 is the SHA-256 fingerprint of the tunnel TLS
	// certificate this host presented the first time the hub ever dialed
	// it successfully — not a secret (unlike the fields above), just not
	// useful to any API consumer, so left out of JSON like the rest of
	// this connection-plumbing group. Empty means "not pinned yet": the
	// next dial trusts whatever cert it sees and records it here (see
	// internal/hub/tunnelpin.go); non-empty means every future dial must
	// match it exactly, the same trust-on-first-use model SSH host keys
	// use, except this one is actually enforced after the first sighting
	// instead of trusted forever.
	TunnelCertSHA256 []byte `json:"-"`
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
	tunnel_enabled, tunnel_token_enc, tunnel_cert_sha256, error_msg, created_at, last_seen_at, group_name,
	parent_id, profile_id`

func scanHost(row interface{ Scan(...any) error }) (Host, error) {
	var h Host
	var lastSeen sql.NullString
	var adminPasswordEnc, tunnelTokenEnc, tunnelCertSHA256 []byte
	err := row.Scan(&h.ID, &h.Name, &h.Addr, &h.SSHPort, &h.SSHUser, &h.SSHAuthKind, &h.SecretEnc,
		&h.Arch, &h.Status, &h.NktVersion, &h.AdminUser, &adminPasswordEnc, &h.SudoStatus, &h.TerminalEnabled,
		&h.TunnelEnabled, &tunnelTokenEnc, &tunnelCertSHA256, &h.ErrorMsg, &h.CreatedAt, &lastSeen, &h.Group,
		&h.ParentID, &h.ProfileID)
	if err != nil {
		return Host{}, err
	}
	h.AdminPasswordEnc = adminPasswordEnc
	h.TunnelTokenEnc = tunnelTokenEnc
	h.TunnelCertSHA256 = tunnelCertSHA256
	h.LastSeenAt = lastSeen.String
	return h, nil
}

// SetHostParent привязывает хост к машине, на которой он работает, и
// сразу переносит его в группу этой машины.
//
// Группа записывается, а не вычисляется на выдаче: иначе она зависела бы
// от того, каким путём список читают, а разные части интерфейса
// показывали бы машину то в одной группе, то в другой.
func (d *DB) SetHostParent(ctx context.Context, id, parentID int64) error {
	_, err := d.ExecContext(ctx, `
		UPDATE hosts SET parent_id = ?,
		                 group_name = COALESCE((SELECT group_name FROM hosts WHERE id = ?), '')
		WHERE id = ?`, parentID, parentID, id)
	return err
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

// SetHostGroup меняет группу хоста. Отдельным методом, а не через
// UpdateHost: перетаскивание строки в списке меняет ровно это, и
// заставлять его пересылать адрес, порт и пользователя было бы способом
// однажды перезаписать их устаревшими значениями из открытой формы.
func (d *DB) SetHostGroup(ctx context.Context, id int64, group string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE hosts SET group_name = ? WHERE id = ?`, group, id); err != nil {
		return err
	}
	// Машины переезжают вместе со своим хостом — в этом и смысл
	// привязки. Одной транзакцией, чтобы список не успел показать хост в
	// новой группе, а его машины в старой.
	if _, err := tx.ExecContext(ctx, `UPDATE hosts SET group_name = ? WHERE parent_id = ?`, group, id); err != nil {
		return err
	}
	return tx.Commit()
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

// SetHostTunnelToken stores a freshly generated reverse-tunnel token,
// secretbox-encrypted by the caller — called once per install/update that
// has TunnelEnabled on, right after a new random token is generated and
// written into the host's own env file. Unlike the first iteration of this
// feature (which only ever stored a SHA-256 digest), the raw value has to
// be recoverable: the hub is now the side presenting the token on every
// reconnect (see internal/hub/tunneldial.go), not the side verifying one.
func (d *DB) SetHostTunnelToken(ctx context.Context, id int64, tokenEnc []byte) error {
	res, err := d.ExecContext(ctx, `UPDATE hosts SET tunnel_token_enc = ? WHERE id = ?`, tokenEnc, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostTunnelCertSHA256 records the SHA-256 fingerprint of the tunnel TLS
// certificate this host presented — either pinning it for the first time
// (see internal/hub/tunnelpin.go's trust-on-first-use dial logic) or, with
// a nil fingerprint, clearing a previous pin so the next dial re-pins from
// scratch. Called with nil from prepareTunnelEnv on every fresh
// install/update: a new token means a new trust bootstrap, and a
// legitimate reinstall (new DataDir, cert regenerated) must not get
// permanently locked out by a pin left over from before.
func (d *DB) SetHostTunnelCertSHA256(ctx context.Context, id int64, fingerprint []byte) error {
	res, err := d.ExecContext(ctx, `UPDATE hosts SET tunnel_cert_sha256 = ? WHERE id = ?`, fingerprint, id)
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

// ListHostGroups возвращает названия групп: и заведённые явно, и те, что
// упомянуты у хостов.
//
// Объединение, а не один источник: группу можно создать заранее пустой
// (тогда она есть только в таблице), а можно вписать её название прямо в
// форме хоста (тогда она есть только у хоста). Оба способа рабочие, и
// список должен показывать результат обоих.
func (d *DB) ListHostGroups(ctx context.Context) ([]string, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT name FROM host_groups
		UNION
		SELECT group_name FROM hosts WHERE group_name <> ''
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// CreateHostGroup заводит пустую группу. Повторное создание существующей —
// не ошибка: результат тот же, что и просили.
func (d *DB) CreateHostGroup(ctx context.Context, name string) error {
	return d.CreateHostGroupWithProfile(ctx, name, 0)
}

// CreateHostGroupWithProfile заводит группу с профилем (0 — без него).
// Профиль задаётся только при создании: у существующей группы он не
// меняется — иначе хосты, уже лежащие в ней, оказались бы «под профилем»,
// который к ним никто не применял.
func (d *DB) CreateHostGroupWithProfile(ctx context.Context, name string, profileID int64) error {
	_, err := d.ExecContext(ctx,
		`INSERT OR IGNORE INTO host_groups(name, created_at, profile_id) VALUES(?, ?, ?)`,
		name, FormatTime(time.Now()), profileID)
	return err
}

// SetHostProfile запоминает, по какому профилю хост создан.
func (d *DB) SetHostProfile(ctx context.Context, id, profileID int64) error {
	_, err := d.ExecContext(ctx, `UPDATE hosts SET profile_id = ? WHERE id = ?`, profileID, id)
	return err
}

// HostGroupProfile — профиль группы: имя группы → идентификатор профиля.
// Группы без профиля в карте нет.
func (d *DB) HostGroupProfiles(ctx context.Context) (map[string]int64, error) {
	rows, err := d.QueryContext(ctx, `SELECT name, profile_id FROM host_groups WHERE profile_id <> 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}

// HostGroupProfile отдаёт профиль одной группы (0 — нет).
func (d *DB) HostGroupProfile(ctx context.Context, name string) (int64, error) {
	var id int64
	err := d.QueryRowContext(ctx, `SELECT profile_id FROM host_groups WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// RenameHostGroup переименовывает группу вместе с хостами в ней.
//
// Одной транзакцией: если переименовать строку в таблице и не дойти до
// хостов, группа с прежним названием тут же появится обратно — она ведь
// выводится в том числе из поля у хостов.
func (d *DB) RenameHostGroup(ctx context.Context, from, to string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE hosts SET group_name = ? WHERE group_name = ?`, to, from); err != nil {
		return err
	}
	// Профиль переезжает вместе с группой.
	var profileID int64
	_ = tx.QueryRowContext(ctx, `SELECT profile_id FROM host_groups WHERE name = ?`, from).Scan(&profileID)
	if _, err := tx.ExecContext(ctx, `DELETE FROM host_groups WHERE name = ?`, from); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO host_groups(name, created_at, profile_id) VALUES(?, ?, ?)`,
		to, FormatTime(time.Now()), profileID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteHostGroup убирает группу, возвращая её хосты в «Без группы».
//
// Хосты не удаляются и не прячутся: группа — это метка на списке, а не
// контейнер, и её исчезновение не должно уносить с собой сервера.
func (d *DB) DeleteHostGroup(ctx context.Context, name string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE hosts SET group_name = '' WHERE group_name = ?`, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM host_groups WHERE name = ?`, name); err != nil {
		return err
	}
	return tx.Commit()
}
