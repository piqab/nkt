// Package portprobe проверяет, отвечает ли порт, — тем, чем это делают
// руками: соединиться и прочитать баннер, послать что-то и дождаться
// ответа, спросить по HTTP, посмотреть сертификат.
//
// Проверка выполняется самим nkt, а не curl или nc на хосте: так она не
// зависит от того, что там установлено. Эквивалентная команда при этом
// показывается — её можно скопировать в терминал. Отдельный вид «curl со
// своими параметрами» запускает настоящий curl: там аргументы задаёт
// оператор, и это осознанно — у него и так есть терминал.
package portprobe

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Виды проверки.
const (
	// KindTCP — соединиться, прочитать баннер, при желании что-то послать
	// и прочитать ответ: то, что делают в nc.
	KindTCP = "tcp"
	// KindHTTP и KindHTTPS — запрос и ответ целиком: код, заголовки, тело.
	KindHTTP  = "http"
	KindHTTPS = "https"
	// KindTLS — рукопожатие и сертификат, а дальше тот же обмен, что у
	// TCP: для почты на 465/993 и всего, что говорит TLS, но не HTTP.
	KindTLS = "tls"
	// KindCurl — настоящий curl с параметрами оператора.
	KindCurl = "curl"
)

const (
	// maxBody — сколько тела ответа отдавать. Страницу с картинками
	// увидеть нужно, а скачивать дистрибутив — нет.
	maxBody = 2 << 20
	// maxRequestBody — потолок тела запроса: окно проверки, а не
	// загрузчик файлов.
	maxRequestBody = 64 << 10
	// maxExchange — сколько читать из сырого TCP-соединения.
	maxExchange = 64 << 10
	// bannerIdle — сколько ждать тишины после последнего байта: сервер,
	// который здоровается, делает это сразу, а молчаливый не заговорит и
	// через минуту.
	bannerIdle = 700 * time.Millisecond
)

