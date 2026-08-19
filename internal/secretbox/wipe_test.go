package secretbox

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSecureWipeFileOverwritesAndRemoves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	original := bytes.Repeat([]byte("A"), 5000)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := SecureWipeFile(path); err != nil {
		t.Fatalf("SecureWipeFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists after SecureWipeFile (err=%v)", err)
	}
}

// TestSecureWipeFileActuallyOverwrites confirms the file's on-disk content
// changes before removal, not just that the file ends up gone (which a
// plain os.Remove would also achieve) — the whole point is the byte pattern
// the original secret formed no longer exists anywhere in the file's
// blocks. Checked by truncating the OS-level unlink out of the picture:
// wipe a copy opened for inspection via a hard link taken beforehand.
func TestSecureWipeFileActuallyOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	witness := filepath.Join(dir, "witness")
	original := bytes.Repeat([]byte("SECRET-KEY-MATERIAL"), 200)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Link(path, witness); err != nil {
		t.Skipf("hard links unsupported on this filesystem: %v", err)
	}

	if err := SecureWipeFile(path); err != nil {
		t.Fatalf("SecureWipeFile: %v", err)
	}

	// The hard link keeps the inode (and its now-overwritten data blocks)
	// alive after path's own directory entry is gone — reading through it
	// is exactly what recovering the "deleted" data from disk would see.
	got, err := os.ReadFile(witness)
	if err != nil {
		t.Fatalf("read witness: %v", err)
	}
	if bytes.Contains(got, []byte("SECRET-KEY-MATERIAL")) {
		t.Error("original secret bytes are still present on disk after SecureWipeFile")
	}
	if len(got) != len(original) {
		t.Errorf("witness length = %d, want unchanged %d (wipe must overwrite in place, not truncate)", len(got), len(original))
	}
}

func TestSecureWipeFileMissingIsNoop(t *testing.T) {
	if err := SecureWipeFile(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("SecureWipeFile on a missing file: %v, want nil", err)
	}
}

func TestSecureWipeDirRemovesEverythingIncludingNested(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "data")
	nested := filepath.Join(root, "bin-cache", "sub")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := []string{
		filepath.Join(root, "hub.key"),
		filepath.Join(root, "netknownsthat.db"),
		filepath.Join(nested, "nkt-linux-amd64"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("sensitive content"), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", f, err)
		}
	}

	if err := SecureWipeDir(root); err != nil {
		t.Fatalf("SecureWipeDir: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("data dir still exists after SecureWipeDir (err=%v)", err)
	}
}

func TestSecureWipeDirMissingIsNoop(t *testing.T) {
	if err := SecureWipeDir(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("SecureWipeDir on a missing dir: %v, want nil", err)
	}
}

func TestSecureWipeDirLeavesSiblingsAlone(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "data")
	sibling := filepath.Join(dir, "keep-me.txt")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "hub.key"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(sibling, []byte("unrelated"), 0o600); err != nil {
		t.Fatalf("WriteFile sibling: %v", err)
	}

	if err := SecureWipeDir(root); err != nil {
		t.Fatalf("SecureWipeDir: %v", err)
	}
	got, err := os.ReadFile(sibling)
	if err != nil {
		t.Fatalf("sibling file was affected: %v", err)
	}
	if string(got) != "unrelated" {
		t.Errorf("sibling content changed: %q", got)
	}
}
