// Package portprobe проверяет, отвечает ли порт, — тем, чем это делают
// руками: соединиться, спросить по HTTP, посмотреть сертификат.
//
// Проверка выполняется самим nkt, а не curl или nc на хосте: так она не
// зависит от того, что там установлено, и не запускает произвольную
// команду с параметрами из браузера. Эквивалентная команда при этом
// показывается — её можно скопировать в терминал и повторить руками с
// любыми ключами.
package portprobe

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Виды проверки.
const (
	// KindTCP — просто соединиться: порт слушают или нет.
	KindTCP = "tcp"
	// KindHTTP и KindHTTPS — запрос и ответ целиком: код, заголовки,
	// начало тела.
	KindHTTP  = "http"
	KindHTTPS = "https"
	// KindTLS — рукопожатие и сертификат, без HTTP: для почты, баз данных
	// и всего, что говорит TLS, но не HTTP.
	KindTLS = "tls"
)

// maxBody — сколько тела ответа показывать. Нужно увидеть, что сервис
// отвечает и чем, а не скачать страницу.
const maxBody = 4 << 10

// Request — что проверить.
type Request struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Kind    string `json:"kind"`
	// Path и Method — для http/https; пусто — «/» и GET.
	Path   string `json:"path,omitempty"`
	Method string `json:"method,omitempty"`
	// Host — заголовок Host (и SNI): сервис за именем отвечает по
	// адресу иначе, чем по имени, и это как раз то, что проверяют.
	Host string `json:"host,omitempty"`
	// Insecure — не проверять сертификат: самоподписанный на тестовом
	// стенде — норма, а проверить, что порт отвечает, всё равно нужно.
	Insecure bool `json:"insecure,omitempty"`
	TimeoutS int  `json:"timeout_s,omitempty"`
}

// TLSInfo — то, что видно в сертификате при рукопожатии.
type TLSInfo struct {
	Version   string   `json:"version"`
	Cipher    string   `json:"cipher"`
	Subject   string   `json:"subject"`
	Issuer    string   `json:"issuer"`
	DNSNames  []string `json:"dns_names,omitempty"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
	// Verified — сертификат прошёл проверку доверия; при Insecure всегда
	// false, потому что проверки не было.
	Verified bool `json:"verified"`
}

// Result — что вышло.
type Result struct {
	OK        bool     `json:"ok"`
	ElapsedMS int64    `json:"elapsed_ms"`
	Error     string   `json:"error,omitempty"`
	Status    string   `json:"status,omitempty"`
	Headers   []string `json:"headers,omitempty"`
	Body      string   `json:"body,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	TLS       *TLSInfo `json:"tls,omitempty"`
	// Command — то же самое для терминала: curl, nc или openssl.
	Command string `json:"command"`
}

var methods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "OPTIONS": true, "PUT": true, "DELETE": true}

// Validate приводит запрос в порядок и отвергает то, что не годится.
//
// Адрес — только IP, не имя: проверяют конкретный слушающий сокет, а имя
// открыло бы путь к любому адресу в сети, куда хост ходить не должен.
func (r *Request) Validate() error {
	r.Address = strings.Trim(strings.TrimSpace(r.Address), "[]")
	if net.ParseIP(r.Address) == nil {
		return fmt.Errorf("адрес должен быть IP-адресом, получено %q", r.Address)
	}
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("порт вне диапазона: %d", r.Port)
	}
	switch r.Kind {
	case KindTCP, KindHTTP, KindHTTPS, KindTLS:
	case "":
		r.Kind = KindTCP
	default:
		return fmt.Errorf("неизвестный вид проверки %q", r.Kind)
	}
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	if !methods[r.Method] {
		return fmt.Errorf("метод %q не поддерживается", r.Method)
	}
	r.Path = strings.TrimSpace(r.Path)
	if r.Path == "" {
		r.Path = "/"
	}
	if !strings.HasPrefix(r.Path, "/") || strings.ContainsAny(r.Path, " \r\n") {
		return fmt.Errorf("путь должен начинаться с «/» и не содержать пробелов")
	}
	r.Host = strings.TrimSpace(r.Host)
	if strings.ContainsAny(r.Host, " \r\n/") {
		return fmt.Errorf("некорректный заголовок Host")
	}
	if r.TimeoutS <= 0 {
		r.TimeoutS = 5
	}
	if r.TimeoutS > 30 {
		r.TimeoutS = 30
	}
	return nil
}

func (r Request) hostPort() string {
	return net.JoinHostPort(r.Address, fmt.Sprintf("%d", r.Port))
}

