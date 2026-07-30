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
		t.Fatalf("lineage: %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("lineage[%d] = %q, ожидалось %q (всё: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCombineForHAProxySuccess(t *testing.T) {
	m, db := renewSetup(t)

	certPEM, err := m.c.ReadFile("/etc/letsencrypt/live/app.example.com/fullchain.pem")
	if err != nil {
		t.Fatalf("чтение сертификата: %v", err)
	}
	keyPEM, err := m.c.ReadFile("/etc/letsencrypt/live/app.example.com/privkey.pem")
	if err != nil {
		t.Fatalf("чтение ключа: %v", err)
	}

	res, err := m.CombineForHAProxy(context.Background(), "test", "app.example.com")
	if err != nil {
		t.Fatalf("combine: %v", err)
	}
	if res.Lineage != "app.example.com" {
		t.Errorf("Lineage = %q", res.Lineage)
	}
	if res.CombinedPath == "" || res.Snippet == "" {
		t.Fatalf("ожидались непустые CombinedPath и Snippet, получено %+v", res)
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
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "app.example.com" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

func TestCombineForHAProxyMissingKey(t *testing.T) {
	m, _ := renewSetup(t)
	// api.example.com is fixtures' orphan lineage: fullchain.pem exists,
	// privkey.pem deliberately does not.
	if _, err := m.CombineForHAProxy(context.Background(), "test", "api.example.com"); err == nil {
		t.Fatal("ожидалась ошибка при отсутствующем privkey.pem")
	}
}

func TestCombineForHAProxyRejectsBadLineage(t *testing.T) {
	m, _ := renewSetup(t)
	for _, lineage := range []string{"", "../../etc/passwd", "site;rm -rf /"} {
		if _, err := m.CombineForHAProxy(context.Background(), "test", lineage); err == nil {
			t.Errorf("lineage %q: ожидалась ошибка валидации", lineage)
		}
	}
}
