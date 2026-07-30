package parse

import (
	"context"
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/model"
)

// TestCertificatesExpandsHAProxyCrtDirectory covers haproxy's "bind ... crt
// <dir>" form: the crt argument names a directory of certificate bundles
// picked by SNI, not a single PEM file, and each bundle must be checked on
// its own.
func TestCertificatesExpandsHAProxyCrtDirectory(t *testing.T) {
	c := fixtureCollector(t)
	hap := HAProxy(context.Background(), c, "/etc/haproxy/haproxy.cfg")
	if hap.Status.Error != "" {
		t.Fatalf("парсер haproxy вернул ошибку: %s", hap.Status.Error)
	}

	var found bool
	for _, e := range hap.Endpoints {
		if e.Port == 8443 {
			found = true
			if !e.TLS {
				t.Errorf("fe_https:8443 должен быть отмечен как TLS")
			}
			if e.Extra["ssl_certificate"] != "/etc/haproxy/certs" {
				t.Errorf("ssl_certificate=%q, ожидался каталог /etc/haproxy/certs", e.Extra["ssl_certificate"])
			}
		}
	}
	if !found {
		t.Fatal("не найден endpoint fe_https:8443")
	}

	res := Certificates(context.Background(), c, hap.Endpoints)

	var fromDir []string
	for _, cert := range res.Certs {
		if cert.Error != "" {
			t.Errorf("сертификат %s: неожиданная ошибка %q", cert.Path, cert.Error)
			continue
		}
		if cert.Path == "/etc/haproxy/certs/api.internal.pem" || cert.Path == "/etc/haproxy/certs/app.internal.pem" {
			fromDir = append(fromDir, cert.Path)
		}
		if cert.Path == "/etc/haproxy/certs/app.internal.pem.key" || cert.Path == "/etc/haproxy/certs" {
			t.Errorf("companion-файл или сам каталог не должны попадать в список сертификатов: %s", cert.Path)
		}
	}
	if len(fromDir) != 2 {
		t.Fatalf("ожидалось 2 сертификата из каталога /etc/haproxy/certs (без .key), получено %d: %v",
			len(fromDir), fromDir)
	}
}

// TestCertificatesDetectsCertbotDerivedHAProxyCert covers the case that
// prompted markDerivedCertbotCerts: haproxy's "crt" wants certificate and key
// in one file, so a certbot deploy-hook typically concatenates
// fullchain.pem+privkey.pem from an nginx-facing lineage into a copy living
// outside /etc/letsencrypt. That copy must not be reported as "manual"
// renewal just because of its path — its leaf certificate is byte-identical
// to the /etc/letsencrypt/live source, and the app is expected to notice.
func TestCertificatesDetectsCertbotDerivedHAProxyCert(t *testing.T) {
	c := fixtureCollector(t)
	nginx := Nginx(context.Background(), c, "/etc/nginx/nginx.conf")
	if nginx.Status.Error != "" {
		t.Fatalf("парсер nginx вернул ошибку: %s", nginx.Status.Error)
	}
	hap := HAProxy(context.Background(), c, "/etc/haproxy/haproxy.cfg")
	if hap.Status.Error != "" {
		t.Fatalf("парсер haproxy вернул ошибку: %s", hap.Status.Error)
	}
	endpoints := append(append([]model.Endpoint{}, nginx.Endpoints...), hap.Endpoints...)

	res := Certificates(context.Background(), c, endpoints)
	byPath := map[string]model.Certificate{}
	for _, cert := range res.Certs {
		byPath[cert.Path] = cert
	}

	source, ok := byPath["/etc/letsencrypt/live/app.example.com/fullchain.pem"]
	if !ok {
		t.Fatal("исходный сертификат app.example.com не найден — тест не может ничего проверить")
	}

	derived, ok := byPath["/etc/haproxy/certs-le/app.example.com.pem"]
	if !ok {
		t.Fatal("производный сертификат haproxy не найден")
	}
	if derived.Error != "" {
		t.Fatalf("производный сертификат: неожиданная ошибка %q", derived.Error)
	}
	if derived.Fingerprint != source.Fingerprint {
		t.Fatalf("отпечатки не совпадают: производный %q, исходный %q", derived.Fingerprint, source.Fingerprint)
	}
	if !derived.Renewal.Derived {
		t.Error("ожидался Renewal.Derived = true")
	}
	if derived.Renewal.SourcePath != source.Path {
		t.Errorf("Renewal.SourcePath = %q, ожидалось %q", derived.Renewal.SourcePath, source.Path)
	}
	if derived.Renewal.Tool != "certbot" || !derived.Renewal.Managed {
		t.Errorf("производный сертификат должен унаследовать Tool=certbot/Managed=true от источника: %+v",
			derived.Renewal)
	}
	if derived.Renewal.Lineage != source.Renewal.Lineage {
		t.Errorf("Lineage = %q, ожидалось %q", derived.Renewal.Lineage, source.Renewal.Lineage)
	}
}

// TestCertificatesDetectsCertbotDerivedCertWithoutAnyDirectReference is the
// scenario the fingerprint-vs-parsed-certs version of markDerivedCertbotCerts
// missed: nothing in nginx or haproxy configuration ever references
// /etc/letsencrypt/live/<lineage> directly — only the haproxy combined copy
// is. In that case Certificates() never reads the original file as part of
// its normal endpoint walk, so there is no in-memory "source" certificate to
// compare against. Detection must come from reading the lineage's own
// certificate off disk directly (renewalIndex.fingerprints), not from
// whatever this particular scan happened to parse.
func TestCertificatesDetectsCertbotDerivedCertWithoutAnyDirectReference(t *testing.T) {
	c := fixtureCollector(t)
	hap := HAProxy(context.Background(), c, "/etc/haproxy/haproxy.cfg")
	if hap.Status.Error != "" {
		t.Fatalf("парсер haproxy вернул ошибку: %s", hap.Status.Error)
	}

	// Only haproxy endpoints — deliberately excludes nginx, so nothing in
	// this call ever points at /etc/letsencrypt/live/app.example.com.
	res := Certificates(context.Background(), c, hap.Endpoints)

	for _, cert := range res.Certs {
		if strings.HasPrefix(cert.Path, letsEncryptLive) {
			t.Fatalf("в списке не должно быть прямых ссылок на /etc/letsencrypt/live в этом сценарии, "+
				"нашёлся: %s", cert.Path)
		}
	}

	var derived *model.Certificate
	for i := range res.Certs {
		if res.Certs[i].Path == "/etc/haproxy/certs-le/app.example.com.pem" {
			derived = &res.Certs[i]
		}
	}
	if derived == nil {
		t.Fatal("производный сертификат haproxy не найден")
	}
	if derived.Error != "" {
		t.Fatalf("производный сертификат: неожиданная ошибка %q", derived.Error)
	}
	if !derived.Renewal.Derived {
		t.Fatal("ожидался Renewal.Derived = true даже без прямой ссылки на /etc/letsencrypt/live")
	}
	if derived.Renewal.Lineage != "app.example.com" {
		t.Errorf("Lineage = %q, ожидалось app.example.com", derived.Renewal.Lineage)
	}
	if derived.Renewal.SourcePath != letsEncryptLive+"app.example.com/fullchain.pem" {
		t.Errorf("SourcePath = %q", derived.Renewal.SourcePath)
	}
	if !derived.Renewal.Managed {
		t.Error("app.example.com — управляемая lineage (есть renewal.conf), ожидалось Managed = true")
	}
}
