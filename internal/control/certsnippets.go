package control

import (
	"fmt"
	"path"
	"strings"

	"github.com/piqab/nkt/internal/parse"
)

// Фрагменты конфигурации под конкретный сертификат certbot: пути
// подставлены, имена — из самого сертификата. Показать и скопировать,
// не записать: живой конфиг правится через редактор конфигураций, где
// есть проверка и откат, а здесь — заготовка, которую туда вставляют.

// CertSnippet — заготовка для одного сервиса.
type CertSnippet struct {
	Service string `json:"service"`
	// File — куда такой фрагмент обычно кладут на этом хосте.
	File    string `json:"file"`
	Content string `json:"content"`
	// CombinedPath — у haproxy: склеенный PEM, который ещё надо собрать.
	// Оговорку об этом пишет интерфейс на своём языке.
	CombinedPath string `json:"combined_path,omitempty"`
}

// CertSnippets собирает заготовки под lineage.
//
// names — имена из сертификата (SAN); пусто — берётся имя lineage. Пути
// к каталогам сервисов выводятся из главных конфигов, как они заданы
// для этого хоста: /etc/nginx/nginx.conf → /etc/nginx/sites-available.
func CertSnippets(lineage string, names []string, nginxMain, haproxyMain, caddyMain string) []CertSnippet {
	if len(names) == 0 {
		names = []string{lineage}
	}
	live := parse.LetsEncryptLive + lineage
	fullchain := live + "/fullchain.pem"
	privkey := live + "/privkey.pem"
	serverNames := strings.Join(names, " ")
	primary := names[0]

	nginxDir := path.Dir(orDefault(nginxMain, "/etc/nginx/nginx.conf"))
	haproxyDir := path.Dir(orDefault(haproxyMain, "/etc/haproxy/haproxy.cfg"))
	caddyFile := orDefault(caddyMain, "/etc/caddy/Caddyfile")

	nginx := fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %[1]s;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name %[1]s;

    ssl_certificate     %[2]s;
    ssl_certificate_key %[3]s;
    ssl_protocols       TLSv1.2 TLSv1.3;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
`, serverNames, fullchain, privkey)

	combined := path.Join(haproxyDir, "certs", lineage+".pem")
	haproxy := fmt.Sprintf(`frontend https
    bind *:443 ssl crt %[1]s
    bind *:80
    http-request redirect scheme https unless { ssl_fc }
    default_backend app

backend app
    server app1 127.0.0.1:8080 check
`, combined)

	caddy := fmt.Sprintf(`%[1]s {
    tls %[2]s %[3]s
    reverse_proxy 127.0.0.1:8080
}
`, serverNames, fullchain, privkey)

	return []CertSnippet{
		{
			Service: "nginx",
			File:    path.Join(nginxDir, "sites-available", primary+".conf"),
			Content: nginx,
		},
		{
			Service:      "haproxy",
			File:         haproxyMainOrDefault(haproxyMain),
			Content:      haproxy,
			CombinedPath: combined,
		},
		{
			Service: "caddy",
			File:    caddyFile,
			Content: caddy,
		},
	}
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func haproxyMainOrDefault(v string) string { return orDefault(v, "/etc/haproxy/haproxy.cfg") }
