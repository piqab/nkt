// Package secretbox encrypts small secrets (SSH credentials, remote session
// tokens) at rest with AES-256-GCM, for the hub's host registry. Nothing else
// in this codebase stores a secret that needs to survive being read back —
// user passwords are hashed, never decrypted — so this is the one place that
// needs reversible encryption at all.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// KeySize is the required master key length, in bytes.
const KeySize = 32

// GenerateKey returns a new random AES-256 key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("генерация ключа: %w", err)
	}
	return key, nil
}

// Encrypt seals plaintext under key, returning nonce||ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("генерация nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt.
func Decrypt(key, data []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("secretbox: слишком короткий шифротекст")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: ключ должен быть %d байт, получено %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ResolveKey returns the master key to use: envValue (base64) when set,
// otherwise a key persisted at keyFilePath — generated once and reused on
// every later start, so a hub deployed via docker-compose without an
// explicit NKT_HUB_MASTER_KEY still keeps working across restarts as long as
// its data volume survives.
func ResolveKey(envValue, keyFilePath string) ([]byte, error) {
	if envValue != "" {
		key, err := base64.StdEncoding.DecodeString(envValue)
		if err != nil {
			return nil, fmt.Errorf("NKT_HUB_MASTER_KEY: не удалось разобрать base64: %w", err)
		}
		if len(key) != KeySize {
			return nil, fmt.Errorf("NKT_HUB_MASTER_KEY: ожидается %d байт, получено %d", KeySize, len(key))
		}
		return key, nil
	}

	if raw, err := os.ReadFile(keyFilePath); err == nil {
		key, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil || len(key) != KeySize {
			return nil, fmt.Errorf("%s: повреждённый файл ключа", keyFilePath)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("чтение %s: %w", keyFilePath, err)
	}

	key, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFilePath, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("запись %s: %w", keyFilePath, err)
	}
	return key, nil
}
