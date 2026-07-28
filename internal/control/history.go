// Package control performs every state-changing operation on the host: service
// actions, config edits with versioning and rollback, and firewall changes.
package control

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// history stores the bytes of every observed and edited config revision.
// The index lives in SQLite; the payloads live here, one file per revision.
type history struct {
	root string
}

func newHistory(root string) *history { return &history{root: root} }

// blobName derives a deterministic, filesystem-safe name for a revision.
func blobName(path, sum string) string {
	pathHash := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%s/%s.blob", hex.EncodeToString(pathHash[:8]), sum[:16])
}

// put writes content into the store and returns its name and checksum. Writing
// the same bytes twice is a no-op, so an unchanged file costs nothing.
func (h *history) put(path string, content []byte) (name, sum string, err error) {
	digest := sha256.Sum256(content)
	sum = hex.EncodeToString(digest[:])
	name = blobName(path, sum)

	full := filepath.Join(h.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", "", fmt.Errorf("создание каталога истории: %w", err)
	}
	if _, err := os.Stat(full); err == nil {
		return name, sum, nil
	}
	if err := os.WriteFile(full, content, 0o640); err != nil {
		return "", "", fmt.Errorf("запись версии конфига: %w", err)
	}
	return name, sum, nil
}

// get reads a stored revision.
func (h *history) get(name string) ([]byte, error) {
	if strings.Contains(name, "..") {
		return nil, fmt.Errorf("недопустимое имя версии: %s", name)
	}
	return os.ReadFile(filepath.Join(h.root, filepath.FromSlash(name)))
}
