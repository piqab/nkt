package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/piqab/nkt/internal/api"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// ExportHosts returns the hub's host registry for backup/migration.
// includeKey additionally embeds this hub's own master key (base64) in the
// export — a one-time bridge, not something meant to be kept around: it
// lets ImportHosts on a *different* hub decrypt each secret and
// immediately re-encrypt it with ITS OWN key (see ImportHosts), so after
// import nothing about the two hubs' keys needs to match ever again.
// Off by default (same reasoning as every other opt-in gate this project
// adds for something more sensitive than its neighbours): while the key is
// embedded, anyone holding the file can decrypt every secret in it — treat
// it exactly like a credentials file (not a shared drive, not email,
// delete once imported), same as without a key, just without also having
// to separately move NKT_HUB_MASTER_KEY.
func (m *Manager) ExportHosts(ctx context.Context, includeKey bool) (store.HubExport, error) {
	return m.ExportHub(ctx, includeKey, false)
}

// ExportHub — ExportHosts с выбором: includeUsers добавляет учётные
// записи веб-интерфейса (с хэшами паролей — только по явному выбору).
func (m *Manager) ExportHub(ctx context.Context, includeKey, includeUsers bool) (store.HubExport, error) {
	export, err := m.db.ExportHosts(ctx)
	if err != nil {
		return store.HubExport{}, err
	}
	if includeKey {
		export.MasterKey = base64.StdEncoding.EncodeToString(m.key)
	}
	if includeUsers {
		if export.Users, err = m.db.ExportUsers(ctx); err != nil {
			return store.HubExport{}, err
		}
	}
	// Образы для кластеров — только список: файлы копируют руками, а
	// импорт скажет, каких не хватает.
	for _, img := range m.ClusterImages() {
		export.ClusterImages = append(export.ClusterImages, store.ClusterImageExport{Name: img.Name, Size: img.Size})
	}
	export.Edges = m.exportEdges(ctx)
	for i := range export.Edges {
		// Старому хабу — edge вебхуков, как раньше.
		if e := export.Edges[i]; len(e.Roles) == 0 || slices.Contains(e.Roles, EdgeRoleHooks) {
			export.Edge = &e
			break
		}
	}
	if export.F2BTemplates, err = m.exportF2BTemplates(ctx); err != nil {
		return store.HubExport{}, err
	}
	return export, nil
}

// exportEdges — все edge (хост — по имени).
func (m *Manager) exportEdges(ctx context.Context) []store.EdgeExport {
	var out []store.EdgeExport
	for _, st := range loadEdges(ctx, m.db) {
		if st.Address == "" && !st.Enabled {
			continue
		}
		e := store.EdgeExport{Enabled: st.Enabled, Address: st.Address, Domain: st.Domain, TokenEnc: st.TokenEnc,
			CertPEM: st.CertPEM, Fingerprint: st.Fingerprint, Roles: st.roles()}
		if st.HostID != 0 {
			if h, err := m.db.HostByID(ctx, st.HostID); err == nil {
				e.Host = h.Name
			}
		}
		out = append(out, e)
	}
	return out
}

// edgeExists — у хаба уже есть edge с этим адресом туннеля.
func (m *Manager) edgeExists(ctx context.Context, address string) bool {
	return slices.ContainsFunc(loadEdges(ctx, m.db), func(e EdgeSettings) bool { return e.Address == address })
}

// exportF2BTemplates — свои шаблоны fail2ban с историей версий (старые
// первыми).
func (m *Manager) exportF2BTemplates(ctx context.Context) ([]store.F2BTemplateExport, error) {
	raw, ok, err := m.db.KVGet(ctx, api.F2BTemplatesKey)
	if err != nil || !ok {
		return nil, err
	}
	var list []fail2ban.Template
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, nil
	}
	var out []store.F2BTemplateExport
	for _, t := range list {
		te := store.F2BTemplateExport{Name: t.Name, Description: t.Description, Jail: t.Jail, Filter: t.Filter}
		versions, err := m.db.ListVersions(ctx, api.F2BTemplatePrefix+t.Name, 500)
		if err != nil {
			return nil, err
		}
		for i := len(versions) - 1; i >= 0; i-- {
			v := versions[i]
			content, err := control.HistoryRead(m.cfg.HistoryDir(), v.BlobName)
			if err != nil {
				continue
			}
			te.Versions = append(te.Versions, store.ProfileVersionExport{TS: v.TS, Author: v.Author, Note: v.Note, Content: string(content)})
		}
		out = append(out, te)
	}
	return out, nil
}

