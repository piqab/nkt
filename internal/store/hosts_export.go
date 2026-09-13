package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
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
//
// Версия 2 добавила группы, связь машины с хостом-родителем, профили,
// шаблоны машин и настройки хаба; файлы версии 1 читаются по-прежнему.
const ExportFormatVersion = 2

// minExportFormatVersion — самая старая версия, которую импорт ещё
// понимает.
const minExportFormatVersion = 1

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
	// Group — группа хоста; Parent — имя хоста-родителя для машины,
	// созданной внутри хоста (идентификаторы в другом хабе другие, имя —
	// единственное, что переживает переезд).
	Group  string `json:"group,omitempty"`
	Parent string `json:"parent,omitempty"`
	// Profile — имя профиля, по которому хост создан.
	Profile string `json:"profile,omitempty"`
}

// ProfileExport — профиль хаба с историей редакций.
type ProfileExport struct {
	Name     string                 `json:"name"`
	Color    string                 `json:"color,omitempty"`
	Content  string                 `json:"content"`
	Note     string                 `json:"note,omitempty"`
	Author   string                 `json:"author,omitempty"`
	Versions []ProfileVersionExport `json:"versions,omitempty"`
}

// ProfileVersionExport — одна прошлая редакция профиля.
type ProfileVersionExport struct {
	TS      string `json:"ts"`
	Author  string `json:"author,omitempty"`
	Note    string `json:"note,omitempty"`
	Content string `json:"content"`
}

// VMTemplateExport — шаблон машины.
type VMTemplateExport struct {
	Name   string `json:"name"`
	Spec   string `json:"spec"`
	Author string `json:"author,omitempty"`
}

// HubExport is the full document GET /hub/export hands back and POST
// /hub/import expects — deliberately just the host registry (not user
// accounts or the audit log): the thing that actually takes real effort to
// recreate by hand is which hosts the hub knows and how to reach them.
type HubExport struct {
	Version    int          `json:"version"`
	ExportedAt string       `json:"exported_at"`
	Hosts      []HostExport `json:"hosts"`
	// Groups — все группы, включая пустые: пустая группа — тоже
	// настройка, которую заводили руками.
	Groups []string `json:"groups,omitempty"`
	// GroupProfiles — профиль группы по имени профиля: идентификаторы в
	// другом хабе другие.
	GroupProfiles map[string]string  `json:"group_profiles,omitempty"`
	Profiles      []ProfileExport    `json:"profiles,omitempty"`
	VMTemplates   []VMTemplateExport `json:"vm_templates,omitempty"`
	// Settings — настройки хаба из таблицы kv по ключу (настройки
	// оповещений, группа строки localhost, умолчания подготовки).
	Settings map[string]string `json:"settings,omitempty"`
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
		Group: h.Group,
	}
}

// ExportedSettingKeys — какие ключи kv едут в экспорт. Перечислены явно:
// в kv лежит и то, что переносить нельзя (например, что уже показано
// пользователю).
var ExportedSettingKeys = []string{"hub.events.settings", "hub.localhost.group", "hub.bootstrap.defaults"}

