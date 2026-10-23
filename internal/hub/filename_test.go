package hub

import "testing"

func TestASCIIFileName(t *testing.T) {
	for in, want := range map[string]string{
		"Ассистент 23.10.2026 15:32 — reg1-fr": "Assistent-23.10.2026-15-32-reg1-fr",
		"web-farm":        "web-farm",
		"щука/\"x\"\nend": "schuka-x-end",
		"——":              "script",
		"Перенос Forgejo": "Perenos-Forgejo",
	} {
		if got := asciiFileName(in, "script"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
