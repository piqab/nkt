package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"regexp"
	"sort"
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
// шаблоны машин и настройки хаба; версия 3 — кластеры Kubernetes с
// узлами, способ связи и доставки у хостов, историю редакций сценариев,
// лимит кэша пакетов и список образов для кластеров; версия 4 —
// конвейеры выкладок с историей, учётные записи веб-интерфейса (по
// выбору), шаблоны fail2ban с историей, настройки nkt-edge и все
// инструкции ИИ. Файлы версий 1–3 читаются по-прежнему.
const ExportFormatVersion = 4

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
	AptViaHub        bool   `json:"apt_via_hub,omitempty"`
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
	// Версия 3: связь с машиной (via), итог пробы доставки (binary_via),
	// кластер и роль узла (по имени кластера).
	Via       string `json:"via,omitempty"`
	BinaryVia string `json:"binary_via,omitempty"`
	// SSHHostKey — запомненный ключ SSH хоста (base64), с v1.10.70.
	SSHHostKey string `json:"ssh_host_key,omitempty"`
	// APIPort — порт API nkt на хосте (0 — общий у хаба), с v1.10.79.
	APIPort int    `json:"api_port,omitempty"`
	Cluster string `json:"cluster,omitempty"`
	K8sRole string `json:"k8s_role,omitempty"`
}

// ClusterExport — кластер Kubernetes: запись с зашифрованными kubeconfig
// и планом WireGuard (читаются тем же мастер-ключом, что и секреты
// хостов); узлы — хосты файла с полем Cluster.
type ClusterExport struct {
	Name          string `json:"name"`
	Host          string `json:"host"`
	Flavor        string `json:"flavor"`
	Topology      string `json:"topology,omitempty"`
	Workers       int    `json:"workers"`
	Expose        bool   `json:"expose"`
	Status        string `json:"status"`
	ErrorMsg      string `json:"error_msg,omitempty"`
	ServerAddr    string `json:"server_addr,omitempty"`
	KubeconfigEnc []byte `json:"kubeconfig_enc,omitempty"`
	WGEnc         []byte `json:"wg_enc,omitempty"`
	SpecJSON      string `json:"spec_json"`
	CreatedAt     string `json:"created_at"`
}

// ClusterPresetExport — сохранённая форма кластера.
type ClusterPresetExport struct {
	Name   string `json:"name"`
	Form   string `json:"form"`
	Author string `json:"author,omitempty"`
}

// ClusterImageExport — образ из библиотеки хаба: в файле только список,
// сам файл копируют руками (см. hub.importImagesMissing).
type ClusterImageExport struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
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

// ScriptExport — сценарий хаба с историей редакций (версия 3; файлы
// версии 2 — без неё).
type ScriptExport struct {
	Name     string                 `json:"name"`
	Color    string                 `json:"color,omitempty"`
	Content  string                 `json:"content"`
	Note     string                 `json:"note,omitempty"`
	Author   string                 `json:"author,omitempty"`
	Versions []ProfileVersionExport `json:"versions,omitempty"`
}

// VMTemplateExport — шаблон машины.
type VMTemplateExport struct {
	Name   string `json:"name"`
	Spec   string `json:"spec"`
	Author string `json:"author,omitempty"`
}