// Command — эквивалент для терминала.
func Command(r Request) string {
	target := r.hostPort()
	switch r.Kind {
	case KindHTTP, KindHTTPS:
		parts := []string{"curl", "-sS", "-i", "-m", fmt.Sprintf("%d", r.TimeoutS)}
		if r.Method != http.MethodGet {
			parts = append(parts, "-X", r.Method)
		}
		if r.Kind == KindHTTPS && r.Insecure {
			parts = append(parts, "-k")
		}
		if r.Host != "" {
			parts = append(parts, "-H", fmt.Sprintf("'Host: %s'", r.Host))
		}
		parts = append(parts, fmt.Sprintf("%s://%s%s", r.Kind, target, r.Path))
		return strings.Join(parts, " ")
	case KindTLS:
		cmd := fmt.Sprintf("openssl s_client -connect %s", target)
		if r.Host != "" {
			cmd += " -servername " + r.Host
		}
		return cmd + " </dev/null"
	default:
		return fmt.Sprintf("nc -zv -w %d %s %d", r.TimeoutS, r.Address, r.Port)
	}
}

// Probe выполняет проверку.
func Probe(ctx context.Context, r Request) Result {
	res := Result{Command: Command(r)}
	if err := r.Validate(); err != nil {
		res.Error = err.Error()
		return res
	}
	res.Command = Command(r)
	timeout := time.Duration(r.TimeoutS) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	defer func() { res.ElapsedMS = time.Since(started).Milliseconds() }()

	switch r.Kind {
	case KindTCP:
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", r.hostPort())
		if err != nil {
			res.Error = dialError(err)
			return res
		}
		_ = conn.Close()
		// Без словесного статуса: «отвечает/не отвечает» показывает
		// интерфейс на своём языке, а серверная фраза была бы русской и в
		// английском интерфейсе.
		res.OK = true
		return res

	case KindTLS:
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", r.hostPort())
		if err != nil {
			res.Error = dialError(err)
			return res
		}
		defer conn.Close()
		cfg := &tls.Config{InsecureSkipVerify: r.Insecure, ServerName: r.Host} //nolint:gosec // осознанно: проверка стенда с самоподписанным
		if cfg.ServerName == "" {
			cfg.ServerName = r.Address
			// По голому адресу проверка доверия почти всегда провалится
			// на имени — это не ошибка сервиса, поэтому доверие тут не
			// требуем, а сертификат показываем как есть.
			cfg.InsecureSkipVerify = true
		}
		tconn := tls.Client(conn, cfg)
		if err := tconn.HandshakeContext(ctx); err != nil {
			res.Error = "TLS-рукопожатие: " + err.Error()
			return res
		}
		res.TLS = tlsInfo(tconn.ConnectionState(), !cfg.InsecureSkipVerify)
		res.OK = true
		return res

	default: // http, https
		client := &http.Client{
			Timeout: timeout,
			// Перенаправления не следуем: интересует ответ именно этого
			// порта, а 301 на другой хост — уже ответ.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				TLSClientConfig:   &tls.Config{InsecureSkipVerify: r.Insecure, ServerName: r.Host}, //nolint:gosec
				DisableKeepAlives: true,
				Proxy:             nil,
			},
		}
		url := fmt.Sprintf("%s://%s%s", r.Kind, r.hostPort(), r.Path)
		req, err := http.NewRequestWithContext(ctx, r.Method, url, nil)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		if r.Host != "" {
			req.Host = r.Host
		}
		req.Header.Set("User-Agent", "nkt-portprobe")
		resp, err := client.Do(req)
		if err != nil {
			res.Error = dialError(err)
			return res
		}
		defer resp.Body.Close()
		res.OK = true
		res.Status = resp.Proto + " " + resp.Status
		for k, vs := range resp.Header {
			for _, v := range vs {
				res.Headers = append(res.Headers, k+": "+v)
			}
		}
		sort.Strings(res.Headers)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		if len(body) > maxBody {
			body = body[:maxBody]
			res.Truncated = true
		}
		res.Body = string(body)
		if resp.TLS != nil {
			res.TLS = tlsInfo(*resp.TLS, !r.Insecure)
		}
		return res
	}
}

func tlsInfo(st tls.ConnectionState, verified bool) *TLSInfo {
	info := &TLSInfo{Version: tls.VersionName(st.Version), Cipher: tls.CipherSuiteName(st.CipherSuite), Verified: verified}
	if len(st.PeerCertificates) > 0 {
		c := st.PeerCertificates[0]
		info.Subject = c.Subject.String()
		info.Issuer = c.Issuer.String()
		info.DNSNames = c.DNSNames
		info.NotBefore = c.NotBefore.UTC().Format(time.RFC3339)
		info.NotAfter = c.NotAfter.UTC().Format(time.RFC3339)
	}
	return info
}

// dialError переводит самые частые исходы на язык, по которому понятно,
// что делать: «отказано» — порт никто не слушает, «таймаут» — пакеты
// теряются (обычно фаервол).
func dialError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "соединение отклонено — на этом адресе и порту никто не слушает"
	case strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "timeout"):
		return "таймаут — ответа нет; так ведёт себя порт, закрытый фаерволом"
	case strings.Contains(msg, "no route to host"):
		return "нет маршрута к адресу"
	}
	return msg
}
