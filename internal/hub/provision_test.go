package hub

import (
	"strings"
	"testing"
)

func TestRenderEnvContainsExpectedLines(t *testing.T) {
	out := renderEnv("admin", "s3cr3t-pw")

	for _, want := range []string{
		"NKT_MODE=local",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD=s3cr3t-pw",
		// The hub only ever reaches the remote nkt through a plain-HTTP SSH
		// tunnel — requiring HTTPS here would stop the remote from setting
		// the session cookie bootstrapLogin needs to capture.
		"NKT_COOKIE_SECURE=false",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderEnv output missing %q, got:\n%s", want, out)
		}
	}
}

func TestGeneratePasswordIsRandomAndLong(t *testing.T) {
	a, err := generatePassword()
	if err != nil {
		t.Fatalf("generatePassword: %v", err)
	}
	b, err := generatePassword()
	if err != nil {
		t.Fatalf("generatePassword: %v", err)
	}
	if a == b {
		t.Fatal("two calls to generatePassword produced the same value")
	}
	if len(a) < 20 {
		t.Fatalf("generatePassword produced a suspiciously short value: %q", a)
	}
}
