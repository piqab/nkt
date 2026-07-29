package analyze

import (
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/model"
)

// cert builds a certificate that is healthy in every respect except what the
// caller changes, so each test states exactly one thing.
func cert(mutate func(*model.Certificate)) model.Certificate {
	c := model.Certificate{
		ID:        "cert:/etc/ssl/site.pem",
		Path:      "/etc/ssl/site.pem",
		Service:   model.ServiceNginx,
		Endpoints: []string{"0.0.0.0:443"},
		Sites:     []string{"site.example.com"},
		Names:     []string{"site.example.com"},
		Subject:   "CN=site.example.com",
		Issuer:    "CN=Example CA",
		NotBefore: time.Now().Add(-30 * 24 * time.Hour),
		NotAfter:  time.Now().Add(90 * 24 * time.Hour),
		DaysLeft:  90,

		KeyAlgorithm: "RSA",
		KeyBits:      2048,
		SigAlgorithm: "SHA256-RSA",
		ChainLength:  2,
		Renewal:      model.RenewalInfo{Tool: "certbot", Managed: true, Automatic: true},
	}
	if mutate != nil {
		mutate(&c)
	}
	return c
}

func certFindings(c model.Certificate) map[string][]model.Finding {
	return rules(Run(&model.Snapshot{Certs: []model.Certificate{c}}))
}

// Expiry thresholds cannot be covered by static fixture files — a certificate
// with a fixed date drifts through every band as time passes — so they are
// checked here against certificates built relative to now.
func TestCertificateExpiryBands(t *testing.T) {
	cases := []struct {
		name     string
		days     int
		rule     string
		severity string
	}{
		{"здоровый", 90, "", ""},
		{"на границе окна", 31, "", ""},
		{"истекает в течение месяца", 25, "tls-cert-expiring", model.SeverityMedium},
		{"истекает на этой неделе", 5, "tls-cert-expiring", model.SeverityHigh},
		{"истекает завтра", 1, "tls-cert-expiring", model.SeverityHigh},
		{"просрочен", -2, "tls-cert-expired", model.SeverityCritical},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			days := c.days
			got := certFindings(cert(func(x *model.Certificate) {
				x.DaysLeft = days
				x.NotAfter = time.Now().Add(time.Duration(days) * 24 * time.Hour)
			}))

			if c.rule == "" {
				for _, r := range []string{"tls-cert-expiring", "tls-cert-expired"} {
					if len(got[r]) > 0 {
						t.Errorf("для %d дней не должно быть находки %s: %+v", days, r, got[r])
					}
				}
				return
			}
			list := got[c.rule]
			if len(list) != 1 {
				t.Fatalf("для %d дней ожидалась одна находка %s, получено %d", days, c.rule, len(list))
			}
			if list[0].Severity != c.severity {
				t.Errorf("серьёзность = %s, ожидалась %s", list[0].Severity, c.severity)
			}
		})
	}
}

// A certificate whose automation exists but never runs is the exact path to an
// outage that everyone believed was handled.
func TestRenewalWithoutTrigger(t *testing.T) {
	got := certFindings(cert(func(x *model.Certificate) {
		x.Renewal = model.RenewalInfo{
			Tool: "certbot", Managed: true, Automatic: false,
			Detail: "таймер certbot.timer не активен",
		}
	}))
	if len(got["tls-cert-renewal-not-automatic"]) != 1 {
		t.Errorf("ожидалась находка о неработающем автообновлении, получено %v", keysOf(got))
	}

	// Working automation must stay quiet.
	if got := certFindings(cert(nil)); len(got["tls-cert-renewal-not-automatic"]) != 0 {
		t.Error("при работающем автообновлении находки быть не должно")
	}
}

func TestCertificateNameMismatch(t *testing.T) {
	got := certFindings(cert(func(x *model.Certificate) {
		x.Sites = []string{"site.example.com", "shop.example.com"}
	}))
	list := got["tls-cert-name-mismatch"]
	if len(list) != 1 {
		t.Fatalf("ожидалась одна находка о несоответствии имени, получено %d", len(list))
	}
	if list[0].Object != "shop.example.com" {
		t.Errorf("объект = %q, ожидался shop.example.com", list[0].Object)
	}
}

// A wildcard covers one label and no more; that is what the rule must encode.
func TestWildcardCoverage(t *testing.T) {
	wildcard := cert(func(x *model.Certificate) {
		x.Names = []string{"*.example.com"}
		x.Sites = []string{"shop.example.com"}
	})
	if !wildcard.CoversName("shop.example.com") {
		t.Error("*.example.com должен покрывать shop.example.com")
	}
	if wildcard.CoversName("deep.shop.example.com") {
		t.Error("*.example.com не должен покрывать вложенный поддомен")
	}
	if wildcard.CoversName("example.com") {
		t.Error("*.example.com не должен покрывать сам домен")
	}
	if len(certFindings(wildcard)["tls-cert-name-mismatch"]) != 0 {
		t.Error("для подходящего wildcard находки быть не должно")
	}
}

func TestWeakKeyAndSignature(t *testing.T) {
	got := certFindings(cert(func(x *model.Certificate) {
		x.KeyBits = 1024
		x.SigAlgorithm = "SHA1-RSA"
	}))
	if len(got["tls-cert-weak-key"]) != 1 {
		t.Error("слабый ключ RSA 1024 должен быть найден")
	}
	if len(got["tls-cert-weak-signature"]) != 1 {
		t.Error("подпись SHA-1 должна быть найдена")
	}

	// ECDSA P-256 is strong despite the small number.
	if got := certFindings(cert(func(x *model.Certificate) {
		x.KeyAlgorithm, x.KeyBits = "ECDSA", 256
	})); len(got["tls-cert-weak-key"]) != 0 {
		t.Error("ECDSA 256 бит не должен считаться слабым")
	}
}

func TestUnreadableCertificate(t *testing.T) {
	got := certFindings(cert(func(x *model.Certificate) {
		x.Error = "файл недоступен: no such file or directory"
		x.Names = nil
	}))
	list := got["tls-cert-unreadable"]
	if len(list) != 1 {
		t.Fatalf("ожидалась находка о нечитаемом сертификате, получено %v", keysOf(got))
	}
	if !strings.Contains(list[0].Title, "site.example.com") {
		t.Errorf("заголовок должен называть сайт, а не имя файла: %q", list[0].Title)
	}
	// Nothing else should be reported about a certificate we could not read.
	for _, rule := range []string{"tls-cert-expired", "tls-cert-expiring", "tls-cert-weak-key"} {
		if len(got[rule]) > 0 {
			t.Errorf("для нечитаемого сертификата не должно быть %s", rule)
		}
	}
}

func keysOf(m map[string][]model.Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
