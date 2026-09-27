// Package edge — протокол между хабом и nkt-edge: маленьким входным
// сервисом на VPS, который принимает вебхуки выкладок из интернета и
// передаёт их хабу, когда сам хаб за NAT и открывать его наружу не нужно.
//
// Соединение устанавливает хаб: TLS до edge (сертификат edge самоподписан,
// хаб запоминает его отпечаток при первом подключении и дальше сверяет),
// затем строка «NKTEDGE <версия> <токен>», ответ «OK <версия>», и поверх —
// yamux. Запросы идут в обратную сторону: edge открывает поток на каждый
// вебхук, хаб отвечает на нём обычным HTTP. Через туннель хаб обслуживает
// только POST /hooks/{id} — интерфейс и API хаба через edge недоступны.
//
// Пакет нарочно без зависимостей nkt: его собирают в отдельный маленький
// бинарник (cmd/nkt-edge).
package edge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Version — версия протокола туннеля.
const Version = 1

// ErrFingerprint — сертификат edge не тот, что запомнил хаб.
var ErrFingerprint = errors.New("edge certificate fingerprint mismatch")

// ErrRejected — edge отверг токен или версию.
var ErrRejected = errors.New("edge rejected the hub")

// Fingerprint — SHA-256 сертификата в hex.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// Dial — сторона хаба: TLS, сверка отпечатка (пусто — принять и вернуть
// увиденный), рукопожатие. Возвращает соединение, готовое к yamux.
func Dial(addr, token, fingerprint string, timeout time.Duration) (net.Conn, string, error) {
	d := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{
		// Доверие — отпечатком, а не цепочкой: сертификат самоподписан.
		InsecureSkipVerify: true, //nolint:gosec // проверка отпечатка ниже
		MinVersion:         tls.VersionTLS13,
	})
	if err != nil {
		return nil, "", err
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		conn.Close()
		return nil, "", ErrFingerprint
	}
	seen := Fingerprint(certs[0].Raw)
	if fingerprint != "" && subtle.ConstantTimeCompare([]byte(seen), []byte(strings.ToLower(fingerprint))) != 1 {
		conn.Close()
		return nil, seen, ErrFingerprint
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := fmt.Fprintf(conn, "NKTEDGE %d %s\n", Version, token); err != nil {
		conn.Close()
		return nil, seen, err
	}
	line, err := readLine(conn)
	if err != nil {
		conn.Close()
		return nil, seen, err
	}
	if !strings.HasPrefix(line, "OK ") {
		conn.Close()
		return nil, seen, fmt.Errorf("%w: %s", ErrRejected, line)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, seen, nil
}

// Accept — сторона edge: проверка строки хаба. Ответ пишется здесь же.
func Accept(conn net.Conn, token string) error {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	line, err := readLine(conn)
	if err != nil {
		return err
	}
	parts := strings.Fields(line)
	if len(parts) != 3 || parts[0] != "NKTEDGE" {
		fmt.Fprint(conn, "ERR protocol\n")
		return ErrRejected
	}
	if v, err := strconv.Atoi(parts[1]); err != nil || v != Version {
		fmt.Fprintf(conn, "ERR version %d\n", Version)
		return fmt.Errorf("%w: version %s", ErrRejected, parts[1])
	}
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(token)) != 1 {
		fmt.Fprint(conn, "ERR token\n")
		return fmt.Errorf("%w: token", ErrRejected)
	}
	if _, err := fmt.Fprintf(conn, "OK %d\n", Version); err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Time{})
	return nil
}

// readLine читает строку до \n, не больше 512 байт и не буферизуя лишнего
// (дальше по тому же соединению идёт yamux).
func readLine(conn net.Conn) (string, error) {
	var b strings.Builder
	one := make([]byte, 1)
	for b.Len() < 512 {
		if _, err := conn.Read(one); err != nil {
			return "", err
		}
		if one[0] == '\n' {
			return strings.TrimSpace(b.String()), nil
		}
		b.WriteByte(one[0])
	}
	return "", ErrRejected
}

// TunnelCert — самоподписанный сертификат туннеля edge: создаётся один
// раз и хранится в каталоге данных (отпечаток не меняется между
// перезапусками — иначе хаб перестал бы ему доверять).
func TunnelCert(dir string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, "tunnel.crt"), filepath.Join(dir, "tunnel.key")
	if _, err := os.Stat(certPath); err != nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return tls.Certificate{}, "", err
		}
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
		tmpl := &x509.Certificate{
			SerialNumber: serial,
			Subject:      pkix.Name{CommonName: "nkt-edge tunnel"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().AddDate(20, 0, 0),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		kb, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
			return tls.Certificate{}, "", err
		}
		if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
			return tls.Certificate{}, "", err
		}
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, Fingerprint(cert.Certificate[0]), nil
}
