package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

// validAdminUser mirrors internal/hub's own check on the same field — kept
// as a separate copy rather than a shared export since store cannot import
// hub (hub already imports store). AdminUser is only ever "admin" in every
// real code path; an imported export is untrusted data (a file an operator
// chose to upload, not something this hub generated), and this field later
// gets interpolated into a remote shell command and a systemd
// EnvironmentFile line on whatever host it's later installed to — reject
// anything that isn't a plain identifier right at import time, rather than
// only failing (still safely — internal/hub checks again) much later at
// install.
var validAdminUser = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ExportFormatVersion guards against feeding an export from an incompatible
// future (or malformed) file into ImportHosts — bumped only if the shape of
// HostExport itself changes in a way old readers couldn't handle.
const ExportFormatVersion = 1

// HostExport is store.Host with its encrypted-blob fields included (Host
// itself hides them behind `json:"-"` to keep them out of the normal
// /hub/hosts API response) — []byte marshals to base64 automatically via
// encoding/json, so the ciphertext round-trips through JSON as-is. It
// decrypts only with whatever NKT_HUB_MASTER_KEY produced it in the first
// place; ImportHosts does not attempt to decrypt anything; a hub with a
// different key simply fails to reach those hosts later; the same clear
// "расшифровка SSH-секрета" error every other secretbox-consuming call
// already gives, not something this file needs to detect ahead of time.
//
// TunnelEnabled/TunnelTokenEnc travel too — dropping them used to leave a
// migrated host's reverse-tunnel fallback silently off on the new hub,
// which is exactly backwards: a hub migrating to a new address/location is
// the single most likely time for the *old* SSH path to stop working (a
// firewall/security group allowlisting only the old hub's IP, say —
// "ssh: handshake failed: EOF" the moment the new hub tries it), which is
// precisely what this fallback exists for. TunnelCertSHA256 deliberately
// does NOT travel: a fresh hub connecting for the first time is a genuine
// first sighting as far as that hub is concerned, and letting it pin its
// own trust-on-first-use fingerprint (see internal/hub/tunnelpin.go) rather
// than inheriting a foreign hub's prior pin keeps that model honest.
type HostExport struct {
	Name             string `json:"name"`
	Addr             string `json:"addr"`
	SSHPort          int    `json:"ssh_port"`
	SSHUser          string `json:"ssh_user"`
	SSHAuthKind      string `json:"ssh_auth_kind"`
	SecretEnc        []byte `json:"secret_enc"`
	Arch             string `json:"arch"`
	Status           string `json:"status"`
	NktVersion       string `json:"nkt_version"`
	AdminUser        string `json:"admin_user,omitempty"`
	AdminPasswordEnc []byte `json:"admin_password_enc,omitempty"`
	SudoStatus       string `json:"sudo_status,omitempty"`
	TerminalEnabled  bool   `json:"terminal_enabled"`
	TunnelEnabled    bool   `json:"tunnel_enabled"`
	TunnelTokenEnc   []byte `json:"tunnel_token_enc,omitempty"`
	ErrorMsg         string `json:"error_msg,omitempty"`
	CreatedAt        string `json:"created_at"`
	LastSeenAt       string `json:"last_seen_at,omitempty"`
}

// HubExport is the full document GET /hub/export hands back and POST
// /hub/import expects — deliberately just the host registry (not user
// accounts or the audit log): the thing that actually takes real effort to
// recreate by hand is which hosts the hub knows and how to reach them.
type HubExport struct {
	Version    int          `json:"version"`
	ExportedAt string       `json:"exported_at"`
	Hosts      []HostExport `json:"hosts"`
	// MasterKey is the exporting hub's own secretbox key (base64), present
	// only when the operator opted into a one-step migration — see
	// Manager.ExportHosts/ImportHosts in internal/hub, which is what
	// actually knows how to use it (this package only carries it through
	// JSON; the store layer itself never decrypts anything).
	MasterKey string `json:"master_key,omitempty"`
}