// ExportHosts returns every managed host in the shape GET /hub/export sends
// to the browser as a downloadable file.
func (d *DB) ExportHosts(ctx context.Context) (HubExport, error) {
	hosts, err := d.ListHosts(ctx)
	if err != nil {
		return HubExport{}, err
	}
	out := HubExport{Version: ExportFormatVersion, ExportedAt: Now(), Hosts: make([]HostExport, len(hosts))}
	byID := map[int64]string{}
	for _, h := range hosts {
		byID[h.ID] = h.Name
	}
	for i, h := range hosts {
		out.Hosts[i] = hostToExport(h)
		if h.ParentID != 0 {
			out.Hosts[i].Parent = byID[h.ParentID]
		}
	}
	if out.Groups, err = d.ListHostGroups(ctx); err != nil {
		return HubExport{}, err
	}
	profiles, err := d.ListProfiles(ctx)
	if err != nil {
		return HubExport{}, err
	}
	profileNames := map[int64]string{}
	for _, p := range profiles {
		profileNames[p.ID] = p.Name
	}
	for i, h := range hosts {
		if h.ProfileID != 0 {
			out.Hosts[i].Profile = profileNames[h.ProfileID]
		}
	}
	if gp, err := d.HostGroupProfiles(ctx); err == nil {
		for group, id := range gp {
			if name := profileNames[id]; name != "" {
				if out.GroupProfiles == nil {
					out.GroupProfiles = map[string]string{}
				}
				out.GroupProfiles[group] = name
			}
		}
	}
	for _, p := range profiles {
		full, err := d.ProfileByID(ctx, p.ID)
		if err != nil {
			return HubExport{}, err
		}
		pe := ProfileExport{Name: full.Name, Color: full.Color, Content: full.Content, Note: full.Note, Author: full.Author}
		versions, err := d.ProfileVersions(ctx, p.ID, 200)
		if err != nil {
			return HubExport{}, err
		}
		// ProfileVersions отдаёт список новыми вперёд и без содержимого;
		// в файл — по порядку и целиком.
		for i := len(versions) - 1; i >= 0; i-- {
			v, err := d.ProfileVersion(ctx, versions[i].ID)
			if err != nil {
				return HubExport{}, err
			}
			pe.Versions = append(pe.Versions, ProfileVersionExport{TS: v.TS, Author: v.Author, Note: v.Note, Content: v.Content})
		}
		out.Profiles = append(out.Profiles, pe)
	}
	templates, err := d.ListVMTemplates(ctx)
	if err != nil {
		return HubExport{}, err
	}
	for _, t := range templates {
		out.VMTemplates = append(out.VMTemplates, VMTemplateExport{Name: t.Name, Spec: t.Spec, Author: t.Author})
	}
	for _, key := range ExportedSettingKeys {
		if v, ok, err := d.KVGet(ctx, key); err == nil && ok && v != "" {
			if out.Settings == nil {
				out.Settings = map[string]string{}
			}
			out.Settings[key] = v
		}
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
//
// Остальное из файла версии 2: группы заводятся (существующие не
// трогаются), родитель машины находится по имени среди хостов файла,
// профили и шаблоны с уже занятым именем пропускаются с сообщением —
// затирать то, что есть в этом хабе, импорт не должен; настройки хаба
// записываются только туда, где их ещё не задавали.
func (d *DB) ImportHosts(ctx context.Context, export HubExport) (imported int, errs []string) {
	for _, g := range export.Groups {
		if g == "" {
			continue
		}
		if err := d.CreateHostGroup(ctx, g); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", g, err))
		}
	}
	ids := map[string]int64{}
	for _, h := range export.Hosts {
		id, err := d.importOneHost(ctx, h)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s (%s): %v", h.Name, h.Addr, err))
			continue
		}
		ids[h.Name] = id
		imported++
	}
	for _, h := range export.Hosts {
		if h.Parent == "" {
			continue
		}
		id, ok := ids[h.Name]
		if !ok {
			continue
		}
		parentID, ok := ids[h.Parent]
		if !ok {
			errs = append(errs, msgs.Tc(ctx, "store.importParentMissing", h.Name, h.Parent))
			continue
		}
		if err := d.SetHostParent(ctx, id, parentID); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", h.Name, err))
		}
	}
	errs = append(errs, d.importProfiles(ctx, export.Profiles)...)
	errs = append(errs, d.importVMTemplates(ctx, export.VMTemplates)...)
	// Профили групп — после профилей: искать их по имени можно только
	// когда они уже заведены. Существующий профиль группы не трогается.
	hostProfiles := false
	for _, h := range export.Hosts {
		if h.Profile != "" {
			hostProfiles = true
		}
	}
	if len(export.GroupProfiles) > 0 || hostProfiles {
		all, err := d.ListProfiles(ctx)
		if err != nil {
			errs = append(errs, err.Error())
		} else {
			byName := map[string]int64{}
			for _, p := range all {
				byName[p.Name] = p.ID
			}
			for group, name := range export.GroupProfiles {
				id, ok := byName[name]
				if !ok {
					errs = append(errs, msgs.Tc(ctx, "store.importGroupProfileMissing", group, name))
					continue
				}
				if _, err := d.ExecContext(ctx, `UPDATE host_groups SET profile_id = ? WHERE name = ? AND profile_id = 0`, id, group); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", group, err))
				}
			}
			for _, h := range export.Hosts {
				if h.Profile == "" {
					continue
				}
				if id, ok := ids[h.Name]; ok {
					if pid, ok := byName[h.Profile]; ok {
						_ = d.SetHostProfile(ctx, id, pid)
					}
				}
			}
		}
	}
	for key, value := range export.Settings {
		if !exportedSettingKey(key) {
			continue
		}
		if _, ok, err := d.KVGet(ctx, key); err != nil || ok {
			continue
		}
		if err := d.KVSet(ctx, key, value); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", key, err))
		}
	}
	return imported, errs
}

