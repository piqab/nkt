package secretbox

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

// passwordMagic tags a password-encrypted envelope (see EncryptWithPassword)
// so IsPasswordEncrypted can tell one apart from plain JSON without needing
// the password — a hub export file is plain JSON by default, and callers
// (nkt hub import) need to know which to expect before they can even ask
// for a password.
var passwordMagic = [4]byte{'N', 'K', 'T', '1'}

const (
	saltSize = 16
	// scrypt "interactive" parameters (Percival's original recommendation
	// for a login-time cost, roughly): strong enough to make brute-forcing
	// a weak password meaningfully expensive, cheap enough not to make
	// decrypting a hub export file annoying on ordinary hardware.
	scryptN = 1 << 15
	scryptR = 8
	scryptP = 1
)

// EncryptWithPassword derives a key from password (scrypt, random salt) and
// seals plaintext under it with AES-256-GCM, returning a single
// self-describing envelope: magic || salt || nonce || ciphertext. Meant for
// data that has to sit on disk as an ordinary file rather than behind an
// already-authenticated API — a hub export (see internal/hub.ExportHosts),
// which is plain JSON otherwise and, with the master key embedded, is on
// its own enough to decrypt every managed host's secrets.
func EncryptWithPassword(password string, plaintext []byte) ([]byte, error) {
	if password == "" {
		return nil, errors.New("secretbox: пароль не должен быть пустым")
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("генерация соли: %w", err)
	}
	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, KeySize)
	if err != nil {
		return nil, fmt.Errorf("вычисление ключа из пароля: %w", err)
	}

	sealed, err := Encrypt(key, plaintext) // nonce||ciphertext, see secretbox.go
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(passwordMagic)+len(salt)+len(sealed))
	out = append(out, passwordMagic[:]...)
	out = append(out, salt...)
	out = append(out, sealed...)
	return out, nil
}

// IsPasswordEncrypted reports whether data starts with an
// EncryptWithPassword envelope's magic bytes — enough to route a hub export
// file to the right decode path without needing (or trying) a password
// first. A false negative just means "definitely not this format", not
// "corrupt": ordinary JSON never happens to start with these four bytes.
func IsPasswordEncrypted(data []byte) bool {
	return len(data) >= len(passwordMagic) && subtle.ConstantTimeCompare(data[:len(passwordMagic)], passwordMagic[:]) == 1
}

// DecryptWithPassword reverses EncryptWithPassword.
func DecryptWithPassword(password string, envelope []byte) ([]byte, error) {
	if !IsPasswordEncrypted(envelope) {
		return nil, errors.New("secretbox: не похоже на файл, зашифрованный паролем")
	}
	rest := envelope[len(passwordMagic):]
	if len(rest) < saltSize {
		return nil, errors.New("secretbox: повреждён (слишком короткий) зашифрованный файл")
	}
	salt, sealed := rest[:saltSize], rest[saltSize:]

	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, KeySize)
	if err != nil {
		return nil, fmt.Errorf("вычисление ключа из пароля: %w", err)
	}
	plaintext, err := Decrypt(key, sealed)
	if err != nil {
		return nil, errors.New("secretbox: неверный пароль или повреждённый файл")
	}
	return plaintext, nil
}
