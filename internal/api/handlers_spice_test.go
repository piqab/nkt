package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestParseSpiceDisplay(t *testing.T) {
	cases := map[string][3]string{
		"spice://127.0.0.1:5901\n":             {"tcp", "127.0.0.1:5901", "1"},
		"spice://0.0.0.0:5900":                 {"tcp", "127.0.0.1:5900", "1"},
		"spice://[::1]:5902":                   {"tcp", "[::1]:5902", "1"},
		"spice+unix:///run/libvirt/spice.sock": {"unix", "/run/libvirt/spice.sock", "1"},
		"spice://127.0.0.1?tls-port=5901":      {"", "", ""},
		"vnc://127.0.0.1:5900":                 {"", "", ""},
	}
	for in, want := range cases {
		n, a, ok := parseSpiceDisplay(in)
		if n != want[0] || a != want[1] || ok != (want[2] == "1") {
			t.Errorf("%q → %q %q %v", in, n, a, ok)
		}
	}
}

// Мост WebSocket ↔ unix-сокет (как у SPICE машины LXD): байты проходят
// в обе стороны, каждый WebSocket — своё соединение с сокетом.
func TestProxyWSToUnix(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "qemu.spice")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); c.Close() }()
		}
	}()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyWSTo(w, r, "unix", sock, time.Minute)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			t.Fatal(err)
		}
		msg := []byte{'R', 'E', 'D', 'Q', byte(i)}
		if err := c.Write(ctx, websocket.MessageBinary, msg); err != nil {
			t.Fatal(err)
		}
		_, got, err := c.Read(ctx)
		if err != nil || string(got) != string(msg) {
			t.Fatalf("канал %d: %q %v", i, got, err)
		}
		c.Close(websocket.StatusNormalClosure, "")
	}
}
