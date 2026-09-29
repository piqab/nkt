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
