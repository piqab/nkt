// Package tlscheck dials a live TLS endpoint and reports the certificate it
// actually presents, so it can be compared against the file a configuration
// claims to be serving. A file on disk that looks perfectly healthy is not the
// same claim as "this is what clients receive": a renewed certificate that
// nothing reloaded into the service is invisible from the filesystem alone.
package tlscheck

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"time"
)

// Served is the leaf certificate a TLS endpoint presented during the handshake.
type Served struct {
	// Fingerprint is the SHA-256 of the leaf certificate's raw DER bytes, hex
	// encoded — the same value model.Certificate.Fingerprint stores for the
	// file on disk, so the two can be compared directly.
	Fingerprint string
	Serial      string
	NotAfter    time.Time
	Names       []string
}

// Fetch dials addr ("host:port") over TLS and returns the leaf certificate it
// presents. serverName sets SNI, which matters whenever more than one
// certificate is multiplexed on the same socket; pass "" for a plain dial.
//
// The server's trust chain is never verified — the point is to see what is
// being served, not to validate it the way a browser would.
func Fetch(ctx context.Context, addr, serverName string, timeout time.Duration) (Served, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: timeout},
		Config:    &tls.Config{InsecureSkipVerify: true, ServerName: serverName}, //nolint:gosec
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Served{}, err
	}
	defer func() { _ = conn.Close() }()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return Served{}, errors.New("соединение не является TLS")
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return Served{}, errors.New("сервер не предъявил сертификат")
	}

	leaf := state.PeerCertificates[0]
	sum := sha256.Sum256(leaf.Raw)
	return Served{
		Fingerprint: hex.EncodeToString(sum[:]),
		Serial:      leaf.SerialNumber.String(),
		NotAfter:    leaf.NotAfter.UTC(),
		Names:       leaf.DNSNames,
	}, nil
}
