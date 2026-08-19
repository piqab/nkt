package tlscert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatalf("%s is not valid PEM", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func TestEnsureSelfSignedGeneratesCoveringDNSAndIPHosts(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	if err := EnsureSelfSigned(certPath, keyPath, []string{"my-host", "127.0.0.1", "::1"}); err != nil {
		t.Fatalf("EnsureSelfSigned: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file missing: %v", err)
	}

	cert := readCert(t, certPath)
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "my-host" {
		t.Errorf("DNSNames = %v, want [my-host]", cert.DNSNames)
	}
	if len(cert.IPAddresses) != 2 {
		t.Errorf("IPAddresses = %v, want 2 entries (127.0.0.1, ::1)", cert.IPAddresses)
	}
	if cert.Subject.CommonName != "my-host" {
		t.Errorf("CommonName = %q, want %q", cert.Subject.CommonName, "my-host")
	}
	if time.Until(cert.NotAfter) < 300*24*time.Hour {
		t.Errorf("NotAfter = %v, want at least ~397 days out", cert.NotAfter)
	}
}

func TestEnsureSelfSignedReusesMatchingCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	hosts := []string{"localhost", "127.0.0.1"}

	if err := EnsureSelfSigned(certPath, keyPath, hosts); err != nil {
		t.Fatalf("first EnsureSelfSigned: %v", err)
	}
	first := readCert(t, certPath)

	if err := EnsureSelfSigned(certPath, keyPath, hosts); err != nil {
		t.Fatalf("second EnsureSelfSigned: %v", err)
	}
	second := readCert(t, certPath)

	if first.SerialNumber.Cmp(second.SerialNumber) != 0 {
		t.Error("second call minted a new certificate for an unchanged host list — a browser that already accepted the first would see a fresh warning for no reason")
	}
}

func TestEnsureSelfSignedRegeneratesOnHostListChange(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	if err := EnsureSelfSigned(certPath, keyPath, []string{"localhost"}); err != nil {
		t.Fatalf("first EnsureSelfSigned: %v", err)
	}
	first := readCert(t, certPath)

	if err := EnsureSelfSigned(certPath, keyPath, []string{"localhost", "192.0.2.1"}); err != nil {
		t.Fatalf("second EnsureSelfSigned: %v", err)
	}
	second := readCert(t, certPath)

	if first.SerialNumber.Cmp(second.SerialNumber) == 0 {
		t.Error("adding a host to the SAN list did not regenerate the certificate")
	}
	if len(second.IPAddresses) != 1 || second.IPAddresses[0].String() != "192.0.2.1" {
		t.Errorf("IPAddresses = %v, want [192.0.2.1]", second.IPAddresses)
	}
}

// TestEnsureSelfSignedRegeneratesNearExpiry writes a certificate directly
// (bypassing generate's fixed validityDays) whose SAN set matches but whose
// NotAfter already falls inside renewalMargin — the state an old
// EnsureSelfSigned-generated file would eventually reach with enough real
// time passed — and confirms a fresh call replaces it rather than treating
// a matching SAN set as reason enough to reuse it.
func TestEnsureSelfSignedRegeneratesNearExpiry(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	hosts := []string{"localhost"}

	oldSerial := writeCertWithExpiry(t, certPath, keyPath, hosts, time.Now().Add(renewalMargin-time.Hour))

	if err := EnsureSelfSigned(certPath, keyPath, hosts); err != nil {
		t.Fatalf("EnsureSelfSigned: %v", err)
	}
	got := readCert(t, certPath)
	if got.SerialNumber.Cmp(oldSerial) == 0 {
		t.Error("a certificate inside its renewal margin was reused instead of regenerated")
	}
	if time.Until(got.NotAfter) < renewalMargin {
		t.Errorf("regenerated NotAfter = %v, still inside the renewal margin", got.NotAfter)
	}
}

// writeCertWithExpiry writes a minimal self-signed certificate covering
// hosts with an explicit notAfter, for setting up TestEnsureSelfSigned...
// tests that need a specific expiry EnsureSelfSigned's own generate()
// (fixed validityDays) can't produce directly. Returns its serial number.
func writeCertWithExpiry(t *testing.T, certPath, keyPath string, hosts []string, notAfter time.Time) *big.Int {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hosts[0]},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              hosts,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return serial
}

func TestEnsureSelfSignedRejectsEmptyHostList(t *testing.T) {
	dir := t.TempDir()
	err := EnsureSelfSigned(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"), nil)
	if err == nil {
		t.Fatal("expected an error for an empty host list, got nil")
	}
}
