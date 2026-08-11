package hub

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/store"
)

// TestSSHProvisioningRoundTrip exercises the actually-new code in this
// package — dialSSH, detectTarget and the SFTP upload half of stageFiles —
// against a throwaway local sshd, instead of only unit-testing the pure
// helpers around them. It stages files under a scratch directory rather
// than /usr/local/bin and never calls activateService, so it needs neither
// root nor systemd; the plan's own manual step (installing onto a real test
// VM) is still the way to verify the systemctl half end to end.
func TestSSHProvisioningRoundTrip(t *testing.T) {
	addr, port, clientKeyPEM := startTestSSHD(t)

	me, err := osuser.Current()
	if err != nil {
		t.Fatalf("os/user.Current: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := dialSSH(ctx, addr, port, me.Username, store.HostAuthKey, clientKeyPEM)
	if err != nil {
		t.Fatalf("dialSSH: %v", err)
	}
	defer client.Close()

	goos, goarch, err := detectTarget(client)
	if err != nil {
		t.Fatalf("detectTarget: %v", err)
	}
	if goos != "linux" {
		t.Errorf("detectTarget goos = %q, want linux", goos)
	}
	if goarch == "" {
		t.Error("detectTarget returned empty goarch")
	}

	localBin := filepath.Join(t.TempDir(), "fake-nkt")
	const fakeBinaryContent = "#!/bin/sh\necho fake nkt\n"
	if err := os.WriteFile(localBin, []byte(fakeBinaryContent), 0o644); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	scratch := t.TempDir()
	binPath := filepath.Join(scratch, "bin", "nkt")
	servicePath := filepath.Join(scratch, "systemd", "netknownsthat.service")
	envPath := filepath.Join(scratch, "env", "nkt.env")

	var events []string
	report := func(s string) { events = append(events, s) }

	if err := stageFiles(client, localBin, "unit-content", "env-content", binPath, servicePath, envPath, report); err != nil {
		t.Fatalf("stageFiles: %v", err)
	}
	if len(events) == 0 {
		t.Error("stageFiles reported no progress")
	}

	gotBin, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read staged binary: %v", err)
	}
	if string(gotBin) != fakeBinaryContent {
		t.Errorf("staged binary content = %q, want %q", gotBin, fakeBinaryContent)
	}
	if info, err := os.Stat(binPath); err != nil {
		t.Fatalf("stat staged binary: %v", err)
	} else if info.Mode().Perm() != 0o755 {
		t.Errorf("staged binary mode = %v, want 0755", info.Mode().Perm())
	}

	gotUnit, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatalf("read staged unit: %v", err)
	}
	if string(gotUnit) != "unit-content" {
		t.Errorf("staged unit content = %q, want %q", gotUnit, "unit-content")
	}

	gotEnv, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read staged env: %v", err)
	}
	if string(gotEnv) != "env-content" {
		t.Errorf("staged env content = %q, want %q", gotEnv, "env-content")
	}
	if info, err := os.Stat(envPath); err != nil {
		t.Fatalf("stat staged env: %v", err)
	} else if info.Mode().Perm() != 0o640 {
		t.Errorf("staged env mode = %v, want 0640", info.Mode().Perm())
	}
}

// startTestSSHD launches a throwaway sshd on 127.0.0.1 for integration
// tests, authenticating the current OS user via a freshly generated ed25519
// keypair. It skips the test (rather than failing) when OpenSSH isn't
// installed, so environments without it still run every other test here.
func startTestSSHD(t *testing.T) (addr string, port int, clientKeyPEM []byte) {
	t.Helper()

	sshdPath := findBinary(t, []string{"/usr/sbin/sshd", "/usr/bin/sshd"})
	sftpServer := findBinary(t, []string{
		"/usr/lib/openssh/sftp-server",
		"/usr/libexec/openssh/sftp-server",
		"/usr/lib/ssh/sftp-server",
	})

	dir := t.TempDir()
	hostKey := filepath.Join(dir, "host_key")
	clientKey := filepath.Join(dir, "client_key")
	runOK(t, "ssh-keygen", "-t", "ed25519", "-f", hostKey, "-N", "", "-q")
	runOK(t, "ssh-keygen", "-t", "ed25519", "-f", clientKey, "-N", "", "-q")

	pub, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatalf("read generated client pubkey: %v", err)
	}
	authorizedKeys := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(authorizedKeys, pub, 0o600); err != nil {
		t.Fatalf("write authorized_keys: %v", err)
	}
	if err := os.Chmod(hostKey, 0o600); err != nil {
		t.Fatalf("chmod host key: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfgPath := filepath.Join(dir, "sshd_config")
	cfg := fmt.Sprintf(`Port %d
ListenAddress 127.0.0.1
HostKey %s
AuthorizedKeysFile %s
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
UsePAM no
StrictModes no
Subsystem sftp %s
`, port, hostKey, authorizedKeys, sftpServer)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}

	cmd := exec.Command(sshdPath, "-f", cfgPath, "-D", "-e")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sshd: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sshd never started listening on 127.0.0.1:%d: %v", port, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	keyPEM, err := os.ReadFile(clientKey)
	if err != nil {
		t.Fatalf("read generated client private key: %v", err)
	}
	return "127.0.0.1", port, keyPEM
}

func findBinary(t *testing.T, candidates []string) string {
	t.Helper()
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(filepath.Base(candidates[0])); err == nil {
		return p
	}
	t.Skipf("none of %v found, skipping SSH integration test", candidates)
	return ""
}

func runOK(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