func hostToExport(h Host) HostExport {
	return HostExport{
		Name: h.Name, Addr: h.Addr, SSHPort: h.SSHPort, SSHUser: h.SSHUser, SSHAuthKind: h.SSHAuthKind,
		SecretEnc: h.SecretEnc, Arch: h.Arch, Status: h.Status, NktVersion: h.NktVersion,
		AdminUser: h.AdminUser, AdminPasswordEnc: h.AdminPasswordEnc, SudoStatus: h.SudoStatus,
		TerminalEnabled: h.TerminalEnabled, TunnelEnabled: h.TunnelEnabled, TunnelTokenEnc: h.TunnelTokenEnc,
		ErrorMsg: h.ErrorMsg, CreatedAt: h.CreatedAt, LastSeenAt: h.LastSeenAt,
	}
}

// ExportHosts returns every managed host in the shape GET /hub/export sends
// to the browser as a downloadable file.
func (d *DB) ExportHosts(ctx context.Context) (HubExport, error) {
	hosts, err := d.ListHosts(ctx)
	if err != nil {
		return HubExport{}, err
	}
	out := HubExport{Version: ExportFormatVersion, ExportedAt: Now(), Hosts: make([]HostExport, len(hosts))}
	for i, h := range hosts {
		out.Hosts[i] = hostToExport(h)
	}
	return out, nil
}

// ImportHosts inserts every host in export as a brand-new row — additive,
// not a replace-or-merge: it never touches an existing host, and does not
// deduplicate by name/address against what's already registered (importing
// the same file twice creates duplicates). Each host is attempted
// independently so one malformed entry (an export file hand-edited badly,
// or from an incompatible future version) doesn't abort the rest — imported
// counts the successes, errs carries one message per row that failed.
func (d *DB) ImportHosts(ctx context.Context, export HubExport) (imported int, errs []string) {
	for _, h := range export.Hosts {
		if err := d.importOneHost(ctx, h); err != nil {
			errs = append(errs, fmt.Sprintf("%s (%s): %v", h.Name, h.Addr, err))
			continue
		}
		imported++
	}
	return imported, errs
}

func (d *DB) importOneHost(ctx context.Context, h HostExport) error {
	if h.Name == "" || h.Addr == "" {
		return fmt.Errorf("пустое имя или адрес")
	}
	if h.AdminUser != "" && !validAdminUser.MatchString(h.AdminUser) {
		return fmt.Errorf("недопустимое имя администратора %q", h.AdminUser)
	}
	_, err := d.ExecContext(ctx,
		`INSERT INTO hosts(
			name, addr, ssh_port, ssh_user, ssh_auth_kind, secret_enc,
			arch, status, nkt_version, admin_user, admin_password_enc,
			sudo_status, terminal_enabled, tunnel_enabled, tunnel_token_enc,
			error_msg, created_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.Name, h.Addr, h.SSHPort, h.SSHUser, h.SSHAuthKind, h.SecretEnc,
		h.Arch, h.Status, h.NktVersion, h.AdminUser, h.AdminPasswordEnc,
		h.SudoStatus, h.TerminalEnabled, h.TunnelEnabled, h.TunnelTokenEnc,
		h.ErrorMsg, h.CreatedAt, h.LastSeenAt)
	return err
}

// DecodeHubExport parses an uploaded export file, rejecting one from an
// export format this build doesn't understand rather than silently
// importing a partial/misread result.
func DecodeHubExport(data []byte) (HubExport, error) {
	var export HubExport
	if err := json.Unmarshal(data, &export); err != nil {
		return HubExport{}, fmt.Errorf("файл не похож на экспорт хаба: %w", err)
	}
	if export.Version != ExportFormatVersion {
		return HubExport{}, fmt.Errorf("версия формата экспорта %d не поддерживается (ожидается %d)",
			export.Version, ExportFormatVersion)
	}
	return export, nil
}
