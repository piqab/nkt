package msgs

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Каждый ключ t('раздел.ключ') из кода интерфейса есть в обоих словарях
// (ru и en): иначе на экране — сам ключ, как было с «common.edit».
// Составные ключи (`раздел.${x}`) проверить нельзя — пропускаются.
func TestEveryUIKeyIsInBothDictionaries(t *testing.T) {
	root := filepath.Join("..", "..", "web", "src")
	if _, err := os.Stat(root); err != nil {
		t.Skip("web/src not found")
	}
	load := func(lang string) map[string]bool {
		raw, err := os.ReadFile(filepath.Join(root, "i18n", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var tree map[string]any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatal(err)
		}
		keys := map[string]bool{}
		var walk func(prefix string, m map[string]any)
		walk = func(prefix string, m map[string]any) {
			for k, v := range m {
				if sub, ok := v.(map[string]any); ok {
					walk(prefix+k+".", sub)
					continue
				}
				keys[prefix+k] = true
				for _, suf := range []string{"_one", "_few", "_many", "_other", "_zero"} {
					if strings.HasSuffix(k, suf) {
						keys[prefix+strings.TrimSuffix(k, suf)] = true
					}
				}
			}
		}
		walk("", tree)
		return keys
	}
	dicts := map[string]map[string]bool{"ru": load("ru"), "en": load("en")}
	re := regexp.MustCompile(`\bt\(\s*'([a-zA-Z0-9_.\-]+)'`)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".tsx") || strings.HasSuffix(p, ".ts")) {
			return nil
		}
		raw, _ := os.ReadFile(p)
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			k := m[1]
			if !strings.Contains(k, ".") || strings.HasSuffix(k, ".") {
				continue
			}
			for lang, keys := range dicts {
				if !keys[k] {
					t.Errorf("%s: %q missing in %s.json", filepath.Base(p), k, lang)
				}
			}
		}
		return nil
	})
}
