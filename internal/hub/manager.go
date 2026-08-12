package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// Event is one line of progress from a StartInstall job — the same shape
// control.RenewEvent already uses for certificate-renewal progress, so the
// frontend can reuse the same polling/log-panel component for both.
type Event struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
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

func (j *installJob) append(text string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, Event{Time: time.Now(), Text: text})
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

	jobsMu    sync.Mutex
	jobs      map[string]*installJob
	jobByHost map[int64]*installJob

	connsMu sync.Mutex
	conns   map[int64]*hostConn

	sessionMu sync.Mutex
	sessions  map[int64]sessionCache

	// goBinMu/resolvedGoBin cache resolveGoBin's result for the Manager's
	// lifetime — the self-install it may trigger is expensive enough
	// (network fetch) that it must run at most once per process.
	goBinMu       sync.Mutex
	resolvedGoBin string
}

// NewManager builds a host manager. key is the resolved secretbox master
// key (see secretbox.ResolveKey); version is stamped into every binary this
// hub cross-compiles, so an installed host reports the hub's own build.
func NewManager(cfg *config.Config, db *store.DB, key []byte, version string) *Manager {
	return &Manager{
		cfg: cfg, db: db, key: key, version: version,
		jobs:      map[string]*installJob{},
		jobByHost: map[int64]*installJob{},
		conns:     map[int64]*hostConn{},
		sessions:  map[int64]sessionCache{},
	}
}

// Run starts the manager's background maintenance (idle SSH connection
// eviction) and blocks until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	m.evictIdleConns(ctx)
}

// AddHost registers a host and encrypts its SSH secret at rest. It does not
// connect to the host — that happens in StartInstall.
func (m *Manager) AddHost(ctx context.Context, name, addr string, sshPort int, sshUser, authKind, secret string) (int64, error) {
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
	return m.db.CreateHost(ctx, name, addr, sshPort, sshUser, authKind, secretEnc)
}

// AddHostGenerated registers a host and has the hub generate its own SSH
// keypair for it, instead of accepting one from the operator — the
// recommended way to add a host: the private half is generated here and
// never leaves the hub; authorizedKeyLine is the public half the caller
// must then place in the host's own ~/.ssh/authorized_keys before
// StartInstall can connect.
func (m *Manager) AddHostGenerated(ctx context.Context, name, addr string, sshPort int, sshUser string) (hostID int64, authorizedKeyLine string, err error) {
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
func (m *Manager) UpdateHost(ctx context.Context, hostID int64, name, addr string, sshPort int, sshUser, authKind, secret string) error {
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

	// Connection details (possibly the credential itself) changed — drop any
	// pooled SSH connection/session so the next request reconnects with the
	// new ones instead of replaying stale ones.
	m.CloseHost(hostID)
	return nil
}

// UpdateHostGenerated updates a host's connection details and replaces its
// stored credential with a freshly hub-generated keypair, the same way
// AddHostGenerated does for a new host — useful to switch an existing
// password-authenticated host over to a key, or to rotate a compromised one.
func (m *Manager) UpdateHostGenerated(ctx context.Context, hostID int64, name, addr string, sshPort int, sshUser string) (authorizedKeyLine string, err error) {
	if name == "" || addr == "" || sshUser == "" {
		return "", fmt.Errorf("укажите имя, адрес и пользователя SSH")
	}
	if sshPort == 0 {
		sshPort = 22
	}

	if err := m.db.UpdateHost(ctx, hostID, name, addr, sshPort, sshUser, store.HostAuthKey); err != nil {
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

	m.CloseHost(hostID)
	return authorizedKeyLine, nil
}

// StartInstall installs nkt on host id in the background and returns a job
// id immediately, so a caller (the "добавить хост" wizard) can poll
// InstallJobStatus instead of blocking on the whole multi-step operation —
// same pattern as control.CertManager.StartRenewCertbot.
func (m *Manager) StartInstall(hostID int64) (string, error) {
	job := &installJob{created: time.Now(), hostID: hostID}
	job.append("Начинаю установку")

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
	job.append(message)
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
		_ = m.db.SetHostStatus(ctx, hostID, store.HostStatusError, err.Error())
		return err
	}

	if err := m.db.SetHostStatus(ctx, hostID, store.HostStatusInstalling, ""); err != nil {
		return fail(err)
	}

	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return fail(fmt.Errorf("расшифровка SSH-секрета: %w", err))
	}

	report("Подключаюсь по SSH к " + host.Addr + "…")
	client, err := dialSSH(ctx, host.Addr, host.SSHPort, host.SSHUser, host.SSHAuthKind, secret)
	if err != nil {
		return fail(err)
	}
	defer client.Close()
	job.setClient(client)

	goos, goarch, err := detectTarget(client)
	if err != nil {
		return fail(err)
	}
	report(fmt.Sprintf("Хост: %s/%s", goos, goarch))
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

	envContent := renderEnv(adminUser, adminPassword)
	if err := stageFiles(client, host.SSHUser, binPath, unitContent, envContent, remoteBinPath, remoteServicePath, remoteEnvPath, report); err != nil {
		return fail(err)
	}
	if err := activateService(client, host.SSHUser, report); err != nil {
		return fail(err)
	}

	report("Жду, пока сервис ответит на /health…")
	healthCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := waitForHealth(healthCtx, client); err != nil {
		return fail(err)
	}

	report("Проверяю учётную запись администратора…")
	if _, err := bootstrapLogin(ctx, client, adminUser, adminPassword); err != nil {
		report("Вход не удался — на хосте уже есть учётная запись администратора с другим паролем " +
			"(например, от прошлой попытки установки); сбрасываю пароль на хосте…")
		if resetErr := resetRemoteAdminPassword(client, host.SSHUser, adminUser, adminPassword, remoteDataDir, remoteBinPath); resetErr != nil {
			return fail(fmt.Errorf("вход администратора не удался (%v), и сбросить пароль на хосте тоже не получилось: %w", err, resetErr))
		}
		if _, err := bootstrapLogin(ctx, client, adminUser, adminPassword); err != nil {
			return fail(fmt.Errorf("вход администратора всё ещё не удаётся после сброса пароля на хосте: %w", err))
		}
		report("Пароль администратора на хосте синхронизирован")
	}
	if err := m.db.SetHostVersion(ctx, hostID, m.version); err != nil {
		return fail(err)
	}
	_ = m.db.TouchHostSeen(ctx, hostID)
	if err := m.db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
		return fail(err)
	}

	report("Готово")
	return nil
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
