package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/ssh"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/msgs"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// Event is one line of progress from a StartInstall job — the same shape
// control.RenewEvent already uses for certificate-renewal progress, so the
// frontend can reuse the same polling/log-panel component for both.
//
// Key/Args (unexported from JSON) carry a translatable line — looked up
// against internal/msgs and resolved into Text only at read time (see
// resolveText), against whichever language the poll asking for it is in,
// not whatever was selected when the job produced the line. Text alone
// (Key empty) is a literal, never-translated line — nothing this job type
// currently produces needs that, but installJob.appendRaw exists for
// symmetry with control.renewJob, which does (raw certbot output).
type Event struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
	Key  string    `json:"-"`
	Args []any     `json:"-"`
}

// resolveText renders lang's localized text for the event — its own Text
// verbatim for a raw/literal line (Key empty), or a fresh msgs.T lookup
// against Key/Args otherwise.
func (e Event) resolveText(lang msgs.Lang) string {
	if e.Key == "" {
		return e.Text
	}
	return msgs.T(lang, e.Key, e.Args...)
}

// localizeEvents returns a copy of events with Text resolved against lang —
// events themselves (msgs.LangFromRequest-independent, from installJob's
// own internal state) always store Key/Args; only the HTTP handler serving
// a poll knows the request's language, so localization happens here, right
// before that response is built, never when a line is first appended.
func localizeEvents(lang msgs.Lang, events []Event) []Event {
	out := make([]Event, len(events))
	for i, e := range events {
		e.Text = e.resolveText(lang)
		out[i] = e
	}
	return out
}

// installJob tracks one StartInstall run in memory, mirroring
// control.CertManager's renewJob.
type installJob struct {
	id      string
	created time.Time
	hostID  int64
	// cancel stops every ctx-aware step still to come (exec.CommandContext
	// cross-compiles, HTTP calls through the tunnel). It cannot by itself
	// interrupt a step already blocked inside an SSH session — the
	// golang.org/x/crypto/ssh API has no context support — which is what
	// client is for: closing the live connection forces any in-flight SFTP
	// upload or remote command to error out immediately instead of running
	// to completion regardless of cancellation.
	cancel context.CancelFunc

	mu     sync.Mutex
	events []Event
	done   bool
	errMsg string
	client *ssh.Client
}

// append logs a translatable line — key looked up in internal/msgs,
// resolved against the reader's language at snapshot time (see
// localizeEvents), not now.
func (j *installJob) append(key string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, Event{Time: time.Now(), Key: key, Args: args})
}

// appendRaw logs a literal, never-translated line — kept for symmetry with
// control.renewJob's identical method, which needs it for raw certbot
// output; nothing installJob currently reports is like that, but the two
// job types are meant to stay interchangeable (see Event's own comment).
func (j *installJob) appendRaw(text string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, Event{Time: time.Now(), Text: text})
}

// replaceLast overwrites the most recent event instead of adding a new one
// — for a step that reports progress repeatedly (an upload percentage
// ticking up) and should read as one line changing in place, not a new log
// line every tick stretching the job modal taller with each update. Falls
// back to appending when there is nothing yet to replace.
func (j *installJob) replaceLast(key string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.events) == 0 {
		j.events = append(j.events, Event{Time: time.Now(), Key: key, Args: args})
		return
	}
	j.events[len(j.events)-1] = Event{Time: time.Now(), Key: key, Args: args}
}

func (j *installJob) finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.done = true
	if err != nil {
		j.errMsg = err.Error()
	}
}

func (j *installJob) snapshot() (events []Event, done bool, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Event{}, j.events...), j.done, j.errMsg
}

// setClient records the SSH connection the job is currently using, so a
// later cancelNow can force it closed.
func (j *installJob) setClient(c *ssh.Client) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.client = c
}

func (j *installJob) isDone() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done
}

// isCurrentJob reports whether job is still the one StartInstall most
// recently registered for hostID — false once a later StartInstall call has
// superseded it (see StartInstall's cancelNow of a still-running previous
// job). install/installOverTunnel guard every host-status write with this:
// a superseded job's goroutine keeps running for a little while after being
// cancelled (SSH/HTTP calls returning, cleanup), and without this check its
// eventual terminal SetHostStatus call could land after the newer job's own
// writes and silently overwrite them — the exact stuck-on-"installing" bug
// this whole guard exists to prevent.
func (m *Manager) isCurrentJob(hostID int64, job *installJob) bool {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	return m.jobByHost[hostID] == job
}

