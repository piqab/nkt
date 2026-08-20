// Package tunnel implements the reverse-tunnel fallback channel between a
// managed host and its hub, used when SSH becomes unreachable (a blocked
// or misconfigured inbound port 22 is the common real-world case — not
// necessarily sshd itself being down). The host dials the hub over a
// single persistent WebSocket connection and multiplexes it with yamux, so
// the hub can open as many concurrent virtual streams as it needs — one
// per proxied dashboard API request, or one long-lived one for the
// terminal's own WebSocket — each piped by this host into its own local
// nkt listener. See internal/hub/tunnel.go for the hub side, and
// internal/hub/proxy.go for where the hub falls back to this channel only
// after an SSH dial fails.
package tunnel

import (
	"crypto/sha256"
	"io"

	"github.com/hashicorp/yamux"
)

// Path is the hub route a host's tunnel client dials — see
// internal/hub/server.go's route registration.
const Path = "/api/hub/tunnel"

// Header names both sides agree on for authenticating the WebSocket
// upgrade with a per-host machine credential, not the browser session
// cookie every other hub route uses.
const (
	HeaderHostID = "X-Nkt-Tunnel-Host-Id"
	HeaderToken  = "X-Nkt-Tunnel-Token"
)

// TokenHash returns the SHA-256 digest of a raw tunnel token — what
// store.Host.TunnelTokenHash actually stores (see SetHostTunnelToken) and
// what a connecting host's presented token is compared against (see
// internal/hub/tunnel.go). Nothing on the hub side ever needs the raw
// token back, only to verify one, so a plain digest is enough — no
// secretbox round trip, unlike the SSH secret or admin password.
func TokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Config returns the yamux configuration both the client (this package)
// and the hub server (internal/hub/tunnel.go) use — yamux's own defaults,
// except its internal transport-level logging (connection resets, closed-
// session reads — expected and already surfaced through this package's own
// error returns/slog calls at the point they matter) is discarded instead
// of going straight to os.Stderr.
func Config() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	return cfg
}