// HubExport is the full document GET /hub/export hands back and POST
// /hub/import expects: всё, что долго заводить заново — хосты и доступ к
// ним, группы, профили, сценарии, кластеры, конвейеры, шаблоны,
// настройки; учётные записи веб-интерфейса — только по выбору при
// экспорте. Журналы (аудит, задания, выкладки, оповещения) не входят.
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
	Scripts       []ScriptExport     `json:"scripts,omitempty"`
	// Settings — настройки хаба из таблицы kv по ключу (настройки
	// оповещений, группа строки localhost, умолчания подготовки, лимит
	// кэша пакетов, разбор моделью, бета-канал).
	Settings map[string]string `json:"settings,omitempty"`
	// Clusters и ClusterImages — версия 3.
	Clusters       []ClusterExport       `json:"clusters,omitempty"`
	ClusterImages  []ClusterImageExport  `json:"cluster_images,omitempty"`
	ClusterPresets []ClusterPresetExport `json:"cluster_presets,omitempty"`
	// Версия 4: конвейеры выкладок с историей редакций (секреты —
	// зашифрованы мастер-ключом, как у хостов), учётные записи
	// веб-интерфейса (только по выбору при экспорте), шаблоны fail2ban с
	// историей и настройки nkt-edge.
	Pipelines    []PipelineExport    `json:"pipelines,omitempty"`
	Users        []UserExport        `json:"users,omitempty"`
	F2BTemplates []F2BTemplateExport `json:"f2b_templates,omitempty"`
	// Edge — до v1.11.71 edge был один (старые файлы и старые хабы);
	// Edges — все edge с ролями.
	Edge  *EdgeExport  `json:"edge,omitempty"`
	Edges []EdgeExport `json:"edges,omitempty"`
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
		TerminalEnabled: h.TerminalEnabled, AptViaHub: h.AptViaHub, TunnelEnabled: h.TunnelEnabled, TunnelTokenEnc: h.TunnelTokenEnc,
		ErrorMsg: h.ErrorMsg, CreatedAt: h.CreatedAt, LastSeenAt: h.LastSeenAt,
		Group: h.Group, Via: h.Via, BinaryVia: h.BinaryVia, K8sRole: h.K8sRole, SSHHostKey: h.SSHHostKey,
		APIPort: h.APIPort,
	}
}

