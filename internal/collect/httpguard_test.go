package collect

import (
	"context"
	"testing"
	"time"
)

func TestCheckLocalURL(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8080/api", "https://localhost/x", "http://10.1.2.3/", "http://[::1]:9901/", "http://192.168.1.10/", "http://[fd00::5]/", "http://169.254.1.1/"} {
		if _, err := CheckLocalURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://example.com/", "http://8.8.8.8/", "http://u:p@127.0.0.1/", "ftp://127.0.0.1/", "http://127.0.0.1.nip.io/", "http://[2001:db8::1]/", "file:///etc/passwd"} {
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
