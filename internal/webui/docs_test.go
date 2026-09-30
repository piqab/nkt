package webui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Разделы справки, на которые ссылается интерфейс (web/src/docsMap.json),
// должны существовать на сайте документации на обоих языках: переименовал
// раздел — поправь и карту, иначе кнопка «Справка» поведёт в никуда.
func TestDocsMapTargetsExist(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "docsMap.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]struct {
		Page string `json:"page"`
		RU   string `json:"ru"`
		EN   string `json:"en"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	headings := func(path string) map[string]bool {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("no docs page %s", path)
		}
		out := map[string]bool{}
		for _, l := range strings.Split(string(b), "\n") {
			for _, p := range []string{"## ", "### "} {
				if strings.HasPrefix(l, p) {
					out[strings.TrimSpace(strings.TrimPrefix(l, p))] = true
				}
			}
		}
		return out
	}
	for key, e := range m {
		ru := headings(filepath.Join("..", "..", "site", "guide", e.Page+".md"))
		en := headings(filepath.Join("..", "..", "site", "en", "guide", e.Page+".md"))
		if (e.RU == "") != (e.EN == "") {
			t.Errorf("%s: section given for one language only", key)
		}
		if e.RU != "" && !ru[e.RU] {
			t.Errorf("%s: no section %q in site/guide/%s.md", key, e.RU, e.Page)
		}
		if e.EN != "" && !en[e.EN] {
			t.Errorf("%s: no section %q in site/en/guide/%s.md", key, e.EN, e.Page)
		}
	}
}
