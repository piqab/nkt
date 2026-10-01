package edge

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Роль probe: хаб просит edge проверить что-то из интернета — так, как
// это видит посторонний: DNS, открыт ли порт, что отдаёт HTTPS и какой
// сертификат. Хаб за NAT сам «снаружи» не видит. Запрос идёт только по
// туннелю (хаб открывает поток), из интернета эта роль недоступна; тела
// ответов сайтов не возвращаются — только код и сертификат.

// ProbePath — путь запроса проверки в туннеле.
const ProbePath = "/probe"

// Ограничения запроса проверки.
const (
	ProbeMaxChecks = 32
	probeTimeout   = 10 * time.Second
)

// ProbeCheck — одна проверка: dns (имя), tcp (адрес и порт), https (имя и
// порт, по умолчанию 443).
type ProbeCheck struct {
	Type string `json:"type"`
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
}

// ProbeRequest — проверки разом.
type ProbeRequest struct {
	Checks []ProbeCheck `json:"checks"`
}

// ProbeResult — итог одной проверки.
type ProbeResult struct {
	ProbeCheck
	// IPs — адреса имени (dns).
	IPs []string `json:"ips,omitempty"`
	// State — tcp: open, refused, timeout или «error: …».
	State string `json:"state,omitempty"`
	// https: код ответа, сертификат.
	Status       int      `json:"status,omitempty"`
	CertNotAfter string   `json:"cert_not_after,omitempty"`
	CertIssuer   string   `json:"cert_issuer,omitempty"`
	CertNames    []string `json:"cert_names,omitempty"`
	WrongCert    bool     `json:"wrong_cert,omitempty"`
	Error        string   `json:"error,omitempty"`
	Millis       int64    `json:"ms"`
}

// ProbeResponse — ответ edge.
type ProbeResponse struct {
	Results []ProbeResult `json:"results"`
}

var probeHostRe = regexp.MustCompile(`^[A-Za-z0-9.:_-]{1,253}$`)

// errProbePrivate — адрес не из интернета: роль «проверки снаружи»
// смотрит на то, что видит интернет, и в сеть самого VPS (локальные
// службы, метаданные облака 169.254.169.254) не ходит.
var errProbePrivate = errors.New("blocked: not a public address")

// probeAllowPrivate — тесты проверяют на loopback.
var probeAllowPrivate = false

// publicAddr — адрес из интернета (не loopback, не частная сеть, не
// link-local, не CGNAT, не служебный).
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	return !netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

// probeDialer — соединения проверок: адрес проверяется после DNS, перед
// соединением, — именем, которое резолвится во внутренний адрес, запрет
// не обойти.
func probeDialer() *net.Dialer {
	return &net.Dialer{Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		a, err := netip.ParseAddr(host)
		if err != nil || (!probeAllowPrivate && !publicAddr(a)) {
			return errProbePrivate
		}
		return nil
	}}
}

// ValidProbe — проверка понятна и безопасна для командной строки и URL.
func ValidProbe(c ProbeCheck) bool {
	if !probeHostRe.MatchString(c.Host) {
		return false
	}
	switch c.Type {
	case "dns":
		return c.Port == 0
	case "tcp":
		return c.Port >= 1 && c.Port <= 65535
	case "https":
		return c.Port >= 0 && c.Port <= 65535
	}
	return false
}

// RunProbe — проверки параллельно (не больше восьми разом).
func RunProbe(ctx context.Context, req ProbeRequest) ProbeResponse {
	out := ProbeResponse{Results: make([]ProbeResult, len(req.Checks))}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, c := range req.Checks {
		out.Results[i] = ProbeResult{ProbeCheck: c}
		if !ValidProbe(c) {
			out.Results[i].Error = "invalid check"
			continue
		}
		wg.Add(1)
		go func(i int, c ProbeCheck) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			r := &out.Results[i]
			cctx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			switch c.Type {
			case "dns":
				addrs, err := net.DefaultResolver.LookupIPAddr(cctx, c.Host)
				if err != nil {
					r.Error = err.Error()
				}
				for _, a := range addrs {
					r.IPs = append(r.IPs, a.IP.String())
				}
			case "tcp":
				r.State = probeTCP(cctx, c.Host, c.Port)
			case "https":
				probeHTTPS(cctx, c, r)
			}
			r.Millis = time.Since(start).Milliseconds()
		}(i, c)
	}
	wg.Wait()
	return out
}

func probeTCP(ctx context.Context, host string, port int) string {
	conn, err := probeDialer().DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		_ = conn.Close()
		return "open"
	}
	if errors.Is(err, errProbePrivate) {
		return errProbePrivate.Error()
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "refused"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "error: " + err.Error()
}

func probeHTTPS(ctx context.Context, c ProbeCheck, r *ProbeResult) {
	host, port := c.Host, c.Port
	if port == 0 {
		port = 443
	}
	if port != 443 || strings.Contains(host, ":") {
		host = net.JoinHostPort(c.Host, strconv.Itoa(port))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		r.Error = err.Error()
		return
	}
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true,
			DialContext: probeDialer().DialContext},
		// Переадресация — тоже ответ сайта.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		r.Error = err.Error()
		var he x509.HostnameError
		if errors.As(err, &he) && he.Certificate != nil {
			r.WrongCert = true
			r.CertNames = certNamesOf(he.Certificate)
			r.CertNotAfter = he.Certificate.NotAfter.UTC().Format(time.RFC3339)
		}
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	r.Status = resp.StatusCode
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		r.CertNotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
		r.CertIssuer = cert.Issuer.CommonName
		r.CertNames = certNamesOf(cert)
	}
}

func certNamesOf(c *x509.Certificate) []string {
	if len(c.DNSNames) > 0 {
		return c.DNSNames
	}
	if c.Subject.CommonName != "" {
		return []string{c.Subject.CommonName}
	}
	return nil
}
