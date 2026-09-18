package msgs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Каждый ключ, который код просит у каталога, должен быть и в ru, и в
// en: иначе пользователь видит «hub.jobOnHost» вместо текста. Тест
// ищет ключи по вызовам msgs.Errorf/T/Tc, jc.Log, report(, Msg( — все
// они принимают литерал ключа первым (или вторым, после lang/ctx)
// аргументом.
func TestEveryUsedKeyIsInBothCatalogs(t *testing.T) {
	root := filepath.Join("..", "..")
	first := regexp.MustCompile(`\b(?:Errorf|Log|report|Msg|logf|warn)\(\s*"([a-z][A-Za-z0-9]*\.[A-Za-z0-9_.]+)"`)
	second := regexp.MustCompile(`\b(?:Tc|T)\(\s*[^,()]+,\s*"([a-z][A-Za-z0-9]*\.[A-Za-z0-9_.]+)"`)
	used := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "web", "site", ".git", "android", "fixtures":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		for _, re := range []*regexp.Regexp{first, second} {
			for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
				used[m[1]] = append(used[m[1]], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for key, files := range used {
		// Ключи с динамическим хвостом (finding.malware.<вид>.title)
		// собираются в коде — их проверяют тесты самих правил.
		if strings.HasSuffix(key, ".") {
			continue
		}
		if _, ok := catalogs[RU][key]; !ok {
			missing = append(missing, "ru: "+key+" ("+files[0]+")")
		}
		if _, ok := catalogs[EN][key]; !ok {
			missing = append(missing, "en: "+key+" ("+files[0]+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("ключей нет в каталоге:\n  %s", strings.Join(missing, "\n  "))
	}
}