func exportedSettingKey(key string) bool {
	for _, k := range ExportedSettingKeys {
		if k == key {
			return true
		}
	}
	return false
}

func (d *DB) importProfiles(ctx context.Context, profiles []ProfileExport) (errs []string) {
	existing, err := d.ListProfiles(ctx)
	if err != nil {
		return []string{err.Error()}
	}
	taken := map[string]bool{}
	for _, p := range existing {
		taken[p.Name] = true
	}
	for _, p := range profiles {
		if p.Name == "" {
			continue
		}
		if taken[p.Name] {
			errs = append(errs, msgs.Tc(ctx, "store.importProfileExists", p.Name))
			continue
		}
		id, err := d.CreateProfile(ctx, Profile{Name: p.Name, Color: p.Color, Content: p.Content, Note: p.Note, Author: p.Author})
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
			continue
		}
		// История — как была: CreateProfile записал редакцию «создан» с
		// текущим содержимым, но раз в файле есть своя история, она и
		// нужна, а не этот дубликат.
		if len(p.Versions) > 0 {
			if _, err := d.ExecContext(ctx, `DELETE FROM profile_versions WHERE profile_id = ?`, id); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
			}
		}
		for _, v := range p.Versions {
			if _, err := d.ExecContext(ctx, `
				INSERT INTO profile_versions (profile_id, ts, author, note, content)
				VALUES (?, ?, ?, ?, ?)`, id, v.TS, v.Author, v.Note, v.Content); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
				break
			}
		}
		taken[p.Name] = true
	}
	return errs
}

func (d *DB) importVMTemplates(ctx context.Context, templates []VMTemplateExport) (errs []string) {
	existing, err := d.ListVMTemplates(ctx)
	if err != nil {
		return []string{err.Error()}
	}
	taken := map[string]bool{}
	for _, t := range existing {
		taken[t.Name] = true
	}
	for _, t := range templates {
		if t.Name == "" {
			continue
		}
		if taken[t.Name] {
			errs = append(errs, msgs.Tc(ctx, "store.importTemplateExists", t.Name))
			continue
		}
		if _, err := d.SaveVMTemplate(ctx, VMTemplate{Name: t.Name, Spec: t.Spec, Author: t.Author}); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.Name, err))
		}
		taken[t.Name] = true
	}
	return errs
}

func (d *DB) importOneHost(ctx context.Context, h HostExport) (int64, error) {
	if h.Name == "" || h.Addr == "" {
		return 0, msgs.Errorf("store.emptyNameAddress")
	}
	if h.AdminUser != "" && !validAdminUser.MatchString(h.AdminUser) {
		return 0, msgs.Errorf("store.invalidAdminName", h.AdminUser)
	}
	res, err := d.ExecContext(ctx,
		`INSERT INTO hosts(
			name, addr, ssh_port, ssh_user, ssh_auth_kind, secret_enc,
			arch, status, nkt_version, admin_user, admin_password_enc,
			sudo_status, terminal_enabled, tunnel_enabled, tunnel_token_enc,
			error_msg, created_at, last_seen_at, group_name
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.Name, h.Addr, h.SSHPort, h.SSHUser, h.SSHAuthKind, h.SecretEnc,
		h.Arch, h.Status, h.NktVersion, h.AdminUser, h.AdminPasswordEnc,
		h.SudoStatus, h.TerminalEnabled, h.TunnelEnabled, h.TunnelTokenEnc,
		h.ErrorMsg, h.CreatedAt, h.LastSeenAt, h.Group)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DecodeHubExport parses an uploaded export file, rejecting one from an
// export format this build doesn't understand rather than silently
// importing a partial/misread result.
func DecodeHubExport(data []byte) (HubExport, error) {
	var export HubExport
	if err := json.Unmarshal(data, &export); err != nil {
		return HubExport{}, msgs.Errorf("store.fileDoesLookLikeHub", err)
	}
	if export.Version < minExportFormatVersion || export.Version > ExportFormatVersion {
		return HubExport{}, msgs.Errorf("store.exportFormatVersionSupportedExpected",
			export.Version, ExportFormatVersion)
	}
	return export, nil
}
