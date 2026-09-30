package deploy

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/piqab/nkt/internal/msgs"
)

func TestExplainYAML(t *testing.T) {
	base := "repo: https://github.com/piqab/nkt.git\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: app\n  hosts: [cn4]\n"
	for _, tc := range []struct{ yaml, want string }{
		// «site: имя» и блок под ним.
		{base + "  site:     hb.example.com\n     domains: [hb.example.com]\n     port: 80\n", "строка 8 «  site:     hb.example.com»"},
		// Как у пользователя: с комментарием после значения.
		{base + "  wait_timeout: 5m\n  site:     hb.xxx.xx                  # сайт: прокси и сертификат (один хост)\n     domains: [hb.xxx.xx]\n     service: httpbin\n     port: 8080\n", "значение «hb.xxx.xx»"},
		{base + "  imagez:\n    web: nginx\n", "допустимы:"},
		{base + "\tfoo: 1\n", "табуляция"},
		{base + "  site:\n    domains: [a.example.com]\n    service: web\n    port: eighty\n", "число"},
	} {
		_, err := ParseSpec(tc.yaml)
		if err == nil {
			t.Fatalf("accepted:\n%s", tc.yaml)
		}
		text := msgs.Localize(msgs.RU, err)
		t.Log(text)
		if !utf8.ValidString(text) {
			t.Fatalf("broken UTF-8: %q", text)
		}
		if !strings.Contains(text, tc.want) {
			t.Fatalf("want %q in %q", tc.want, text)
		}
	}
}
