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
	// Коротко: все команды по группам — ссылка на раздел и первая фраза.
	fmt.Fprintf(&b, "## %s\n\n", tr("script.docs.overview"))
	for _, g := range docGroups {
		fmt.Fprintf(&b, "**%s**\n\n| %s | %s |\n|---|---|\n", tr("script.docs.group."+g), tr("script.docs.colCommand"), tr("script.docs.colWhat"))
		for _, c := range Commands {
			if docGroup[c.Kind] != g {
				continue
			}
			fmt.Fprintf(&b, "| [`%s`](#%s) | %s |\n", cell(shortSyntax(Localize(c.Syntax, en))), anchor(c.Kind), cell(firstSentence(tr(c.Summary))))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## %s\n\n", tr("script.docs.commands"))
	for _, c := range Commands {
		syntax := Localize(c.Syntax, en)
		first := strings.SplitN(syntax, "\n", 2)[0]
		fmt.Fprintf(&b, "### `%s` {#%s}\n\n%s\n\n", strings.ReplaceAll(first, "`", "'"), anchor(c.Kind), tr(c.Summary))
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

// Группы обзора — в порядке справки; у каждой команды своя группа
// (TestEveryCommandHasDocGroup).
var docGroups = []string{"vars", "hub", "host", "deploy", "cluster", "plugins"}

var docGroup = map[Kind]string{
	KindSet: "vars", KindParam: "vars",
	KindGroup: "hub", KindHost: "hub", KindInstall: "hub", KindWait: "hub",
	KindPackages: "host", KindService: "host", KindFirewall: "host", KindDockerInst: "host", KindDockerStack: "host",
	KindVMCreate: "host", KindVMAction: "host", KindApplyProfile: "host", KindFilePut: "host", KindUserAdd: "host",
	KindSystem: "host", KindCert: "host", KindGitClone: "host", KindK8sCreate: "host", KindK8sDestroy: "host",
	KindPipeline: "deploy", KindDeploy: "deploy", KindMigrate: "deploy",
	KindK8sIngress: "cluster", KindK8sApply: "cluster", KindK8sWait: "cluster", KindFlyRoute: "cluster", KindFlyApply: "cluster",
	KindPlugin: "plugins",
}

func anchor(k Kind) string { return "cmd-" + strings.ReplaceAll(string(k), ".", "-") }

// shortSyntax — запись команды для обзора: первая строка синтаксиса до
// необязательной части («[…]», «(…)»), не длиннее шести слов.
func shortSyntax(syntax string) string {
	words := strings.Fields(strings.SplitN(syntax, "\n", 2)[0])
	var out []string
	for _, w := range words {
		if strings.HasPrefix(w, "[") || strings.HasPrefix(w, "(") || w == "|" {
			break
		}
		if len(out) == 6 {
			out = append(out, "…")
			break
		}
		out = append(out, w)
	}
	return strings.ReplaceAll(strings.Join(out, " "), "`", "'")
}

// firstSentence — описание команды до первой точки (обзору хватает).
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}
