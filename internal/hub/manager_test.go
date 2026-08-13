package hub

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

func newTestManager(t *testing.T) (*Manager, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return NewManager(&config.Config{}, db, key, "test"), db
}

func TestRecordSudoOutcome(t *testing.T) {
	ctx := context.Background()

	t.Run("root never needs sudo", func(t *testing.T) {
		m, db := newTestManager(t)
		id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
		if err != nil {
			t.Fatalf("AddHost: %v", err)
		}
		m.recordSudoOutcome(ctx, id, "root", errors.New("irrelevant"))
		host, _ := db.HostByID(ctx, id)
		if host.SudoStatus != store.SudoStatusRoot {
			t.Errorf("SudoStatus = %q, want %q", host.SudoStatus, store.SudoStatusRoot)
		}
	})

	t.Run("non-root success confirms nopasswd", func(t *testing.T) {
		m, db := newTestManager(t)
		id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "deploy", store.HostAuthPassword, "pw", false)
		if err != nil {
			t.Fatalf("AddHost: %v", err)
		}
		m.recordSudoOutcome(ctx, id, "deploy", nil)
		host, _ := db.HostByID(ctx, id)
		if host.SudoStatus != store.SudoStatusNopasswd {
			t.Errorf("SudoStatus = %q, want %q", host.SudoStatus, store.SudoStatusNopasswd)
		}
	})

	t.Run("sudo error records password_required", func(t *testing.T) {
		m, db := newTestManager(t)
		id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "deploy", store.HostAuthPassword, "pw", false)
		if err != nil {
			t.Fatalf("AddHost: %v", err)
		}
		m.recordSudoOutcome(ctx, id, "deploy", errors.New("установка x: deploy нужен sudo без пароля (NOPASSWD): boom"))
		host, _ := db.HostByID(ctx, id)
		if host.SudoStatus != store.SudoStatusPasswordRequired {
			t.Errorf("SudoStatus = %q, want %q", host.SudoStatus, store.SudoStatusPasswordRequired)
		}
	})

	t.Run("unrelated error leaves status unset", func(t *testing.T) {
		m, db := newTestManager(t)
		id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "deploy", store.HostAuthPassword, "pw", false)
		if err != nil {
			t.Fatalf("AddHost: %v", err)
		}
		m.recordSudoOutcome(ctx, id, "deploy", errors.New("connection reset by peer"))
		host, _ := db.HostByID(ctx, id)
		if host.SudoStatus != store.SudoStatusUnknown {
			t.Errorf("SudoStatus = %q, want unset (%q)", host.SudoStatus, store.SudoStatusUnknown)
		}
	})
}

func TestRemoveSudoAccessGuardsAgainstMisuse(t *testing.T) {
	ctx := context.Background()

	t.Run("refuses for root", func(t *testing.T) {
		m, _ := newTestManager(t)
		id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
		if err != nil {
			t.Fatalf("AddHost: %v", err)
		}
		if err := m.RemoveSudoAccess(ctx, id); err == nil {
			t.Fatal("expected RemoveSudoAccess to refuse for a root-auth host")
		}
	})

	t.Run("refuses when nopasswd was never confirmed", func(t *testing.T) {
		m, _ := newTestManager(t)
		id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "deploy", store.HostAuthPassword, "pw", false)
		if err != nil {
			t.Fatalf("AddHost: %v", err)
		}
		if err := m.RemoveSudoAccess(ctx, id); err == nil {
			t.Fatal("expected RemoveSudoAccess to refuse when sudo_status isn't 'nopasswd'")
		}
	})
}

