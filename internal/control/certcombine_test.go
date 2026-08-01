package control

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/althq/netknownsthat/internal/store"
)

func TestListLetsEncryptLineages(t *testing.T) {
	m, _ := renewSetup(t)
	got, err := m.ListLetsEncryptLineages()
	if err != nil {
		t.Fatalf("список lineage: %v", err)
	}
	want := []string{"api.example.com", "app.example.com", "standalone.example.com"}
	if len(got) != len(want) {
		t.Fatalf("lineage: %+v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("lineage[%d].Name = %q, ожидалось %q (всё: %+v)", i, got[i].Name, want[i], got)
		}
		// Every fixture lineage's fullchain.pem is readable — including
		// api.example.com, whose privkey.pem is deliberately missing:
		// ListLetsEncryptLineages only needs the certificate for the expiry,
		// not the key.
		if !got[i].Known {
			t.Errorf("lineage[%d] (%s): Known = false, ожидалось true", i, got[i].Name)
		}
		if got[i].NotAfter.IsZero() {
			t.Errorf("lineage[%d] (%s): NotAfter не заполнен", i, got[i].Name)
		}
	}
}

func TestCombineForHAProxyNewFile(t *testing.T) {
	m, db := renewSetup(t)

	certPEM, err := m.c.ReadFile("/etc/letsencrypt/live/app.example.com/fullchain.pem")
	if err != nil {
		t.Fatalf("чтение сертификата: %v", err)
	}
	keyPEM, err := m.c.ReadFile("/etc/letsencrypt/live/app.example.com/privkey.pem")
	if err != nil {
		t.Fatalf("чтение ключа: %v", err)
	}

	res, err := m.CombineForHAProxy(context.Background(), "test", "app.example.com", "")
	if err != nil {
		t.Fatalf("combine: %v", err)
	}
	if res.Lineage != "app.example.com" {
		t.Errorf("Lineage = %q", res.Lineage)
	}
	if res.CombinedPath == "" || res.Snippet == "" {
		t.Fatalf("режим нового файла: ожидались непустые CombinedPath и Snippet, получено %+v", res)
	}

	block, _ := pem.Decode(certPEM)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("разбор сертификата: %v", err)
	}
	if !res.NotAfter.Equal(leaf.NotAfter.UTC()) {
		t.Errorf("NotAfter = %v, ожидалось %v", res.NotAfter, leaf.NotAfter.UTC())
	}

	got, err := m.c.ReadFile(res.CombinedPath)
	if err != nil {
		t.Fatalf("чтение собранного файла: %v", err)
	}
	want := append(append([]byte{}, certPEM...), keyPEM...)
	if string(got) != string(want) {
		t.Error("собранный файл не совпадает с исходными cert+key")
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "cert.combine_haproxy", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != res.CombinedPath {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

// TestCombineForHAProxyOverwritesKnownTarget covers the case this feature was
// reworked for: /etc/haproxy/certs-le/app.example.com.pem is a haproxy cert
// the last scan already found (see markDerivedCertbotCerts fixtures), built
// with a different lineage's key. Pointing combine at it by path must
// overwrite that exact file — not create a new one — and reload haproxy so
// nothing is left to paste in by hand.
func TestCombineForHAProxyOverwritesKnownTarget(t *testing.T) {
	m, db := renewSetup(t)

	const targetPath = "/etc/haproxy/certs-le/app.example.com.pem"
	knownPaths := m.ListHAProxyCertPaths()
	found := false
	for _, p := range knownPaths {
		if p == targetPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s не найден среди известных путей haproxy: %v", targetPath, knownPaths)
	}

	wantCert, err := m.c.ReadFile("/etc/letsencrypt/live/standalone.example.com/fullchain.pem")
	if err != nil {
		t.Fatalf("чтение сертификата: %v", err)
	}
	wantKey, err := m.c.ReadFile("/etc/letsencrypt/live/standalone.example.com/privkey.pem")
	if err != nil {
		t.Fatalf("чтение ключа: %v", err)
	}

	res, err := m.CombineForHAProxy(context.Background(), "test", "standalone.example.com", targetPath)
	if err != nil {
		t.Fatalf("combine: %v", err)
	}
	if res.CombinedPath != targetPath {
		t.Errorf("CombinedPath = %q, ожидалось %q", res.CombinedPath, targetPath)
	}
	if res.Snippet != "" {
		t.Errorf("режим перезаписи: Snippet должен быть пустым, получено %q", res.Snippet)
	}

	got, err := m.c.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("чтение перезаписанного файла: %v", err)
	}
	want := append(append([]byte{}, wantCert...), wantKey...)
	if string(got) != string(want) {
		t.Error("перезаписанный файл не совпадает с cert+key standalone.example.com")
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	var sawCombine, sawReload bool
	for _, e := range entries {
		if e.Action == "cert.combine_haproxy" && e.Target == targetPath && e.Result == "ok" {
			sawCombine = true
		}
		if e.Action == "service.reload" && e.Target == "haproxy" && e.Result == "ok" {
			sawReload = true
		}
	}
	if !sawCombine {
		t.Error("в журнале нет записи cert.combine_haproxy для " + targetPath)
	}
	if !sawReload {
		t.Error("в журнале нет записи service.reload для haproxy")
	}
}

func TestCombineForHAProxyRejectsUnknownTarget(t *testing.T) {
	m, _ := renewSetup(t)
	_, err := m.CombineForHAProxy(context.Background(), "test", "app.example.com", "/etc/haproxy/certs/nope.pem")
	if err == nil {
		t.Fatal("ожидалась ошибка для пути, которого нет среди текущих сертификатов haproxy")
	}
}

func TestCombineForHAProxyMissingKey(t *testing.T) {
	m, _ := renewSetup(t)
	// api.example.com is fixtures' orphan lineage: fullchain.pem exists,
	// privkey.pem deliberately does not.
	if _, err := m.CombineForHAProxy(context.Background(), "test", "api.example.com", ""); err == nil {
		t.Fatal("ожидалась ошибка при отсутствующем privkey.pem")
	}
}

func TestCombineForHAProxyRejectsBadLineage(t *testing.T) {
	m, _ := renewSetup(t)
	for _, lineage := range []string{"", "../../etc/passwd", "site;rm -rf /"} {
		if _, err := m.CombineForHAProxy(context.Background(), "test", lineage, ""); err == nil {
			t.Errorf("lineage %q: ожидалась ошибка валидации", lineage)
		}
	}
}
