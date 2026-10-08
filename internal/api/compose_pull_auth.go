package api

import (
	"crypto/rand"
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
}

var registryHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252})(:[0-9]{1,5})?$`)

func (a composeRegistryAuth) valid() bool {
	clean := func(v string) bool { return v != "" && len(v) <= 4096 && !strings.ContainsAny(v, "\x00\r\n") }
	return registryHostRe.MatchString(a.Host) && clean(a.User) && clean(a.Token) && !strings.Contains(a.User, ":")
}

// pullAuthStale — каталоги старше этого (хост перезапускался посреди
// выкладки) убираются при следующей.
const pullAuthStale = time.Hour

func (s *Server) pullAuthRoot() string { return filepath.Join(s.cfg.DataDir, "pull-auth") }

// writePullAuth — каталог с config.json для docker --config.
func (s *Server) writePullAuth(a composeRegistryAuth) (string, error) {
	if !a.valid() {
		return "", msgs.Errorf("compose.registryAuthBad")
	}
	root := s.pullAuthRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
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
		return "", err
	}
	dir := filepath.Join(root, hex.EncodeToString(b))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	key := a.Host
	if key == "docker.io" {
		key = "https://index.docker.io/v1/"
	}
	cfg, _ := json.Marshal(map[string]any{"auths": map[string]any{
		key: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(a.User + ":" + a.Token))},
	}})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// pullAuthDir — каталог из параметров задания, только из своего корня.
func (s *Server) pullAuthDir(dir string) string {
	if dir == "" || filepath.Dir(filepath.Clean(dir)) != filepath.Clean(s.pullAuthRoot()) {
		return ""
	}
	return dir
}
