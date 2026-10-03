package hub

import (
	"context"
	"io"
	"os"
	osuser "os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/hubsudo"
	"github.com/piqab/nkt/internal/store"
)

// Узкий sudo целиком, без root: хаб привозит файлы по SSH, подписывает
// запрос своим ключом, hub-sudo с открытым ключом хаба сверяет хэши и
// ставит файлы (во временный корень вместо /).
func TestNarrowSudoInstallRoundTrip(t *testing.T) {
	addr, port, clientKeyPEM := startTestSSHD(t)
	me, err := osuser.Current()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := dialSSH(ctx, addr, port, me.Username, store.HostAuthKey, clientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	m, _ := newTestManager(t)
	root := t.TempDir()
	if err := os.Symlink("/tmp", filepath.Join(root, "tmp")); err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(root, "hub-sign.pub")
	if err := os.WriteFile(pub, []byte(hubsudo.PublicText(m.signKey())), 0o644); err != nil {
		t.Fatal(err)
	}
	host := hubsudo.Host{PubKey: pub, StateDir: filepath.Join(root, "state"), Root: root,
		Run: func(io.Reader, string, ...string) (string, error) { return "", nil }}

	localBin := filepath.Join(t.TempDir(), "nkt")
	if err := os.WriteFile(localBin, []byte("#!/bin/sh\necho nkt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	placed := false
	nop := func(string, ...any) {}
	err = stageFiles(client, me.Username, binarySource{LocalPath: localBin}, "[Unit]\n", "NKT_A=1\n", "", "", "", nop, nop,
		func(tmpDir string, hashes map[string]string) error {
			env, err := hubsudo.Sign(m.signKey(), hubsudo.Request{Op: hubsudo.OpInstall, Args: map[string]string{"stage": tmpDir, "env": "NKT_A=1\n"}, Files: hashes, Serial: nextSerial(0)})
			if err != nil {
				return err
			}
			_, err = host.Execute(env)
			placed = err == nil
			return err
		})
	if err != nil || !placed {
		t.Fatalf("stageFiles with hub-sudo: %v", err)
	}
	for path, want := range map[string]string{hubsudo.BinPath: "#!/bin/sh\necho nkt\n", hubsudo.ServicePath: "[Unit]\n", hubsudo.EnvPath: "NKT_A=1\n"} {
		if b, err := os.ReadFile(filepath.Join(root, path)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", path, b, err)
		}
	}
	// Чужой хаб (другой мастер-ключ) этому хосту не указ.
	other, _ := newTestManager(t)
	other.key = []byte("another-master-key-0123456789abcd")
	env, _ := hubsudo.Sign(other.signKey(), hubsudo.Request{Op: hubsudo.OpPing, Serial: nextSerial(0)})
	if _, err := host.Execute(env); err == nil {
		t.Fatal("foreign hub accepted")
	}
}

func TestNextSerialMonotonic(t *testing.T) {
	a := nextSerial(0)
	b := nextSerial(0)
	c := nextSerial(b + 1000)
	if !(b > a && c > b+1000) {
		t.Fatalf("%d %d %d", a, b, c)
	}
	if mm := staleSerialRe.FindStringSubmatch("hub-sudo: stale serial, last 42"); mm == nil || mm[1] != "42" {
		t.Fatal("stale serial parse")
	}
}
