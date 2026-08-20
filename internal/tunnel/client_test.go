package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// TestClientPipesStreamsToLocalListener spins up a real TLS WS server
// (mirroring what internal/hub/tunnel.go's handler does), a real local TCP
// echo listener (standing in for this host's own nkt), points a tunnel
// Client at the server with its certificate pinned, opens a yamux stream
// from the SERVER side (as the hub always does — see connectOnce's own
// comment on why), writes through it, and confirms the client relays
// bytes to/from the local listener correctly. This is the exact mechanism
// internal/hub/proxy.go's fallback dial will rely on.
func TestClientPipesStreamsToLocalListener(t *testing.T) {
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer echoLn.Close()
	go func() {
		for {
			c, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c) }()
		}
	}()

	var gotHostID, gotToken string
	sessionCh := make(chan *yamux.Session, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Path {
			http.NotFound(w, r)
			return
		}
		gotHostID = r.Header.Get(HeaderHostID)
		gotToken = r.Header.Get(HeaderToken)
		wsConn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		conn := websocket.NetConn(r.Context(), wsConn, websocket.MessageBinary)
		session, err := yamux.Server(conn, nil)
		if err != nil {
			t.Errorf("yamux.Server: %v", err)
			return
		}
		sessionCh <- session
		<-r.Context().Done()
	}))
	defer srv.Close()

	certSum := sha256.Sum256(srv.Certificate().Raw)
	pinned := hex.EncodeToString(certSum[:])
	hubAddr := srv.Listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, ClientConfig{
		HubAddr:          hubAddr,
		HostID:           "42",
		Token:            "test-token",
		PinnedCertSHA256: pinned,
		LocalAddr:        echoLn.Addr().String(),
		Log:              slog.New(slog.DiscardHandler),
	})

	var session *yamux.Session
	select {
	case session = <-sessionCh:
	case <-time.After(5 * time.Second):
		t.Fatal("client never connected to the tunnel server")
	}
	if gotHostID != "42" {
		t.Errorf("HeaderHostID = %q, want %q", gotHostID, "42")
	}
	if gotToken != "test-token" {
		t.Errorf("HeaderToken = %q, want %q", gotToken, "test-token")
	}

	stream, err := session.Open()
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	defer stream.Close()

	msg := []byte("hello through the tunnel")
	if _, err := stream.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("echoed = %q, want %q", buf, msg)
	}
}

// TestClientRejectsWrongCertificatePin confirms the client refuses to
// complete the TLS handshake against a server presenting a DIFFERENT
// certificate than the one pinned — the whole point of pinning instead of
// blind InsecureSkipVerify.
func TestClientRejectsWrongCertificatePin(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should never be reached — the TLS handshake must fail first")
	}))
	defer srv.Close()

	wrongPin := hex.EncodeToString(sha256.New().Sum(nil)) // sha256 of empty input — never matches a real cert
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	connected, err := connectOnce(ctx, ClientConfig{
		HubAddr:          srv.Listener.Addr().String(),
		HostID:           "1",
		Token:            "t",
		PinnedCertSHA256: wrongPin,
		LocalAddr:        "127.0.0.1:1",
		Log:              slog.New(slog.DiscardHandler),
	})
	if err == nil {
		t.Fatal("connectOnce succeeded against a certificate that doesn't match the pin")
	}
	if connected {
		t.Error("connectOnce reported connected=true for a failed handshake")
	}
}
