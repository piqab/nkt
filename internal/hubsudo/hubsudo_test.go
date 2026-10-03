package hubsudo

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeHost struct {
	Host
	calls []string
}

func newHost(t *testing.T, secret string) (*fakeHost, func(Request) []byte) {
	t.Helper()
	root := t.TempDir()
	priv := KeyFromSecret([]byte(secret))
	pub := filepath.Join(root, "hub-sign.pub")
	if err := os.WriteFile(pub, []byte(PublicText(priv)), 0o644); err != nil {
		t.Fatal(err)
	}
	fh := &fakeHost{}
	fh.Host = Host{PubKey: pub, StateDir: filepath.Join(root, "state"), Root: root, Run: func(stdin io.Reader, name string, args ...string) (string, error) {
		in := ""
		if stdin != nil {
			b, _ := io.ReadAll(stdin)
			in = " <" + strings.TrimSpace(string(b))
		}
		fh.calls = append(fh.calls, name+" "+strings.Join(args, " ")+in)
		return "", nil
	}}
	serial := int64(0)
	sign := func(r Request) []byte {
		serial++
		if r.Serial == 0 {
			r.Serial = serial
		}
		b, err := Sign(priv, r)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return fh, sign
}

func stage(t *testing.T, root string, files map[string]string) (string, map[string]string) {
	t.Helper()
	dir := "/tmp/nkt-install-123"
	hashes := map[string]string{}
	for name, content := range files {
		p := filepath.Join(root, dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		hashes[name] = BytesHash([]byte(content))
	}
	return dir, hashes
}

func TestSignatureAndSerial(t *testing.T) {
	h, sign := newHost(t, "hub-secret")
	if out, err := h.Execute(sign(Request{Op: OpPing})); err != nil || out != "ok" {
		t.Fatalf("ping: %q %v", out, err)
	}
	// Чужой хаб (другой ключ) — отказ.
	other, _ := newHost(t, "other-hub")
	_, otherSign := newHost(t, "other-hub")
	_ = other
	if _, err := h.Execute(otherSign(Request{Op: OpPing, Serial: 100})); !errors.Is(err, ErrSignature) {
		t.Fatalf("foreign key: %v", err)
	}
	// Подменённый запрос с прежней подписью — отказ.
	env := sign(Request{Op: OpService, Args: map[string]string{"action": "stop"}})
	tampered := []byte(strings.Replace(string(env), "stop", "restart", 1))
	if _, err := h.Execute(tampered); err == nil {
		t.Fatal("tampered request accepted")
	}
	// Повтор выполненного — отказ по номеру.
	ok := sign(Request{Op: OpPing})
	if _, err := h.Execute(ok); err != nil {
		t.Fatal(err)
	}
	var se ErrSerial
	if _, err := h.Execute(ok); !errors.As(err, &se) || se.Last == 0 {
		t.Fatalf("replay: %v", err)
	}
	// Без ключа на хосте — отказ.
	h.PubKey = filepath.Join(h.Root, "nope")
	if _, err := h.Execute(sign(Request{Op: OpPing})); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
}

func TestInstall(t *testing.T) {
	h, sign := newHost(t, "hub-secret")
	files := map[string]string{"nkt": "BINARY", "netknownsthat.service": "[Unit]\n", "nkt.env": "NKT_X=1\n"}
	dir, hashes := stage(t, h.Root, files)
	if _, err := h.Execute(sign(Request{Op: OpInstall, Args: map[string]string{"stage": dir}, Files: hashes})); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, BinPath)); string(b) != "BINARY" {
		t.Fatalf("binary %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(h.Root, EnvPath)); fi == nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("env mode %v", fi)
	}
	// Подменённый после подписи файл — отказ, на месте прежний.
	if err := os.WriteFile(filepath.Join(h.Root, dir, "nkt"), []byte("EVIL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Execute(sign(Request{Op: OpInstall, Args: map[string]string{"stage": dir}, Files: hashes})); err == nil {
		t.Fatal("swapped binary accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, BinPath)); string(b) != "BINARY" {
		t.Fatalf("binary replaced: %q", b)
	}
	// Ссылка вместо файла — отказ.
	_ = os.Remove(filepath.Join(h.Root, dir, "nkt"))
	_ = os.Symlink("/etc/shadow", filepath.Join(h.Root, dir, "nkt"))
	if _, err := h.Execute(sign(Request{Op: OpInstall, Args: map[string]string{"stage": dir}, Files: hashes})); err == nil {
		t.Fatal("symlink accepted")
	}
	// Каталог подготовки вне /tmp/nkt-install-N — отказ.
	if _, err := h.Execute(sign(Request{Op: OpInstall, Args: map[string]string{"stage": "/etc"}, Files: hashes})); err == nil {
		t.Fatal("bad stage accepted")
	}
}

func TestOps(t *testing.T) {
	h, sign := newHost(t, "hub-secret")
	for _, r := range []Request{
		{Op: OpActivate},
		{Op: OpService, Args: map[string]string{"action": "stop"}},
		{Op: OpPasswd, Args: map[string]string{"user": "admin", "password": "s3cret"}},
		{Op: OpJournal},
	} {
		if _, err := h.Execute(sign(r)); err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
	}
	want := []string{"systemctl daemon-reload", "systemctl enable netknownsthat", "systemctl restart netknownsthat", "systemctl stop netknownsthat"}
	for i, w := range want {
		if strings.TrimSpace(h.calls[i]) != w {
			t.Errorf("call %d = %q, want %q", i, h.calls[i], w)
		}
	}
	if !strings.Contains(h.calls[4], "passwd admin <s3cret") || !strings.Contains(h.calls[4], "NKT_DATA_DIR="+DataDir) {
		t.Errorf("passwd call %q", h.calls[4])
	}
	for _, bad := range []Request{
		{Op: OpService, Args: map[string]string{"action": "disable; rm -rf /"}},
		{Op: OpPasswd, Args: map[string]string{"user": "a b", "password": "x"}},
		{Op: OpAptProxy, Args: map[string]string{"enabled": "true", "port": "0"}},
		{Op: "shell"},
	} {
		if _, err := h.Execute(sign(bad)); err == nil {
			t.Errorf("%s %v accepted", bad.Op, bad.Args)
		}
	}
	if _, err := h.Execute(sign(Request{Op: OpAptProxy, Args: map[string]string{"enabled": "true", "port": "3142"}})); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, AptProxyDetect)); !strings.Contains(string(b), "/dev/tcp/127.0.0.1/3142") {
		t.Fatalf("apt detect %q", b)
	}
	if _, err := h.Execute(sign(Request{Op: OpAptProxy, Args: map[string]string{"enabled": "false"}})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.Root, AptProxyConf)); !os.IsNotExist(err) {
		t.Fatal("apt conf not removed")
	}
	if r := SudoersRule("deploy"); !strings.Contains(r, "deploy ALL=(root) NOPASSWD: /usr/local/bin/nkt hub-sudo\n") {
		t.Fatalf("rule %q", r)
	}
}
