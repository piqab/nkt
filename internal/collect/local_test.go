package collect

import (
	"os"
	"path/filepath"
	"testing"
)

// A freshly generated file (a self-signed certificate, for instance) needs its
// directory created on first write; editing an existing config never hit this
// because the directory was always already there.
func TestLocalWriteFileCreatesParentDirectory(t *testing.T) {
	root := t.TempDir()
	l := NewLocal("/var/run/docker.sock", 0)

	target := filepath.Join(root, "nested", "deeper", "file.pem")
	if err := l.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("запись в несуществующий каталог: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("чтение записанного файла: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("содержимое = %q, ожидалось hello", got)
	}
}

// A write into an existing directory must keep working exactly as before.
func TestLocalWriteFileExistingDirectory(t *testing.T) {
	root := t.TempDir()
	l := NewLocal("/var/run/docker.sock", 0)

	target := filepath.Join(root, "file.txt")
	if err := l.WriteFile(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("первая запись: %v", err)
	}
	if err := l.WriteFile(target, []byte("second"), 0o644); err != nil {
		t.Fatalf("повторная запись: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "second" {
		t.Fatalf("содержимое = %q, err=%v, ожидалось second", got, err)
	}
}
