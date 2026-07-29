package parse

import (
	"context"
	"testing"
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