// cancelNow stops the job as promptly as the underlying APIs allow: the
// context cancellation covers everything ctx-aware, and force-closing the
// SSH connection covers whatever it might currently be blocked inside that
// isn't.
func (j *installJob) cancelNow() {
	j.mu.Lock()
	cancel, client := j.cancel, j.client
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		_ = client.Close()
	}
}

// Manager registers remote hosts, installs nkt on them over SSH, and proxies
// their own web API through the connections it keeps open (see proxy.go).
type Manager struct {
	cfg     *config.Config
	db      *store.DB
	key     []byte
	version string
	log     *slog.Logger

	jobsMu    sync.Mutex
	jobs      map[string]*installJob
	jobByHost map[int64]*installJob

	connsMu sync.Mutex
	conns   map[int64]*hostConn

	sessionMu sync.Mutex
	sessions  map[int64]sessionCache

	// overviewMu/overview cache what pollOverviews last learned about each
	// online host's findings/reachability — see overview_poll.go.
	overviewMu sync.Mutex
	overview   map[int64]hostOverview

	// relayMu/relaySessions hold each host's live reverse-tunnel session
	// (see relay.go) — populated by tunneldial.go's runTunnelDialer, which
	// keeps one such connection alive per TunnelEnabled host, consumed by
	// dialerFor only after an SSH dial has already failed.
	relayMu       sync.Mutex
	relaySessions map[int64]*yamux.Session

	// goBinMu/resolvedGoBin cache resolveGoBin's result for the Manager's
	// lifetime — the self-install it may trigger is expensive enough
	// (network fetch) that it must run at most once per process.
	goBinMu       sync.Mutex
	resolvedGoBin string
}

// Version returns the hub's own build version — every binary it
// cross-compiles for a managed host is stamped with the same one (see
// ensureBinary), so comparing it against a host's stored nkt_version is
// how the UI knows an "переустановить" would actually deploy something
// newer rather than just repeat the same build.
func (m *Manager) Version() string { return m.version }

// NewManager builds a host manager. key is the resolved secretbox master
// key (see secretbox.ResolveKey); version is stamped into every binary this
// hub cross-compiles, so an installed host reports the hub's own build.
func NewManager(cfg *config.Config, db *store.DB, key []byte, version string, log *slog.Logger) *Manager {
	return &Manager{
		cfg: cfg, db: db, key: key, version: version, log: log,
		jobs:          map[string]*installJob{},
		jobByHost:     map[int64]*installJob{},
		conns:         map[int64]*hostConn{},
		sessions:      map[int64]sessionCache{},
		overview:      map[int64]hostOverview{},
		relaySessions: map[int64]*yamux.Session{},
	}
}

// Run starts the manager's background maintenance — idle SSH connection
// eviction, the periodic host findings/reachability poll, and the
// reverse-tunnel dialers — and blocks until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	go m.pollOverviews(ctx)
	go m.maintainTunnelDialers(ctx)
	m.evictIdleConns(ctx)
}

// AddHost registers a host and encrypts its SSH secret at rest. It does not
// connect to the host — that happens in StartInstall.
func (m *Manager) AddHost(ctx context.Context, name, addr string, sshPort int, sshUser, authKind, secret string, terminalEnabled bool) (int64, error) {
	if name == "" || addr == "" || sshUser == "" || secret == "" {
		return 0, fmt.Errorf("укажите имя, адрес, пользователя SSH и секрет (пароль или приватный ключ)")
	}
	if authKind != store.HostAuthPassword && authKind != store.HostAuthKey {
		return 0, fmt.Errorf("способ входа должен быть %q или %q, получено %q",
			store.HostAuthPassword, store.HostAuthKey, authKind)
	}
	if sshPort == 0 {
		sshPort = 22
	}
	if authKind == store.HostAuthKey {
		if err := validatePrivateKey(secret); err != nil {
			return 0, err
		}
	}

	secretEnc, err := secretbox.Encrypt(m.key, []byte(secret))
	if err != nil {
		return 0, fmt.Errorf("шифрование секрета: %w", err)
	}
	id, err := m.db.CreateHost(ctx, name, addr, sshPort, sshUser, authKind, secretEnc)
	if err != nil {
		return 0, err
	}
	if err := m.db.SetHostTerminalEnabled(ctx, id, terminalEnabled); err != nil {
		return 0, err
	}
	return id, nil
}

