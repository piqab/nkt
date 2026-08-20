package secretbox

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptWithPasswordRoundTrip(t *testing.T) {
	plaintext := []byte(`{"version":1,"hosts":[{"name":"h1"}],"master_key":"deadbeef"}`)
	envelope, err := EncryptWithPassword("correct horse battery staple", plaintext)
	if err != nil {
		t.Fatalf("EncryptWithPassword: %v", err)
	}
	if bytes.Contains(envelope, plaintext) {
		t.Fatal("envelope contains the plaintext verbatim — not actually encrypted")
	}

	got, err := DecryptWithPassword("correct horse battery staple", envelope)
	if err != nil {
		t.Fatalf("DecryptWithPassword: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypted = %q, want %q", got, plaintext)
	}
}

func TestDecryptWithPasswordWrongPasswordFails(t *testing.T) {
	envelope, err := EncryptWithPassword("right-password", []byte("secret data"))
	if err != nil {
		t.Fatalf("EncryptWithPassword: %v", err)
	}
	if _, err := DecryptWithPassword("wrong-password", envelope); err == nil {
		t.Error("DecryptWithPassword with the wrong password succeeded, want an error")
	}
}

func TestEncryptWithPasswordRejectsEmptyPassword(t *testing.T) {
	if _, err := EncryptWithPassword("", []byte("x")); err == nil {
		t.Error("EncryptWithPassword(\"\", ...) succeeded, want an error")
	}
}

func TestIsPasswordEncryptedDistinguishesPlainJSON(t *testing.T) {
	envelope, err := EncryptWithPassword("pw", []byte("x"))
	if err != nil {
		t.Fatalf("EncryptWithPassword: %v", err)
	}
	if !IsPasswordEncrypted(envelope) {
		t.Error("IsPasswordEncrypted(envelope) = false, want true")
	}

	plainJSON := []byte(`{"version":1,"hosts":[]}`)
	if IsPasswordEncrypted(plainJSON) {
		t.Error("IsPasswordEncrypted(plain JSON) = true, want false")
	}
	if IsPasswordEncrypted(nil) {
		t.Error("IsPasswordEncrypted(nil) = true, want false")
	}
}

func TestDecryptWithPasswordRejectsNonEnvelope(t *testing.T) {
	if _, err := DecryptWithPassword("pw", []byte(`{"version":1}`)); err == nil {
		t.Error("DecryptWithPassword on plain JSON succeeded, want an error")
	}
}

// TestEncryptWithPasswordUsesFreshSaltEachTime confirms two encryptions of
// the same plaintext under the same password produce different envelopes —
// a fixed salt would make two exports of an unchanged host list
// distinguishable (or worse, comparable) by anyone who only sees ciphertext.
func TestEncryptWithPasswordUsesFreshSaltEachTime(t *testing.T) {
	plaintext := []byte("same plaintext both times")
	a, err := EncryptWithPassword("pw", plaintext)
	if err != nil {
		t.Fatalf("EncryptWithPassword: %v", err)
	}
	b, err := EncryptWithPassword("pw", plaintext)
	if err != nil {
		t.Fatalf("EncryptWithPassword: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Error("two encryptions of the same plaintext/password produced identical envelopes")
	}
}
