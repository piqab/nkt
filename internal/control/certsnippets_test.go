package control

import (
	"strings"
	"testing"
)

// Заготовка под сертификат должна быть про этот сертификат: пути lineage,
// все имена из него, а у haproxy — путь к склеенному PEM и оговорка о
// нём, иначе фрагмент обманывает: bind есть, файла нет.
func TestCertSnippets(t *testing.T) {
	snips := CertSnippets("shop.example.com", []string{"shop.example.com", "www.shop.example.com"},
		"/etc/nginx/nginx.conf", "/etc/haproxy/haproxy.cfg", "")
	if len(snips) != 3 {
		t.Fatalf("заготовок %d, ожидалось три", len(snips))
	}
	by := map[string]CertSnippet{}
	for _, s := range snips {
		by[s.Service] = s
	}
	nginx := by["nginx"]
	for _, want := range []string{
		"server_name shop.example.com www.shop.example.com;",
		"ssl_certificate     /etc/letsencrypt/live/shop.example.com/fullchain.pem;",
		"ssl_certificate_key /etc/letsencrypt/live/shop.example.com/privkey.pem;",
	} {
		if !strings.Contains(nginx.Content, want) {
			t.Errorf("nginx: нет %q:\n%s", want, nginx.Content)
		}
	}
	if nginx.File != "/etc/nginx/sites-available/shop.example.com.conf" {
		t.Errorf("nginx: файл = %q", nginx.File)
	}
	ha := by["haproxy"]
	if !strings.Contains(ha.Content, "bind *:443 ssl crt /etc/haproxy/certs/shop.example.com.pem") ||
		ha.CombinedPath != "/etc/haproxy/certs/shop.example.com.pem" {
		t.Errorf("haproxy: %q / combined=%q", ha.Content, ha.CombinedPath)
	}
	caddy := by["caddy"]
	if !strings.HasPrefix(caddy.Content, "shop.example.com www.shop.example.com {") ||
		!strings.Contains(caddy.Content, "tls /etc/letsencrypt/live/shop.example.com/fullchain.pem /etc/letsencrypt/live/shop.example.com/privkey.pem") {
		t.Errorf("caddy: %q", caddy.Content)
	}
	if caddy.File != "/etc/caddy/Caddyfile" {
		t.Errorf("caddy: файл по умолчанию = %q", caddy.File)
	}

	// Без имён из сертификата — имя lineage.
	bare := CertSnippets("one.example.org", nil, "", "", "")
	if !strings.Contains(bare[0].Content, "server_name one.example.org;") {
		t.Errorf("без SAN: %s", bare[0].Content)
	}
}