// AddHostGenerated registers a host and has the hub generate its own SSH
// keypair for it, instead of accepting one from the operator — the
// recommended way to add a host: the private half is generated here and
// never leaves the hub; authorizedKeyLine is the public half the caller
// must then place in the host's own ~/.ssh/authorized_keys before
// StartInstall can connect.
func (m *Manager) AddHostGenerated(ctx context.Context, name, addr string, sshPort int, sshUser string, terminalEnabled bool) (hostID int64, authorizedKeyLine string, err error) {
	if name == "" || addr == "" || sshUser == "" {
		return 0, "", fmt.Errorf("укажите имя, адрес и пользователя SSH")
	}
	if sshPort == 0 {
		sshPort = 22
	}

	privatePEM, authorizedKeyLine, err := generateHostKeyPair()
	if err != nil {
		return 0, "", err
	}
	secretEnc, err := secretbox.Encrypt(m.key, []byte(privatePEM))
	if err != nil {
		return 0, "", fmt.Errorf("шифрование сгенерированного ключа: %w", err)
	}
	id, err := m.db.CreateHost(ctx, name, addr, sshPort, sshUser, store.HostAuthKey, secretEnc)
	if err != nil {
		return 0, "", err
	}
	if err := m.db.SetHostTerminalEnabled(ctx, id, terminalEnabled); err != nil {
		return 0, "", err
	}
	return id, authorizedKeyLine, nil
}

// PublicKeyLine returns the authorized_keys line for a key-auth host's
// stored private key, so it can be re-copied onto the host at any time
// (e.g. after rebuilding it) without ever exposing the private half —
// works the same whether the hub generated the key or the operator
// supplied their own.
func (m *Manager) PublicKeyLine(ctx context.Context, hostID int64) (string, error) {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return "", err
	}
	if host.SSHAuthKind != store.HostAuthKey {
		return "", fmt.Errorf("у хоста способ входа %q, приватного ключа нет", host.SSHAuthKind)
	}
	privatePEM, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return "", fmt.Errorf("расшифровка ключа: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(privatePEM)
	if err != nil {
		return "", diagnoseKeyError(string(privatePEM), err)
	}
	return formatAuthorizedKey(signer.PublicKey()), nil
}

// UpdateHost changes a host's connection details. secret is optional — an
// empty string keeps whatever SSH credential is already stored, so renaming
// a host or fixing a typo'd address does not force re-entering it.
func (m *Manager) UpdateHost(ctx context.Context, hostID int64, name, addr string, sshPort int, sshUser, authKind, secret string, terminalEnabled bool) error {
	if name == "" || addr == "" || sshUser == "" {
		return fmt.Errorf("укажите имя, адрес и пользователя SSH")
	}
	if authKind != store.HostAuthPassword && authKind != store.HostAuthKey {
		return fmt.Errorf("способ входа должен быть %q или %q, получено %q",
			store.HostAuthPassword, store.HostAuthKey, authKind)
	}
	if sshPort == 0 {
		sshPort = 22
	}

	if err := m.db.UpdateHost(ctx, hostID, name, addr, sshPort, sshUser, authKind); err != nil {
		return err
	}
	if err := m.db.SetHostTerminalEnabled(ctx, hostID, terminalEnabled); err != nil {
		return err
	}

	if secret != "" {
		if authKind == store.HostAuthKey {
			if err := validatePrivateKey(secret); err != nil {
				return err
			}
		}
		secretEnc, err := secretbox.Encrypt(m.key, []byte(secret))
		if err != nil {
			return fmt.Errorf("шифрование секрета: %w", err)
		}
		if err := m.db.SetHostSecret(ctx, hostID, authKind, secretEnc); err != nil {
			return err
		}
	}

	// Connection details (possibly the credential itself) changed — drop the
	// pooled SSH connection/session so the next request reconnects with the
	// new ones instead of replaying stale ones. Not CloseHost: the
	// reverse-tunnel session (if any) is unaffected by any of this and must
	// survive a save here — see dropSSHPool's doc comment.
	m.dropSSHPool(hostID)
	return nil
}

