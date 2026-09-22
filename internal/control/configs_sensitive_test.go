package control

import (
	"context"
	"errors"
	"testing"
)

// Файлы с ключами не отдаются через API конфигураций, даже когда путь
// лежит под разрешённым корнем и набран руками (исключение из списка
// одного мало): чтение, блоки, история версий — отказ до Stat. В тестах
// корень sshd не задан, поэтому пути — под корнем nginx.
func TestSensitiveFilesRejectedInsideRoot(t *testing.T) {
	m := configsSetup(t)
	ctx := context.Background()
	for _, path := range []string{
		"/etc/nginx/ssl/server.key",
		"/etc/nginx/ssl/fullchain.pem",
		"/etc/nginx/id_ed25519",
		"/etc/nginx/ssh_host_rsa_key",
	} {
		if _, err := m.Read(path); !errors.Is(err, ErrSensitiveFile) {
			t.Errorf("Read(%q) = %v, ждали ErrSensitiveFile", path, err)
		}
		if _, err := m.ListBlocks(path); !errors.Is(err, ErrSensitiveFile) {
			t.Errorf("ListBlocks(%q) = %v, ждали ErrSensitiveFile", path, err)
		}
		if _, err := m.Versions(ctx, path, 10); !errors.Is(err, ErrSensitiveFile) {
			t.Errorf("Versions(%q) = %v, ждали ErrSensitiveFile", path, err)
		}
	}
	// Обычный конфиг под тем же корнем читается по-прежнему.
	if _, err := m.Read(nginxSiteFile); err != nil {
		t.Errorf("Read(%q): %v", nginxSiteFile, err)
	}
}

func TestIsSensitiveFile(t *testing.T) {
	for path, want := range map[string]bool{
		"/etc/ssh/ssh_host_rsa_key":      true,
		"/etc/ssh/ssh_host_ed25519_key":  true,
		"/root/.ssh/id_ecdsa":            true,
		"/etc/ssl/private/server.key":    true,
		"/etc/ssl/certs/cert.pem":        true,
		"/etc/nginx/nginx.conf":          false,
		"/etc/ssh/sshd_config":           false,
		"/etc/ssh/ssh_host_rsa_key.pub":  false,
		"/home/alice/docker-compose.yml": false,
	} {
		if got := isSensitiveFile(path); got != want {
			t.Errorf("isSensitiveFile(%q) = %v, ждали %v", path, got, want)
		}
	}
}
