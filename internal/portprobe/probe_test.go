package portprobe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
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
// заголовки, тело с его типом — и команду curl, которой то же самое
// повторяют в терминале.
func TestProbeHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Probe", "yes")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("<b>hello from " + r.Host + "</b>"))
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
	if !strings.Contains(string(res.Body), "hello from shop.local") || res.ContentType != "text/html" {
		t.Errorf("тело = %q (%s), заголовок Host не дошёл или тип потерян", res.Body, res.ContentType)
	}
	for _, want := range []string{"curl", "-H 'Host: shop.local'", "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/x"} {
		if !strings.Contains(res.Command, want) {
			t.Errorf("в команде нет %q: %s", want, res.Command)
		}
	}
}

// Тело запроса, свой метод и заголовки доходят до сервера как есть, а
// JSON проверяется до отправки: битый JSON — ошибка формы, не сервера.
func TestProbeHTTPBodyAndHeaders(t *testing.T) {
	var got struct {
		method, ctype, auth, body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.ctype, got.auth, got.body = r.Method, r.Header.Get("Content-Type"), r.Header.Get("Authorization"), string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)

	res := Probe(context.Background(), Request{
		Address: host, Port: port, Kind: KindHTTP, Method: "PATCH",
		Body: `{"a":1}`, ContentType: "application/json",
		Headers: []string{"Authorization: Bearer t0k", ""},
	})
	if !res.OK || got.method != "PATCH" || got.ctype != "application/json" || got.auth != "Bearer t0k" || got.body != `{"a":1}` {
		t.Errorf("до сервера дошло не то: ok=%v err=%q %+v", res.OK, res.Error, got)
	}
	for _, want := range []string{"-X PATCH", "-H 'Authorization: Bearer t0k'", "--data-binary '{\"a\":1}'"} {
		if !strings.Contains(res.Command, want) {
			t.Errorf("в команде нет %q: %s", want, res.Command)
		}
	}

	bad := Request{Address: host, Port: port, Kind: KindHTTP, Method: "POST", Body: "{oops", ContentType: "application/json"}
	if err := bad.Validate(); err == nil {
		t.Error("битый JSON принят")
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

// Сырое соединение: баннер читается сам, посланное уходит, ответ
// возвращается — то, что делают в nc.
func TestProbeTCPExchange(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = c.Write([]byte("220 mail.local ESMTP\r\n"))
				buf := make([]byte, 256)
				n, _ := c.Read(buf)
				if strings.HasPrefix(string(buf[:n]), "EHLO") {
					_, _ = c.Write([]byte("250 hello\r\n"))
				}
			}(c)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	banner := Probe(context.Background(), Request{Address: "127.0.0.1", Port: port, TimeoutS: 3})
	if !banner.OK || !strings.Contains(string(banner.Received), "220 mail.local") || !banner.Printable {
		t.Errorf("баннер: ok=%v received=%q printable=%v err=%q", banner.OK, banner.Received, banner.Printable, banner.Error)
	}

	reply := Probe(context.Background(), Request{Address: "127.0.0.1", Port: port, TimeoutS: 3, Send: "EHLO probe", SendCRLF: true})
	if !strings.Contains(string(reply.Received), "250 hello") {
		t.Errorf("ответ на посланное не прочитан: %q (%s)", reply.Received, reply.Error)
	}
	if !strings.Contains(reply.Command, "printf") || !strings.Contains(reply.Command, "nc -w") {
		t.Errorf("команда = %s", reply.Command)
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
}

// Адрес — только IP: имя открыло бы путь к любому узлу сети, куда хост
// ходить не должен. Метод — любой похожий на HTTP-метод, а не список.
func TestRequestValidate(t *testing.T) {
	bad := []Request{
		{Address: "example.com", Port: 80},
		{Address: "127.0.0.1", Port: 0},
		{Address: "127.0.0.1", Port: 80, Kind: "udp"},
		{Address: "127.0.0.1", Port: 80, Kind: KindHTTP, Method: "not a method"},
		{Address: "127.0.0.1", Port: 80, Kind: KindHTTP, Path: "no-slash"},
		{Address: "127.0.0.1", Port: 80, Kind: KindHTTP, Headers: []string{"no colon"}},
		{Kind: KindCurl, Args: "   "},
	}
	for _, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v принят", r)
		}
	}
	ok := Request{Address: "[::1]", Port: 443, Kind: KindHTTPS, Method: "propfind"}
	if err := ok.Validate(); err != nil || ok.Address != "::1" || ok.Path != "/" || ok.TimeoutS != 5 || ok.Method != "PROPFIND" {
		t.Errorf("верный запрос: err=%v, %+v", err, ok)
	}
}

// Аргументы curl разбираются как в оболочке, но без неё: кавычки и
// экранирование работают, а «;» и «|» остаются просто символами.
func TestSplitArgs(t *testing.T) {
	got, err := SplitArgs(`-sS -H 'Host: a b' -d "x=\"1\"" http://h/;rm -rf /`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-sS", "-H", "Host: a b", "-d", `x="1"`, "http://h/;rm", "-rf", "/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitArgs = %q, want %q", got, want)
	}
	if _, err := SplitArgs(`-H 'unclosed`); err == nil {
		t.Error("незакрытая кавычка принята")
	}
}

// curl запускается без оболочки, с таймаутом, а его вывод возвращается
// как тело; отказ curl — ошибка с его же последней строкой.
func TestRunCurl(t *testing.T) {
	var argv []string
	run := func(_ context.Context, a ...string) (collect.CommandResult, error) {
		argv = a
		if strings.Contains(strings.Join(a, " "), "fail") {
			return collect.CommandResult{ExitCode: 7, Stderr: "curl: (7) Failed to connect\n"}, nil
		}
		return collect.CommandResult{Stdout: "HTTP/1.1 200 OK\r\n\r\nbody"}, nil
	}
	res := RunCurl(context.Background(), run, Request{Kind: KindCurl, Args: "-sS -i 'http://127.0.0.1:80/'"})
	if !res.OK || !strings.Contains(string(res.Body), "200 OK") {
		t.Errorf("ok=%v err=%q body=%q", res.OK, res.Error, res.Body)
	}
	if argv[0] != "curl" || argv[1] != "-m" || argv[len(argv)-1] != "http://127.0.0.1:80/" {
		t.Errorf("argv = %q: нет curl, таймаута или адрес развалился", argv)
	}
	bad := RunCurl(context.Background(), run, Request{Kind: KindCurl, Args: "http://fail/"})
	if bad.OK || !strings.Contains(bad.Error, "Failed to connect") {
		t.Errorf("отказ curl: ok=%v err=%q", bad.OK, bad.Error)
	}
}