// UpdateHostGenerated updates a host's connection details and replaces its
// stored credential with a freshly hub-generated keypair, the same way
// AddHostGenerated does for a new host — useful to switch an existing
// password-authenticated host over to a key, or to rotate a compromised one.
func (m *Manager) UpdateHostGenerated(ctx context.Context, hostID int64, name, addr string, sshPort int, sshUser string, terminalEnabled bool) (authorizedKeyLine string, err error) {
	if name == "" || addr == "" || sshUser == "" {
		return "", fmt.Errorf("укажите имя, адрес и пользователя SSH")
	}
	if sshPort == 0 {
		sshPort = 22
	}

	if err := m.db.UpdateHost(ctx, hostID, name, addr, sshPort, sshUser, store.HostAuthKey); err != nil {
		return "", err
	}
	if err := m.db.SetHostTerminalEnabled(ctx, hostID, terminalEnabled); err != nil {
		return "", err
	}

	privatePEM, authorizedKeyLine, err := generateHostKeyPair()
	if err != nil {
		return "", err
	}
	secretEnc, err := secretbox.Encrypt(m.key, []byte(privatePEM))
	if err != nil {
		return "", fmt.Errorf("шифрование сгенерированного ключа: %w", err)
	}
	if err := m.db.SetHostSecret(ctx, hostID, store.HostAuthKey, secretEnc); err != nil {
		return "", err
	}

	// Not CloseHost — see dropSSHPool's doc comment.
	m.dropSSHPool(hostID)
	return authorizedKeyLine, nil
}

// SetTunnelEnabled turns the reverse-tunnel fallback channel on or off for
// one host — a separate call from AddHost/UpdateHost (unlike
// TerminalEnabled, which those thread through directly) so this feature's
// wiring stays out of every existing caller/test of those two, added
// before this feature existed. Taking effect needs an install/update
// regardless — this alone doesn't generate a token or touch the host's env
// file, see install()'s own tunnel-token step.
func (m *Manager) SetTunnelEnabled(ctx context.Context, hostID int64, enabled bool) error {
	return m.db.SetHostTunnelEnabled(ctx, hostID, enabled)
}

// StartInstall installs nkt on host id in the background and returns a job
// id immediately, so a caller (the "добавить хост" wizard) can poll
// InstallJobStatus instead of blocking on the whole multi-step operation —
// same pattern as control.CertManager.StartRenewCertbot.
// ForeignInstallError is returned by StartInstall when the target already
// runs an nkt this hub has no record of installing itself, and force was
// not set — the caller (the HTTP handler) turns this into a 409 the
// frontend recognizes and re-prompts the operator with, rather than
// silently overwriting a binary/unit/admin-credential that isn't the
// hub's own to begin with.
type ForeignInstallError struct {
	// Detail is a human-readable description of what was found (version
	// string and/or systemd active state), for the confirmation prompt.
	Detail string
}

func (e *ForeignInstallError) Error() string {
	return "на хосте уже есть nkt, установленный не через этот хаб: " + e.Detail
}

// checkForeignInstall connects briefly and reports whether the target
// already has an nkt on it that this hub never itself installed — judged
// by host.NktVersion being empty, which SetHostVersion only ever sets
// after install() completes successfully (so "empty" reliably means
// either a genuinely fresh host, or a previous attempt by this hub that
// never got that far). A positive here is not proof of anything malicious
// — it's just as likely a manual install, or a host being migrated from
// another hub — but proceeding without asking would silently overwrite
// that binary/unit and reset whatever admin password it already has (see
// install's bootstrap-login fallback), so it always needs an explicit
// override once surfaced.
//
// A probe that fails to even connect returns (false, nil) rather than an
// error — the real install attempt right after this will hit the exact
// same connection problem and report it properly; duplicating that here
// would just be a worse-quality copy of the same error message.
func (m *Manager) checkForeignInstall(ctx context.Context, host store.Host) (*ForeignInstallError, error) {
	if host.NktVersion != "" {
		return nil, nil
	}
	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return nil, nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, sshDialTimeout)
	defer cancel()
	client, err := dialSSH(dialCtx, host.Addr, host.SSHPort, host.SSHUser, host.SSHAuthKind, secret)
	if err != nil {
		return nil, nil
	}
	defer client.Close()

	version, active, err := probeExistingInstall(client, remoteBinPath, "netknownsthat")
	if err != nil || (version == "" && active != "active") {
		return nil, nil
	}
	detail := "статус сервиса: " + orUnknown(active)
	if version != "" {
		detail = version + ", " + detail
	}
	return &ForeignInstallError{Detail: detail}, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "неизвестен"
	}
	return s
}

