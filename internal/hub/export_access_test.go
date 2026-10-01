package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Токены, исходящие вебхуки и боты — с хаба на хаб с другим ключом:
// секреты перешифрованы, хосты сопоставлены по именам; токен, чьего хоста
// нет, не импортируется (иначе пределы расширились бы до «всех хостов»).
func TestExportImportAccess(t *testing.T) {
	m1, db1 := newTestManager(t)
	m2, db2 := newTestManager(t)
	m1.cfg.DataDir, m2.cfg.DataDir = t.TempDir(), t.TempDir()
	ctx := context.Background()

	web1, _ := m1.AddHost(ctx, "web1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	db1Only, _ := m1.AddHost(ctx, "db-only", "10.0.0.2", 22, "root", store.HostAuthPassword, "pw", false)
	// В хабе-получателе web1 есть (с другим номером), db-only — нет.
	_, _ = m2.AddHost(ctx, "other", "10.0.1.1", 22, "root", store.HostAuthPassword, "pw", false)
	web1New, _ := m2.AddHost(ctx, "web1", "10.0.1.2", 22, "root", store.HostAuthPassword, "pw", false)

	enc := func(s string) []byte { b, _ := secretbox.Encrypt(m1.key, []byte(s)); return b }
	mustToken := func(tok store.APIToken) {
		if _, err := db1.CreateAPIToken(ctx, tok); err != nil {
			t.Fatal(err)
		}
	}
	mustToken(store.APIToken{Name: "n8n", KeyID: "aaaaaaaaaaaaaaaa", SecretEnc: enc("tok-secret"), Role: store.TokenRoleAdmin,
		Hosts: []int64{web1, localHostID}, Groups: []string{"prod"}, ViaEdge: true})
	mustToken(store.APIToken{Name: "db", KeyID: "bbbbbbbbbbbbbbbb", SecretEnc: enc("x"), Role: store.TokenRoleRead, Hosts: []int64{db1Only}})
	hooks := []OutHook{{ID: 1, Name: "chat", URL: "https://chat.example.com/h", Kinds: []string{"unreachable"}, Hosts: []int64{web1}, Lang: "en", Enabled: true, SecretEnc: enc("hook-secret")}}
	if err := m1.saveOutHooks(ctx, hooks); err != nil {
		t.Fatal(err)
	}
	tg, _ := json.Marshal(TelegramSettings{Enabled: true, TokenEnc: enc("123:tg-token"), BotName: "nkt_bot", Chats: []TelegramChat{{ID: 5, Role: "admin", Notify: true}}})
	_ = db1.KVSet(ctx, tgSettingsKey, string(tg))
	sl, _ := json.Marshal(SlackSettings{Enabled: true, TokenEnc: enc("xoxb-slack"), SigningEnc: enc("0123456789abcdef0123456789abcdef"), Team: "acme"})
	_ = db1.KVSet(ctx, slackSettingsKey, string(sl))

	export, err := m1.ExportHub(ctx, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(export.APITokens) != 2 || len(export.OutHooks) != 1 || len(export.Bots) != 2 {
		t.Fatalf("export: tokens %d hooks %d bots %d", len(export.APITokens), len(export.OutHooks), len(export.Bots))
	}
	if got := strings.Join(export.APITokens[1].Hosts, ","); got != "web1,localhost" && strings.Join(export.APITokens[0].Hosts, ",") != "web1,localhost" {
		t.Fatalf("host names: %+v", export.APITokens)
	}
	raw, _ := json.Marshal(export)
	decoded, err := store.DecodeHubExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Хост db-only в файл не попал (скажем, экспорт выборочный) — токена
	// с ним в пределах импорт не заводит.
	var keep []store.HostExport
	for _, h := range decoded.Hosts {
		if h.Name != "db-only" {
			keep = append(keep, h)
		}
	}
	decoded.Hosts = keep
	plan, _ := m2.ImportPlan(ctx, decoded)
	sections := map[string]int{}
	for _, ps := range plan {
		sections[ps.Section] = len(ps.Items)
	}
	if sections[store.SectionAPITokens] != 2 || sections[store.SectionWebhooks] != 1 || sections[store.SectionBots] != 2 {
		t.Fatalf("plan: %v", sections)
	}

	rep := m2.ImportHosts(ctx, decoded, nil)
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "db-only") {
		t.Fatalf("errors: %v", rep.Errors)
	}
	tok, err := db2.APITokenByKeyID(ctx, "aaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(tok.Hosts) != 2 || tok.Hosts[0] != web1New || tok.Hosts[1] != localHostID || !tok.ViaEdge || tok.Role != store.TokenRoleAdmin {
		t.Fatalf("token: %+v", tok)
	}
	if got, err := secretbox.Decrypt(m2.key, tok.SecretEnc); err != nil || string(got) != "tok-secret" {
		t.Fatalf("token secret: %q %v", got, err)
	}
	if _, err := db2.APITokenByKeyID(ctx, "bbbbbbbbbbbbbbbb"); err == nil {
		t.Fatal("token with a missing host imported")
	}
	got := m2.outHooks(ctx)
	if len(got) != 1 || len(got[0].Hosts) != 1 || got[0].Hosts[0] != web1New {
		t.Fatalf("hooks: %+v", got)
	}
	if s, err := secretbox.Decrypt(m2.key, got[0].SecretEnc); err != nil || string(s) != "hook-secret" {
		t.Fatalf("hook secret: %q %v", s, err)
	}
	var tg2 TelegramSettings
	rawTG, _, _ := db2.KVGet(ctx, tgSettingsKey)
	_ = json.Unmarshal([]byte(rawTG), &tg2)
	if s, err := secretbox.Decrypt(m2.key, tg2.TokenEnc); err != nil || string(s) != "123:tg-token" || len(tg2.Chats) != 1 {
		t.Fatalf("telegram: %+v %q %v", tg2, s, err)
	}
	var sl2 SlackSettings
	rawSL, _, _ := db2.KVGet(ctx, slackSettingsKey)
	_ = json.Unmarshal([]byte(rawSL), &sl2)
	if s, err := secretbox.Decrypt(m2.key, sl2.SigningEnc); err != nil || len(s) != 32 {
		t.Fatalf("slack signing: %v", err)
	}

	// Повторный импорт: без выбора — пропуск, с «заменить» — замена.
	rep = m2.ImportHosts(ctx, decoded, nil)
	if c := rep.Sections[store.SectionAPITokens]; c == nil || c.Skipped != 1 {
		t.Fatalf("second import: %+v", rep.Sections[store.SectionAPITokens])
	}
	rep = m2.ImportHosts(ctx, decoded, store.ImportResolutions{store.SectionWebhooks: {"chat": "replace"}, store.SectionBots: {"telegram": "replace"}})
	if c := rep.Sections[store.SectionWebhooks]; c == nil || c.Replaced != 1 {
		t.Fatalf("replace hooks: %+v", rep.Sections[store.SectionWebhooks])
	}
	if c := rep.Sections[store.SectionBots]; c == nil || c.Replaced != 1 || c.Skipped != 1 {
		t.Fatalf("bots: %+v", rep.Sections[store.SectionBots])
	}
}
