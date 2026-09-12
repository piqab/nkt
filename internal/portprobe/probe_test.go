package portprobe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func hostPort(t *testing.T, u string) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://"))
	if err != nil {
		t.Fatalf("split %q: %v", u, err)
	}
	n, _ := strconv.Atoi(port)
	return host, n
}

// Проверка по HTTP приносит то, ради чего её делают: код ответа,
// заголовки и начало тела — и команду curl, которой то же самое
// повторяют в терминале.
func TestProbeHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Probe", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello from " + r.Host))
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)

	res := Probe(context.Background(), Request{Address: host, Port: port, Kind: KindHTTP, Path: "/x", Host: "shop.local"})
	if !res.OK {
		t.Fatalf("проверка не удалась: %s", res.Error)
	}
	if !strings.Contains(res.Status, "418") {
		t.Errorf("статус = %q", res.Status)
	}
	if !strings.Contains(strings.Join(res.Headers, "\n"), "X-Probe: yes") {
		t.Errorf("заголовки = %v", res.Headers)
	}
	if !strings.Contains(res.Body, "hello from shop.local") {
		t.Errorf("тело = %q, заголовок Host не дошёл", res.Body)
	}
	for _, want := range []string{"curl", "-H 'Host: shop.local'", "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/x"} {
		if !strings.Contains(res.Command, want) {
			t.Errorf("в команде нет %q: %s", want, res.Command)
		}
	}
}

// Самоподписанный сертификат стенда — норма: с Insecure рукопожатие
// проходит, а в ответе видно, что сертификат не проверялся.
func TestProbeHTTPSAndTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)

	strict := Probe(context.Background(), Request{Address: host, Port: port, Kind: KindHTTPS})
	if strict.OK {
		t.Errorf("самоподписанный сертификат принят без -k")
	}
	loose := Probe(context.Background(), Request{Address: host, Port: port, Kind: KindHTTPS, Insecure: true})
	if !loose.OK {
		t.Fatalf("с Insecure проверка не удалась: %s", loose.Error)
	}
	if loose.TLS == nil || loose.TLS.Verified {
		t.Errorf("TLS = %+v, ожидался сертификат с пометкой «не проверялся»", loose.TLS)
	}
	if !strings.Contains(loose.Command, " -k ") {
		t.Errorf("в команде нет -k: %s", loose.Command)
	}

	tlsOnly := Probe(context.Background(), Request{Address: host, Port: port, Kind: KindTLS})
	if !tlsOnly.OK || tlsOnly.TLS == nil || tlsOnly.TLS.NotAfter == "" {
		t.Errorf("TLS-рукопожатие: ok=%v err=%q tls=%+v", tlsOnly.OK, tlsOnly.Error, tlsOnly.TLS)
	}
	if !strings.HasPrefix(tlsOnly.Command, "openssl s_client") {
		t.Errorf("команда = %s", tlsOnly.Command)
	}
}

// Закрытый порт — понятное «никто не слушает», а не сырое «connection
// refused».
func TestProbeTCPRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	res := Probe(context.Background(), Request{Address: "127.0.0.1", Port: port})
	if res.OK {
		t.Fatal("закрытый порт объявлен открытым")
	}
	if !strings.Contains(res.Error, "никто не слушает") {
		t.Errorf("ошибка = %q", res.Error)
	}
	if !strings.HasPrefix(res.Command, "nc -zv") {
		t.Errorf("команда = %s", res.Command)
	}
}

// Адрес — только IP: имя открыло бы путь к любому узлу сети, куда хост
// ходить не должен.
func TestRequestValidate(t *testing.T) {
	bad := []Request{
		{Address: "example.com", Port: 80},
		{Address: "127.0.0.1", Port: 0},
		{Address: "127.0.0.1", Port: 80, Kind: "udp"},
		{Address: "127.0.0.1", Port: 80, Kind: KindHTTP, Method: "TRACE"},
		{Address: "127.0.0.1", Port: 80, Kind: KindHTTP, Path: "no-slash"},
	}
	for _, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v принят", r)
		}
	}
	ok := Request{Address: "[::1]", Port: 443, Kind: KindHTTPS}
	if err := ok.Validate(); err != nil || ok.Address != "::1" || ok.Path != "/" || ok.TimeoutS != 5 {
		t.Errorf("верный запрос: err=%v, %+v", err, ok)
	}
}
