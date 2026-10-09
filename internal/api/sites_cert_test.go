package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Сертификат сайта без certbot: готовый Let's Encrypt (режим «вручную») и
// свой файл — имена, ключ в том же файле или отдельно, истёкший.
func TestSiteCertModes(t *testing.T) {
	s, root := sitesServer(t)
	if name, days := s.siteLineage([]string{"shop.example.com"}, 0); name != "shop.example.com" || days < 70 {
		t.Errorf("lineage: %q %d", name, days)
	}
	if name, _ := s.siteLineage([]string{"shop.example.com", "www.shop.example.com"}, 0); name != "" {
		t.Errorf("не на все имена, а найден %q", name)
	}

	write := func(rel string, names []string, notAfter time.Time, withKey bool) string {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
			NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: notAfter}
		der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
		out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		if withKey {
			kb, _ := x509.MarshalECPrivateKey(key)
			out = append(out, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})...)
		}
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, out, 0o600)
		return "/" + rel
	}
	soon := time.Now().AddDate(0, 0, 40)
	combined := write("etc/ssl/app.pem", []string{"app.example.com", "*.app.example.com"}, soon, true)
	if days, err := s.siteCertFromFile(combined, "", []string{"app.example.com", "www.app.example.com"}); err != nil || days < 35 {
		t.Errorf("свой файл с ключом: %d %v", days, err)
	}
	noKey := write("etc/ssl/only.pem", []string{"app.example.com"}, soon, false)
	if _, err := s.siteCertFromFile(noKey, "", []string{"app.example.com"}); err == nil {
		t.Error("без ключа принят")
	}
	if _, err := s.siteCertFromFile(noKey, combined, []string{"app.example.com"}); err != nil {
		t.Errorf("ключ отдельно: %v", err)
	}
	if _, err := s.siteCertFromFile(combined, "", []string{"other.example.com"}); err == nil {
		t.Error("чужое имя принято")
	}
	old := write("etc/ssl/old.pem", []string{"app.example.com"}, time.Now().Add(-time.Hour), true)
	if _, err := s.siteCertFromFile(old, "", []string{"app.example.com"}); err == nil {
		t.Error("истёкший принят")
	}
	for _, c := range []struct{ cert, key string }{{"auto", "/x"}, {"../etc/x", ""}, {"/a b", ""}, {"/ok.pem", "rel.key"}} {
		if checkCert(c.cert, c.key) == nil {
			t.Errorf("принят режим %+v", c)
		}
	}
	for _, c := range []struct{ cert, key string }{{"", ""}, {"manual", ""}, {"auto", ""}, {"/etc/ssl/a.pem", "/etc/ssl/a.key"}} {
		if err := checkCert(c.cert, c.key); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
}
