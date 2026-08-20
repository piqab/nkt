package secretbox

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

// passwordMagic tags a password-encrypted envelope (see EncryptWithPassword)
// so IsPasswordEncrypted can tell one apart from plain JSON without needing
// the password — a hub export file is plain JSON by default, and callers
// (nkt hub import, the web UI's own import) need to know which to expect
// before they can even ask for a password.
var passwordMagic = [4]byte{'N', 'K', 'T', '1'}

const (
	saltSize = 16
	// defaultIterations follows OWASP's 2023 recommendation for PBKDF2-
	// HMAC-SHA256 (at least 600,000). Stored in the envelope itself, not
	// hardcoded on the decrypt side — so this can be raised later without
	// making an older export file unreadable.
	defaultIterations = 600_000
	pbkdf2KeyLen      = KeySize // 32 bytes, AES-256

	// PBKDF2 (not scrypt/argon2) specifically because it's what the export
	// file needs to round-trip through TWO independent implementations: the
	// CLI (this package) and the browser's own Web Crypto SubtleCrypto API
	// for the web UI's "экспорт с ключом" — which only has PBKDF2 built in
	// natively (no scrypt/argon2 without an extra JS dependency). Encrypting
	// in one and decrypting in the other only works if both derive the
	// identical key from the identical envelope fields.
	envelopeHeaderSize = len(passwordMagic) + saltSize + 4 // magic + salt + iterations(uint32 BE)
)

// EncryptWithPassword derives a key from password (PBKDF2-HMAC-SHA256,
// random salt) and seals plaintext under it with AES-256-GCM, returning a
// single self-describing envelope: magic || salt || iterations(uint32 BE)
// || nonce || ciphertext. Meant for data that has to sit on disk (or in a
// browser download) as an ordinary file rather than behind an
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
	key := pbkdf2.Key([]byte(password), salt, defaultIterations, pbkdf2KeyLen, sha256.New)

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("генерация nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, nil)

	out := make([]byte, 0, envelopeHeaderSize+len(nonce)+len(sealed))
	out = append(out, passwordMagic[:]...)
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, defaultIterations)
	out = append(out, nonce...)
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
	if len(envelope) < envelopeHeaderSize {
		return nil, errors.New("secretbox: повреждён (слишком короткий) зашифрованный файл")
	}
	rest := envelope[len(passwordMagic):]
	salt, rest := rest[:saltSize], rest[saltSize:]
	iterations, rest := binary.BigEndian.Uint32(rest[:4]), rest[4:]

	key := pbkdf2.Key([]byte(password), salt, int(iterations), pbkdf2KeyLen, sha256.New)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(rest) < gcm.NonceSize() {
		return nil, errors.New("secretbox: повреждён (слишком короткий) зашифрованный файл")
	}
	nonce, sealed := rest[:gcm.NonceSize()], rest[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, errors.New("secretbox: неверный пароль или повреждённый файл")
	}
	return plaintext, nil
}
