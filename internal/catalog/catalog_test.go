package catalog_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/catalog"
	"github.com/piqab/nkt/internal/catalog/catalogtest"
)

// Каталог из examples/: все плагины с версиями, примеры конвейеров с
// заготовками окна; файл плагина и README — по отпечаткам.
func TestBuildFromExamples(t *testing.T) {
	index, sig, files, pub, err := catalogtest.Signed(catalogtest.ExamplesDir())
	if err != nil {
		t.Fatal(err)
	}
	snap, err := catalog.Open(index, sig, files, []ed25519.PublicKey{pub}, "test")
	if err != nil {
		t.Fatal(err)
	}
	catalog.SetCurrent(snap)
	defer catalog.SetCurrent(nil)
	keys := []string{}
	for _, p := range catalog.PluginPackages() {
		keys = append(keys, p.Key)
		if p.Version == "" || p.MinNKT != "1.16.0" || p.Tests != "passed" || len(p.Content) < 1000 || p.Readme["ru"] != "README.md" || p.Readme["en"] != "README.en.md" {
			t.Errorf("%s: %+v", p.Key, p.Plugin)
		}
		if _, ok := p.Files["deploy/test_plugin.py"]; ok {
			t.Errorf("%s: test in catalog", p.Key)
		}
	}
	if strings.Join(keys, ",") != "cloudflare,connector,docker-mailserver,home-assistant,litellm,mikrotik,technitium,windows,yandex-cloud" {
		t.Fatalf("plugins: %v", keys)
	}
	pipes := map[string]bool{}
	for _, p := range catalog.Pipelines("en") {
		pipes[p.Key] = true
		for _, r := range p.Title {
			if r >= 'а' && r <= 'я' {
				t.Errorf("%s: Russian title %q", p.Key, p.Title)
				break
			}
		}
	}
	for _, k := range []string{"forgejo", "forgejo-fly", "forgejo-runner", "technitium", "technitium-secondary", "docker-mailserver", "litellm"} {
		if !pipes[k] {
			t.Errorf("no pipeline %s", k)
		}
	}
	ui := 0
	for _, e := range snap.Index.Examples {
		if len(e.UI) > 0 {
			ui++
		}
	}
	if ui != 15 {
		t.Errorf("examples with UI: %d", ui)
	}
}

// Подпись чужим ключом, изменённый файл, путь с «..» — каталог не принят.
func TestOpenRejects(t *testing.T) {
	index, sig, files, pub, err := catalogtest.Signed(catalogtest.ExamplesDir())
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := catalog.Open(index, sig, files, []ed25519.PublicKey{other}, "x"); err != catalog.ErrSignature {
		t.Fatalf("foreign key: %v", err)
	}
	bad := map[string][]byte{}
	for k, v := range files {
		bad[k] = v
	}
	bad["windows/deploy/nkt-plugin.py"] = append([]byte("import os; os.system('id')\n"), bad["windows/deploy/nkt-plugin.py"]...)
	if _, err := catalog.Open(index, sig, bad, []ed25519.PublicKey{pub}, "x"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tampered file: %v", err)
	}
	tampered := bytes.Replace(index, []byte(`"version":"1.0.1"`), []byte(`"version":"9.0.0"`), 1)
	if _, err := catalog.Open(tampered, sig, files, []ed25519.PublicKey{pub}, "x"); err != catalog.ErrSignature {
		t.Fatalf("tampered index: %v", err)
	}
	for _, p := range []string{"../etc/passwd", "a/../b", "/abs", "a//b", "a b"} {
		if catalog.SafePath(p) {
			t.Errorf("SafePath(%q)", p)
		}
	}
}