// StartInstall launches install as a background job and returns its id
// immediately. force must be true to proceed when checkForeignInstall
// finds an nkt on the target this hub didn't put there itself — set by the
// operator explicitly confirming the overwrite after seeing
// ForeignInstallError's detail.
func (m *Manager) StartInstall(ctx context.Context, hostID int64, force bool) (string, error) {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return "", fmt.Errorf("хост не найден: %w", err)
	}
	if !force {
		if foreign, err := m.checkForeignInstall(ctx, host); err == nil && foreign != nil {
			return "", foreign
		}
	}

	job := &installJob{created: time.Now(), hostID: hostID}
	job.append("hub.startingInstall")

	id, err := newJobID()
	if err != nil {
		return "", fmt.Errorf("генерация id задачи: %w", err)
	}
	job.id = id

	// Detached from the HTTP request's context on purpose: the request that
	// started this job returns long before the install finishes. cancel is
	// kept on the job itself so CancelInstall can stop it later.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	job.cancel = cancel

	m.jobsMu.Lock()
	// A still-running previous job for this host (a stray double click, or
	// "открыть"'s auto-update racing a manual "обновить") must not be left
	// running alongside this one: two install() goroutines both writing
	// this host's status is a straight last-write-wins race — whichever
	// finishes last decides the host's final status, so a slower goroutine
	// that's still stuck (or simply behind) can silently overwrite a
	// perfectly good "error"/"online" from the other with a stale
	// "installing" that then never gets corrected, leaving the "обновить"
	// button spinning forever. cancelNow forces it to stop and write its
	// own terminal status (see CancelInstall) before this one starts, so
	// only ever one install runs per host at a time.
	if prev, ok := m.jobByHost[hostID]; ok && !prev.isDone() {
		prev.cancelNow()
	}
	m.jobs[id] = job
	m.jobByHost[hostID] = job
	m.evictOldJobsLocked()
	m.jobsMu.Unlock()

	go func() {
		defer cancel()
		job.finish(m.install(ctx, hostID, job))
	}()

	return id, nil
}

// CancelInstall stops host's in-flight install, if the hub still has a live
// goroutine running it, and marks the host as errored either way. When
// there is no job to cancel — most often because the hub itself restarted
// mid-install and lost it, the same situation ResetStuckInstalls handles at
// startup — this just clears the stuck status so the host's controls have
// something sane to act on again.
func (m *Manager) CancelInstall(ctx context.Context, hostID int64) error {
	m.jobsMu.Lock()
	job := m.jobByHost[hostID]
	m.jobsMu.Unlock()

	const message = "установка отменена пользователем"
	if job == nil || job.isDone() {
		return m.db.SetHostStatus(ctx, hostID, store.HostStatusError, message)
	}

	job.cancelNow()
	job.append("hub.installCancelledByUser")
	job.finish(errors.New(message))
	return m.db.SetHostStatus(ctx, hostID, store.HostStatusError, message)
}

