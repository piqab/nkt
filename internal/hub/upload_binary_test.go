package hub

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	osuser "os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"github.com/piqab/nkt/internal/store"
)

// Бинарник уходит на хост сжатым (настоящий sshd этой машины), там
// распаковывается и совпадает побайтно; сжатая копия остаётся в кэше,
// временный .gz на хосте — нет.
func TestUploadBinaryCompressed(t *testing.T) {
	addr, port, clientKeyPEM := startTestSSHD(t)
	me, _ := osuser.Current()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := dialSSH(ctx, addr, port, me.Username, store.HostAuthKey, clientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sc, err := sftp.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()

	dir := t.TempDir()
	local := filepath.Join(dir, "nkt-linux-amd64-1.0.0")
	// Половина — случайные байты, половина — повторы: как у бинарника,
	// сжимается, но не в ноль.
	data := make([]byte, 2<<20)
	_, _ = rand.Read(data[:1<<20])
	copy(data[1<<20:], bytes.Repeat([]byte("nkt "), 1<<18))
	if err := os.WriteFile(local, data, 0o644); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(t.TempDir(), "stage", "nkt")
	var lines []string
	report := func(key string, _ ...any) { lines = append(lines, key) }
	if err := uploadBinary(client, sc, local, remote, 0o755, report, report); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(remote)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("remote binary differs (err %v)", err)
	}
	if st, _ := os.Stat(remote); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", st.Mode())
	}
	if _, err := os.Stat(remote + ".gz"); !os.IsNotExist(err) {
		t.Fatal("remote .gz left behind")
	}
	if _, err := os.Stat(local + ".gz"); err != nil {
		t.Fatal("no cached .gz")
	}
	if len(lines) == 0 || lines[0] != "hub.uploadingCompressed" {
		t.Fatalf("report: %v", lines)
	}
}
