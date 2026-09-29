// Package site — сайт на хосте: домен → прокси (nginx, HAProxy, Caddy) →
// сервис compose-стека или адрес:порт, с сертификатом Let's Encrypt.
//
// Здесь только то, что не зависит от хоста: проверка имён и адресов,
// тексты конфигураций и файла публикации сервиса compose. Записывают их
// на хосте общим путём конфигураций (проверка, откат, история).
package site

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Прокси.
const (
	ProxyNginx   = "nginx"
	ProxyHAProxy = "haproxy"
	ProxyCaddy   = "caddy"
)

// Proxies — в порядке показа.
var Proxies = []string{ProxyNginx, ProxyHAProxy, ProxyCaddy}

// OverrideFile — файл nkt рядом со стеком: публикация сервиса на
// 127.0.0.1 для прокси. docker compose применяет его вторым -f.
const OverrideFile = "compose.nkt.yml"

// PortRangeStart/End — из какого диапазона выдаются локальные порты под
// публикацию сервисов.
const (
	PortRangeStart = 18000
	PortRangeEnd   = 18999
)

var domainRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// NormalizeDomains — имена в нижнем регистре, без повторов, проверенные.
func NormalizeDomains(list []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range list {
		for _, d := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
			d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
			if d == "" || seen[d] {
				continue
			}
			if !domainRe.MatchString(d) || len(d) > 253 {
				return nil, msgs.Errorf("site.badDomain", d)
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, msgs.Errorf("site.noDomain")
	}
	if len(out) > 20 {
		return nil, msgs.Errorf("site.tooManyDomains", len(out))
	}
	return out, nil
}

// ValidUpstream — «адрес:порт» цели прокси: IP или localhost и порт.
func ValidUpstream(s string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil {
		return "", msgs.Errorf("site.badUpstream", s)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", msgs.Errorf("site.badUpstream", s)
	}
	if host != "localhost" && net.ParseIP(host) == nil {
		return "", msgs.Errorf("site.badUpstream", s)
	}
	return net.JoinHostPort(host, port), nil
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ValidName — имя стека или сервиса compose.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Config — что нужно прокси для одного сайта.
type Config struct {
	Domains  []string
	Upstream string
	// Lineage — сертификат certbot (/etc/letsencrypt/live/<lineage>);
	// Caddy получает сертификат сам и lineage не нужен.
	Lineage string
	// PEM — объединённый файл сертификата и ключа (HAProxy).
	PEM string
}

// ID — имя сайта в конфигурациях: первый домен.
func (c Config) ID() string { return c.Domains[0] }

func slug(s string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(s)
}

// NginxFile — файл сайта для nginx (conf.d подключается в http{}).
func NginxFile(root, domain string) string { return root + "/conf.d/nkt-" + domain + ".conf" }

// Nginx — сервер 80 (редирект) и 443 с сертификатом и проксированием.
func Nginx(c Config) string {
	names := strings.Join(c.Domains, " ")
	live := "/etc/letsencrypt/live/" + c.Lineage
	conn := "$nkt_conn_" + slug(c.ID())
	return "# Managed by nkt: site " + c.ID() + "\n" +
		"map $http_upgrade " + conn + " {\n    default upgrade;\n    ''      close;\n}\n\n" +
		"server {\n" +
		"    listen 80;\n    listen [::]:80;\n" +
		"    server_name " + names + ";\n" +
		"    location / {\n        return 301 https://$host$request_uri;\n    }\n}\n\n" +
		"server {\n" +
		"    listen 443 ssl http2;\n    listen [::]:443 ssl http2;\n" +
		"    server_name " + names + ";\n" +
		"    ssl_certificate " + live + "/fullchain.pem;\n" +
		"    ssl_certificate_key " + live + "/privkey.pem;\n" +
		"    client_max_body_size 64m;\n\n" +
		"    location / {\n" +
		"        proxy_pass http://" + c.Upstream + ";\n" +
		"        proxy_http_version 1.1;\n" +
		"        proxy_set_header Host $host;\n" +
		"        proxy_set_header X-Real-IP $remote_addr;\n" +
		"        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n" +
		"        proxy_set_header X-Forwarded-Proto $scheme;\n" +
		"        proxy_set_header Upgrade $http_upgrade;\n" +
		"        proxy_set_header Connection " + conn + ";\n" +
		"        proxy_read_timeout 300s;\n" +
		"    }\n}\n"
}

// CaddyDir / CaddyFile — сайты Caddy, подключаемые из основного
// Caddyfile строкой CaddyImport.
func CaddyDir(root string) string          { return root + "/nkt" }
func CaddyFile(root, domain string) string { return CaddyDir(root) + "/" + domain + ".caddy" }
func CaddyImport(root string) string       { return "import " + CaddyDir(root) + "/*.caddy" }

// Caddy — блок сайта. Сертификат Caddy получает и продлевает сам: ключи
// certbot ему, работающему не от root, не прочитать.
func Caddy(c Config) string {
	return "# Managed by nkt: site " + c.ID() + "\n" +
		strings.Join(c.Domains, ", ") + " {\n" +
		"    encode gzip\n" +
		"    reverse_proxy " + c.Upstream + "\n}\n"
}

// EnsureCaddyImport — основной Caddyfile с подключением сайтов nkt
// (строка добавляется в начало, если её нет).
func EnsureCaddyImport(main, root string) (string, bool) {
	line := CaddyImport(root)
	for _, l := range strings.Split(main, "\n") {
		if strings.TrimSpace(l) == line {
			return main, false
		}
	}
	return "# Managed by nkt: sites\n" + line + "\n\n" + main, true
}

// HAProxy: блок между метками в основном конфиге — все сайты nkt хоста.
const (
	HAProxyBegin = "# BEGIN nkt-sites (managed by nkt, regenerated on every site change)"
	HAProxyEnd   = "# END nkt-sites"
)

// HAProxyCertDir — объединённые PEM сайтов nkt (bind … crt <каталог>).
const HAProxyCertDir = "/etc/haproxy/nkt-certs"

// HAProxyPEM — объединённый PEM сайта.
func HAProxyPEM(domain string) string { return HAProxyCertDir + "/" + domain + ".pem" }

// HAProxyBlock — frontend 80 (редирект) и 443 (SNI по каталогу PEM) и по
// backend на сайт.
func HAProxyBlock(sites []Config) string {
	if len(sites) == 0 {
		return ""
	}
	sorted := append([]Config(nil), sites...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID() < sorted[j].ID() })
	var b strings.Builder
	b.WriteString(HAProxyBegin + "\n")
	b.WriteString("frontend nkt_http\n    bind :80\n    bind :::80\n    mode http\n    http-request redirect scheme https code 301\n\n")
	b.WriteString("frontend nkt_https\n    bind :443 ssl crt " + HAProxyCertDir + "/ alpn h2,http/1.1\n    bind :::443 ssl crt " + HAProxyCertDir + "/ alpn h2,http/1.1\n")
	b.WriteString("    mode http\n    option forwardfor\n    http-request set-header X-Forwarded-Proto https\n")
	for _, c := range sorted {
		fmt.Fprintf(&b, "    use_backend nkt_%s if { hdr(host),field(1,:) -i %s }\n", slug(c.ID()), strings.Join(c.Domains, " "))
	}
	b.WriteString("\n")
	for _, c := range sorted {
		fmt.Fprintf(&b, "backend nkt_%s\n    mode http\n    server app %s\n\n", slug(c.ID()), c.Upstream)
	}
	b.WriteString(HAProxyEnd + "\n")
	return b.String()
}

// ReplaceHAProxyBlock — основной конфиг с новым блоком nkt (пустой блок —
// убрать). Прежний блок заменяется целиком.
func ReplaceHAProxyBlock(main, block string) string {
	if i := strings.Index(main, HAProxyBegin); i >= 0 {
		if j := strings.Index(main[i:], HAProxyEnd); j >= 0 {
			end := i + j + len(HAProxyEnd)
			if end < len(main) && main[end] == '\n' {
				end++
			}
			main = strings.TrimRight(main[:i], "\n") + "\n" + main[end:]
		}
	}
	main = strings.TrimRight(main, "\n") + "\n"
	if block == "" {
		return main
	}
	return main + "\n" + block
}

var bindRe = regexp.MustCompile(`(?m)^\s*bind\s+\S*:(80|443)\b`)

// HAProxyForeignBind — порт 80/443 уже слушает frontend вне блока nkt:
// два frontend на одном порту HAProxy не поднимет.
func HAProxyForeignBind(main string) (string, bool) {
	outside := main
	if i := strings.Index(main, HAProxyBegin); i >= 0 {
		if j := strings.Index(main[i:], HAProxyEnd); j >= 0 {
			outside = main[:i] + main[i+j+len(HAProxyEnd):]
		}
	}
	if m := bindRe.FindStringSubmatch(outside); m != nil {
		return m[1], true
	}
	return "", false
}

// Port — опубликованный порт сервиса compose.
type Port struct {
	Target    int    `json:"target"`
	Published int    `json:"published"`
	HostIP    string `json:"host_ip,omitempty"`
}

// Override — файл публикации: сервис на 127.0.0.1:<hostPort>. Прежние
// записи файла сохраняются (другие сервисы, другие сайты).
func Override(existing, service string, hostPort, containerPort int) (string, error) {
	doc := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := yaml.Unmarshal([]byte(existing), &doc); err != nil {
			return "", msgs.Errorf("site.badOverride", err)
		}
	}
	services, _ := doc["services"].(map[string]any)
	if services == nil {
		services = map[string]any{}
	}
	svc, _ := services[service].(map[string]any)
	if svc == nil {
		svc = map[string]any{}
	}
	mapping := fmt.Sprintf("127.0.0.1:%d:%d", hostPort, containerPort)
	var ports []any
	if list, ok := svc["ports"].([]any); ok {
		for _, p := range list {
			if s, ok := p.(string); ok && strings.HasSuffix(s, fmt.Sprintf(":%d", containerPort)) && strings.HasPrefix(s, "127.0.0.1:") {
				continue // прежняя публикация того же порта заменяется
			}
			ports = append(ports, p)
		}
	}
	svc["ports"] = append(ports, mapping)
	services[service] = svc
	doc["services"] = services
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return "# Managed by nkt: services published on 127.0.0.1 for the site proxy\n" + string(out), nil
}