// Скачивание: проверка, сохранение копии, чтение копии; при плохой подписи
// прежняя копия остаётся.
func TestFetchSaveLoad(t *testing.T) {
	index, sig, files, pub, err := catalogtest.Signed(catalogtest.ExamplesDir())
	if err != nil {
		t.Fatal(err)
	}
	serveSig := sig
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/catalog/")
		switch p {
		case "index.json":
			_, _ = w.Write(index)
		case "index.json.sig":
			_, _ = w.Write(serveSig)
		default:
			if b, ok := files[p]; ok {
				_, _ = w.Write(b)
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := filepath.Join(t.TempDir(), "catalog")
	keys := []ed25519.PublicKey{pub}
	snap, err := catalog.Fetch(context.Background(), srv.Client(), srv.URL+"/catalog/", keys, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Index.Plugins) != 9 || snap.Source != srv.URL+"/catalog/" {
		t.Fatalf("snapshot: %d %s", len(snap.Index.Plugins), snap.Source)
	}
	again, err := catalog.Load(dir, keys)
	if err != nil || len(again.Index.Plugins) != 9 || again.Source != snap.Source {
		t.Fatalf("load: %v", err)
	}
	// Плохая подпись — ошибка, копия на диске прежняя.
	serveSig = make([]byte, 64)
	if _, err := catalog.Fetch(context.Background(), srv.Client(), srv.URL+"/catalog/", keys, dir); err != catalog.ErrSignature {
		t.Fatalf("bad sig fetch: %v", err)
	}
	if _, err := catalog.Load(dir, keys); err != nil {
		t.Fatalf("copy lost: %v", err)
	}
	// Файл копии изменён на диске — копия не принимается.
	_ = os.WriteFile(filepath.Join(dir, "mikrotik", "deploy", "nkt-plugin.py"), []byte("x"), 0o640)
	if _, err := catalog.Load(dir, keys); err == nil {
		t.Fatal("tampered copy loaded")
	}
	if _, err := catalog.Fetch(context.Background(), srv.Client(), "http://user:pass@"+strings.TrimPrefix(srv.URL, "http://")+"/catalog/", keys, dir); err == nil {
		t.Fatal("credentials in URL accepted")
	}
}

// Архив без интернета: tar.gz с index.json, подписью и файлами.
func TestImport(t *testing.T) {
	index, sig, files, pub, err := catalogtest.Signed(catalogtest.ExamplesDir())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	put := func(name string, b []byte) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(b)
	}
	put("catalog/index.json", index)
	put("catalog/index.json.sig", sig)
	for p, b := range files {
		put("catalog/"+p, b)
	}
	put("catalog/../../evil", []byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	dir := filepath.Join(t.TempDir(), "catalog")
	snap, err := catalog.Import(&buf, []ed25519.PublicKey{pub}, dir)
	if err != nil || snap.Source != "upload" || len(snap.Index.Examples) == 0 {
		t.Fatalf("import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
		t.Fatal("path escaped")
	}
}

// Файлы плагина изменились, версия та же — сборка отказывает.
func TestBuildRequiresVersionBump(t *testing.T) {
	ix, _, err := catalog.Build(catalogtest.ExamplesDir(), catalog.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prev := *ix
	raw, _ := json.Marshal(prev)
	_ = json.Unmarshal(raw, &prev)
	f := prev.Plugins[0].Files["deploy/nkt-plugin.py"]
	f.SHA256 = strings.Repeat("0", 64)
	prev.Plugins[0].Files["deploy/nkt-plugin.py"] = f
	if _, _, err := catalog.Build(catalogtest.ExamplesDir(), catalog.BuildOptions{Prev: &prev}); err == nil || !strings.Contains(err.Error(), "raise version") {
		t.Fatalf("no version check: %v", err)
	}
	if _, _, err := catalog.Build(catalogtest.ExamplesDir(), catalog.BuildOptions{Failed: map[string]bool{"windows": true}}); err != nil {
		t.Fatal(err)
	}
}

// Подпись nkt проверяет и openssl (pkeyutl -verify -rawin).
func TestSignatureOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("no openssl")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "k.pem")
	if out, err := exec.Command("openssl", "genpkey", "-algorithm", "ed25519", "-out", key).CombinedOutput(); err != nil {
		t.Skip("openssl without ed25519:", string(out))
	}
	pemKey, _ := os.ReadFile(key)
	priv, err := catalog.ParsePrivateKey(pemKey)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"format":1}`)
	_ = os.WriteFile(filepath.Join(dir, "index.json"), data, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "index.json.sig"), catalog.Sign(priv, data), 0o644)
	pubOut, err := exec.Command("openssl", "pkey", "-in", key, "-pubout").Output()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := catalog.ParsePublicKeys(string(pubOut))
	if err != nil || catalog.Verify(data, catalog.Sign(priv, data), keys) != nil {
		t.Fatalf("parse pub: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "k.pub"), pubOut, 0o644)
	out, err := exec.Command("openssl", "pkeyutl", "-verify", "-pubin", "-inkey", filepath.Join(dir, "k.pub"), "-rawin", "-in", filepath.Join(dir, "index.json"), "-sigfile", filepath.Join(dir, "index.json.sig")).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "-rawin") {
		t.Fatalf("openssl verify: %v %s", err, out)
	}
	if len(catalog.DefaultKeys()) != 1 {
		t.Fatal("built-in key")
	}
}

// Ключ в секрете CI вставляют по-разному: целиком, в одну строку, с CRLF,
// только тело base64. Чужой тип — понятная ошибка без содержимого ключа.
func TestParsePrivateKeyForms(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	full := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	body := strings.TrimSpace(strings.SplitN(strings.SplitN(full, "-----\n", 2)[1], "-----END", 2)[0])
	for name, text := range map[string]string{
		"pem": full, "crlf": strings.ReplaceAll(full, "\n", "\r\n"), "one line": strings.ReplaceAll(full, "\n", " "),
		"body": body, "indented": "  " + strings.ReplaceAll(full, "\n", "\n  "),
	} {
		k, err := catalog.ParsePrivateKey([]byte(text))
		if err != nil || !k.Equal(priv) {
			t.Errorf("%s: %v", name, err)
		}
	}
	pubDER, _ := x509.MarshalPKIXPublicKey(priv.Public())
	pub := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	for text, want := range map[string]string{pub: "public key", "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----": "OpenSSH",
		"-----BEGIN ENCRYPTED PRIVATE KEY-----\nAAAA\n-----END ENCRYPTED PRIVATE KEY-----": "password", "not a key at all!": "not base64", "": "empty"} {
		if _, err := catalog.ParsePrivateKey([]byte(text)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", want, err)
		} else if strings.Contains(err.Error(), "AAAA") {
			t.Errorf("key material in error: %v", err)
		}
	}
}
