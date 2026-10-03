package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// Причина, по которой fail2ban не поднял джейл, — его строки ERROR из
// журнала, без чужих.
func TestJailStartError(t *testing.T) {
	root := t.TempDir()
	log := "2026-10-03 21:00:00,1 fail2ban.jail [1]: INFO Creating new jail 'sshd'\n" +
		"2026-10-03 21:00:00,2 fail2ban [1]: ERROR Failed during configuration: Have not found any log file for sshd jail\n" +
		"2026-10-03 21:00:00,3 fail2ban [1]: ERROR Errors in jail 'openvpn'.\n" +
		"2026-10-03 21:00:00,4 fail2ban [1]: ERROR Async configuration of server failed\n"
	_ = os.MkdirAll(filepath.Join(root, "var", "log"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "var", "log", "fail2ban.log"), []byte(log), 0o644)
	got := jailStartError(collect.NewFixtures(root), "sshd")
	if !strings.Contains(got, "Have not found any log file for sshd jail") || strings.Contains(got, "openvpn") {
		t.Fatalf("reason: %q", got)
	}
	if jailStartError(collect.NewFixtures(root), "nginx-http-auth") != "" {
		t.Fatal("unrelated jail got a reason")
	}
}
