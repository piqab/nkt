// Package tlscert generates and persists a self-signed TLS certificate for
// nkt's own HTTP listener — separate from internal/control's certgen.go,
// which issues self-signed certificates for services on a managed host
// (nginx/haproxy) rather than for this process's own listener.
package tlscert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	bits = 2048
	// validityDays mirrors control.SelfSignedRequest's own default — no
	// ACME-style 398-day ceiling applies here, since nothing chains this
	// certificate to a trusted root, but there is no reason to diverge from
	// the convention the rest of the app already uses for a self-signed cert.
	validityDays = 397
	// renewalMargin is how close to NotAfter EnsureSelfSigned will still
	// treat an existing certificate as reusable — generous on purpose: this
	// certificate never renews itself while the process is running, only on
	// the next start, so it needs to survive a machine being off for a
	// while, not just a quick restart.
	renewalMargin = 30 * 24 * time.Hour
)

// EnsureSelfSigned makes sure certPath/keyPath hold a currently-valid
// self-signed certificate whose SAN set is exactly hosts (a mix of DNS
// names and/or IP literals). An existing pair is reused unmodified when it
// already matches — regenerating on every start would make a browser that
// already accepted this certificate's warning face a brand new fingerprint
// (and the warning again) for no reason. Otherwise it (re)generates and
// overwrites both files.
func EnsureSelfSigned(certPath, keyPath string, hosts []string) error {
	if len(hosts) == 0 {
		return fmt.Errorf("tlscert: не задан ни один адрес/имя для сертификата")
	}
	if reusable(certPath, hosts) {
		return nil
	}
	return generate(certPath, keyPath, hosts)
}

func reusable(certPath string, hosts []string) bool {
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if time.Until(cert.NotAfter) < renewalMargin {
		return false
	}
	return sameSANs(cert, hosts)
}

// sameSANs reports whether the certificate's DNS names and IP addresses,
// taken together, are exactly the set hosts names — not a subset in either
// direction, so removing a host from NKT_TLS_HOSTS also drops it from the
// certificate on the next start rather than leaving it there forever.
func sameSANs(cert *x509.Certificate, hosts []string) bool {
	got := make(map[string]bool, len(cert.DNSNames)+len(cert.IPAddresses))
	for _, n := range cert.DNSNames {
		got[n] = true
	}
	for _, ip := range cert.IPAddresses {
		got[ip.String()] = true
	}
	if len(got) != len(hosts) {
		return false
	}
	for _, h := range hosts {
		if !got[h] {
			return false
		}
	}
	return true
}

func generate(certPath, keyPath string, hosts []string) error {
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return fmt.Errorf("генерация ключа: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("генерация серийного номера: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0]},
		// A few minutes of backdating tolerates clock skew between this
		// host and whatever machine first connects — same margin
		// control.GenerateSelfSigned uses.
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(0, 0, validityDays),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("создание сертификата: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("сериализация ключа: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0o750); err != nil {
		return fmt.Errorf("создание каталога для сертификата: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
		return fmt.Errorf("создание каталога для ключа: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("запись сертификата: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("запись ключа: %w", err)
	}
	return nil
}
