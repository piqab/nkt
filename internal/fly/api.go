package fly

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// Команды хаба шлюзу (по туннелю edge) и хосту (по API хоста).
const (
	PathKey    = "/fly/key"    // POST — создать ключ, если нет; ответ — публичный
	PathConfig = "/fly/config" // PUT — применить конфигурацию
	PathStatus = "/fly/status" // GET — сводка
)

// KeyResponse — публичный ключ (закрытый не покидает машину).
type KeyResponse struct {
	PublicKey string `json:"public_key"`
}

// GatewayStatus — сводка шлюза для карты.
type GatewayStatus struct {
	Revision  int64        `json:"revision"`
	PublicKey string       `json:"public_key,omitempty"`
	Now       int64        `json:"now"`
	Routes    []RouteStats `json:"routes"`
	Peers     []PeerStats  `json:"peers"`
	Errors    []string     `json:"errors,omitempty"`
}

// HostIfaceStatus — туннель хоста к шлюзу.
type HostIfaceStatus struct {
	Name      string `json:"name"`
	Up        bool   `json:"up"`
	Handshake int64  `json:"handshake"`
	RxBytes   int64  `json:"rx_bytes"`
	TxBytes   int64  `json:"tx_bytes"`
	Error     string `json:"error,omitempty"`
}

// HostStatus — сводка хоста.
type HostStatus struct {
	Revision int64             `json:"revision"`
	Now      int64             `json:"now"`
	Ifaces   []HostIfaceStatus `json:"ifaces"`
}

// LoadOrCreateKey — закрытый ключ WireGuard из файла (0600); нет — создаёт.
// Возвращает публичный.
func LoadOrCreateKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		return PublicKey(strings.TrimSpace(string(raw)))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", err
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	priv := base64.StdEncoding.EncodeToString(k[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(priv+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return PublicKey(priv)
}

// PublicKey — публичный ключ по закрытому (base64).
func PublicKey(priv string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(priv)
	if err != nil || len(k) != 32 {
		return "", errors.New("fly: bad private key")
	}
	pub, err := curve25519.X25519(k, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}
