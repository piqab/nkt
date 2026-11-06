package site

import (
	"strings"
	"testing"
)

// Путь, по которому хаб проверяет сайт снаружи: абсолютный путь запроса.
// Ведущий «/» дописывается (в окне сайта и в блоке site: пишут и так, и
// так), строка запроса, фрагмент, пробелы и чужой адрес («//host») — нет.
func TestValidCheckPath(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "   ": "", "/": "/", "/ui": "/ui", "ui": "/ui", " /ui/ ": "/ui/",
		"/ui/login": "/ui/login", "/a.b_c~1": "/a.b_c~1", "/files/x%20y": "/files/x%20y",
		"/@me/it's": "/@me/it's", "/a+b,c=d:e": "/a+b,c=d:e",
	} {
		got, err := ValidCheckPath(in)
		if err != nil || got != want {
			t.Errorf("ValidCheckPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"//evil.example.com/x", "//", "/a b", "/?x=1", "/#top", "/a\nb", "/a\rb",
		"/a<b", "/a|b", `/a"b`, "/" + strings.Repeat("x", 255), "/привет",
	} {
		if got, err := ValidCheckPath(bad); err == nil {
			t.Errorf("accepted %q as %q", bad, got)
		}
	}
}
