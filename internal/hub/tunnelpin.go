package hub

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
)

// tunnelCertMismatchError is returned by verifyPinnedTunnelCert when a host
// presents a certificate that doesn't match its previously pinned
// fingerprint — distinguished from an ordinary dial failure (host down,
// wrong port, network blip) so tunnelDialOnce can escalate it into a
// visible host status instead of a routine backoff-and-retry log line: a
// changed certificate on an already-pinned host means either the host was
// legitimately reinstalled (which clears the pin itself, see
// prepareTunnelEnv) or something is intercepting the connection.
type tunnelCertMismatchError struct {
	pinned, got []byte
}

func (e *tunnelCertMismatchError) Error() string {
	return fmt.Sprintf(
		"сертификат резервного канала хоста изменился (ожидался %x, получен %x) — либо хост был переустановлен без сброса привязки, либо соединение перехватывается",
		e.pinned, e.got)
}

// verifyPinnedTunnelCert checks a freshly presented tunnel certificate
// against pinned, the fingerprint recorded the first time the hub ever
// dialed this host successfully (store.Host.TunnelCertSHA256). Trust-on-
// first-use: an empty pinned means nothing has been recorded yet, so
// whatever is presented now is accepted and returned as the fingerprint to
// pin going forward — mirroring how dialSSH accepts a host's SSH key the
// first time, except this one is actually remembered and enforced
// afterward instead of being ignored forever (see tunneldial.go's own
// InsecureSkipVerify, which is why this callback exists at all: a
// self-signed cert can't be checked any other way, but "any cert at all,
// every single time" is a strictly weaker bar than this).
func verifyPinnedTunnelCert(rawCerts [][]byte, pinned []byte) (fingerprint []byte, err error) {
	if len(rawCerts) == 0 {
		return nil, fmt.Errorf("хост не предъявил сертификат")
	}
	if _, err := x509.ParseCertificate(rawCerts[0]); err != nil {
		return nil, fmt.Errorf("не удалось разобрать сертификат хоста: %w", err)
	}
	sum := sha256.Sum256(rawCerts[0])
	fingerprint = sum[:]
	if len(pinned) > 0 && !bytes.Equal(pinned, fingerprint) {
		return fingerprint, &tunnelCertMismatchError{pinned: pinned, got: fingerprint}
	}
	return fingerprint, nil
}
