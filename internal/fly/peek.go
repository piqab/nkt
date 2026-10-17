package fly

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strings"
)

// Имя сайта из начала соединения — без расшифровки и без ответа клиенту:
// прочитанное потом целиком уходит цели.

var errNoName = errors.New("fly: no server name")

// maxPeek — сколько байт можно прочитать до имени (ClientHello с
// длинными расширениями, заголовки HTTP).
const maxPeek = 16 << 10

// peekSNI читает TLS ClientHello и достаёт server_name. Возвращает имя
// и прочитанные байты.
func peekSNI(r *bufio.Reader) (string, []byte, error) {
	hdr, err := r.Peek(5)
	if err != nil {
		return "", nil, err
	}
	if hdr[0] != 0x16 { // handshake
		return "", nil, errNoName
	}
	n := int(binary.BigEndian.Uint16(hdr[3:5]))
	if n <= 0 || n+5 > maxPeek {
		return "", nil, errNoName
	}
	rec, err := r.Peek(5 + n)
	if err != nil {
		return "", nil, err
	}
	name, err := sniFromHello(rec[5:])
	return name, rec, err
}

// sniFromHello — server_name из тела ClientHello (одна запись TLS).
func sniFromHello(b []byte) (string, error) {
	if len(b) < 4 || b[0] != 0x01 { // client_hello
		return "", errNoName
	}
	b = b[4:]
	// версия (2) + random (32)
	if len(b) < 34 {
		return "", errNoName
	}
	b = b[34:]
	skip := func(lenBytes int) bool {
		if len(b) < lenBytes {
			return false
		}
		var n int
		for i := 0; i < lenBytes; i++ {
			n = n<<8 | int(b[i])
		}
		if len(b) < lenBytes+n {
			return false
		}
		b = b[lenBytes+n:]
		return true
	}
	if !skip(1) || !skip(2) || !skip(1) { // session id, cipher suites, compression
		return "", errNoName
	}
	if len(b) < 2 {
		return "", errNoName
	}
	ext := b[2:]
	if int(binary.BigEndian.Uint16(b)) < len(ext) {
		ext = ext[:binary.BigEndian.Uint16(b)]
	}
	for len(ext) >= 4 {
		typ := binary.BigEndian.Uint16(ext)
		l := int(binary.BigEndian.Uint16(ext[2:]))
		if len(ext) < 4+l {
			break
		}
		data := ext[4 : 4+l]
		ext = ext[4+l:]
		if typ != 0 { // server_name
			continue
		}
		if len(data) < 2 {
			break
		}
		list := data[2:]
		for len(list) >= 3 {
			nt := list[0]
			nl := int(binary.BigEndian.Uint16(list[1:]))
			if len(list) < 3+nl {
				break
			}
			if nt == 0 {
				return strings.ToLower(string(list[3 : 3+nl])), nil
			}
			list = list[3+nl:]
		}
	}
	return "", errNoName
}

// peekHost читает заголовки HTTP-запроса и достаёт Host (без порта).
// Читается только то, что клиент уже прислал (и ещё по байту, пока
// заголовки не кончатся): короткий запрос не ждёт добивки до размера
// буфера. r — с буфером не меньше maxPeek.
func peekHost(r *bufio.Reader) (string, []byte, error) {
	n := 1
	for {
		if _, err := r.Peek(n); err != nil {
			b, _ := r.Peek(r.Buffered())
			return "", b, err
		}
		b, _ := r.Peek(r.Buffered())
		if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
			return hostHeader(b[:i]), b, nil
		}
		if len(b) >= maxPeek {
			return "", b, errNoName
		}
		n = len(b) + 1
	}
}

func hostHeader(head []byte) string {
	for _, line := range strings.Split(string(head), "\r\n")[1:] {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "host") {
			h := strings.ToLower(strings.TrimSpace(v))
			if host, _, err := net.SplitHostPort(h); err == nil {
				return host
			}
			return h
		}
	}
	return ""
}

// matchName — имя подходит под шаблон (точное или *.example.com — один
// уровень и глубже).
func matchName(pattern, name string) bool {
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(name, pattern[1:]) && len(name) > len(pattern)-1
	}
	return pattern == name
}

// proxyHeaderV2 — заголовок PROXY protocol v2 (TCP over IPv4/IPv6): цель
// узнаёт настоящий адрес клиента.
func proxyHeaderV2(src, dst netip.AddrPort) []byte {
	sig := []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}
	var b bytes.Buffer
	b.Write(sig)
	b.WriteByte(0x21) // v2, PROXY
	s, d := src.Addr().Unmap(), dst.Addr().Unmap()
	if s.Is4() && d.Is4() {
		b.WriteByte(0x11) // TCP over IPv4
		_ = binary.Write(&b, binary.BigEndian, uint16(12))
		a, c := s.As4(), d.As4()
		b.Write(a[:])
		b.Write(c[:])
	} else {
		b.WriteByte(0x21) // TCP over IPv6
		_ = binary.Write(&b, binary.BigEndian, uint16(36))
		a, c := s.As16(), d.As16()
		b.Write(a[:])
		b.Write(c[:])
	}
	_ = binary.Write(&b, binary.BigEndian, src.Port())
	_ = binary.Write(&b, binary.BigEndian, dst.Port())
	return b.Bytes()
}