// TestResolveAdminCredentialIsStableAcrossReinstalls reproduces the bug a
// reinstall used to hit: bootstrapLogin failing with "неверный логин или
// пароль" because a retry generated a fresh password that never matched
// whatever the remote's own accounts table was actually bootstrapped with
// on an earlier, partially-successful attempt. resolveAdminCredential must
// persist a generated password immediately and reuse it on every later
// call for the same host, never handing back two different passwords.
func TestResolveAdminCredentialIsStableAcrossReinstalls(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "ssh-pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	host, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	user1, pw1, err := m.resolveAdminCredential(ctx, id, host)
	if err != nil {
		t.Fatalf("resolveAdminCredential (first call): %v", err)
	}
	if user1 == "" || pw1 == "" {
		t.Fatalf("resolveAdminCredential returned empty user/password: %q/%q", user1, pw1)
	}

	// Simulate a reinstall: re-read the host row (now carrying whatever
	// resolveAdminCredential just persisted) exactly as install() does on
	// every call, and resolve again.
	host, err = db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID (reload): %v", err)
	}
	user2, pw2, err := m.resolveAdminCredential(ctx, id, host)
	if err != nil {
		t.Fatalf("resolveAdminCredential (second call): %v", err)
	}
	if user2 != user1 || pw2 != pw1 {
		t.Errorf("resolveAdminCredential is not stable across calls: got (%q,%q) then (%q,%q)",
			user1, pw1, user2, pw2)
	}
}

// TestCancelInstallWithNoLiveJobResetsStatus covers the "hub restarted
// mid-install" case: nothing left in jobByHost to actually cancel, but the
// host must not stay stuck on 'installing' forever with no working control
// — this is the same situation ResetStuckInstalls handles at hub startup,
// exercised here through the on-demand "отменить" button's own code path.
func TestCancelInstallWithNoLiveJobResetsStatus(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if err := db.SetHostStatus(ctx, id, store.HostStatusInstalling, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}

	if err := m.CancelInstall(ctx, id); err != nil {
		t.Fatalf("CancelInstall: %v", err)
	}

	host, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if host.Status != store.HostStatusError {
		t.Errorf("status after CancelInstall = %q, want %q", host.Status, store.HostStatusError)
	}
}

// TestCancelInstallStopsLiveJob covers the normal case: a job the hub is
// still actively tracking. CancelInstall must invoke the job's own cancel
// func (interrupting any ctx-aware step still to come), mark it done, and
// leave the host in 'error' — not just silently forget about it.
func TestCancelInstallStopsLiveJob(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	jobCtx, jobCancel := context.WithCancel(context.Background())
	job := &installJob{created: time.Now(), hostID: id, cancel: jobCancel}
	m.jobsMu.Lock()
	m.jobByHost[id] = job
	m.jobsMu.Unlock()

	if err := m.CancelInstall(ctx, id); err != nil {
		t.Fatalf("CancelInstall: %v", err)
	}

	if jobCtx.Err() == nil {
		t.Error("CancelInstall did not cancel the job's context")
	}
	if !job.isDone() {
		t.Error("CancelInstall did not mark the job done")
	}
	host, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if host.Status != store.HostStatusError {
		t.Errorf("status after CancelInstall = %q, want %q", host.Status, store.HostStatusError)
	}
}

func TestAddHostRejectsBadKey(t *testing.T) {
	m, _ := newTestManager(t)
	_, err := m.AddHost(context.Background(), "h1", "10.0.0.1", 22, "root", store.HostAuthKey, "not a key", false)
	if err == nil {
		t.Fatal("expected AddHost to reject an unparsable private key")
	}
}

func TestUpdateHostRenameKeepsSecret(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "old-name", "10.0.0.1", 22, "root", store.HostAuthPassword, "s3cret", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	before, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	if err := m.UpdateHost(ctx, id, "new-name", "10.0.0.2", 2222, "admin", store.HostAuthPassword, "", false); err != nil {
		t.Fatalf("UpdateHost: %v", err)
	}

	after, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID after update: %v", err)
	}
	if after.Name != "new-name" || after.Addr != "10.0.0.2" || after.SSHPort != 2222 || after.SSHUser != "admin" {
		t.Fatalf("UpdateHost did not apply the new fields: %+v", after)
	}
	if string(after.SecretEnc) != string(before.SecretEnc) {
		t.Error("UpdateHost with an empty secret must not touch the stored credential")
	}
}

