package collect

import (
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"

	"github.com/piqab/nkt/internal/msgs"
)

// Источники HTTP описаний сервисов ходят только к API программ на этом
// хосте и во внутренней сети: loopback, частные сети (RFC 1918, fc00::/7)
// и link-local. Имя хоста — только localhost: иначе описание могло бы
// отправить файл секрета (basic_auth) на чужой адрес.

// LocalAddr — адрес разрешён для HTTP-источника.
func LocalAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast()
}

// CheckLocalURL — адрес http(s) без логина в адресе, к localhost или к IP
// из LocalAddr.
func CheckLocalURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, msgs.Errorf("collect.httpNotLocal", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return u, nil
	}
	a, err := netip.ParseAddr(host)
	if err != nil || !LocalAddr(a) {
		return nil, msgs.Errorf("collect.httpNotLocal", raw)
	}
	return u, nil
}

// localOnlyControl — проверка при соединении (после разрешения имени): тот
// же список адресов, что у CheckLocalURL.
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
