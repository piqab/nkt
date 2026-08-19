package secretbox

import (
	"crypto/rand"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// wipeChunkSize bounds how much random data SecureWipeFile buffers in memory
// at once — large enough to make wiping a multi-megabyte SQLite database
// fast, small enough that it never matters for anything this project writes.
const wipeChunkSize = 1 << 20 // 1 MiB

// SecureWipeFile overwrites path's entire contents with random bytes before
// unlinking it, so a plain filesystem-level `rm` — which only removes the
// directory entry and leaves the data blocks intact until something else
// happens to reuse them — cannot be relied on to keep former key material
// or ciphertext away from someone who later gets raw access to the disk.
// A no-op, not an error, when path does not exist.
//
// This is a best-effort measure, not a guarantee: journaling filesystems and
// copy-on-write ones (btrfs, ZFS) can retain old blocks in a journal or
// snapshot regardless of what gets written over the live file, and SSD wear
// levelling routinely relocates writes instead of overwriting in place. What
// it does reliably defeat is casual recovery from an otherwise-untouched
// filesystem — the same class of exposure `rm` alone leaves wide open.
func SecureWipeFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		// A symlink or device node has nothing of its own to overwrite —
		// just remove the entry itself.
		return os.Remove(path)
	}

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("открытие %s для затирания: %w", path, err)
	}
	defer f.Close()

	remaining := info.Size()
	buf := make([]byte, min(remaining, wipeChunkSize))
	for remaining > 0 {
		n := min(remaining, int64(len(buf)))
		chunk := buf[:n]
		if _, err := rand.Read(chunk); err != nil {
			return fmt.Errorf("генерация случайных данных: %w", err)
		}
		if _, err := f.Write(chunk); err != nil {
			return fmt.Errorf("запись поверх %s: %w", path, err)
		}
		remaining -= n
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("закрытие %s: %w", path, err)
	}
	return os.Remove(path)
}

// SecureWipeDir overwrites every regular file under dir (recursively, via
// SecureWipeFile) before removing the whole tree — for a directory that may
// hold encryption keys or other secrets alongside ordinary state (the hub's
// own DataDir: hub.key, netknownsthat.db and its WAL/SHM siblings, cached
// binaries, TLS private keys), where deleting only the files known to be
// sensitive risks missing one. A no-op, not an error, when dir does not
// exist.
func SecureWipeDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		return SecureWipeFile(path)
	})
	if err != nil {
		return err
	}
	// Everything of substance is already overwritten; RemoveAll here only
	// has empty directories and already-wiped (zero-content) file entries
	// left to clear away.
	return os.RemoveAll(dir)
}
