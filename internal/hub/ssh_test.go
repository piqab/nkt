package hub

import (
	"os"
	"strings"
	"testing"
)

func TestMapUnameOS(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Linux", "linux", false},
		{"linux", "linux", false},
		{"Darwin", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := mapUnameOS(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("mapUnameOS(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("mapUnameOS(%q): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("mapUnameOS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidatePrivateKeyAcceptsAGoodKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := dir + "/id_ed25519"
	runOK(t, "ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "", "-q")
	pem := readFile(t, keyPath)

	if err := validatePrivateKey(pem); err != nil {
		t.Fatalf("validatePrivateKey rejected a good key: %v", err)
	}
}

func TestValidatePrivateKeyDiagnostics(t *testing.T) {
	dir := t.TempDir()
	keyPath := dir + "/id_ed25519_pw"
	runOK(t, "ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "hunter2", "-q")
	passphraseProtected := readFile(t, keyPath)
	pubKey := readFile(t, keyPath+".pub")

	cases := []struct {
		name   string
		secret string
		want   string
	}{
		{"garbage", "this is not a key at all", "PEM"},
		{"public key pasted instead", pubKey, "PEM"},
		{"putty ppk", "PuTTY-User-Key-File-3: ssh-ed25519\nEncryption: none\n", "PuTTY"},
		{"passphrase protected", passphraseProtected, "паролем"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePrivateKey(c.secret)
			if err == nil {
				t.Fatalf("expected an error, got none")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err.Error(), c.want)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestMapUnameArch(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"x86_64", "amd64", false},
		{"amd64", "amd64", false},
		{"aarch64", "arm64", false},
		{"arm64", "arm64", false},
		{"armv6l", "arm", false},
		{"armv7l", "arm", false},
		{"armv7", "arm", false},
		{"arm", "arm", false},
		{"i386", "", true},
	}
	for _, c := range cases {
		got, err := mapUnameArch(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("mapUnameArch(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("mapUnameArch(%q): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("mapUnameArch(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
