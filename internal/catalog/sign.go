package catalog

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// TrustedKeys — открытые ключи каталога по умолчанию (PEM, как выдаёт
// «openssl pkey -pubout»). Список, а не один ключ: при смене ключа новый
// добавляется сюда за выпуск до перехода, старый убирается позже — хабы не
// теряют каталог в промежутке.
var TrustedKeys = []string{
	`-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAWVBxlhI+w6UXyAL/iF9lzm+P8qQtH5jE15jku19ftn8=
-----END PUBLIC KEY-----`,
}

// ParsePublicKeys — ключи ed25519 из одного или нескольких блоков PEM.
func ParsePublicKeys(text string) ([]ed25519.PublicKey, error) {
	var out []ed25519.PublicKey
	rest := []byte(strings.TrimSpace(text))
	for len(rest) > 0 {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if b.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("PEM block %q is not a public key", b.Type)
		}
		k, err := x509.ParsePKIXPublicKey(b.Bytes)
		if err != nil {
			return nil, err
		}
		ek, ok := k.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("not an ed25519 key")
		}
		out = append(out, ek)
	}
	if len(out) == 0 {
		return nil, errors.New("no public key")
	}
	return out, nil
}

// DefaultKeys — встроенные ключи.
func DefaultKeys() []ed25519.PublicKey {
	keys, err := ParsePublicKeys(strings.Join(TrustedKeys, "\n"))
	if err != nil {
		panic("catalog: bad built-in key: " + err.Error())
	}
	return keys
}

// ParsePrivateKey — закрытый ключ ed25519 в PEM (PKCS#8, «openssl genpkey
// -algorithm ed25519»).
func ParsePrivateKey(text []byte) (ed25519.PrivateKey, error) {
	b, _ := pem.Decode(text)
	if b == nil || b.Type != "PRIVATE KEY" {
		return nil, errors.New("no PEM private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an ed25519 key")
	}
	return ek, nil
}

// Sign — подпись index.json: 64 байта ed25519, как «openssl pkeyutl -sign
// -rawin» (проверяется и openssl, и хабом).
func Sign(key ed25519.PrivateKey, index []byte) []byte {
	return ed25519.Sign(key, index)
}

// ErrSignature — подпись не сходится ни с одним доверенным ключом.
var ErrSignature = errors.New("catalog signature does not match a trusted key")

// Verify — подпись хоть одного доверенного ключа.
func Verify(index, sig []byte, keys []ed25519.PublicKey) error {
	if len(sig) != ed25519.SignatureSize {
		return ErrSignature
	}
	for _, k := range keys {
		if ed25519.Verify(k, index, sig) {
			return nil
		}
	}
	return ErrSignature
}
