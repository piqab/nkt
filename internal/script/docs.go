package script

import (
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// DocsMarkdown — страница сайта «Команды сценариев» (site/guide/
// script-commands.md и en/): собирается из Commands и Examples — тех же,
// по которым работают разбор, «Проверить», встроенная справка и подсказки
// моделям, поэтому от них не отстаёт (сверяет TestScriptCommandsPage).
func DocsMarkdown(lang msgs.Lang) string {
	en := lang == msgs.EN
	tr := func(key string, args ...any) string { return msgs.T(lang, key, args...) }
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ") }
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s\n---\n\n", tr("script.docs.title"))
	b.WriteString("<!-- " + tr("script.docs.generated") + " -->\n\n")
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", tr("script.docs.title"), tr("script.docs.intro"))
	fmt.Fprintf(&b, "## %s\n\n", tr("script.docs.commands"))
	for _, c := range Commands {
		syntax := Localize(c.Syntax, en)
		first := strings.SplitN(syntax, "\n", 2)[0]
		fmt.Fprintf(&b, "### `%s` {#cmd-%s}\n\n%s\n\n", strings.ReplaceAll(first, "`", "'"), strings.ReplaceAll(string(c.Kind), ".", "-"), tr(c.Summary))
		if strings.Contains(syntax, "\n") {
			fmt.Fprintf(&b, "```\n%s\n```\n\n", syntax)
		}
		if len(c.Args) > 0 {
			fmt.Fprintf(&b, "| %s | %s | %s |\n|---|---|---|\n", tr("script.docs.colArg"), tr("script.docs.colRequired"), tr("script.docs.colDesc"))
			for _, a := range c.Args {
				req := ""
				if a.Required {
					req = "✓"
				}
				fmt.Fprintf(&b, "| `%s` | %s | %s |\n", cell(Localize(a.Name, en)), req, cell(tr(a.Desc)))
			}
			b.WriteString("\n")
		}
		if c.Example != "" {
			fmt.Fprintf(&b, "%s:\n\n```\n%s\n```\n\n", tr("script.docs.example"), Localize(c.Example, en))
		}
	}
	fmt.Fprintf(&b, "## %s\n\n", tr("script.docs.examples"))
	for _, e := range Examples {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n```\n%s```\n\n", tr(e.Title), tr(e.Summary), Localize(e.Content, en))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
