package hub

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

// fakeLeafCert is a syntactically valid (if minimal) DER certificate good
// enough for x509.ParseCertificate to accept — verifyPinnedTunnelCert only
// needs a certificate it can parse and hash, not one that would pass real
// chain validation. ECDSA/P256 purely for speed: several of these get
// generated per test run, and unlike internal/tlscert's own test helper
// (which needs an RSA cert matching what EnsureSelfSigned actually
// produces), nothing here cares which key algorithm was used.
func fakeLeafCert(t *testing.T, commonName string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return der
}

func TestVerifyPinnedTunnelCert(t *testing.T) {
	t.Run("no certs presented", func(t *testing.T) {
		_, err := verifyPinnedTunnelCert(nil, nil)
		if err == nil {
			t.Fatal("verifyPinnedTunnelCert(nil, nil) = nil error, want one")
		}
	})

	t.Run("unparseable certificate", func(t *testing.T) {
		_, err := verifyPinnedTunnelCert([][]byte{[]byte("not a certificate")}, nil)
		if err == nil {
			t.Fatal("verifyPinnedTunnelCert with garbage bytes = nil error, want one")
		}
	})

	t.Run("nothing pinned yet: accepts and returns the fingerprint (TOFU)", func(t *testing.T) {
		der := fakeLeafCert(t, "host-a")
		fp, err := verifyPinnedTunnelCert([][]byte{der}, nil)
		if err != nil {
			t.Fatalf("verifyPinnedTunnelCert with no pin = %v, want nil error", err)
		}
		want := sha256.Sum256(der)
		if !bytes.Equal(fp, want[:]) {
			t.Errorf("fingerprint = %x, want %x", fp, want)
		}
	})

	t.Run("matches the pin: accepted", func(t *testing.T) {
		der := fakeLeafCert(t, "host-b")
		sum := sha256.Sum256(der)
		fp, err := verifyPinnedTunnelCert([][]byte{der}, sum[:])
		if err != nil {
			t.Fatalf("verifyPinnedTunnelCert with matching pin = %v, want nil error", err)
		}
		if !bytes.Equal(fp, sum[:]) {
			t.Errorf("fingerprint = %x, want %x", fp, sum)
		}
	})

	t.Run("does not match the pin: rejected with tunnelCertMismatchError", func(t *testing.T) {
		pinned := sha256.Sum256(fakeLeafCert(t, "host-old"))
		der := fakeLeafCert(t, "host-new")
		_, err := verifyPinnedTunnelCert([][]byte{der}, pinned[:])
		if err == nil {
			t.Fatal("verifyPinnedTunnelCert with mismatched pin = nil error, want one")
		}
		var mismatch *tunnelCertMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("verifyPinnedTunnelCert error = %T, want *tunnelCertMismatchError", err)
		}
	})
}
