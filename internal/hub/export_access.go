package hub

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/piqab/nkt/internal/store"
)

// Экспорт и импорт доступа извне: API-токены, исходящие вебхуки, боты
// Telegram и Slack. Хосты в пределах — по именам (номера в другом хабе
// другие); хоста с таким именем нет — объект не импортируется: пустые
// пределы означали бы «все хосты», то есть больше, чем было.

const localHostName = "localhost"

// hostNames — номер → имя и обратно (машина хаба — localhost).
func (m *Manager) hostNames(ctx context.Context) (map[int64]string, map[string]int64) {
	byID := map[int64]string{localHostID: localHostName}
	byName := map[string]int64{localHostName: localHostID}
	if hosts, err := m.db.ListHosts(ctx); err == nil {
		for _, h := range hosts {
			byID[h.ID], byName[h.Name] = h.Name, h.ID
		}
	}
	return byID, byName
}

func idsToNames(ids []int64, byID map[int64]string) []string {
	var out []string
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
		}
	}
	return out
}

// namesToIDs — номера по именам; ok=false — какого-то хоста нет.
func namesToIDs(names []string, byName map[string]int64) ([]int64, string, bool) {
	out := []int64{}
	for _, n := range names {
		id, ok := byName[n]
		if !ok {
			return nil, n, false
		}
		out = append(out, id)
	}
	return out, "", true
}

// exportAccess — токены, вебхуки и боты для файла экспорта.
func (m *Manager) exportAccess(ctx context.Context, export *store.HubExport) error {
	byID, _ := m.hostNames(ctx)
	tokens, err := m.db.ListAPITokens(ctx)
	if err != nil {
		return err
	}
	for _, t := range tokens {
		export.APITokens = append(export.APITokens, store.APITokenExport{Name: t.Name, KeyID: t.KeyID, Role: t.Role,
			Hosts: idsToNames(t.Hosts, byID), Groups: t.Groups, IPs: t.IPs, ExpiresAt: t.ExpiresAt, ViaEdge: t.ViaEdge,
			Author: t.Author, SecretEnc: t.SecretEnc})
	}
	for _, h := range m.outHooks(ctx) {
		export.OutHooks = append(export.OutHooks, store.OutHookExport{Name: h.Name, URL: h.URL, Kinds: h.Kinds,
			Hosts: idsToNames(h.Hosts, byID), Groups: h.Groups, Lang: h.Lang, Enabled: h.Enabled, SecretEnc: h.SecretEnc})
	}
	for name, key := range map[string]string{"telegram": tgSettingsKey, "slack": slackSettingsKey} {
		if raw, ok, err := m.db.KVGet(ctx, key); err == nil && ok && raw != "" && raw != "{}" {
			if export.Bots == nil {
				export.Bots = map[string]json.RawMessage{}
			}
			export.Bots[name] = json.RawMessage(raw)
		}
	}
	return nil
}

// planAccess — разделы плана импорта.
func (m *Manager) planAccess(ctx context.Context, export store.HubExport) []store.PlanSection {
	tokens, _ := m.db.ListAPITokens(ctx)
	ts := store.PlanSection{Section: store.SectionAPITokens, Items: []store.PlanItem{}}
	for _, t := range export.APITokens {
		ts.Items = append(ts.Items, store.PlanItem{Name: t.Name, Replaceable: true,
			Conflict: slices.ContainsFunc(tokens, func(x store.APIToken) bool { return x.Name == t.Name })})
	}
	hooks := m.outHooks(ctx)
	ws := store.PlanSection{Section: store.SectionWebhooks, Items: []store.PlanItem{}}
	for _, h := range export.OutHooks {
		ws.Items = append(ws.Items, store.PlanItem{Name: h.Name, Replaceable: true,
			Conflict: slices.ContainsFunc(hooks, func(x OutHook) bool { return x.Name == h.Name })})
	}
	bs := store.PlanSection{Section: store.SectionBots, Items: []store.PlanItem{}}
	for _, name := range []string{"telegram", "slack"} {
		if _, ok := export.Bots[name]; ok {
			bs.Items = append(bs.Items, store.PlanItem{Name: name, Replaceable: true, Conflict: m.botConfigured(ctx, name)})
		}
	}
	return []store.PlanSection{ts, ws, bs}
}

func botKey(name string) string {
	if name == "slack" {
		return slackSettingsKey
	}
	return tgSettingsKey
}

// botConfigured — у бота этого хаба уже есть токен.
func (m *Manager) botConfigured(ctx context.Context, name string) bool {
	raw, ok, err := m.db.KVGet(ctx, botKey(name))
	if err != nil || !ok {
		return false
	}
	var st struct {
		TokenEnc []byte `json:"token_enc"`
	}
	return json.Unmarshal([]byte(raw), &st) == nil && len(st.TokenEnc) > 0
}