// install does the real work behind StartInstall: connect, detect the
// target architecture, cross-compile or reuse a cached binary, ship it over
// SFTP with its systemd unit and env file, start the service, then log in
// through the SSH tunnel so the hub can proxy requests as the remote's own
// bootstrap admin from then on (see tunnel.go, server.go). job carries both
// the progress log (job.append) and, once connected, the live SSH
// connection CancelInstall needs to be able to interrupt this from outside.
func (m *Manager) install(ctx context.Context, hostID int64, job *installJob) error {
	report := job.append
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return fmt.Errorf("хост не найден: %w", err)
	}

	fail := func(err error) error {
		if m.isCurrentJob(hostID, job) {
			_ = m.db.SetHostStatus(ctx, hostID, store.HostStatusError, err.Error())
		}
		return err
	}

	if m.isCurrentJob(hostID, job) {
		if err := m.db.SetHostStatus(ctx, hostID, store.HostStatusInstalling, ""); err != nil {
			return fail(err)
		}
	}

	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return fail(fmt.Errorf("расшифровка SSH-секрета: %w", err))
	}

	report("hub.connectingSSH", host.Addr)
	client, sshErr := dialSSH(ctx, host.Addr, host.SSHPort, host.SSHUser, host.SSHAuthKind, secret)
	if sshErr != nil {
		if m.awaitTunnelReinstallFallback(ctx, host) {
			return m.installOverTunnel(ctx, hostID, host, job)
		}
		return fail(sshErr)
	}
	defer client.Close()
	job.setClient(client)

	goos, goarch, err := detectTarget(client)
	if err != nil {
		return fail(err)
	}
	report("hub.hostArch", goos, goarch)
	if err := m.db.SetHostArch(ctx, hostID, goos+"/"+goarch); err != nil {
		return fail(err)
	}

	binPath, err := m.ensureBinary(ctx, goos, goarch, report)
	if err != nil {
		return fail(err)
	}

	unitContent, err := m.loadUnitTemplate()
	if err != nil {
		return fail(err)
	}

	adminUser, adminPassword, err := m.resolveAdminCredential(ctx, hostID, host)
	if err != nil {
		return fail(err)
	}

	tun, err := m.prepareTunnelEnv(ctx, hostID, host)
	if err != nil {
		return fail(err)
	}

	envContent := renderEnv(adminUser, adminPassword, host.TerminalEnabled, host.SSHUser, tun)
	if err := stageFiles(client, host.SSHUser, binPath, unitContent, envContent, remoteBinPath, remoteServicePath, remoteEnvPath, report, job.replaceLast); err != nil {
		m.recordSudoOutcome(ctx, hostID, host.SSHUser, err)
		return fail(err)
	}
	if err := activateService(client, host.SSHUser, report); err != nil {
		m.recordSudoOutcome(ctx, hostID, host.SSHUser, err)
		return fail(err)
	}
	// Both steps above needed sudo for a non-root SSHUser and neither
	// failed on it — nopasswd sudo (or root, needing none at all) is
	// confirmed working, right here, for free, with no separate probe.
	m.recordSudoOutcome(ctx, hostID, host.SSHUser, nil)

	report("hub.waitingHealth")
	healthCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := waitForHealth(healthCtx, client.Dial); err != nil {
		return fail(err)
	}

	report("hub.checkingAdminAccount")
	if _, err := bootstrapLogin(ctx, client.Dial, adminUser, adminPassword); err != nil {
		report("hub.loginFailedResetting")
		if resetErr := resetRemoteAdminPassword(client, host.SSHUser, adminUser, adminPassword, remoteDataDir, remoteBinPath); resetErr != nil {
			return fail(fmt.Errorf("вход администратора не удался (%v), и сбросить пароль на хосте тоже не получилось: %w", err, resetErr))
		}
		if _, err := bootstrapLogin(ctx, client.Dial, adminUser, adminPassword); err != nil {
			return fail(fmt.Errorf("вход администратора всё ещё не удаётся после сброса пароля на хосте: %w", err))
		}
		report("hub.adminPasswordSynced")
	}
	if err := m.db.SetHostVersion(ctx, hostID, m.version); err != nil {
		return fail(err)
	}
	_ = m.db.TouchHostSeen(ctx, hostID)
	if m.isCurrentJob(hostID, job) {
		if err := m.db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
			return fail(err)
		}
	}

	report("hub.done")
	return nil
}

// prepareTunnelEnv generates this install's reverse-tunnel fallback
// credentials (see internal/tunnel) when the host has TunnelEnabled — the
// zero value (Enabled: false) tells renderEnv to write none of the
// NKT_HUB_TUNNEL_* variables, the same as if this feature didn't exist.
// Unlike the first iteration of this feature, no hub address needs
// resolving here at all: the hub is the side that dials out (see
// tunneldial.go), using host.Addr — already known, the same address SSH
// itself already reaches this host at — so there is nothing for the host
// to be told about how to find the hub.
//
// A fresh random token is generated on every install/update that has
// TunnelEnabled on — not reused from a previous install — mirroring how
// the SSH keypair itself gets regenerated on "изменить" → "переустановить"
// (see UpdateHostGenerated): whatever a *previous* install wrote into this
// host's env file no longer matters once a new one is about to overwrite
// it, and there is no benefit to keeping an old token alive. Encrypted at
// rest (unlike the first iteration's plain hash) because the hub now needs
// the raw value back to present it on every future reconnect, not just to
// verify one — the host is the verifier this time (see internal/tunnel).
func (m *Manager) prepareTunnelEnv(ctx context.Context, hostID int64, host store.Host) (tunnelEnvParams, error) {
	if !host.TunnelEnabled {
		return tunnelEnvParams{}, nil
	}
	token, err := generatePassword() // 24 random bytes, base64 — plenty of entropy for a machine token too
	if err != nil {
		return tunnelEnvParams{}, fmt.Errorf("генерация токена резервного канала: %w", err)
	}
	tokenEnc, err := secretbox.Encrypt(m.key, []byte(token))
	if err != nil {
		return tunnelEnvParams{}, fmt.Errorf("шифрование токена резервного канала: %w", err)
	}
	if err := m.db.SetHostTunnelToken(ctx, hostID, tokenEnc); err != nil {
		return tunnelEnvParams{}, fmt.Errorf("сохранение токена резервного канала: %w", err)
	}
	// A fresh token means a fresh trust bootstrap: clear whatever
	// certificate fingerprint was pinned from a previous install (see
	// internal/hub/tunnelpin.go) so the next dial re-pins from scratch
	// instead of permanently refusing a host that was legitimately
	// reinstalled (new DataDir, regenerated tunnel-tls cert). Best-effort —
	// a failure here just means the old pin lingers and the very next
	// dial's cert-mismatch warning explains why, not a reason to fail the
	// install itself.
	if err := m.db.SetHostTunnelCertSHA256(ctx, hostID, nil); err != nil {
		m.log.Warn("резервный канал: не удалось сбросить привязку сертификата хоста перед переустановкой", "host_id", hostID, "err", err)
	}
	return tunnelEnvParams{
		Enabled:    true,
		ListenAddr: fmt.Sprintf("0.0.0.0:%d", m.cfg.HubTunnelPort),
		Token:      token,
	}, nil
}

