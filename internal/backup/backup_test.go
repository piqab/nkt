package backup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Сценарии бэкапа и восстановления всех видов — синтаксически верный bash
// и пишут туда, куда обещают.
func TestScriptsAreValidBash(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	for _, p := range []Params{
		{Kind: KindVM, Name: "web-vm"},
		{Kind: KindDocker, Name: "acme-api"},
		{Kind: KindPodman, Name: "db"},
		{Kind: KindCompose, Name: "acme", ProjectDir: "/srv/docker", IncludeImages: true},
	} {
		sc, out, err := Script(root, p, now)
		if err != nil {
			t.Fatalf("%s: %v", p.Kind, err)
		}
		if out != filepath.Join(root, p.Kind, p.Name, p.Name+"-20260925-100000.tar") {
			t.Errorf("%s: out = %s", p.Kind, out)
		}
		checkBash(t, p.Kind, sc)
		// восстановление из будущего архива
		rs, err := RestoreScript(root, RestoreParams{Path: out, NewName: "copy-1"})
		if err != nil {
			t.Fatalf("%s restore: %v", p.Kind, err)
		}
		checkBash(t, p.Kind+" restore", rs)
	}
	if _, _, err := Script(root, Params{Kind: KindCompose, Name: "x"}, now); err == nil {
		t.Error("compose без каталога проекта принят")
	}
	if _, _, err := Script(root, Params{Kind: KindVM, Name: "../etc"}, now); err == nil {
		t.Error("имя с выходом из каталога принято")
	}
	// Имя с кавычкой не пролезает — ValidName; а экранирование sh() держит
	// и её.
	if sh("a'b") != `'a'\''b'` {
		t.Errorf("sh = %s", sh("a'b"))
	}
}

func checkBash(t *testing.T, what, script string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash не найден")
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("%s: bash -n: %v\n%s", what, err, out)
	}
}

// Resolve пропускает только архивы внутри <root>/<вид>/<имя>/.
func TestResolveAndList(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root, KindVM, "web-vm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"web-vm-1.tar", "web-vm-2.tar", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "web-vm-1.tar"), old, old)
	list, err := List(root, KindVM, "web-vm")
	if err != nil || len(list) != 2 || list[0].File != "web-vm-2.tar" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	all, _ := List(root, KindVM, "")
	if len(all) != 2 {
		t.Errorf("все бэкапы вида: %d", len(all))
	}
	if _, err := Resolve(root, filepath.Join(dir, "web-vm-1.tar")); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"/etc/passwd", filepath.Join(root, "vm", "web-vm", "..", "..", "x.tar"), filepath.Join(dir, "notes.txt"), filepath.Join(root, "evil", "x", "y.tar")} {
		if _, err := Resolve(root, bad); err == nil {
			t.Errorf("пропущен %s", bad)
		}
	}
}

// Сценарий уходит файлом: runner пишет его в <root>/.run и запускает
// «bash <файл>» — без ${…}-подстановки systemd.
func TestRunnerWritesScriptFile(t *testing.T) {
	root := t.TempDir()
	path, err := writeScript(root, 7, "echo ${#X[@]}")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "echo ${#X[@]}" || filepath.Dir(path) != filepath.Join(root, ".run") {
		t.Errorf("path=%s content=%q", path, b)
	}
}
