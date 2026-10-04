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
	files := map[string]string{"nkt": "BINARY", "netknownsthat.service": "[Unit]\n"}
	dir, hashes := stage(t, h.Root, files)
	if _, err := h.Execute(sign(Request{Op: OpInstall, Args: map[string]string{"stage": dir, "env": "NKT_X=1\n"}, Files: hashes})); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, BinPath)); string(b) != "BINARY" {
		t.Fatalf("binary %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(h.Root, EnvPath)); fi == nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("env mode %v", fi)
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, EnvPath)); string(b) != "NKT_X=1\n" {
		t.Fatalf("env %q", b)
	}
	// Прежняя форма — env файлом по хэшу, без env в запросе — отказ.
	if _, err := h.Execute(sign(Request{Op: OpInstall, Args: map[string]string{"stage": dir}, Files: hashes})); err == nil {
		t.Fatal("install without signed env accepted")
	}
	// Подменённый после подписи файл — отказ, на месте прежний.
	if err := os.WriteFile(filepath.Join(h.Root, dir, "nkt"), []byte("EVIL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Execute(sign(Request{Op: OpInstall, Args: map[string]string{"stage": dir, "env": "NKT_X=1\n"}, Files: hashes})); err == nil {
		t.Fatal("swapped binary accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, BinPath)); string(b) != "BINARY" {
		t.Fatalf("binary replaced: %q", b)
	}
	// Ссылка вместо файла — отказ.
	_ = os.Remove(filepath.Join(h.Root, dir, "nkt"))
	_ = os.Symlink("/etc/shadow", filepath.Join(h.Root, dir, "nkt"))
	if _, err := h.Execute(sign(Request{Op: OpInstall, Args: map[string]string{"stage": dir, "env": "NKT_X=1\n"}, Files: hashes})); err == nil {
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

func TestPurgeAndClamAV(t *testing.T) {
	h, sign := newHost(t, "hub-secret")
	put := func(p, c string) {
		full := filepath.Join(h.Root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(BinPath, "bin")
	put(ServicePath, "unit")
	put(EnvPath, "env")
	put(DataDir+"/nkt.db", "db")
	put(SudoersPath, "rule")
	put("/home/deploy/.ssh/authorized_keys", "ssh-ed25519 AAAAOTHERKEYBODYAAAAAAAA other\nssh-ed25519 AAAAHUBKEYBODYAAAAAAAAAA nkt-hub\n")
	out, err := h.Execute(sign(Request{Op: OpPurge, Args: map[string]string{"service": "true", "data": "true", "access": "true",
		"user": "deploy", "key_body": "AAAAHUBKEYBODYAAAAAAAAAA"}}))
	if err != nil || strings.Contains(out, "fail") {
		t.Fatalf("purge: %q %v", out, err)
	}
	for _, p := range []string{BinPath, ServicePath, EnvPath, DataDir, SudoersPath} {
		if _, err := os.Stat(filepath.Join(h.Root, p)); !os.IsNotExist(err) {
			t.Errorf("%s still there", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, "/home/deploy/.ssh/authorized_keys")); strings.Contains(string(b), "HUBKEY") || !strings.Contains(string(b), "OTHERKEY") {
		t.Errorf("authorized_keys %q", b)
	}
	// Подмена тела ключа на что-то шелловое — отказ шага.
	out, _ = h.Execute(sign(Request{Op: OpPurge, Args: map[string]string{"access": "true", "user": "deploy", "key_body": "x; rm -rf /"}}))
	if !strings.Contains(out, "fail access") {
		t.Errorf("bad key accepted: %q", out)
	}
	// ClamAV: файлы по подписанным хэшам.
	dir := "/tmp/nkt-clamdb-77"
	put(dir+"/main.cvd", "MAIN")
	put(dir+"/daily.cvd", "DAILY")
	files := map[string]string{"main.cvd": BytesHash([]byte("MAIN")), "daily.cvd": BytesHash([]byte("DAILY"))}
	if _, err := h.Execute(sign(Request{Op: OpClamAV, Args: map[string]string{"stage": dir}, Files: files})); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, ClamDir, "daily.cvd")); string(b) != "DAILY" {
		t.Fatalf("daily %q", b)
	}
	files["main.cvd"] = BytesHash([]byte("OTHER"))
	if _, err := h.Execute(sign(Request{Op: OpClamAV, Args: map[string]string{"stage": dir}, Files: files})); err == nil {
		t.Fatal("wrong clamav hash accepted")
	}
	if _, err := h.Execute(sign(Request{Op: OpClamAV, Args: map[string]string{"stage": "/etc"}, Files: files})); err == nil {
		t.Fatal("bad clamav stage accepted")
	}
}

func TestPurgeDeleteUserFlag(t *testing.T) {
	h, sign := newHost(t, "hub-secret")
	if _, err := h.Execute(sign(Request{Op: OpPurge, Args: map[string]string{"user": "deploy", "access": "true"}})); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "userdel") {
			t.Fatalf("user deleted without delete_user: %v", h.calls)
		}
	}
	out, _ := h.Execute(sign(Request{Op: OpPurge, Args: map[string]string{"user": "deploy", "delete_user": "true"}}))
	if !strings.Contains(out, "ok delete_user") || !strings.Contains(strings.Join(h.calls, "|"), "userdel -r deploy") {
		t.Fatalf("delete_user: %q %v", out, h.calls)
	}
	out, _ = h.Execute(sign(Request{Op: OpPurge, Args: map[string]string{"user": "root", "delete_user": "true"}}))
	if !strings.Contains(out, "fail delete_user") {
		t.Fatalf("root deletion allowed: %q", out)
	}
}

// Смена ключа: подпись старым ключом ставит новый; дальше старый не
// принимается, новый — да; чужой ключ ключ не меняет; мусор — отказ.
func TestRekey(t *testing.T) {
	h, signOld := newHost(t, "old-hub")
	newPriv := KeyFromSecret([]byte("new-hub"))
	newPub := strings.TrimSpace(PublicText(newPriv))

	// Чужой хаб сменить ключ не может.
	stranger := KeyFromSecret([]byte("stranger"))
	env, _ := Sign(stranger, Request{Op: OpRekey, Args: map[string]string{"pub": strings.TrimSpace(PublicText(stranger))}, Serial: 100})
	if _, err := h.Execute(env); err == nil {
		t.Fatal("rekey by a foreign key accepted")
	}
	if _, err := h.Execute(signOld(Request{Op: OpRekey, Args: map[string]string{"pub": "not-a-key"}})); err == nil {
		t.Fatal("garbage key accepted")
	}
	if _, err := h.Execute(signOld(Request{Op: OpRekey, Args: map[string]string{"pub": newPub}})); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(h.PubKey); strings.TrimSpace(string(b)) != newPub {
		t.Fatalf("key file %q", b)
	}
	if _, err := h.Execute(signOld(Request{Op: OpPing})); err == nil {
		t.Fatal("old key still accepted")
	}
	env, _ = Sign(newPriv, Request{Op: OpPing, Serial: 1 << 40})
	if out, err := h.Execute(env); err != nil || out != "ok" {
		t.Fatalf("new key: %q %v", out, err)
	}
}
