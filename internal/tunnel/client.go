package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// ClientConfig configures the host-side tunnel client — see Run.
type ClientConfig struct {
	// HubAddr is the hub's own externally-reachable address:port (see
	// config.Config.HubPublicAddr) — written into this host's env at
	// install time as NKT_HUB_TUNNEL_ADDR.
	HubAddr string
	HostID  string
	Token   string
	// PinnedCertSHA256 is the hub's TLS certificate fingerprint to pin
	// against instead of normal CA verification, hex-encoded — set when
	// the hub uses a self-signed certificate (see internal/tlscert),
	// pushed down at install time over the already-trusted SSH session
	// (the same reasoning SSH host keys would ideally get, stronger than
	// blind trust-on-first-use since it travels over an already-
	// authenticated channel). Empty means use normal certificate
	// verification — a real CA-issued certificate, or a reverse proxy in
	// front of the hub handling that itself.
	PinnedCertSHA256 string
	// LocalAddr is where this host's own nkt API listens — every stream
	// the hub opens over the tunnel gets piped here. Almost always
	// 127.0.0.1:<NKT_ADDR's port>, the same loopback address the
	// SSH-tunneled path already reaches today.
	LocalAddr string
	Log       *slog.Logger
}

// Run maintains a persistent tunnel connection to the hub, reconnecting
// with exponential backoff (capped at 30s) on any failure, until ctx is
// cancelled. Meant to run as a long-lived background goroutine — see
// cmd/nkt/main.go's runServer, mirroring the same ctx-driven lifecycle
// every other background job there already uses.
func Run(ctx context.Context, cfg ClientConfig) {
	const maxBackoff = 30 * time.Second
	backoff := time.Second
	for ctx.Err() == nil {
		connected, err := connectOnce(ctx, cfg)
		if err != nil && ctx.Err() == nil {
			cfg.Log.Warn("резервный канал: соединение с хабом не установлено или прервано", "err", err)
		}
		// A connection that was actually established and ran for a while
		// (not an immediate dial failure) means the hub/network is fine —
		// no reason to keep making the operator wait through an
		// accumulated backoff for the next attempt.
		if connected {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connectOnce dials the hub once, serves accepted streams until the
// connection drops, and reports whether it ever got far enough to be
// useful (so Run can decide whether to reset its backoff).
func connectOnce(ctx context.Context, cfg ClientConfig) (connected bool, err error) {
	httpClient, err := httpClientFor(cfg.PinnedCertSHA256)
	if err != nil {
		return false, fmt.Errorf("настройка TLS: %w", err)
	}

	url := "wss://" + cfg.HubAddr + Path
	header := http.Header{}
	header.Set(HeaderHostID, cfg.HostID)
	header.Set(HeaderToken, cfg.Token)

	wsConn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient: httpClient,
		HTTPHeader: header,
	})
	if err != nil {
		return false, fmt.Errorf("подключение к %s: %w", url, err)
	}
	defer wsConn.CloseNow()

	conn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	session, err := yamux.Client(conn, Config())
	if err != nil {
		return false, fmt.Errorf("настройка мультиплексирования: %w", err)
	}
	defer session.Close()

	cfg.Log.Info("резервный канал: подключено к хабу", "addr", cfg.HubAddr)

	for {
		stream, err := session.Accept()
		if err != nil {
			// Session/connection closed — Run's caller reconnects. Already
			// connected once, so this isn't a dial failure.
			return true, err
		}
		go pipeStream(cfg, stream)
	}
}

// pipeStream connects one hub-opened virtual stream to this host's own
// local nkt listener — the same thing an SSH-forwarded channel does today
// for Proxy(), just carried over the tunnel instead.
func pipeStream(cfg ClientConfig, stream net.Conn) {
	defer stream.Close()
	local, err := net.DialTimeout("tcp", cfg.LocalAddr, 5*time.Second)
	if err != nil {
		cfg.Log.Warn("резервный канал: не удалось подключиться к локальному nkt", "addr", cfg.LocalAddr, "err", err)
		return
	}
	defer local.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(local, stream)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(stream, local)
		done <- struct{}{}
	}()
	// Either direction finishing (the hub closed its side, or the local
	// nkt did) means this stream is done — both Close defers above then
	// unblock whichever io.Copy is still running.
	<-done
}

// httpClientFor builds the *http.Client the WebSocket dial uses — plain
// default verification when no fingerprint is pinned, otherwise a
// transport that skips normal chain validation and instead compares the
// server's leaf certificate against exactly the pinned digest.
func httpClientFor(pinnedSHA256Hex string) (*http.Client, error) {
	if pinnedSHA256Hex == "" {
		return http.DefaultClient, nil
	}
	want, err := hex.DecodeString(pinnedSHA256Hex)
	if err != nil {
		return nil, fmt.Errorf("некорректный отпечаток сертификата хаба: %w", err)
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // not blind trust — VerifyPeerCertificate pins the exact fingerprint below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("хаб не представил сертификат")
			}
			got := sha256.Sum256(rawCerts[0])
			if !bytes.Equal(got[:], want) {
				return errors.New("отпечаток сертификата хаба не совпадает с ожидаемым — возможна подмена")
			}
			return nil
		},
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}, nil
}
