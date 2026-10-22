package collect

import (
	"context"
	"testing"
	"time"
)

func TestCheckLocalURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:8080/api?x=1": "http://127.0.0.1:8080/api?x=1",
		"https://localhost/x":           "https://localhost/x",
		"http://[::1]:9901/stats":       "http://[::1]:9901/stats",
		"http://127.0.1.1:15672/api":    "http://127.0.1.1:15672/api",
	} {
		got, err := CheckLocalURL(in)
		if err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"http://example.com/", "http://8.8.8.8/", "http://10.1.2.3/", "http://192.168.1.10/", "http://[fd00::5]/",
		"http://u:p@127.0.0.1/", "ftp://127.0.0.1/", "http://127.0.0.1.nip.io/", "http://[2001:db8::1]/", "file:///etc/passwd", "http://127.0.0.1:99999/"} {
		if _, err := CheckLocalURL(bad); err == nil {
			t.Errorf("%s: want error", bad)
		}
	}
	// Local не идёт наружу ещё до соединения.
	l := &Local{}
	if _, _, err := l.HTTPGet(context.Background(), "http://8.8.8.8/", nil, time.Second); err == nil {
		t.Error("external address: want error")
	}
}
