package hub

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/ai"
	"github.com/piqab/nkt/internal/store"
)

// Каждый ключ kv, который пишет хаб, либо уезжает в экспорт (списком
// настроек или своим разделом), либо явно не переносится — с причиной.
// Новый ключ, забытый в экспорте, роняет тест: так экспорт не отстаёт.
func TestEveryHubKVKeyIsExportedOrExplained(t *testing.T) {
	// Переносятся своими разделами файла экспорта.
	bySection := map[string]string{
		"hub.fly.doc": "Traffic Fly", "hub.fly.versions": "Traffic Fly", "migrations": "переносы стеков",
		"hub.edge": "nkt-edge", "hub.edges": "nkt-edge", "hub.telegram": "боты", "hub.slack": "боты",
		"hub.webhooks.out": "исходящие вебхуки", "pipeline.": "конвейеры: где выложен стек, оставшиеся",
		"deploy.stopped.": "конвейеры: метка «остановлена»", "assistant.favorites.": "учётные записи: избранное ассистента",
	}
	// Не переносятся.
	notExported := map[string]string{
		"hub.fly.applied":     "что применено на шлюзах — после импорта применится заново",
		"hub.events.seen":     "что уже показано в оповещениях — у каждого хаба своё",
		"hub.instance_id":     "id экземпляра хаба — у каждого свой",
		"hub.telegram.offset": "смещение очереди бота — у каждого хаба своё",
		"mon.k8s.":            "кэш мониторинга кластеров — соберётся опросом",
		"plugin.doc.":         "история правок документов плагина — сами документы живут в плагине",
	}
	settings := map[string]bool{}
	for _, k := range store.ExportedSettingKeys {
		settings[k] = true
	}
	// Ключи по шаблону: инструкции моделей и раскладка меню — все виды.
	for _, pk := range ai.PromptKinds {
		for _, l := range []string{"ru", "en"} {
			if !settings[aiPromptKVKey(pk+"/"+l)] {
				t.Errorf("prompt %s/%s is not exported", pk, l)
			}
		}
	}
	for _, nk := range NavKinds {
		if !settings["ui.nav."+nk] || !settings["ui.nav."+nk+".history"] {
			t.Errorf("nav %s is not exported", nk)
		}
	}
	templated := []string{"ai.prompt.", "ui.nav."}

	lit := regexp.MustCompile(`KVSet\(\s*ctx[^,]*,\s*"([^"]+)"`)
	cons := regexp.MustCompile(`(?m)^\s*(?:const\s+)?\w*(?:Key|KVKey)\w*\s*=\s*"([a-z][a-z0-9_./-]*)"`)
	fn := regexp.MustCompile(`func \w*[Kk]ey\([^)]*\) string \{\s*return "([a-z][a-z0-9_./-]*)"`)
	files, _ := filepath.Glob("*.go")
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, re := range []*regexp.Regexp{lit, cons, fn} {
			for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
				keys = append(keys, m[1])
			}
		}
		for _, k := range keys {
			found++
			switch {
			case settings[k], bySection[k] != "", notExported[k] != "":
			case slices.ContainsFunc(templated, func(p string) bool { return k == p }):
			default:
				t.Errorf("%s: kv key %q is neither exported nor explained (store.ExportedSettingKeys or the lists in this test)", f, k)
			}
		}
	}
	if found < 20 {
		t.Fatalf("found only %d keys — the scan is broken", found)
	}
}
