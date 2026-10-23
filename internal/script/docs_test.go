package script

import (
	"os"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
)

// Страница «Команды сценариев» на сайте совпадает с тем, что собирается из
// Commands и Examples: поменяли команду — перегенерируйте страницу:
// NKT_UPDATE_DOCS=1 go test ./internal/script -run TestScriptCommandsPage
func TestScriptCommandsPage(t *testing.T) {
	for _, c := range []struct {
		lang msgs.Lang
		path string
	}{{msgs.RU, "../../site/guide/script-commands.md"}, {msgs.EN, "../../site/en/guide/script-commands.md"}} {
		want := DocsMarkdown(c.lang)
		if os.Getenv("NKT_UPDATE_DOCS") == "1" {
			if err := os.WriteFile(c.path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(c.path)
		if err != nil || string(got) != want {
			t.Errorf("%s устарела: NKT_UPDATE_DOCS=1 go test ./internal/script -run TestScriptCommandsPage", c.path)
		}
	}
}
