package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Ключ registry конвейера на время pull: хаб присылает его в запросе
// выкладки, хост кладёт во временный config.json (0600) в своём каталоге
// данных и скачивает образы через docker --config <каталог> — без
// docker login и без следа в настройках Docker. В параметры задания идёт
// только путь каталога (не секрет); каталог удаляется сразу после pull.

// composeRegistryAuth — ключ registry из запроса выкладки.
type composeRegistryAuth struct {
	Host  string `json:"host"`
	User  string `json:"user"`
	Token string `json:"token"`
	// CA — свой центр сертификации registry (PEM): ставится в certs.d
	// движка — постоянно, только для этого registry (секрета в нём нет).
	CA string `json:"ca,omitempty"`
}

var registryHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252})(:[0-9]{1,5})?$`)

func (a composeRegistryAuth) valid() bool {
	clean := func(v string) bool { return v != "" && len(v) <= 4096 && !strings.ContainsAny(v, "\x00\r\n") }
	caOK := a.CA == "" || (len(a.CA) <= 64<<10 && strings.Contains(a.CA, "-----BEGIN CERTIFICATE-----"))
	return registryHostRe.MatchString(a.Host) && clean(a.User) && clean(a.Token) && !strings.Contains(a.User, ":") && caOK
}

// caFile — имя файла CA registry в каталоге ключей: по хэшу адреса, чтобы
// путь не собирался из присланной строки.
func caFile(host string) string {
	sum := sha256.Sum256([]byte(host))
	return "ca-" + hex.EncodeToString(sum[:8]) + ".crt"
}

// pullAuthStale — каталоги старше этого (хост перезапускался посреди
// выкладки) убираются при следующей.
const pullAuthStale = time.Hour

func (s *Server) pullAuthRoot() string { return filepath.Join(s.cfg.DataDir, "pull-auth") }

// writePullAuth — каталог с config.json (docker --config, podman
// --authfile) со всеми ключами и CA registry; ответ — каталог и registry
// со своим CA.
func (s *Server) writePullAuth(list []composeRegistryAuth) (string, []string, error) {
	for _, a := range list {
		if !a.valid() {
			return "", nil, msgs.Errorf("compose.registryAuthBad")
		}
	}
	root := s.pullAuthRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > pullAuthStale {
				_ = os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	dir := filepath.Join(root, hex.EncodeToString(b))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", nil, err
	}
	auths := map[string]any{}
	var caHosts []string
	for _, a := range list {
		key := a.Host
		if key == "docker.io" {
			key = "https://index.docker.io/v1/"
		}
		auths[key] = map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(a.User + ":" + a.Token))}
		if a.CA != "" {
			if err := os.WriteFile(filepath.Join(dir, caFile(a.Host)), []byte(a.CA), 0o644); err != nil {
				_ = os.RemoveAll(dir)
				return "", nil, err
			}
			caHosts = append(caHosts, a.Host)
		}
	}
	cfg, _ := json.Marshal(map[string]any{"auths": auths})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return dir, caHosts, nil
}

// certsDir — каталог CA registry у движка: docker и podman читают
// certs.d/<registry>/ca.crt и доверяют ему только для этого registry.
func certsDir(engine, host string) string {
	if engine == "podman" {
		return "/etc/containers/certs.d/" + host
	}
	return "/etc/docker/certs.d/" + host
}

// caInstallScript — копия CA в certs.d снаружи песочницы службы (/etc у
// неё только для чтения). Пути — аргументами: адрес проверен
// (registryHostRe), файл — из каталога ключей nkt.
const caInstallScript = `mkdir -p "$1" && cp "$2" "$1/ca.crt" && chmod 0644 "$1/ca.crt"`

// pullAuthDir — каталог из параметров задания, только из своего корня.
func (s *Server) pullAuthDir(dir string) string {
	if dir == "" || filepath.Dir(filepath.Clean(dir)) != filepath.Clean(s.pullAuthRoot()) {
		return ""
	}
	return dir
}