// reencryptAccess — секреты токенов, вебхуков и ботов — ключом этого
// хаба. Не вышло — объект выбрасывается с записью в отчёт.
func reencryptAccess(export *store.HubExport, reenc func([]byte) ([]byte, error), report func(string, error)) {
	tokens := export.APITokens[:0:0]
	for _, t := range export.APITokens {
		enc, err := reenc(t.SecretEnc)
		if err != nil {
			report("token "+t.Name, err)
			continue
		}
		t.SecretEnc = enc
		tokens = append(tokens, t)
	}
	export.APITokens = tokens
	hooks := export.OutHooks[:0:0]
	for _, h := range export.OutHooks {
		enc, err := reenc(h.SecretEnc)
		if err != nil {
			report("webhook "+h.Name, err)
			continue
		}
		h.SecretEnc = enc
		hooks = append(hooks, h)
	}
	export.OutHooks = hooks
	bots := map[string]json.RawMessage{}
	for name, raw := range export.Bots {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			report("bot "+name, err)
			continue
		}
		bad := false
		for _, f := range []string{"token_enc", "signing_enc"} {
			v, ok := fields[f]
			if !ok {
				continue
			}
			var b []byte
			if err := json.Unmarshal(v, &b); err != nil {
				bad = true
				break
			}
			enc, err := reenc(b)
			if err != nil {
				report("bot "+name, err)
				bad = true
				break
			}
			fields[f], _ = json.Marshal(enc)
		}
		if !bad {
			bots[name], _ = json.Marshal(fields)
		}
	}
	export.Bots = bots
}

// importAccess — токены, вебхуки и боты из файла.
func (m *Manager) importAccess(ctx context.Context, export store.HubExport, res store.ImportResolutions, rep *store.ImportReport) {
	_, byName := m.hostNames(ctx)
	if len(export.APITokens) > 0 {
		cnt := rep.Count(store.SectionAPITokens)
		current, _ := m.db.ListAPITokens(ctx)
		for _, e := range export.APITokens {
			hosts, missing, ok := namesToIDs(e.Hosts, byName)
			if !ok {
				rep.Err("token %q: host %q missing — not imported", e.Name, missing)
				continue
			}
			if e.Role != store.TokenRoleAdmin {
				e.Role = store.TokenRoleRead
			}
			t := store.APIToken{Name: e.Name, KeyID: e.KeyID, SecretEnc: e.SecretEnc, Role: e.Role, Hosts: hosts, Groups: e.Groups,
				IPs: e.IPs, ExpiresAt: e.ExpiresAt, ViaEdge: e.ViaEdge, Author: e.Author}
			i := slices.IndexFunc(current, func(x store.APIToken) bool { return x.Name == e.Name })
			if i >= 0 {
				if !res.Replace(store.SectionAPITokens, e.Name) {
					cnt.Skipped++
					continue
				}
				t.ID = current[i].ID
				err := m.db.UpdateAPIToken(ctx, t)
				if err == nil {
					err = m.db.SetAPITokenSecret(ctx, t.ID, t.KeyID, t.SecretEnc)
				}
				if err != nil {
					rep.Err("token %q: %v", e.Name, err)
					continue
				}
				cnt.Replaced++
				continue
			}
			if _, err := m.db.CreateAPIToken(ctx, t); err != nil {
				rep.Err("token %q: %v", e.Name, err)
				continue
			}
			cnt.Added++
		}
	}
	if len(export.OutHooks) > 0 {
		cnt := rep.Count(store.SectionWebhooks)
		outHooksMu.Lock()
		list := m.outHooks(ctx)
		for _, e := range export.OutHooks {
			hosts, missing, ok := namesToIDs(e.Hosts, byName)
			if !ok {
				rep.Err("webhook %q: host %q missing — not imported", e.Name, missing)
				continue
			}
			h := OutHook{Name: e.Name, URL: e.URL, Kinds: e.Kinds, Hosts: hosts, Groups: e.Groups, Lang: e.Lang, Enabled: e.Enabled,
				SecretEnc: e.SecretEnc, Author: "import", Created: store.Now()}
			if i := slices.IndexFunc(list, func(x OutHook) bool { return x.Name == e.Name }); i >= 0 {
				if !res.Replace(store.SectionWebhooks, e.Name) {
					cnt.Skipped++
					continue
				}
				h.ID = list[i].ID
				list[i] = h
				cnt.Replaced++
				continue
			}
			for _, x := range list {
				h.ID = max(h.ID, x.ID)
			}
			h.ID++
			list = append(list, h)
			cnt.Added++
		}
		if err := m.saveOutHooks(ctx, list); err != nil {
			rep.Err("webhooks: %v", err)
		}
		outHooksMu.Unlock()
	}
	for _, name := range []string{"telegram", "slack"} {
		raw, ok := export.Bots[name]
		if !ok {
			continue
		}
		cnt := rep.Count(store.SectionBots)
		if m.botConfigured(ctx, name) && !res.Replace(store.SectionBots, name) {
			cnt.Skipped++
			continue
		}
		existed := m.botConfigured(ctx, name)
		if err := m.db.KVSet(ctx, botKey(name), string(raw)); err != nil {
			rep.Err("bot %q: %v", name, err)
			continue
		}
		if name == "telegram" {
			_ = m.db.KVSet(ctx, tgOffsetKey, "0")
		}
		if existed {
			cnt.Replaced++
		} else {
			cnt.Added++
		}
	}
}
