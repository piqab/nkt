package control

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"path/filepath"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/store"
)

func certgenSetup(t *testing.T) (*CertManager, collect.Collector) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{NginxRoot: "/etc/nginx", HAProxyRoot: "/etc/haproxy"}
	c := collect.NewFixtures(root)
	db, err := store.Open(filepath.Join(root, "nkt.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewCertManager(cfg, c, db), c
}

func TestGenerateSelfSignedNginx(t *testing.T) {
	m, c := certgenSetup(t)
	res, err := m.GenerateSelfSigned(context.Background(), "test", SelfSignedRequest{
		Names:   []string{"Site.Example.com", "www.site.example.com"},
		Service: "nginx",
	})
	if err != nil {
		t.Fatalf("генерация: %v", err)
	}
	if res.CertPath == "" || res.KeyPath == "" {
		t.Fatalf("ожидались отдельные пути сертификата и ключа для nginx, получено %+v", res)
	}
	if res.CombinedPath != "" {
		t.Error("для nginx не должно быть объединённого файла")
	}

	raw, err := c.ReadFile(res.CertPath)
	if err != nil {
		t.Fatalf("чтение сертификата: %v", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("сертификат не в формате PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("разбор сертификата: %v", err)
	}
	if leaf.Subject.CommonName != "site.example.com" {
		t.Errorf("CN = %q, ожидалось имя в нижнем регистре", leaf.Subject.CommonName)
	}
	if len(leaf.DNSNames) != 2 {
		t.Errorf("DNSNames = %v, ожидалось 2 имени", leaf.DNSNames)
	}

	sum := sha256.Sum256(leaf.Raw)
	if hex.EncodeToString(sum[:]) != res.Fingerprint {
		t.Error("отпечаток в результате не совпадает с фактическим сертификатом")
	}

	keyRaw, err := c.ReadFile(res.KeyPath)
	if err != nil {
		t.Fatalf("чтение ключа: %v", err)
	}
	keyBlock, _ := pem.Decode(keyRaw)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" {
		t.Fatal("ключ не в ожидаемом формате PEM")
	}
	if _, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err != nil {
		t.Fatalf("ключ не разбирается: %v", err)
	}
}

func TestGenerateSelfSignedHAProxyCombinesCertAndKey(t *testing.T) {
	m, c := certgenSetup(t)
	res, err := m.GenerateSelfSigned(context.Background(), "test", SelfSignedRequest{
		Names: []string{"internal.example.com"}, Service: "haproxy",
	})
	if err != nil {
		t.Fatalf("генерация: %v", err)
	}
	if res.CombinedPath == "" {
		t.Fatal("для haproxy ожидался объединённый файл")
	}
	if res.CertPath != "" || res.KeyPath != "" {
		t.Error("для haproxy не должно быть раздельных путей")
	}

	raw, err := c.ReadFile(res.CombinedPath)
	if err != nil {
		t.Fatalf("чтение объединённого файла: %v", err)
	}
	var certBlocks, keyBlocks int
	var firstType string
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if firstType == "" {
			firstType = block.Type
		}
		switch block.Type {
		case "CERTIFICATE":
			certBlocks++
		case "PRIVATE KEY":
			keyBlocks++
		}
	}
	if certBlocks != 1 || keyBlocks != 1 {
		t.Errorf("объединённый файл: сертификатов %d, ключей %d, ожидалось по одному", certBlocks, keyBlocks)
	}
	if firstType != "CERTIFICATE" {
		t.Errorf("haproxy требует сертификат первым в файле, получено %q первым блоком", firstType)
	}
}

func TestGenerateSelfSignedValidation(t *testing.T) {
	m, _ := certgenSetup(t)
	cases := map[string]SelfSignedRequest{
		"нет имён":            {Names: nil, Service: "nginx"},
		"недопустимое имя":    {Names: []string{"bad name with spaces"}, Service: "nginx"},
		"неизвестный сервис":  {Names: []string{"site.example.com"}, Service: "caddy"},
		"слабый ключ":         {Names: []string{"site.example.com"}, Service: "nginx", Bits: 1024},
		"слишком долгий срок": {Names: []string{"site.example.com"}, Service: "nginx", Days: 9000},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := m.GenerateSelfSigned(context.Background(), "test", req); err == nil {
				t.Errorf("%+v: ожидалась ошибка валидации", req)
			}
		})
	}
}

// Defaults must be sane without the caller specifying anything beyond names
// and service.
func TestGenerateSelfSignedDefaults(t *testing.T) {
	m, _ := certgenSetup(t)
	res, err := m.GenerateSelfSigned(context.Background(), "test", SelfSignedRequest{
		Names: []string{"defaults.example.com"}, Service: "nginx",
	})
	if err != nil {
		t.Fatalf("генерация: %v", err)
	}
	daysLeft := int(time.Until(res.NotAfter).Hours() / 24)
	if daysLeft < defaultDays-1 || daysLeft > defaultDays+1 {
		t.Errorf("срок действия по умолчанию: %d дней, ожидалось около %d", daysLeft, defaultDays)
	}
}

// Wildcard names must survive validation and directory-name construction.
func TestGenerateSelfSignedWildcard(t *testing.T) {
	m, c := certgenSetup(t)
	res, err := m.GenerateSelfSigned(context.Background(), "test", SelfSignedRequest{
		Names: []string{"*.internal.example.com"}, Service: "nginx",
	})
	if err != nil {
		t.Fatalf("генерация с wildcard-именем: %v", err)
	}
	if !c.Exists(res.CertPath) {
		t.Fatalf("файл сертификата не создан: %s", res.CertPath)
	}
}