// Request — что проверить.
type Request struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Kind    string `json:"kind"`

	// HTTP: метод, путь, заголовки строками «Имя: значение», тело и его тип.
	Path        string   `json:"path,omitempty"`
	Method      string   `json:"method,omitempty"`
	Headers     []string `json:"headers,omitempty"`
	Body        string   `json:"body,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	// Host — заголовок Host (и SNI): сервис за именем отвечает по
	// адресу иначе, чем по имени, и это как раз то, что проверяют.
	Host string `json:"host,omitempty"`
	// Insecure — не проверять сертификат: самоподписанный на тестовом
	// стенде — норма, а проверить, что порт отвечает, всё равно нужно.
	Insecure bool `json:"insecure,omitempty"`

	// TCP/TLS: что послать после соединения (и баннера). SendCRLF
	// дописывает «\r\n» — так заканчивается команда у SMTP, Redis, HTTP.
	Send     string `json:"send,omitempty"`
	SendCRLF bool   `json:"send_crlf,omitempty"`

	// Curl: аргументы командной строки как есть, без оболочки.
	Args string `json:"args,omitempty"`

	TimeoutS int `json:"timeout_s,omitempty"`
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
	// ContentType и Body — тело ответа как байты: html отрисовывают,
	// картинку показывают, бинарь скачивают. Body — base64 в JSON.
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"body,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	// Received — что пришло по сырому соединению (баннер и ответ на
	// посланное); Printable — оно же, если это текст.
	Received  []byte `json:"received,omitempty"`
	Printable bool   `json:"printable,omitempty"`
	TLS       *TLSInfo `json:"tls,omitempty"`
	// Command — то же самое для терминала: curl, nc или openssl.
	Command string `json:"command"`
}

var (
	methodRe = regexp.MustCompile(`^[A-Z][A-Z-]{1,15}$`)
	headerRe = regexp.MustCompile(`^[A-Za-z0-9-]+:\s*.*$`)
)

// Validate приводит запрос в порядок и отвергает то, что не годится.
//
// Адрес — только IP, не имя: проверяют конкретный слушающий сокет, а имя
// открыло бы путь к любому адресу в сети, куда хост ходить не должен.
// У curl адрес не проверяется — там его задаёт оператор в аргументах.
func (r *Request) Validate() error {
	switch r.Kind {
	case KindTCP, KindHTTP, KindHTTPS, KindTLS, KindCurl:
	case "":
		r.Kind = KindTCP
	default:
		return fmt.Errorf("неизвестный вид проверки %q", r.Kind)
	}
	if r.TimeoutS <= 0 {
		r.TimeoutS = 5
	}
	if r.TimeoutS > 30 {
		r.TimeoutS = 30
	}
	if r.Kind == KindCurl {
		if strings.TrimSpace(r.Args) == "" {
			return fmt.Errorf("укажите аргументы curl")
		}
		return nil
	}

	r.Address = strings.Trim(strings.TrimSpace(r.Address), "[]")
	if net.ParseIP(r.Address) == nil {
		return fmt.Errorf("адрес должен быть IP-адресом, получено %q", r.Address)
	}
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("порт вне диапазона: %d", r.Port)
	}
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	if !methodRe.MatchString(r.Method) {
		return fmt.Errorf("метод %q не похож на HTTP-метод", r.Method)
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
	var headers []string
	for _, h := range r.Headers {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if !headerRe.MatchString(h) {
			return fmt.Errorf("заголовок должен быть вида «Имя: значение», получено %q", h)
		}
		headers = append(headers, h)
	}
	r.Headers = headers
	if len(r.Body) > maxRequestBody {
		return fmt.Errorf("тело запроса длиннее %d КиБ", maxRequestBody>>10)
	}
	if strings.ContainsAny(r.ContentType, "\r\n") {
		return fmt.Errorf("некорректный Content-Type")
	}
	if r.ContentType == "application/json" && strings.TrimSpace(r.Body) != "" && !json.Valid([]byte(r.Body)) {
		return fmt.Errorf("тело объявлено как JSON, но JSON в нём не разбирается")
	}
	if len(r.Send) > maxRequestBody {
		return fmt.Errorf("отправляемые данные длиннее %d КиБ", maxRequestBody>>10)
	}
	return nil
}

func (r Request) hostPort() string {
	return net.JoinHostPort(r.Address, fmt.Sprintf("%d", r.Port))
}

func (r Request) payload() []byte {
	if r.Send == "" {
		return nil
	}
	if r.SendCRLF && !strings.HasSuffix(r.Send, "\n") {
		return []byte(r.Send + "\r\n")
	}
	return []byte(r.Send)
}

// shellQuote — одинарные кавычки для показа команды: то, что попадёт в
// терминал, должно повторить запрос буквально.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`!*?;&|<>()[]{}") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Command — эквивалент для терминала.
func Command(r Request) string {
	target := r.hostPort()
	switch r.Kind {
	case KindCurl:
		return "curl " + strings.TrimSpace(r.Args)
	case KindHTTP, KindHTTPS:
		parts := []string{"curl", "-sS", "-i", "-m", fmt.Sprintf("%d", r.TimeoutS)}
		if r.Method != http.MethodGet {
			parts = append(parts, "-X", r.Method)
		}
		if r.Kind == KindHTTPS && r.Insecure {
			parts = append(parts, "-k")
		}
		if r.Host != "" {
			parts = append(parts, "-H", shellQuote("Host: "+r.Host))
		}
		for _, h := range r.Headers {
			parts = append(parts, "-H", shellQuote(h))
		}
		if r.Body != "" {
			if r.ContentType != "" {
				parts = append(parts, "-H", shellQuote("Content-Type: "+r.ContentType))
			}
			parts = append(parts, "--data-binary", shellQuote(r.Body))
		}
		parts = append(parts, fmt.Sprintf("%s://%s%s", r.Kind, target, r.Path))
		return strings.Join(parts, " ")
	case KindTLS:
		cmd := fmt.Sprintf("openssl s_client -quiet -connect %s", target)
		if r.Host != "" {
			cmd += " -servername " + r.Host
		}
		if p := r.payload(); p != nil {
			return fmt.Sprintf("printf %s | %s", shellQuote(string(p)), cmd)
		}
		return cmd + " </dev/null"
	default:
		cmd := fmt.Sprintf("nc -w %d %s %d", r.TimeoutS, r.Address, r.Port)
		if p := r.payload(); p != nil {
			return fmt.Sprintf("printf %s | %s", shellQuote(string(p)), cmd)
		}
		return cmd + " </dev/null"
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
	case KindCurl:
		res.Error = "curl выполняется на хосте, а не здесь"
		return res

	case KindTCP:
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", r.hostPort())
		if err != nil {
			res.Error = dialError(err)
			return res
		}
		defer conn.Close()
		res.OK = true
		exchange(ctx, conn, r.payload(), &res)
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
		exchange(ctx, tconn, r.payload(), &res)
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
		var body io.Reader
		if r.Body != "" {
			body = strings.NewReader(r.Body)
		}
		req, err := http.NewRequestWithContext(ctx, r.Method, url, body)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		if r.Host != "" {
			req.Host = r.Host
		}
		req.Header.Set("User-Agent", "nkt-portprobe")
		if r.Body != "" && r.ContentType != "" {
			req.Header.Set("Content-Type", r.ContentType)
		}
		for _, h := range r.Headers {
			name, value, _ := strings.Cut(h, ":")
			req.Header.Set(strings.TrimSpace(name), strings.TrimSpace(value))
		}
		resp, err := client.Do(req)
		if err != nil {
			res.Error = dialError(err)
			return res
		}
		defer resp.Body.Close()
		res.OK = true
		res.Status = resp.Proto + " " + resp.Status
		res.ContentType = resp.Header.Get("Content-Type")
		for k, vs := range resp.Header {
			for _, v := range vs {
				res.Headers = append(res.Headers, k+": "+v)
			}
		}
		sort.Strings(res.Headers)
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		if len(data) > maxBody {
			data = data[:maxBody]
			res.Truncated = true
		}
		res.Body = data
		if resp.TLS != nil {
			res.TLS = tlsInfo(*resp.TLS, !r.Insecure)
		}
		return res
	}
}

