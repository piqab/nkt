package profile

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
)

// Страница «Формат профиля» на сайте совпадает с тем, что собирает код;
// NKT_UPDATE_DOCS=1 — пересобрать.
func TestProfileFormatPage(t *testing.T) {
	cyr := regexp.MustCompile(`[А-Яа-яЁё]`)
	for lang, file := range map[msgs.Lang]string{
		msgs.RU: filepath.Join("..", "..", "site", "guide", "profile-format.md"),
		msgs.EN: filepath.Join("..", "..", "site", "en", "guide", "profile-format.md"),
	} {
		want := DocsMarkdown(lang)
		if lang == msgs.EN && cyr.MatchString(want) {
			t.Errorf("en page has Cyrillic: %q", cyr.FindString(want))
		}
		if os.Getenv("NKT_UPDATE_DOCS") == "1" {
			if err := os.WriteFile(file, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(file)
		if err != nil || string(got) != want {
			t.Errorf("%s is stale: NKT_UPDATE_DOCS=1 go test ./internal/profile -run TestProfileFormatPage", file)
		}
	}
}

// Примеры справки и готовые профили сохраняются: каждый проходит Parse.
func TestDocExamplesParse(t *testing.T) {
	for _, s := range Sections {
		if _, err := Parse([]byte(s.Example)); err != nil {
			t.Errorf("section %s: %v", s.Key, err)
		}
	}
	for _, e := range Examples {
		if _, err := Parse([]byte(e.Content)); err != nil {
			t.Errorf("example %s: %v", e.Key, err)
		}
	}
}

// Примеры профилей на странице «Профили» сайта сохраняются: блок yaml,
// начинающийся с «version:», проходит Parse.
func TestProfilesPageExamplesParse(t *testing.T) {
	block := regexp.MustCompile("(?s)```yaml\n(version:.*?)```")
	for _, file := range []string{
		filepath.Join("..", "..", "site", "guide", "profiles.md"),
		filepath.Join("..", "..", "site", "en", "guide", "profiles.md"),
	} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found := block.FindAllStringSubmatch(string(raw), -1)
		if len(found) == 0 {
			t.Errorf("%s: no profile examples", file)
		}
		for _, m := range found {
			if _, err := Parse([]byte(m[1])); err != nil {
				t.Errorf("%s: %v\n%s", file, err, m[1])
			}
		}
	}
}