func TestUpdateHostReplacesSecret(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "old-password", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	before, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	if err := m.UpdateHost(ctx, id, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "new-password", false); err != nil {
		t.Fatalf("UpdateHost: %v", err)
	}

	after, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID after update: %v", err)
	}
	if string(after.SecretEnc) == string(before.SecretEnc) {
		t.Error("UpdateHost with a new secret must replace the stored credential")
	}
}

func TestAddHostGeneratedProducesAWorkingKeyPair(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, authorizedKey, err := m.AddHostGenerated(ctx, "h1", "10.0.0.1", 22, "root", false)
	if err != nil {
		t.Fatalf("AddHostGenerated: %v", err)
	}
	if authorizedKey == "" {
		t.Fatal("AddHostGenerated returned an empty authorized_keys line")
	}
	if !strings.HasPrefix(authorizedKey, "ssh-ed25519 ") {
		t.Errorf("authorized_keys line has an unexpected format: %q", authorizedKey)
	}

	host, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if host.SSHAuthKind != store.HostAuthKey {
		t.Errorf("stored auth kind = %q, want %q", host.SSHAuthKind, store.HostAuthKey)
	}

	// PublicKeyLine must derive exactly the same line from the stored
	// (encrypted) private key — it is the mechanism a caller uses to
	// re-fetch the key later, so it has to agree with what AddHostGenerated
	// already handed back once.
	got, err := m.PublicKeyLine(ctx, id)
	if err != nil {
		t.Fatalf("PublicKeyLine: %v", err)
	}
	if got != authorizedKey {
		t.Errorf("PublicKeyLine() = %q, want %q", got, authorizedKey)
	}
}

func TestPublicKeyLineRejectsPasswordHost(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if _, err := m.PublicKeyLine(ctx, id); err == nil {
		t.Fatal("expected PublicKeyLine to reject a password-auth host")
	}
}

func TestUpdateHostGeneratedRotatesKey(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()

	id, firstKey, err := m.AddHostGenerated(ctx, "h1", "10.0.0.1", 22, "root", false)
	if err != nil {
		t.Fatalf("AddHostGenerated: %v", err)
	}
	before, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}

	secondKey, err := m.UpdateHostGenerated(ctx, id, "h1", "10.0.0.1", 22, "root", false)
	if err != nil {
		t.Fatalf("UpdateHostGenerated: %v", err)
	}
	if secondKey == firstKey {
		t.Error("UpdateHostGenerated must produce a fresh key, not repeat the old one")
	}

	after, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatalf("HostByID after update: %v", err)
	}
	if string(after.SecretEnc) == string(before.SecretEnc) {
		t.Error("UpdateHostGenerated must replace the stored credential")
	}
}

func TestUpdateHostRejectsBadKey(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	err = m.UpdateHost(ctx, id, "h1", "10.0.0.1", 22, "root", store.HostAuthKey, "not a key", false)
	if err == nil {
		t.Fatal("expected UpdateHost to reject an unparsable private key")
	}
}

// TestSetServiceRunningRejectsUninstalledHost covers the guard that needs
// no SSH connection at all: a freshly added host is still
// store.HostStatusNew (nothing has ever been installed on it), so
// start/stop has nothing real to act on and must say so plainly rather
// than dialing SSH only to fail on a command that could never have
// succeeded.
func TestSetServiceRunningRejectsUninstalledHost(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	if err := m.SetServiceRunning(ctx, id, true); err == nil {
		t.Fatal("expected SetServiceRunning to refuse a host that was never installed")
	} else if !strings.Contains(err.Error(), "не установлен") {
		t.Errorf("error does not explain the host is not installed: %v", err)
	}
}