// exchange — обмен по сырому соединению: сначала баннер (то, что сервер
// шлёт сам), потом, если есть что, отправка и чтение ответа.
//
// Читается до тишины, а не до конца: сервер соединение не закрывает, и
// ждать конца значило бы ждать таймаута каждый раз.
func exchange(ctx context.Context, conn net.Conn, payload []byte, res *Result) {
	var buf bytes.Buffer
	readUntilQuiet(ctx, conn, &buf)
	if payload != nil {
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetWriteDeadline(deadline)
		}
		if _, err := conn.Write(payload); err != nil {
			res.Error = "отправка: " + err.Error()
			return
		}
		readUntilQuiet(ctx, conn, &buf)
	}
	res.Received = buf.Bytes()
	res.Printable = isPrintable(res.Received)
}

func readUntilQuiet(ctx context.Context, conn net.Conn, buf *bytes.Buffer) {
	chunk := make([]byte, 4096)
	for buf.Len() < maxExchange {
		idle := time.Now().Add(bannerIdle)
		if deadline, ok := ctx.Deadline(); ok && deadline.Before(idle) {
			idle = deadline
		}
		_ = conn.SetReadDeadline(idle)
		n, err := conn.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if err != nil {
			return
		}
	}
}

// isPrintable отвечает, можно ли показать байты как текст.
func isPrintable(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, r := range string(b) {
		if r == unicode.ReplacementChar {
			return false
		}
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
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
