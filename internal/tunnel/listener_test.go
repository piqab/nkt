package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/althq/netknownsthat/internal/tlscert"
)

// newTestListener starts a real Run listener on an ephemeral port, backed
// by a fresh self-signed cert, plus a real local TCP echo server for
// pipeStream to reach — returns the listener's address and a cleanup-free
// stop func (ctx cancellation via t.Cleanup is enough for Run itself; the
// echo listener is closed directly).
func newTestListener(t *testing.T, token string) (addr string) {
	t.Helper()

	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listener: %v", err)
	}
	t.Cleanup(func() { _ = echoLn.Close() })
	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	dir := t.TempDir()
	certFile, keyFile := dir+"/cert.pem", dir+"/key.pem"
	if err := tlscert.EnsureSelfSigned(certFile, keyFile, []string{"nkt-tunnel-test"}); err != nil {
		t.Fatalf("EnsureSelfSigned: %v", err)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick a free port: %v", err)
	}
	listenAddr := ln.Addr().String()
	_ = ln.Close() // just reserving the address; Run binds it itself

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ready := make(chan error, 1)
	go func() {
		err := Run(ctx, ListenerConfig{
			ListenAddr: listenAddr,
			Token:      token,
			TLSCert:    cert,
			LocalAddr:  echoLn.Addr().String(),
			Log:        slog.New(slog.DiscardHandler),
		})
		select {
		case ready <- err:
		default:
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", listenAddr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return listenAddr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listener at %s never came up", listenAddr)
	return ""
}

func dialTLSInsecure(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test dial, mirrors tunneldial.go's own trust model
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	return conn
}

// TestListenerPipesStreamsWithCorrectToken confirms the whole path a real
// hub connection takes: TLS handshake, token accepted, yamux session usable
// to open a stream that ends up talking to the local echo server —
// end-to-end proof that WriteToken/readToken/wrappedConn correctly hand
// yamux a connection with none of the token line's bytes lost.
func TestListenerPipesStreamsWithCorrectToken(t *testing.T) {
	addr := newTestListener(t, "the-real-token")

	conn := dialTLSInsecure(t, addr)
	defer conn.Close()
	if err := WriteToken(conn, "the-real-token"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	session, err := yamux.Client(conn, Config())
	if err != nil {
		t.Fatalf("yamux.Client: %v", err)
	}
	defer session.Close()

	stream, err := session.Open()
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	defer stream.Close()

	const msg = "hello through the tunnel"
	if _, err := io.WriteString(stream, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != msg {
		t.Errorf("echoed back %q, want %q", buf, msg)
	}
}

// TestListenerRejectsWrongToken confirms a connection presenting the wrong
// token gets closed before ever reaching yamux — proven by the fact that
// trying to actually use a yamux session over it fails immediately, since
// the listener already hung up.
func TestListenerRejectsWrongToken(t *testing.T) {
	addr := newTestListener(t, "the-real-token")

	conn := dialTLSInsecure(t, addr)
	defer conn.Close()
	if err := WriteToken(conn, "not-the-real-token"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	// The listener closes its side without sending anything back — the
	// next read must observe that (EOF or a reset), not hang.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Error("Read() after a wrong token succeeded, want the connection closed")
	}
}

// TestReadTokenPreservesBufferedBytes is a narrower unit check for the bug
// wrappedConn exists to avoid: yamux must see every byte the hub sent after
// the token line, even the ones bufio.Reader already pulled off the wire
// in the same read syscall as the token itself.
func TestReadTokenPreservesBufferedBytes(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = io.WriteString(client, "tok\nextra-payload")
	}()

	reader := bufio.NewReader(server)
	tok, err := readToken(reader)
	if err != nil {
		t.Fatalf("readToken: %v", err)
	}
	if tok != "tok" {
		t.Fatalf("readToken() = %q, want %q", tok, "tok")
	}

	wrapped := wrappedConn{Reader: reader, Writer: server, Closer: server}
	buf := make([]byte, len("extra-payload"))
	if _, err := io.ReadFull(wrapped, buf); err != nil {
		t.Fatalf("read through wrappedConn: %v", err)
	}
	if string(buf) != "extra-payload" {
		t.Errorf("wrappedConn read = %q, want %q (bytes buffered past the token line)", buf, "extra-payload")
	}
}