// ImportPlan — разделы файла и совпадения с тем, что есть в этом хабе.
func (m *Manager) ImportPlan(ctx context.Context, export store.HubExport) ([]store.PlanSection, error) {
	plan, err := m.db.ImportPlan(ctx, export)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, t := range m.f2bTemplates(ctx) {
		taken[t.Name] = true
	}
	ps := store.PlanSection{Section: store.SectionF2BTemplates, Items: []store.PlanItem{}}
	for _, t := range export.F2BTemplates {
		ps.Items = append(ps.Items, store.PlanItem{Name: t.Name, Conflict: taken[t.Name], Replaceable: true})
	}
	plan = append(plan, ps)
	edge := store.PlanSection{Section: store.SectionEdge, Items: []store.PlanItem{}}
	for _, e := range export.EdgeList() {
		edge.Items = append(edge.Items, store.PlanItem{Name: e.Address, Conflict: m.edgeExists(ctx, e.Address), Replaceable: true})
	}
	plan = append(plan, edge)
	return plan, nil
}

func (m *Manager) f2bTemplates(ctx context.Context) []fail2ban.Template {
	var list []fail2ban.Template
	if raw, ok, err := m.db.KVGet(ctx, api.F2BTemplatesKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	return list
}

// ImportHosts adds every host in export to this hub's registry via
// store.ImportHosts (совпадения по имени — пропуск или замена по res).
// When export carries a master key (see ExportHosts), each secret is
// decrypted with THAT key and re-encrypted with this hub's own before
// being stored — an object whose secrets fail that step (a corrupted or
// mismatched embedded key) is dropped and reported rather than stored
// with ciphertext this hub could never decrypt. Without an embedded key,
// ciphertext passes through unexamined — only usable if this hub's key
// already matches whatever produced the export.
func (m *Manager) ImportHosts(ctx context.Context, export store.HubExport, res store.ImportResolutions) store.ImportReport {
	var pre []string
	if export.MasterKey != "" {
		oldKey, err := base64.StdEncoding.DecodeString(export.MasterKey)
		if err != nil {
			return store.ImportReport{Errors: []string{msgs.Tc(ctx, "hub.encryptionKeyFileCorrupted", err)}}
		}
		reenc := func(b []byte) ([]byte, error) {
			if len(b) == 0 {
				return b, nil
			}
			raw, err := secretbox.Decrypt(oldKey, b)
			if err != nil {
				return nil, err
			}
			return secretbox.Encrypt(m.key, raw)
		}

		// То, что будет пропущено (имя занято, «заменить» не выбрано), не
		// перешифровывается: его секреты не понадобятся, и ошибка
		// расшифровки там была бы шумом.
		skipped := func(section, name string, taken map[string]bool) bool {
			return taken[name] && !res.Replace(section, name)
		}
		hostNames, pipeNames := map[string]bool{}, map[string]bool{}
		if list, err := m.db.ListHosts(ctx); err == nil {
			for _, h := range list {
				hostNames[h.Name] = true
			}
		}
		if list, err := m.db.ListPipelines(ctx); err == nil {
			for _, p := range list {
				pipeNames[p.Name] = true
			}
		}

		ok := make([]store.HostExport, 0, len(export.Hosts))
		for _, h := range export.Hosts {
			if skipped(store.SectionHosts, h.Name, hostNames) {
				ok = append(ok, h)
				continue
			}
			r, err := reencryptHostSecrets(oldKey, m.key, h)
			if err != nil {
				pre = append(pre, fmt.Sprintf("%s (%s): %v", h.Name, h.Addr, err))
				continue
			}
			ok = append(ok, r)
		}
		export.Hosts = ok
		// Секреты кластеров — kubeconfig и план WireGuard — тем же ключом.
		okc := make([]store.ClusterExport, 0, len(export.Clusters))
		for _, c := range export.Clusters {
			r, err := reencryptClusterSecrets(oldKey, m.key, c)
			if err != nil {
				pre = append(pre, fmt.Sprintf("%s: %v", c.Name, err))
				continue
			}
			okc = append(okc, r)
		}
		export.Clusters = okc
		// Секреты конвейеров: подпись вебхука, git и registry.
		okp := make([]store.PipelineExport, 0, len(export.Pipelines))
		for _, p := range export.Pipelines {
			if skipped(store.SectionPipelines, p.Name, pipeNames) {
				okp = append(okp, p)
				continue
			}
			var e1, e2, e3, e4 error
			p.HookSecret, e1 = reenc(p.HookSecret)
			p.GitCred, e2 = reenc(p.GitCred)
			p.RegistryCred, e3 = reenc(p.RegistryCred)
			p.EnvEnc, e4 = reenc(p.EnvEnc)
			if err := errors.Join(e1, e2, e3, e4); err != nil {
				pre = append(pre, fmt.Sprintf("%s: %v", p.Name, err))
				continue
			}
			okp = append(okp, p)
		}
		export.Pipelines = okp
		// Файл вызывающего не меняется: всё перешифрованное — в копиях.
		if export.Settings != nil {
			settings := make(map[string]string, len(export.Settings))
			for k, v := range export.Settings {
				settings[k] = v
			}
			export.Settings = settings
		}
		var edges []store.EdgeExport
		for _, e := range export.EdgeList() {
			if m.edgeExists(ctx, e.Address) && !res.Replace(store.SectionEdge, e.Address) {
				edges = append(edges, e) // будет пропущен — секрет не нужен
				continue
			}
			t, err := reenc(e.TokenEnc)
			if err != nil {
				pre = append(pre, fmt.Sprintf("edge %s: %v", e.Address, err))
				continue
			}
			e.TokenEnc = t
			edges = append(edges, e)
		}
		export.Edges, export.Edge = edges, nil
		// Ключ модели — тем же мастер-ключом, что и секреты хостов.
		if enc := export.Settings[aiKeyKVKey]; enc != "" {
			raw, err := secretbox.Decrypt(oldKey, []byte(enc))
			if err != nil {
				pre = append(pre, fmt.Sprintf("%s: %v", aiKeyKVKey, err))
				delete(export.Settings, aiKeyKVKey)
			} else if r, err := secretbox.Encrypt(m.key, raw); err != nil {
				pre = append(pre, fmt.Sprintf("%s: %v", aiKeyKVKey, err))
				delete(export.Settings, aiKeyKVKey)
			} else {
				export.Settings[aiKeyKVKey] = string(r)
			}
		}
		export.MasterKey = "" // never persisted; the point of this whole path is to not need it again
	}

	rep := m.db.ImportHosts(ctx, export, res)
	rep.Errors = append(pre, rep.Errors...)
	m.importF2BTemplates(ctx, export.F2BTemplates, res, &rep)
	m.importEdges(ctx, export.EdgeList(), res, &rep)
	// Образы для кластеров: чего нет в библиотеке этого хаба.
	have := map[string]bool{}
	for _, img := range m.ClusterImages() {
		have[img.Name] = true
	}
	var missing []string
	for _, img := range export.ClusterImages {
		if !have[img.Name] {
			missing = append(missing, img.Name)
		}
	}
	if len(missing) > 0 {
		rep.Errors = append(rep.Errors, msgs.Tc(ctx, "hub.importImagesMissing", strings.Join(missing, ", "), m.clusterImagesDir()))
	}
	return rep
}

// importF2BTemplates — свои шаблоны fail2ban: новые — с историей из
// файла, заменённые — новой версией поверх своей истории.
func (m *Manager) importF2BTemplates(ctx context.Context, list []store.F2BTemplateExport, res store.ImportResolutions, rep *store.ImportReport) {
	if len(list) == 0 {
		return
	}
	current := m.f2bTemplates(ctx)
	idx := map[string]int{}
	for i, t := range current {
		idx[t.Name] = i
	}
	dir := m.cfg.HistoryDir()
	addVersion := func(name, ts, author, action, note, content string) {
		p := api.F2BTemplatePrefix + name
		blob, sum, err := control.HistoryWrite(dir, p, []byte(content))
		if err != nil {
			rep.Err("%s: %v", name, err)
			return
		}
		id, err := m.db.AddVersion(ctx, store.ConfigVersion{Path: p, Service: "fail2ban", Author: author, Action: action,
			Note: note, Size: int64(len(content)), SHA256: sum, BlobName: blob})
		if err == nil && ts != "" {
			_, _ = m.db.ExecContext(ctx, `UPDATE config_versions SET ts = ? WHERE id = ?`, ts, id)
		}
	}
	for _, t := range list {
		if !fail2ban.ValidTemplateName(t.Name) {
			continue
		}
		cnt := rep.Count(store.SectionF2BTemplates)
		tpl := fail2ban.Template{Name: t.Name, Description: t.Description, Jail: t.Jail, Filter: t.Filter}
		if i, ok := idx[t.Name]; ok {
			if !res.Replace(store.SectionF2BTemplates, t.Name) {
				cnt.Skipped++
				continue
			}
			current[i] = tpl
			addVersion(t.Name, "", "import", store.ActionEdit, msgs.Tc(ctx, "store.importReplacedNote"), api.F2BTemplateDoc(tpl))
			cnt.Replaced++
			continue
		}
		current = append(current, tpl)
		idx[t.Name] = len(current) - 1
		for _, v := range t.Versions {
			addVersion(t.Name, v.TS, v.Author, store.ActionEdit, v.Note, v.Content)
		}
		cnt.Added++
	}
	b, _ := json.Marshal(current)
	if err := m.db.KVSet(ctx, api.F2BTemplatesKey, string(b)); err != nil {
		rep.Err("%s: %v", api.F2BTemplatesKey, err)
	}
}

// importEdges — edge из файла; хост — по имени среди хостов хаба, edge
// с тем же адресом туннеля — замена по выбору.
func (m *Manager) importEdges(ctx context.Context, list []store.EdgeExport, res store.ImportResolutions, rep *store.ImportReport) {
	if len(list) == 0 {
		return
	}
	cnt := rep.Count(store.SectionEdge)
	hosts, _ := m.db.ListHosts(ctx)
	for _, e := range list {
		var existing EdgeSettings
		for _, cur := range loadEdges(ctx, m.db) {
			if cur.Address == e.Address {
				existing = cur
			}
		}
		if existing.ID != 0 && !res.Replace(store.SectionEdge, e.Address) {
			cnt.Skipped++
			continue
		}
		roles, ok := validRoles(e.Roles)
		if !ok {
			roles = []string{EdgeRoleHooks}
		}
		st := EdgeSettings{ID: existing.ID, Enabled: e.Enabled, Address: e.Address, Domain: e.Domain, TokenEnc: e.TokenEnc,
			CertPEM: e.CertPEM, Fingerprint: e.Fingerprint, Roles: roles}
		for _, h := range hosts {
			if e.Host != "" && h.Name == e.Host {
				st.HostID = h.ID
			}
		}
		if _, err := putEdgeDB(ctx, m.db, st); err != nil {
			rep.Err("edge %s: %v", e.Address, err)
			continue
		}
		if existing.ID != 0 {
			cnt.Replaced++
		} else {
			cnt.Added++
		}
	}
}

// reencryptClusterSecrets перешифровывает kubeconfig и план WireGuard
// кластера с ключа старого хаба на ключ этого.
func reencryptClusterSecrets(oldKey, newKey []byte, c store.ClusterExport) (store.ClusterExport, error) {
	for _, f := range []*[]byte{&c.KubeconfigEnc, &c.WGEnc} {
		if len(*f) == 0 {
			continue
		}
		raw, err := secretbox.Decrypt(oldKey, *f)
		if err != nil {
			return store.ClusterExport{}, msgs.Errorf("hub.decryptingClusterSecret", err)
		}
		enc, err := secretbox.Encrypt(newKey, raw)
		if err != nil {
			return store.ClusterExport{}, err
		}
		*f = enc
	}
	return c, nil
}

// reencryptHostSecrets decrypts h's secret_enc/admin_password_enc/
// tunnel_token_enc with oldKey and re-encrypts them with newKey, leaving
// every other field untouched. Missing TunnelTokenEnc here (added along
// with the field itself, but originally overlooked in this specific
// re-encryption step) meant a host imported via "экспорт с ключом" kept a
// tunnel token still encrypted under the *old* hub's key — the new hub's
// own secretbox.Decrypt(m.key, ...) call in tunnelDialOnce would then
// simply fail every time, so the reverse-tunnel session this token gates
// could never come up, and every fallback dial silently had nothing to
// fall back to.
func reencryptHostSecrets(oldKey, newKey []byte, h store.HostExport) (store.HostExport, error) {
	secret, err := secretbox.Decrypt(oldKey, h.SecretEnc)
	if err != nil {
		return store.HostExport{}, msgs.Errorf("hub.decryptingSSHSecretBuiltKey", err)
	}
	secretEnc, err := secretbox.Encrypt(newKey, secret)
	if err != nil {
		return store.HostExport{}, msgs.Errorf("hub.reEncryptingSSHSecret", err)
	}
	h.SecretEnc = secretEnc

	if len(h.AdminPasswordEnc) > 0 {
		pw, err := secretbox.Decrypt(oldKey, h.AdminPasswordEnc)
		if err != nil {
			return store.HostExport{}, msgs.Errorf("hub.decryptingAdminPasswordBuiltKey", err)
		}
		pwEnc, err := secretbox.Encrypt(newKey, pw)
		if err != nil {
			return store.HostExport{}, msgs.Errorf("hub.reEncryptingAdminPassword", err)
		}
		h.AdminPasswordEnc = pwEnc
	}

	if len(h.TunnelTokenEnc) > 0 {
		token, err := secretbox.Decrypt(oldKey, h.TunnelTokenEnc)
		if err != nil {
			return store.HostExport{}, msgs.Errorf("hub.decryptingFallbackChannelTokenBuilt", err)
		}
		tokenEnc, err := secretbox.Encrypt(newKey, token)
		if err != nil {
			return store.HostExport{}, msgs.Errorf("hub.reEncryptingFallbackChannelToken", err)
		}
		h.TunnelTokenEnc = tokenEnc
	}
	return h, nil
}
