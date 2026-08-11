package secretbox

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	plaintext := []byte("hunter2 supersecret ssh key material")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext must not equal plaintext")
	}

	got, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()

	ciphertext, err := Encrypt(key1, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(key2, ciphertext); err == nil {
		t.Fatal("expected decryption with the wrong key to fail")
	}
}

func TestResolveKeyFromEnv(t *testing.T) {
	key, _ := GenerateKey()
	encoded := base64.StdEncoding.EncodeToString(key)

	got, err := ResolveKey(encoded, filepath.Join(t.TempDir(), "unused.key"))
	if err != nil {
		t.Fatalf("ResolveKey: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatal("ResolveKey did not return the env-provided key")
	}
}

func TestResolveKeyGeneratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.key")

	first, err := ResolveKey("", path)
	if err != nil {
		t.Fatalf("ResolveKey (generate): %v", err)
	}
	if len(first) != KeySize {
		t.Fatalf("got key of length %d, want %d", len(first), KeySize)
	}

	second, err := ResolveKey("", path)
	if err != nil {
		t.Fatalf("ResolveKey (reload): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("ResolveKey did not reuse the persisted key across calls")
	}
}
