package collect

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"github.com/piqab/nkt/internal/msgs"
)

// Источники HTTP описаний сервисов ходят только к API программ на этом
// хосте: localhost, 127.0.0.1 (127.0.0.0/8) и ::1. Иначе описание могло бы
// отправить файл секрета (basic_auth) на чужой адрес или опрашивать
// соседние машины.

// LocalAddr — адрес разрешён для HTTP-источника: loopback.
func LocalAddr(a netip.Addr) bool { return a.Unmap().IsLoopback() }

// CheckLocalURL проверяет адрес и собирает его заново: схема и хост — из
// постоянных строк, порт — числом, путь и запрос — из исходного адреса.
func CheckLocalURL(raw string) (string, error) {
	bad := msgs.Errorf("collect.httpNotLocal", raw)
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" {
		return "", bad
	}
	scheme := ""
	switch u.Scheme {
	case "http":
		scheme = "http"
	case "https":
		scheme = "https"
	default:
		return "", bad
	}
	host := ""
	switch h := strings.ToLower(u.Hostname()); {
	case h == "localhost":
		host = "localhost"
	case h == "::1":
		host = "[::1]"
	default:
		a, err := netip.ParseAddr(h)
		if err != nil || !a.Is4() || !a.IsLoopback() {
			return "", bad
		}
		host = "127.0.0.1"
		if a != netip.MustParseAddr("127.0.0.1") {
			// Другой адрес из 127.0.0.0/8 — собирается из чисел.
			b := a.As4()
			host = "127." + strconv.Itoa(int(b[1])) + "." + strconv.Itoa(int(b[2])) + "." + strconv.Itoa(int(b[3]))
		}
	}
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return "", bad
		}
		host += ":" + strconv.FormatUint(n, 10)
	}
	out := url.URL{Scheme: scheme, Host: host, Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}
	return out.String(), nil
}

// localOnlyControl — проверка при соединении (после разрешения имени
// localhost): только loopback.
func localOnlyControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	a, err := netip.ParseAddr(host)
	if err != nil || !LocalAddr(a) {
		return msgs.Errorf("collect.httpNotLocal", address)
	}
	return nil
}
