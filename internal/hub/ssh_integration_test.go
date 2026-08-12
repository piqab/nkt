package hub

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// TestInstallJobCancelNowClosesSSHClient proves cancelNow actually renders
// a live SSH connection unusable, not just cancels a context nothing inside
// golang.org/x/crypto/ssh listens to — the reason CancelInstall closes the
// job's client explicitly instead of relying on ctx cancellation alone (see
// installJob's doc comment).
func TestInstallJobCancelNowClosesSSHClient(t *testing.T) {
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

	jobCtx, jobCancel := context.WithCancel(context.Background())
	job := &installJob{created: time.Now(), cancel: jobCancel}
	job.setClient(client)

	job.cancelNow()

	if jobCtx.Err() == nil {
		t.Error("cancelNow did not cancel the job's context")
	}
	if _, err := client.NewSession(); err == nil {
		t.Error("cancelNow did not close the SSH client — NewSession still succeeds on it")
	}
}

// TestSSHProvisioningRoundTrip exercises the actually-new code in this
// package — dialSSH, detectTarget and the SFTP-then-install half of
// stageFiles — against a throwaway local sshd, instead of only unit-testing
// the pure helpers around them. It stages files under a scratch directory
// rather than /usr/local/bin and never calls activateService, so it needs
// neither root nor systemd; passing "root" as the sshUser skips
// installRemoteFile's sudo prefix (the connecting test user already owns
// the scratch directory, no escalation needed to write there) — sudo's own
// behavior is covered separately by TestInstallRemoteFileNeedsSudoForNonRoot.
// The plan's own manual step (installing onto a real test VM) is still the
// way to verify the systemctl half end to end.
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

	if err := stageFiles(client, "root", localBin, "unit-content", "env-content", binPath, servicePath, envPath, report); err != nil {
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

// TestGeneratedKeyWorksAgainstRealSSHD proves generateHostKeyPair's output
// is actually usable, not just internally self-consistent: the authorized
// key line it produces is trusted by a real sshd, and the private PEM it
// produces successfully authenticates against it via dialSSH — the exact
// two halves AddHostGenerated/UpdateHostGenerated split between the caller
// (public) and the hub's own encrypted storage (private).
func TestGeneratedKeyWorksAgainstRealSSHD(t *testing.T) {
	sshdPath := findBinary(t, []string{"/usr/sbin/sshd", "/usr/bin/sshd"})
	sftpServer := findBinary(t, []string{
		"/usr/lib/openssh/sftp-server",
		"/usr/libexec/openssh/sftp-server",
		"/usr/lib/ssh/sftp-server",
	})

	privatePEM, authorizedKey, err := generateHostKeyPair()
	if err != nil {
		t.Fatalf("generateHostKeyPair: %v", err)
	}

	addr, port := launchTestSSHD(t, sshdPath, sftpServer, t.TempDir(), authorizedKey+"\n")

	me, err := osuser.Current()
	if err != nil {
		t.Fatalf("os/user.Current: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := dialSSH(ctx, addr, port, me.Username, store.HostAuthKey, []byte(privatePEM))
	if err != nil {
		t.Fatalf("dialSSH with the generated key: %v", err)
	}
	defer client.Close()

	out, err := runRemote(client, "echo ok")
	if err != nil {
		t.Fatalf("runRemote: %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Errorf("runRemote output = %q, want %q", out, "ok")
	}
}

// TestInstallRemoteFileNeedsSudoForNonRoot exercises the actual `sudo -n`
// escalation path against a real sshd: passing any sshUser other than
// "root" makes installRemoteFile prefix the remote command with `sudo -n`,
// which — on a machine with no NOPASSWD rule for the connecting account —
// fails fast (no hang, no password prompt to nowhere) with output
// diagnoseInstallError must recognise and explain, not just relay as a bare
// "permission denied". Skipped if the environment happens to grant
// passwordless sudo, since then there is nothing to observe failing.
func TestInstallRemoteFileNeedsSudoForNonRoot(t *testing.T) {
	if exec.Command("sudo", "-n", "true").Run() == nil {
		t.Skip("this environment has passwordless sudo for the test user — nothing to observe failing")
	}

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

	src := filepath.Join(t.TempDir(), "nkt")
	if err := os.WriteFile(src, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	err = installRemoteFile(client, "not-root", src, "/usr/local/bin/nkt-nkt-test-should-never-exist", 0o755)
	if err == nil {
		t.Fatal("expected installRemoteFile to fail without passwordless sudo")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error does not explain the sudo/NOPASSWD problem: %v", err)
	}
}

// TestRemoveSudoAccessFailsWithoutNopasswd exercises RemoveSudoAccess's real
// SSH/sudo path — dialSSH, the `sudo -n rm -f` exec, diagnoseInstallError —
// against the test sshd. It cannot verify a successful removal (that would
// need a real, pre-existing sudoers rule, which requires root to set up in
// the first place — not available in a sandboxed test environment), only
// that a confirmed-but-since-broken NOPASSWD grant fails the same
// diagnosable way installRemoteFile's own sudo path does, rather than
// hanging or panicking.
func TestRemoveSudoAccessFailsWithoutNopasswd(t *testing.T) {
	if exec.Command("sudo", "-n", "true").Run() == nil {
		t.Skip("this environment has passwordless sudo for the test user — nothing to observe failing")
	}

	addr, port, clientKeyPEM := startTestSSHD(t)
	me, err := osuser.Current()
	if err != nil {
		t.Fatalf("os/user.Current: %v", err)
	}

	m, db := newTestManager(t)
	ctx := context.Background()

	secretEnc, err := secretbox.Encrypt(m.key, clientKeyPEM)
	if err != nil {
		t.Fatalf("encrypt ssh key: %v", err)
	}
	id, err := db.CreateHost(ctx, "h1", addr, port, me.Username, store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	// Simulate a host the hub previously confirmed had NOPASSWD sudo — the
	// only state RemoveSudoAccess is willing to act on at all.
	if err := db.SetHostSudoStatus(ctx, id, store.SudoStatusNopasswd); err != nil {
		t.Fatalf("SetHostSudoStatus: %v", err)
	}

	err = m.RemoveSudoAccess(ctx, id)
	if err == nil {
		t.Fatal("expected RemoveSudoAccess to fail without passwordless sudo")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error does not explain the sudo/NOPASSWD problem: %v", err)
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
	clientKey := filepath.Join(dir, "client_key")
	runOK(t, "ssh-keygen", "-t", "ed25519", "-f", clientKey, "-N", "", "-q")
	pub, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatalf("read generated client pubkey: %v", err)
	}

	addr, port = launchTestSSHD(t, sshdPath, sftpServer, dir, string(pub))

	keyPEM, err := os.ReadFile(clientKey)
	if err != nil {
		t.Fatalf("read generated client private key: %v", err)
	}
	return addr, port, keyPEM
}

// launchTestSSHD is the shared plumbing behind startTestSSHD and
// TestGeneratedKeyWorksAgainstRealSSHD: writes authorizedKeysContent to an
// authorized_keys file, starts sshd trusting only that, and waits for it to
// accept connections.
func launchTestSSHD(t *testing.T, sshdPath, sftpServer, dir, authorizedKeysContent string) (addr string, port int) {
	t.Helper()

	hostKey := filepath.Join(dir, "host_key")
	runOK(t, "ssh-keygen", "-t", "ed25519", "-f", hostKey, "-N", "", "-q")
	if err := os.Chmod(hostKey, 0o600); err != nil {
		t.Fatalf("chmod host key: %v", err)
	}

	authorizedKeys := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(authorizedKeys, []byte(authorizedKeysContent), 0o600); err != nil {
		t.Fatalf("write authorized_keys: %v", err)
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

	return "127.0.0.1", port
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
