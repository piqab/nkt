package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/edge"
)

// После продления (новые файлы) edge отдаёт новый сертификат без
// перезапуска; битые файлы посреди замены не роняют уже загруженный.
func TestCertFileReload(t *testing.T) {
	dir := t.TempDir()
	mk := func(sub string) (string, string) {
		if _, _, err := edge.TunnelCert(filepath.Join(dir, sub)); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, sub, "tunnel.crt"), filepath.Join(dir, sub, "tunnel.key")
	}
	c1, k1 := mk("a")
	c2, k2 := mk("b")
	certPath, keyPath := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	cp := func(src, dst string) {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cp(c1, certPath)
	cp(k1, keyPath)
	cf := &certFile{certPath: certPath, keyPath: keyPath}
	now := time.Now()
	first, err := cf.get(now)
	if err != nil {
		t.Fatal(err)
	}
	cp(c2, certPath)
	cp(k2, keyPath)
	later := now.Add(2 * time.Hour)
	_ = os.Chtimes(certPath, later, later)
	if same, _ := cf.get(now.Add(10 * time.Second)); same != first {
		t.Error("перечитан раньше минуты")
	}
	second, err := cf.get(now.Add(2 * time.Minute))
	if err != nil || second == first {
		t.Fatalf("не перечитан: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	even := later.Add(time.Hour)
	_ = os.Chtimes(certPath, even, even)
	if got, err := cf.get(now.Add(5 * time.Minute)); err != nil || got != second {
		t.Errorf("битые файлы: %v", err)
	}
}
