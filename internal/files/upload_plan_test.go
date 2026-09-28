package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// Защищённые шаблоны: имя файла на любом уровне, каталог «имя/», путь со «/».
func TestProtected(t *testing.T) {
	for rel, want := range map[string]bool{
		".env":                    true,
		"app/.env.production":     true,
		"config.local.php":        true,
		"wp-config.php":           true,
		"public/uploads/a.png":    true,
		"storage/logs/x.log":      true,
		"index.php":               false,
		"src/data.go":             false,
		"themes/site/uploads.css": false,
	} {
		if got := Protected(rel, DefaultProtected); got != want {
			t.Errorf("%s: %v", rel, got)
		}
	}
	if !Protected("conf/app.ini", []string{"conf/*.ini"}) || Protected("other/app.ini", []string{"conf/*.ini"}) {
		t.Error("шаблон пути")
	}
}

// План: новый, тот же (по сумме), изменённый, защищённый, конфликт.
func TestPlan(t *testing.T) {
	m, root, _ := testManager(t)
	site := filepath.Join(root, "site")
	_ = os.MkdirAll(filepath.Join(site, "css"), 0o755)
	write := func(rel, s string) {
		_ = os.WriteFile(filepath.Join(site, rel), []byte(s), 0o644)
	}
	write("index.html", "old")
	write("same.txt", "same")
	write(".env", "SECRET=1")
	write("css", "") // не трогаем: это каталог
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	items, err := m.Plan(context.Background(), site, []PlanEntry{
		{Rel: "index.html", Size: 3, SHA256: sum("new")},
		{Rel: "same.txt", Size: 4, SHA256: sum("same")},
		{Rel: ".env", Size: 8, SHA256: sum("SECRET=2")},
		{Rel: "new/page.html", Size: 1},
		{Rel: "css", Size: 1},
		{Rel: "../x", Size: 1},
	}, DefaultProtected)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		status    string
		protected bool
	}{{PlanChanged, false}, {PlanSame, false}, {PlanChanged, true}, {PlanNew, false}, {PlanConflict, false}, {PlanInvalid, false}}
	for i, w := range want {
		if items[i].Status != w.status || items[i].Protected != w.protected {
			t.Errorf("%s: %+v", items[i].Rel, items[i])
		}
	}
}