// resolveAdminCredential returns the bootstrap admin username/password to
// write into the remote's env file, reusing whatever is already stored for
// this host instead of generating a fresh one every time install runs.
//
// This matters on a reinstall (or a retry after an earlier attempt failed
// partway through): the systemd unit may have already started once with a
// previously generated password and bootstrapped that exact account into
// the remote's own database — auth.Service.Bootstrap only ever creates the
// admin account when the accounts table is still empty, so a *different*
// password written on a later attempt is simply never picked up, and
// bootstrapLogin fails with "неверный логин или пароль" against an account
// that still holds the first password. Persisting the generated password
// immediately (before ever attempting to use it remotely), rather than only
// after a successful login, closes the gap where an interrupted attempt —
// one that got far enough to start the service but not far enough to log
// in — would otherwise leave the hub about to try a different password
// next time than whatever the remote ended up bootstrapped with.
func (m *Manager) resolveAdminCredential(ctx context.Context, hostID int64, host store.Host) (user, password string, err error) {
	user = host.AdminUser
	if user == "" {
		user = "admin"
	}
	if len(host.AdminPasswordEnc) > 0 {
		decrypted, err := secretbox.Decrypt(m.key, host.AdminPasswordEnc)
		if err != nil {
			return "", "", fmt.Errorf("расшифровка сохранённого пароля администратора: %w", err)
		}
		return user, string(decrypted), nil
	}

	password, err = generatePassword()
	if err != nil {
		return "", "", err
	}
	passwordEnc, err := secretbox.Encrypt(m.key, []byte(password))
	if err != nil {
		return "", "", fmt.Errorf("шифрование пароля администратора: %w", err)
	}
	if err := m.db.SetHostAdmin(ctx, hostID, user, passwordEnc); err != nil {
		return "", "", err
	}
	return user, password, nil
}

// recordSudoOutcome updates a host's sudo_status from what stageFiles/
// activateService just observed: root never needs sudo at all; a non-root
// user either got past both steps (nopasswd confirmed, err == nil) or
// didn't. A failure unrelated to sudo itself (network hiccup, disk full —
// anything sudoRequiresPassword doesn't recognise) says nothing about sudo
// either way and is left alone rather than recorded as a guess.
func (m *Manager) recordSudoOutcome(ctx context.Context, hostID int64, sshUser string, err error) {
	status := store.SudoStatusNopasswd
	switch {
	case sshUser == "root":
		status = store.SudoStatusRoot
	case err != nil && !sudoRequiresPassword(err):
		return
	case err != nil:
		status = store.SudoStatusPasswordRequired
	}
	_ = m.db.SetHostSudoStatus(ctx, hostID, status)
}

