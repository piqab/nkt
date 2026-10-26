package collect

import (
	"os"
	"path/filepath"
	"testing"
)

// DeleteFile не удаляет по относительному пути и пути с «..».
func TestLocalDeleteFileRejectsBadPaths(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "keep")
	_ = os.WriteFile(victim, []byte("x"), 0o644)
	l := &Local{}
	for _, p := range []string{"keep", dir + "/sub/../keep", dir + "//keep", dir + "/keep/"} {
		if err := l.DeleteFile(p); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("file removed through a bad path")
	}
	if err := l.DeleteFile(victim); err != nil {
		t.Fatal(err)
	}
}

// Fixtures.DeleteFile — та же проверка пути, что у Local.
func TestFixturesDeleteFileRejectsBadPaths(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc"), 0o755)
	victim := filepath.Join(root, "etc", "keep")
	_ = os.WriteFile(victim, []byte("x"), 0o644)
	f := NewFixtures(root)
	for _, p := range []string{"etc/keep", "/etc/../etc/keep", "/etc//keep"} {
		if err := f.DeleteFile(p); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if err := f.DeleteFile("/etc/keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatal("file not removed")
	}
}