// ExportedSettingKeys — какие ключи kv едут в экспорт. Перечислены явно:
// в kv лежит и то, что переносить нельзя (например, что уже показано
// пользователю).
var ExportedSettingKeys = []string{
	"hub.events.settings", "hub.localhost.group", "hub.bootstrap.defaults", "aptcache_max_gb",
	// Разбор моделью: настройки, ключ (перешифровывается при импорте с
	// мастер-ключом — см. hub.ImportHosts) и правленые инструкции.
	"ai.settings", "ai.api_key_enc",
	"ai.prompt.finding/ru", "ai.prompt.finding/en", "ai.prompt.map/ru", "ai.prompt.map/en",
	"ai.prompt.config/ru", "ai.prompt.config/en", "ai.prompt.ip/ru", "ai.prompt.ip/en",
	// Бета-канал обновлений.
	"update.beta",
}

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
	clusters, err := d.ListClusters(ctx)
	if err != nil {
		return HubExport{}, err
	}
	clusterNames := map[int64]string{}
	for _, c := range clusters {
		clusterNames[c.ID] = c.Name
		out.Clusters = append(out.Clusters, ClusterExport{Name: c.Name, Host: byID[c.HostID], Flavor: c.Flavor, Topology: c.Topology,
			Workers: c.Workers, Expose: c.Expose, Status: c.Status, ErrorMsg: c.ErrorMsg, ServerAddr: c.ServerAddr,
			KubeconfigEnc: c.KubeconfigEnc, WGEnc: c.WGEnc, SpecJSON: c.SpecJSON, CreatedAt: c.CreatedAt})
	}
	for i, h := range hosts {
		out.Hosts[i] = hostToExport(h)
		if h.ParentID != 0 {
			out.Hosts[i].Parent = byID[h.ParentID]
		}
		if h.ClusterID != 0 {
			out.Hosts[i].Cluster = clusterNames[h.ClusterID]
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
	scripts, err := d.ListScripts(ctx)
	if err != nil {
		return HubExport{}, err
	}
	for _, sc := range scripts {
		full, err := d.ScriptByID(ctx, sc.ID)
		if err != nil {
			return HubExport{}, err
		}
		se := ScriptExport{Name: full.Name, Color: full.Color, Content: full.Content, Note: full.Note, Author: full.Author}
		versions, err := d.ScriptVersions(ctx, sc.ID, 200)
		if err != nil {
			return HubExport{}, err
		}
		for i := len(versions) - 1; i >= 0; i-- {
			v, err := d.ScriptVersion(ctx, versions[i].ID)
			if err != nil {
				return HubExport{}, err
			}
			se.Versions = append(se.Versions, ProfileVersionExport{TS: v.TS, Author: v.Author, Note: v.Note, Content: v.Content})
		}
		out.Scripts = append(out.Scripts, se)
	}
	presets, err := d.ListClusterPresets(ctx)
	if err != nil {
		return HubExport{}, err
	}
	for _, p := range presets {
		out.ClusterPresets = append(out.ClusterPresets, ClusterPresetExport{Name: p.Name, Form: p.Form, Author: p.Author})
	}
	if out.Pipelines, err = d.ExportPipelines(ctx); err != nil {
		return HubExport{}, err
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

// Разделы файла экспорта — ключи плана импорта, выбора «заменить» и
// отчёта.
const (
	SectionHosts          = "hosts"
	SectionProfiles       = "profiles"
	SectionScripts        = "scripts"
	SectionVMTemplates    = "vm_templates"
	SectionClusters       = "clusters"
	SectionClusterPresets = "cluster_presets"
	SectionPipelines      = "pipelines"
	SectionUsers          = "users"
	SectionSettings       = "settings"
	SectionF2BTemplates   = "f2b_templates"
	SectionEdge           = "edge"
)

// ImportResolutions — что делать с тем, что в этом хабе уже есть под тем
// же именем: раздел → имя → "replace". Всё остальное пропускается —
// затирать без явного выбора импорт не должен.
type ImportResolutions map[string]map[string]string

// Replace — выбрано ли «заменить».
func (r ImportResolutions) Replace(section, name string) bool {
	return r != nil && r[section] != nil && r[section][name] == "replace"
}

// SectionCount — итог раздела.
type SectionCount struct {
	Added    int `json:"added"`
	Replaced int `json:"replaced"`
	Skipped  int `json:"skipped"`
}

// ImportReport — итог импорта по разделам и ошибки.
type ImportReport struct {
	Sections map[string]*SectionCount `json:"sections"`
	Errors   []string                 `json:"errors"`
}

// Count — счётчик раздела (заводится при первом обращении).
func (r *ImportReport) Count(section string) *SectionCount {
	if r.Sections == nil {
		r.Sections = map[string]*SectionCount{}
	}
	if r.Sections[section] == nil {
		r.Sections[section] = &SectionCount{}
	}
	return r.Sections[section]
}

// Err — добавить ошибку.
func (r *ImportReport) Err(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// PlanItem — объект файла и есть ли такой в этом хабе.
type PlanItem struct {
	Name     string `json:"name"`
	Conflict bool   `json:"conflict"`
	// Replaceable — можно ли заменить существующий (кластеры — только
	// пропуск: узлы и секреты кластера живут на хостах).
	Replaceable bool `json:"replaceable"`
}

// PlanSection — раздел плана.
type PlanSection struct {
	Section string     `json:"section"`
	Items   []PlanItem `json:"items"`
}

func planSection(section string, names []string, taken map[string]bool, replaceable bool) PlanSection {
	ps := PlanSection{Section: section, Items: []PlanItem{}}
	for _, n := range names {
		if n == "" {
			continue
		}
		ps.Items = append(ps.Items, PlanItem{Name: n, Conflict: taken[n], Replaceable: replaceable})
	}
	return ps
}

// ImportPlan — что есть в файле по разделам и что из этого уже есть в
// хабе (по имени): по нему окно импорта спрашивает «пропустить или
// заменить».
func (d *DB) ImportPlan(ctx context.Context, export HubExport) ([]PlanSection, error) {
	var out []PlanSection
	names := func(n int, f func(int) string) []string {
		l := make([]string, n)
		for i := range l {
			l[i] = f(i)
		}
		return l
	}
	hosts, err := d.ListHosts(ctx)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, h := range hosts {
		taken[h.Name] = true
	}
	out = append(out, planSection(SectionHosts, names(len(export.Hosts), func(i int) string { return export.Hosts[i].Name }), taken, true))

	taken = map[string]bool{}
	if list, err := d.ListProfiles(ctx); err == nil {
		for _, p := range list {
			taken[p.Name] = true
		}
	}
	out = append(out, planSection(SectionProfiles, names(len(export.Profiles), func(i int) string { return export.Profiles[i].Name }), taken, true))

	taken = map[string]bool{}
	if list, err := d.ListScripts(ctx); err == nil {
		for _, p := range list {
			taken[p.Name] = true
		}
	}
	out = append(out, planSection(SectionScripts, names(len(export.Scripts), func(i int) string { return export.Scripts[i].Name }), taken, true))

	taken = map[string]bool{}
	if list, err := d.ListVMTemplates(ctx); err == nil {
		for _, p := range list {
			taken[p.Name] = true
		}
	}
	out = append(out, planSection(SectionVMTemplates, names(len(export.VMTemplates), func(i int) string { return export.VMTemplates[i].Name }), taken, true))

	taken = map[string]bool{}
	if list, err := d.ListClusters(ctx); err == nil {
		for _, p := range list {
			taken[p.Name] = true
		}
	}
	out = append(out, planSection(SectionClusters, names(len(export.Clusters), func(i int) string { return export.Clusters[i].Name }), taken, false))

	taken = map[string]bool{}
	if list, err := d.ListClusterPresets(ctx); err == nil {
		for _, p := range list {
			taken[p.Name] = true
		}
	}
	out = append(out, planSection(SectionClusterPresets, names(len(export.ClusterPresets), func(i int) string { return export.ClusterPresets[i].Name }), taken, true))

	taken = map[string]bool{}
	if list, err := d.ListPipelines(ctx); err == nil {
		for _, p := range list {
			taken[p.Name] = true
		}
	}
	out = append(out, planSection(SectionPipelines, names(len(export.Pipelines), func(i int) string { return export.Pipelines[i].Name }), taken, true))

	taken = map[string]bool{}
	if list, err := d.ListUsers(ctx); err == nil {
		for _, u := range list {
			taken[u.Username] = true
		}
	}
	out = append(out, planSection(SectionUsers, names(len(export.Users), func(i int) string { return export.Users[i].Username }), taken, true))

	taken = map[string]bool{}
	var keys []string
	for key := range export.Settings {
		if !exportedSettingKey(key) {
			continue
		}
		keys = append(keys, key)
		if _, ok, err := d.KVGet(ctx, key); err == nil && ok {
			taken[key] = true
		}
	}
	sort.Strings(keys)
	out = append(out, planSection(SectionSettings, keys, taken, true))
	return out, nil
}

// ImportHosts переносит файл в этот хаб. Совпадения по имени по
// умолчанию пропускаются; res выбирает «заменить» поштучно. Каждый объект
// — отдельно: одна испорченная запись не останавливает остальные, её
// ошибка — в отчёте.
//
// Группы заводятся (существующие не трогаются), родитель машины и узлы
// кластеров находятся по имени среди хостов файла (добавленных и
// заменённых).
func (d *DB) ImportHosts(ctx context.Context, export HubExport, res ImportResolutions) ImportReport {
	var rep ImportReport
	for _, g := range export.Groups {
		if g == "" {
			continue
		}
		if err := d.CreateHostGroup(ctx, g); err != nil {
			rep.Err("%s: %v", g, err)
		}
	}
	existing := map[string]int64{}
	if list, err := d.ListHosts(ctx); err == nil {
		for _, h := range list {
			existing[h.Name] = h.ID
		}
	}
	ids := map[string]int64{}
	for _, h := range export.Hosts {
		cnt := rep.Count(SectionHosts)
		if oldID, ok := existing[h.Name]; ok && h.Name != "" {
			if !res.Replace(SectionHosts, h.Name) {
				cnt.Skipped++
				continue
			}
			if err := d.replaceHost(ctx, oldID, h); err != nil {
				rep.Err("%s (%s): %v", h.Name, h.Addr, err)
				continue
			}
			ids[h.Name] = oldID
			cnt.Replaced++
			continue
		}
		id, err := d.importOneHost(ctx, h)
		if err != nil {
			rep.Err("%s (%s): %v", h.Name, h.Addr, err)
			continue
		}
		ids[h.Name] = id
		existing[h.Name] = id
		cnt.Added++
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
			parentID, ok = existing[h.Parent]
		}
		if !ok {
			rep.Errors = append(rep.Errors, msgs.Tc(ctx, "store.importParentMissing", h.Name, h.Parent))
			continue
		}
		if err := d.SetHostParent(ctx, id, parentID); err != nil {
			rep.Err("%s: %v", h.Name, err)
		}
	}
	d.importClusters(ctx, export, ids, &rep)
	d.importProfiles(ctx, export.Profiles, res, &rep)
	d.importVMTemplates(ctx, export.VMTemplates, res, &rep)
	d.importScripts(ctx, export.Scripts, res, &rep)
	d.importClusterPresets(ctx, export.ClusterPresets, res, &rep)
	d.importPipelines(ctx, export.Pipelines, res, &rep)
	d.importUsers(ctx, export.Users, res, &rep)
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
			rep.Errors = append(rep.Errors, err.Error())
		} else {
			byName := map[string]int64{}
			for _, p := range all {
				byName[p.Name] = p.ID
			}
			for group, name := range export.GroupProfiles {
				id, ok := byName[name]
				if !ok {
					rep.Errors = append(rep.Errors, msgs.Tc(ctx, "store.importGroupProfileMissing", group, name))
					continue
				}
				if _, err := d.ExecContext(ctx, `UPDATE host_groups SET profile_id = ? WHERE name = ? AND profile_id = 0`, id, group); err != nil {
					rep.Err("%s: %v", group, err)
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
	keys := make([]string, 0, len(export.Settings))
	for key := range export.Settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := export.Settings[key]
		if !exportedSettingKey(key) {
			continue
		}
		cnt := rep.Count(SectionSettings)
		_, ok, err := d.KVGet(ctx, key)
		if err != nil {
			rep.Err("%s: %v", key, err)
			continue
		}
		if ok && !res.Replace(SectionSettings, key) {
			cnt.Skipped++
			continue
		}
		if err := d.KVSet(ctx, key, value); err != nil {
			rep.Err("%s: %v", key, err)
			continue
		}
		if ok {
			cnt.Replaced++
		} else {
			cnt.Added++
		}
	}
	return rep
}

func exportedSettingKey(key string) bool {
	for _, k := range ExportedSettingKeys {
		if k == key {
			return true
		}
	}
	return false
}

// importNote — пометка редакции, заменённой импортом.
func importNote(ctx context.Context) string { return msgs.Tc(ctx, "store.importReplacedNote") }

func (d *DB) importProfiles(ctx context.Context, profiles []ProfileExport, res ImportResolutions, rep *ImportReport) {
	existing, err := d.ListProfiles(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return
	}
	taken := map[string]int64{}
	for _, p := range existing {
		taken[p.Name] = p.ID
	}
	for _, p := range profiles {
		if p.Name == "" {
			continue
		}
		cnt := rep.Count(SectionProfiles)
		if oldID, ok := taken[p.Name]; ok {
			if !res.Replace(SectionProfiles, p.Name) {
				cnt.Skipped++
				continue
			}
			// Замена — новой редакцией: своя история этого хаба остаётся,
			// откатиться к прежнему можно.
			if err := d.UpdateProfile(ctx, Profile{ID: oldID, Name: p.Name, Color: p.Color, Content: p.Content, Note: importNote(ctx), Author: p.Author}); err != nil {
				rep.Err("%s: %v", p.Name, err)
				continue
			}
			cnt.Replaced++
			continue
		}
		id, err := d.CreateProfile(ctx, Profile{Name: p.Name, Color: p.Color, Content: p.Content, Note: p.Note, Author: p.Author})
		if err != nil {
			rep.Err("%s: %v", p.Name, err)
			continue
		}
		// История — как была: CreateProfile записал редакцию «создан» с
		// текущим содержимым, но раз в файле есть своя история, она и
		// нужна, а не этот дубликат.
		if len(p.Versions) > 0 {
			if _, err := d.ExecContext(ctx, `DELETE FROM profile_versions WHERE profile_id = ?`, id); err != nil {
				rep.Err("%s: %v", p.Name, err)
			}
		}
		for _, v := range p.Versions {
			if _, err := d.ExecContext(ctx, `
				INSERT INTO profile_versions (profile_id, ts, author, note, content)
				VALUES (?, ?, ?, ?, ?)`, id, v.TS, v.Author, v.Note, v.Content); err != nil {
				rep.Err("%s: %v", p.Name, err)
				break
			}
		}
		taken[p.Name] = id
		cnt.Added++
	}
}

func (d *DB) importScripts(ctx context.Context, scripts []ScriptExport, res ImportResolutions, rep *ImportReport) {
	existing, err := d.ListScripts(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return
	}
	taken := map[string]int64{}
	for _, s := range existing {
		taken[s.Name] = s.ID
	}
	for _, s := range scripts {
		if s.Name == "" {
			continue
		}
		cnt := rep.Count(SectionScripts)
		if oldID, ok := taken[s.Name]; ok {
			if !res.Replace(SectionScripts, s.Name) {
				cnt.Skipped++
				continue
			}
			if err := d.UpdateScript(ctx, Script{ID: oldID, Name: s.Name, Color: s.Color, Content: s.Content, Note: importNote(ctx), Author: s.Author}); err != nil {
				rep.Err("%s: %v", s.Name, err)
				continue
			}
			cnt.Replaced++
			continue
		}
		id, err := d.CreateScript(ctx, Script{Name: s.Name, Color: s.Color, Content: s.Content, Note: s.Note, Author: s.Author})
		if err != nil {
			rep.Err("%s: %v", s.Name, err)
			continue
		}
		// История редакций из файла заменяет единственную «создан».
		if len(s.Versions) > 0 {
			if _, err := d.ExecContext(ctx, `DELETE FROM script_versions WHERE script_id = ?`, id); err != nil {
				rep.Err("%s: %v", s.Name, err)
			}
			for _, v := range s.Versions {
				if _, err := d.ExecContext(ctx, `
					INSERT INTO script_versions (script_id, ts, author, note, content)
					VALUES (?, ?, ?, ?, ?)`, id, v.TS, v.Author, v.Note, v.Content); err != nil {
					rep.Err("%s: %v", s.Name, err)
					break
				}
			}
		}
		taken[s.Name] = id
		cnt.Added++
	}
}

// importClusters заводит кластеры файла (существующее имя — пропуск) и
// привязывает к ним узлы среди хостов файла.
func (d *DB) importClusters(ctx context.Context, export HubExport, ids map[string]int64, rep *ImportReport) {
	if len(export.Clusters) == 0 {
		return
	}
	existing, err := d.ListClusters(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return
	}
	taken := map[string]bool{}
	for _, c := range existing {
		taken[c.Name] = true
	}
	clusterIDs := map[string]int64{}
	for _, c := range export.Clusters {
		if c.Name == "" {
			continue
		}
		cnt := rep.Count(SectionClusters)
		if taken[c.Name] {
			cnt.Skipped++
			continue
		}
		hostID, ok := ids[c.Host]
		if !ok {
			rep.Errors = append(rep.Errors, msgs.Tc(ctx, "store.importClusterHostMissing", c.Name, c.Host))
			continue
		}
		res, err := d.ExecContext(ctx, `INSERT INTO clusters(name, host_id, flavor, topology, workers, expose, status, error_msg, server_addr, kubeconfig_enc, spec_json, wg_enc, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.Name, hostID, c.Flavor, c.Topology, c.Workers, c.Expose, c.Status, c.ErrorMsg, c.ServerAddr, c.KubeconfigEnc, c.SpecJSON, c.WGEnc, c.CreatedAt, Now())
		if err != nil {
			rep.Err("%s: %v", c.Name, err)
			continue
		}
		id, _ := res.LastInsertId()
		clusterIDs[c.Name] = id
		taken[c.Name] = true
		cnt.Added++
	}
	for _, h := range export.Hosts {
		if h.Cluster == "" {
			continue
		}
		hostID, ok := ids[h.Name]
		cid, ok2 := clusterIDs[h.Cluster]
		if !ok || !ok2 {
			continue
		}
		if err := d.SetHostCluster(ctx, hostID, cid, h.K8sRole); err != nil {
			rep.Err("%s: %v", h.Name, err)
		}
	}
}

func (d *DB) importClusterPresets(ctx context.Context, presets []ClusterPresetExport, res ImportResolutions, rep *ImportReport) {
	existing, err := d.ListClusterPresets(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return
	}
	taken := map[string]bool{}
	for _, p := range existing {
		taken[p.Name] = true
	}
	for _, p := range presets {
		if p.Name == "" {
			continue
		}
		cnt := rep.Count(SectionClusterPresets)
		replace := taken[p.Name]
		if replace && !res.Replace(SectionClusterPresets, p.Name) {
			cnt.Skipped++
			continue
		}
		if _, err := d.SaveClusterPreset(ctx, ClusterPreset{Name: p.Name, Form: p.Form, Author: p.Author}); err != nil {
			rep.Err("%s: %v", p.Name, err)
			continue
		}
		if replace {
			cnt.Replaced++
		} else {
			cnt.Added++
		}
		taken[p.Name] = true
	}
}

func (d *DB) importVMTemplates(ctx context.Context, templates []VMTemplateExport, res ImportResolutions, rep *ImportReport) {
	existing, err := d.ListVMTemplates(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return
	}
	taken := map[string]bool{}
	for _, t := range existing {
		taken[t.Name] = true
	}
	for _, t := range templates {
		if t.Name == "" {
			continue
		}
		cnt := rep.Count(SectionVMTemplates)
		replace := taken[t.Name]
		if replace && !res.Replace(SectionVMTemplates, t.Name) {
			cnt.Skipped++
			continue
		}
		if _, err := d.SaveVMTemplate(ctx, VMTemplate{Name: t.Name, Spec: t.Spec, Author: t.Author}); err != nil {
			rep.Err("%s: %v", t.Name, err)
			continue
		}
		if replace {
			cnt.Replaced++
		} else {
			cnt.Added++
		}
		taken[t.Name] = true
	}
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
			error_msg, created_at, last_seen_at, group_name, apt_via_hub,
			via, binary_via, ssh_host_key, api_port
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.Name, h.Addr, h.SSHPort, h.SSHUser, h.SSHAuthKind, h.SecretEnc,
		h.Arch, h.Status, h.NktVersion, h.AdminUser, h.AdminPasswordEnc,
		h.SudoStatus, h.TerminalEnabled, h.TunnelEnabled, h.TunnelTokenEnc,
		h.ErrorMsg, h.CreatedAt, h.LastSeenAt, h.Group, h.AptViaHub,
		h.Via, h.BinaryVia, h.SSHHostKey, h.APIPort)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// replaceHost переписывает существующий хост данными из файла: адрес,
// доступ и секреты, группа, способ связи. Идентификатор, журнал и
// привязки этого хаба остаются.
func (d *DB) replaceHost(ctx context.Context, id int64, h HostExport) error {
	if h.Addr == "" {
		return msgs.Errorf("store.emptyNameAddress")
	}
	if h.AdminUser != "" && !validAdminUser.MatchString(h.AdminUser) {
		return msgs.Errorf("store.invalidAdminName", h.AdminUser)
	}
	_, err := d.ExecContext(ctx,
		`UPDATE hosts SET
			addr = ?, ssh_port = ?, ssh_user = ?, ssh_auth_kind = ?, secret_enc = ?,
			arch = ?, status = ?, nkt_version = ?, admin_user = ?, admin_password_enc = ?,
			sudo_status = ?, terminal_enabled = ?, tunnel_enabled = ?, tunnel_token_enc = ?,
			error_msg = ?, group_name = ?, apt_via_hub = ?, via = ?, binary_via = ?,
			ssh_host_key = ?, api_port = ?
		WHERE id = ?`,
		h.Addr, h.SSHPort, h.SSHUser, h.SSHAuthKind, h.SecretEnc,
		h.Arch, h.Status, h.NktVersion, h.AdminUser, h.AdminPasswordEnc,
		h.SudoStatus, h.TerminalEnabled, h.TunnelEnabled, h.TunnelTokenEnc,
		h.ErrorMsg, h.Group, h.AptViaHub, h.Via, h.BinaryVia,
		h.SSHHostKey, h.APIPort, id)
	return err
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