// SetServiceRunning starts or stops the netknownsthat systemd unit on a
// host — not a reinstall, just start/stop against whatever is already on
// disk there; use StartInstall for anything that needs to touch the
// binary/unit/env themselves. Escalated with sudo -n the same way
// activateService is when SSHUser isn't root.
func (m *Manager) SetServiceRunning(ctx context.Context, hostID int64, running bool) error {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return err
	}
	if host.Status == store.HostStatusNew {
		return fmt.Errorf("хост ещё не установлен — нечего останавливать/запускать")
	}

	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return fmt.Errorf("расшифровка SSH-секрета: %w", err)
	}
	client, err := dialSSH(ctx, host.Addr, host.SSHPort, host.SSHUser, host.SSHAuthKind, secret)
	if err != nil {
		return err
	}
	defer client.Close()

	action := "stop"
	if running {
		action = "start"
	}
	cmd := "systemctl " + action + " netknownsthat"
	if host.SSHUser != "root" {
		cmd = "sudo -n " + cmd
	}
	out, err := runRemote(client, cmd)
	if err != nil {
		return diagnoseInstallError(host.SSHUser, "netknownsthat.service", err, out)
	}
	return nil
}

// RemoveSudoAccess deletes the sudoers drop-in file HUB.md tells an
// operator to create by hand (sudoersDropIn) — a deliberate cleanup step
// for someone who wants to revoke the standing NOPASSWD grant once they no
// longer expect the hub to install/update this host again. Doing this
// obviously means every later install/update needs sudo access restored
// (or the host's SSH user switched to root) before it can do anything that
// needs root on the host again.
func (m *Manager) RemoveSudoAccess(ctx context.Context, hostID int64) error {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return err
	}
	if host.SSHUser == "root" {
		return fmt.Errorf("хост подключён под root — sudo не используется, нечего убирать")
	}
	if host.SudoStatus != store.SudoStatusNopasswd {
		return fmt.Errorf("для хоста не подтверждён доступ sudo без пароля — нечего убирать")
	}

	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return fmt.Errorf("расшифровка SSH-секрета: %w", err)
	}
	client, err := dialSSH(ctx, host.Addr, host.SSHPort, host.SSHUser, host.SSHAuthKind, secret)
	if err != nil {
		return err
	}
	defer client.Close()

	out, err := runRemote(client, "sudo -n rm -f "+sudoersDropIn)
	if err != nil {
		return diagnoseInstallError(host.SSHUser, sudoersDropIn, err, out)
	}
	// Not password_required: some *other* NOPASSWD rule this file didn't
	// create might still grant access. Unknown is the honest answer until
	// the next install/update actually re-observes it either way.
	return m.db.SetHostSudoStatus(ctx, hostID, store.SudoStatusUnknown)
}

// loadUnitTemplate reads deploy/netknownsthat.service as-is from the hub's
// source checkout — the same file a manual install would copy — rather than
// keeping a second embedded copy in internal/hub that could drift from it.
func (m *Manager) loadUnitTemplate() (string, error) {
	path := filepath.Join(m.cfg.HubSourceRoot, "deploy", "netknownsthat.service")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("чтение шаблона systemd-юнита %s: %w", path, err)
	}
	return string(data), nil
}

// InstallJobStatus returns everything reported for an install job so far.
// ok is false when the job id is unknown — never existed, or evicted a
// while after finishing.
func (m *Manager) InstallJobStatus(id string) (events []Event, done bool, errMsg string, ok bool) {
	m.jobsMu.Lock()
	job := m.jobs[id]
	m.jobsMu.Unlock()
	if job == nil {
		return nil, false, "", false
	}
	events, done, errMsg = job.snapshot()
	return events, done, errMsg, true
}

// LatestJobID returns the id of the most recent install job for a host, so
// the UI can reopen its progress/log — after closing the modal, or after a
// page reload loses all local state — instead of that log becoming
// unreachable the moment nothing is actively watching it.
func (m *Manager) LatestJobID(hostID int64) (string, bool) {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	job, ok := m.jobByHost[hostID]
	if !ok {
		return "", false
	}
	return job.id, true
}

// evictOldJobsLocked drops finished jobs older than an hour. Called with
// jobsMu already held.
func (m *Manager) evictOldJobsLocked() {
	cutoff := time.Now().Add(-time.Hour)
	for id, job := range m.jobs {
		if job.created.Before(cutoff) {
			if _, done, _ := job.snapshot(); done {
				delete(m.jobs, id)
			}
		}
	}
}

// newJobID generates a short, unguessable-enough handle for an in-memory
// job — nothing sensitive is keyed by it, but a predictable sequence would
// let one admin session poke at another's in-flight install.
func newJobID() (string, error) {
	buf := make([]byte, 9)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
