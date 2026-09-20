package control

import (
	"context"
	"testing"
)

func TestReadRejectsSensitiveFiles(t *testing.T) {
	m := configsSetup(t)
	
	sensitiveFiles := []string{
		"/etc/ssh/ssh_host_rsa_key",
		"/etc/ssh/ssh_host_ed25519_key",
		"/etc/ssh/id_rsa",
		"/etc/ssh/id_ed25519",
		"/etc/ssl/private/server.key",
		"/etc/ssl/private/cert.pem",
	}
	
	for _, path := range sensitiveFiles {
		t.Run(path, func(t *testing.T) {
			_, err := m.Read(path)
			if err != ErrSensitiveFile {
				t.Errorf("Read(%q) = %v, want ErrSensitiveFile", path, err)
			}
		})
	}
}

func TestListBlocksRejectsSensitiveFiles(t *testing.T) {
	m := configsSetup(t)
	
	_, err := m.ListBlocks("/etc/ssh/ssh_host_rsa_key")
	if err != ErrSensitiveFile {
		t.Errorf("ListBlocks(ssh_host_rsa_key) = %v, want ErrSensitiveFile", err)
	}
}

func TestVersionsRejectsSensitiveFiles(t *testing.T) {
	m := configsSetup(t)
	ctx := context.Background()
	
	_, err := m.Versions(ctx, "/etc/ssh/ssh_host_rsa_key", 100)
	if err != ErrSensitiveFile {
		t.Errorf("Versions(ssh_host_rsa_key) = %v, want ErrSensitiveFile", err)
	}
}

func TestIsSensitiveFile(t *testing.T) {
	tests := []struct {
		path      string
		sensitive bool
	}{
		{"/etc/ssh/ssh_host_rsa_key", true},
		{"/etc/ssh/ssh_host_ed25519_key", true},
		{"/etc/ssh/ssh_host_ecdsa_key", true},
		{"/etc/ssh/id_rsa", true},
		{"/etc/ssh/id_dsa", true},
		{"/etc/ssh/id_ecdsa", true},
		{"/etc/ssh/id_ed25519", true},
		{"/etc/ssl/private/server.key", true},
		{"/etc/ssl/private/cert.pem", true},
		{"/etc/nginx/nginx.conf", false},
		{"/etc/ssh/sshd_config", false},
		{"/etc/haproxy/haproxy.cfg", false},
		{"/home/alice/docker-compose.yml", false},
	}
	
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isSensitiveFile(tt.path)
			if got != tt.sensitive {
				t.Errorf("isSensitiveFile(%q) = %v, want %v", tt.path, got, tt.sensitive)
			}
		})
	}
}
