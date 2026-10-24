package script

import "testing"

func TestPluginCommands(t *testing.T) {
	SetPluginWords(map[string]string{"dns": "technitium", "fly": "evil"})
	defer SetPluginWords(nil)
	sc, issues := Parse("dns set app.example.com A 203.0.113.10\nplugin technitium dns rm old.example.com\n", nil)
	if len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	if len(sc.Steps) != 2 || sc.Steps[0].Kind != KindPlugin || sc.Steps[0].Name != "technitium" || sc.Steps[0].List[0] != "dns" || len(sc.Steps[0].List) != 5 {
		t.Fatalf("word form: %+v", sc.Steps)
	}
	if sc.Steps[1].Name != "technitium" || sc.Steps[1].List[0] != "dns" || sc.Steps[1].List[2] != "old.example.com" {
		t.Fatalf("plugin form: %+v", sc.Steps[1])
	}
	// Слово языка плагину не занять: «fly apply» — своя команда.
	sc, issues = Parse("fly apply\n", nil)
	if len(issues) > 0 || sc.Steps[0].Kind != KindFlyApply {
		t.Fatalf("builtin word taken: %+v %v", sc.Steps, issues)
	}
	if _, issues := Parse("plugin technitium\n", nil); len(issues) == 0 {
		t.Fatal("plugin without a word accepted")
	}
	// Не подключён — ошибка проверки.
	sc, _ = Parse("plugin mail dkim add example.com\n", nil)
	if issues := Check(sc, Refs{Plugins: map[string]bool{"technitium": true}}); len(issues) != 1 {
		t.Fatalf("unknown plugin: %v", issues)
	}
}
