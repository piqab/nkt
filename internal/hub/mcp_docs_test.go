package hub

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// Справка «MCP для ИИ-агентов» (ru и en) перечисляет ровно встроенные
// инструменты: новый инструмент без строки в таблице или строка без
// инструмента роняет тест — справка не отстаёт от кода.
func TestMCPDocsListEveryTool(t *testing.T) {
	code := map[string]bool{}
	for _, tool := range mcpTools {
		code[tool.Name] = true
	}
	if len(code) < 20 {
		t.Fatalf("only %d tools in code — the list moved?", len(code))
	}
	// Первая ячейка строки таблицы: один или несколько `инструментов`.
	row := regexp.MustCompile("(?m)^\\| (`[a-z][a-z0-9_]*`(?:, `[a-z][a-z0-9_]*`)*) \\|")
	name := regexp.MustCompile("`([a-z][a-z0-9_]*)`")
	for _, f := range []string{"../../site/guide/hub-mcp.md", "../../site/en/guide/hub-mcp.md"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		docs := map[string]bool{}
		for _, m := range row.FindAllStringSubmatch(string(raw), -1) {
			for _, n := range name.FindAllStringSubmatch(m[1], -1) {
				docs[n[1]] = true
			}
		}
		var missing, extra []string
		for n := range code {
			if !docs[n] {
				missing = append(missing, n)
			}
		}
		for n := range docs {
			if !code[n] {
				extra = append(extra, n)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 || len(extra) > 0 {
			t.Errorf("%s: not in the table %v; in the table but not in code %v", f, missing, extra)
		}
	}
}
