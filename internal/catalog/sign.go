package catalog

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
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
// -algorithm ed25519»). Секрет CI часто вставляют неаккуратно, поэтому
// принимается и PEM, склеенный в одну строку (переносы стали пробелами),
// и один base64 без строк BEGIN/END. Ошибка называет, что найдено, но не
// содержимое ключа.
func ParsePrivateKey(text []byte) (ed25519.PrivateKey, error) {
	der, err := privateKeyDER(strings.TrimSpace(string(text)))
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("not a PKCS#8 private key (%d bytes after base64): %w", len(der), err)
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an ed25519 key (%T) — openssl genpkey -algorithm ed25519", k)
	}
	return ek, nil
}

var pemHeadRe = regexp.MustCompile(`-----BEGIN ([A-Z0-9 ]+)-----`)

func privateKeyDER(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("the key is empty")
	}
	if b, _ := pem.Decode([]byte(s)); b != nil {
		if b.Type != "PRIVATE KEY" {
			return nil, privateKeyTypeErr(b.Type)
		}
		return b.Bytes, nil
	}
	if m := pemHeadRe.FindStringSubmatch(s); m != nil {
		if m[1] != "PRIVATE KEY" {
			return nil, privateKeyTypeErr(m[1])
		}
		// PEM в одну строку: тело между BEGIN и END, пробелы — бывшие переносы.
		body := s[strings.Index(s, m[0])+len(m[0]):]
		if i := strings.Index(body, "-----END"); i >= 0 {
			body = body[:i]
		}
		return decodeB64(body)
	}
	return decodeB64(s) // только тело base64
}

func privateKeyTypeErr(t string) error {
	switch {
	case t == "PUBLIC KEY":
		return errors.New("this is the public key — the secret needs the private one (the file from openssl genpkey)")
	case strings.Contains(t, "ENCRYPTED"):
		return errors.New("the private key is password-protected — make one without a password: openssl genpkey -algorithm ed25519")
	case t == "OPENSSH PRIVATE KEY":
		return errors.New("this is an OpenSSH key (ssh-keygen) — make one with openssl genpkey -algorithm ed25519")
	}
	return fmt.Errorf("PEM block %q, need \"PRIVATE KEY\" (openssl genpkey -algorithm ed25519)", t)
}

func decodeB64(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	der, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(der) == 0 {
		return nil, fmt.Errorf("no PEM private key: no BEGIN PRIVATE KEY line and not base64 (%d characters)", len(s))
	}
	return der, nil
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
