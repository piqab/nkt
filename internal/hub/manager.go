package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

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
	created time.Time

	mu     sync.Mutex
	events []Event
	done   bool
	errMsg string
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

// Manager registers remote hosts, installs nkt on them over SSH, and proxies
// their own web API through the connections it keeps open (see proxy.go).
type Manager struct {
	cfg     *config.Config
	db      *store.DB
	key     []byte
	version string

	jobsMu sync.Mutex
	jobs   map[string]*installJob

	connsMu sync.Mutex
	conns   map[int64]*hostConn

	sessionMu sync.Mutex
	sessions  map[int64]sessionCache
}

// NewManager builds a host manager. key is the resolved secretbox master
// key (see secretbox.ResolveKey); version is stamped into every binary this
// hub cross-compiles, so an installed host reports the hub's own build.
func NewManager(cfg *config.Config, db *store.DB, key []byte, version string) *Manager {
	return &Manager{
		cfg: cfg, db: db, key: key, version: version,
		jobs:     map[string]*installJob{},
		conns:    map[int64]*hostConn{},
		sessions: map[int64]sessionCache{},
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

	secretEnc, err := secretbox.Encrypt(m.key, []byte(secret))
	if err != nil {
		return 0, fmt.Errorf("шифрование секрета: %w", err)
	}
	return m.db.CreateHost(ctx, name, addr, sshPort, sshUser, authKind, secretEnc)
}

// StartInstall installs nkt on host id in the background and returns a job
// id immediately, so a caller (the "добавить хост" wizard) can poll
// InstallJobStatus instead of blocking on the whole multi-step operation —
// same pattern as control.CertManager.StartRenewCertbot.
func (m *Manager) StartInstall(hostID int64) (string, error) {
	job := &installJob{created: time.Now()}
	job.append("Начинаю установку")

	id, err := newJobID()
	if err != nil {
		return "", fmt.Errorf("генерация id задачи: %w", err)
	}

	m.jobsMu.Lock()
	m.jobs[id] = job
	m.evictOldJobsLocked()
	m.jobsMu.Unlock()

	go func() {
		// Detached from the HTTP request's context on purpose: the request
		// that started this job returns long before the install finishes.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		job.finish(m.install(ctx, hostID, job.append))
	}()

	return id, nil
}

// install does the real work behind StartInstall: connect, detect the
// target architecture, cross-compile or reuse a cached binary, ship it over
// SFTP with its systemd unit and env file, start the service, then log in
// through the SSH tunnel so the hub can proxy requests as the remote's own
// bootstrap admin from then on (see tunnel.go, server.go).
func (m *Manager) install(ctx context.Context, hostID int64, report func(string)) error {
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

	const adminUser = "admin"
	adminPassword, err := generatePassword()
	if err != nil {
		return fail(err)
	}

	envContent := renderEnv(adminUser, adminPassword)
	if err := stageFiles(client, binPath, unitContent, envContent, remoteBinPath, remoteServicePath, remoteEnvPath, report); err != nil {
		return fail(err)
	}
	if err := activateService(client, report); err != nil {
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
		return fail(err)
	}
	adminPasswordEnc, err := secretbox.Encrypt(m.key, []byte(adminPassword))
	if err != nil {
		return fail(fmt.Errorf("шифрование пароля администратора: %w", err))
	}
	if err := m.db.SetHostAdmin(ctx, hostID, adminUser, adminPasswordEnc); err != nil {
		return fail(err)
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
