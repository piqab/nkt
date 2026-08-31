// Command nkt is NetKnownsThat: it inspects nginx, haproxy, docker and the host
// firewall, and exposes the result three ways — a web dashboard, a terminal
// interface, and a one-shot report for cron or CI.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/althq/netknownsthat/internal/api"
	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/hub"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/monitor"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
	"github.com/althq/netknownsthat/internal/tlscert"
	"github.com/althq/netknownsthat/internal/tui"
	"github.com/althq/netknownsthat/internal/tunnel"
	"github.com/althq/netknownsthat/internal/webui"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `NetKnownsThat %s — карта сетевых ресурсов и проверка конфигураций хоста.

Использование:
  nkt [serve]         запустить веб-дашборд и фоновый сбор данных (по умолчанию)
  nkt tui             терминальный интерфейс: то же самое без браузера
  nkt scan            разовая проверка, отчёт в stdout, код 2 при критичных находках
  nkt hub             управляющий центр: установка и проксирование nkt на других хостах по SSH
  nkt hub delete      безвозвратно стереть данные и ключи хаба (сервис и бинарник остаются)
  nkt hub import -file <файл>   восстановить хосты из экспорта (обычного или с паролем)
  nkt users           показать учётные записи веб-интерфейса
  nkt passwd [логин]  сменить пароль (по умолчанию admin), спросит его без эха
  nkt version         показать версию

Флаги:
  -v                  подробный лог
  -random             в passwd: сгенерировать пароль и напечатать один раз
  -role admin|viewer  в passwd: создать учётную запись с этой ролью, если её нет
  -yes                в hub delete: не спрашивать подтверждения
  -export <файл>      в hub delete: сохранить сюда экспорт хостов с ключом перед удалением
  -no-export          в hub delete: не предлагать и не делать экспорт перед удалением
  -file <файл>        в hub import: путь к файлу экспорта

Примеры:
  sudo nkt passwd                     сменить пароль администратора
  sudo nkt passwd ops -role viewer    завести учётку только на чтение
  sudo nkt passwd -random             выдать новый случайный пароль admin
  sudo nkt hub delete                 удалить хаб: спросит про экспорт и подтверждение
  sudo nkt hub delete -export a.json  удалить хаб, предварительно сохранив экспорт в a.json
  sudo nkt hub import -file a.json    восстановить хосты из ранее сохранённого экспорта

Настройка — через переменные окружения NKT_*, см. deploy/nkt.env.example.
`

func main() {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		command, args = args[0], args[1:]
	}

	fs := flag.NewFlagSet("nkt", flag.ExitOnError)
	verbose := fs.Bool("v", false, "подробный лог")
	// Kept so that the documented `nkt -scan` keeps working.
	scanFlag := fs.Bool("scan", false, "разовая проверка и выход")
	versionFlag := fs.Bool("version", false, "показать версию и выйти")
	randomFlag := fs.Bool("random", false, "сгенерировать пароль вместо ввода")
	roleFlag := fs.String("role", "", "роль создаваемой учётной записи: admin или viewer")
	yesFlag := fs.Bool("yes", false, "в hub delete: не спрашивать подтверждения")
	exportFlag := fs.String("export", "", "в hub delete: сохранить сюда экспорт хостов с ключом перед удалением")
	noExportFlag := fs.Bool("no-export", false, "в hub delete: не предлагать и не делать экспорт перед удалением")
	fileFlag := fs.String("file", "", "в hub import: путь к файлу экспорта (обычному или зашифрованному паролем)")
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usage, version) }

	// The flag package stops at the first non-flag argument, so `passwd ops
	// -role viewer` would silently ignore the role. Parse what precedes the
	// positional argument, then continue with what follows it.
	_ = fs.Parse(args)
	positional := ""
	if rest := fs.Args(); len(rest) > 0 {
		positional = rest[0]
		_ = fs.Parse(rest[1:])
	}

	switch {
	case *versionFlag:
		command = "version"
	case *scanFlag:
		command = "scan"
	}

	opts := commandOptions{
		username: positional,
		role:     *roleFlag,
		random:   *randomFlag,
		yes:      *yesFlag,
		export:   *exportFlag,
		noExport: *noExportFlag,
		file:     *fileFlag,
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if err := dispatch(command, opts, log); err != nil {
		log.Error("command failed", "command", command, "err", err)
		os.Exit(1)
	}
}

// commandOptions carries the flags and positional argument the subcommands
// read. username doubles as the `hub` subcommand name (e.g. "delete") when
// command == "hub" — both are just "whatever followed the command word",
// the same slot passwd's <username> and hub's <subcommand> both fill.
type commandOptions struct {
	username string
	role     string
	random   bool
	yes      bool
	export   string
	noExport bool
	file     string
}

func dispatch(command string, opts commandOptions, log *slog.Logger) error {
	switch command {
	case "version":
		fmt.Printf("netknownsthat %s\n", version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprintf(os.Stderr, usage, version)
		return nil
	case "hub":
		switch opts.username {
		case "delete":
			return runHubDelete(opts, log)
		case "import":
			return runHubImport(opts, log)
		}
		// A hub never inspects a local host, so it gets its own much smaller
		// runtime instead of newRuntime() — see hubRuntime.
		app, err := newHubRuntime()
		if err != nil {
			return err
		}
		defer app.close()
		return app.runHub(log)
	case "users", "passwd":
		// Neither command ever touches the collector — they only read and
		// write the accounts table — so they work the same way under every
		// NKT_MODE, including hub, unlike newRuntime() below which would
		// fail trying to build a local-host collector for NKT_MODE=hub.
		app, err := newAccountsRuntime()
		if err != nil {
			return err
		}
		defer app.close()
		if command == "users" {
			return app.listUsers(context.Background())
		}
		return app.setPassword(context.Background(), opts.username, opts.role, opts.random)
	case "serve", "tui", "scan":
	default:
		fmt.Fprintf(os.Stderr, usage, version)
		return fmt.Errorf("неизвестная команда %q", command)
	}

	app, err := newRuntime()
	if err != nil {
		return err
	}
	defer app.close()

	ctx := context.Background()
	switch command {
	case "scan":
		return app.printScanReport(ctx)
	case "tui":
		return app.runTUI()
	default:
		return app.runServer(log)
	}
}

// runtime holds the subsystems every command shares.
type runtime struct {
	cfg       *config.Config
	db        *store.DB
	collector collect.Collector
	scanner   *inventory.Scanner
	services  *control.ServiceManager
	configs   *control.ConfigManager
	firewall  *control.FirewallManager
	firewalld *control.FirewalldManager
	certs     *control.CertManager
	podman    *control.PodmanManager
	lxd       *control.LXDManager
	libvirt   *control.LibvirtManager
}

func newRuntime() (*runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	collector, err := collect.New(string(cfg.Mode), cfg.FixturesRoot, cfg.DockerSocket, cfg.PodmanSocket, cfg.CommandTimeout)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	scanner := inventory.New(cfg, collector, db)
	services := control.NewServiceManager(cfg, collector, db)

	return &runtime{
		cfg:       cfg,
		db:        db,
		collector: collector,
		scanner:   scanner,
		services:  services,
		configs:   control.NewConfigManager(cfg, collector, db, scanner, services),
		firewall:  control.NewFirewallManager(cfg, collector, db),
		firewalld: control.NewFirewalldManager(cfg, collector, db),
		certs:     control.NewCertManager(cfg, collector, db, services, scanner),
		podman:    control.NewPodmanManager(collector, db),
		lxd:       control.NewLXDManager(collector, db),
		libvirt:   control.NewLibvirtManager(cfg, collector, db, scanner),
	}, nil
}

func (r *runtime) close() {
	if r.db != nil {
		_ = r.db.Close()
	}
}

// ------------------------------------------------------------------ terminal

func (r *runtime) runTUI() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return tui.Run(ctx, tui.Deps{
		Cfg:       r.cfg,
		DB:        r.db,
		Collector: r.collector,
		Scanner:   r.scanner,
		Services:  r.services,
		Configs:   r.configs,
		Firewall:  r.firewall,
		Certs:     r.certs,
		Podman:    r.podman,
		LXD:       r.lxd,
		Libvirt:   r.libvirt,
		Prober:    monitor.NewProber(r.db, r.cfg),
	})
}

// --------------------------------------------------------------------- serve

// ensureTLS resolves which certificate/key files the HTTP server should
// listen with — used by both runServer and runHub, since either one can
// terminate TLS itself instead of relying on an external reverse proxy (see
// config.Config.TLSEnabled's own doc comment for why that's off by
// default). Empty return values mean "plain HTTP", the default: cfg.
// TLSEnabled is off. With it on, an explicit cfg.TLSCert/TLSKey pair is
// used as-is; left empty, a self-signed certificate is generated (or
// reused, if one already on disk still matches — see internal/tlscert)
// under cfg's own DataDir, covering cfg.TLSHosts.
func ensureTLS(cfg *config.Config, log *slog.Logger) (certFile, keyFile string, err error) {
	if !cfg.TLSEnabled {
		return "", "", nil
	}
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		log.Info("TLS enabled", "cert", cfg.TLSCert, "self_signed", false)
		return cfg.TLSCert, cfg.TLSKey, nil
	}
	certFile = cfg.TLSSelfSignedCertFile()
	keyFile = cfg.TLSSelfSignedKeyFile()
	if err := tlscert.EnsureSelfSigned(certFile, keyFile, cfg.TLSHosts); err != nil {
		return "", "", fmt.Errorf("preparing self-signed TLS certificate: %w", err)
	}
	log.Info("TLS enabled: self-signed certificate", "hosts", cfg.TLSHosts, "cert", certFile)
	return certFile, keyFile, nil
}

func (r *runtime) runServer(log *slog.Logger) error {
	authSvc := auth.NewService(r.db, r.cfg)
	generated, err := authSvc.Bootstrap(context.Background())
	if err != nil {
		return fmt.Errorf("creating admin account: %w", err)
	}
	if generated != "" {
		// Printed once and never stored in clear text anywhere.
		fmt.Fprintf(os.Stderr,
			"\n=== Admin account created ===\n  username: %s\n  password: %s\n"+
				"  Save this password: it is never shown again.\n\n",
			r.cfg.BootstrapAdminUser, generated)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scheduler := monitor.NewScheduler(r.cfg, r.db, r.scanner, r.certs, log)
	var jobs sync.WaitGroup
	scheduler.Start(ctx, &jobs)

	// The reverse-tunnel fallback channel (internal/tunnel) — only present
	// when this host was installed by a hub with TunnelEnabled on (see
	// internal/hub/provision.go's renderEnv); TunnelListenAddr empty is the
	// common case (a plain standalone install, or one added to a hub
	// without this feature turned on) and starts nothing here at all. The
	// hub is the side that dials in (see internal/hub/tunneldial.go) — this
	// host only needs to accept on TunnelListenAddr and check whatever
	// token the connection presents, so unlike the dashboard's own optional
	// TLS (ensureTLS above), there is no NKT_TLS_ENABLED gate here: the
	// listener always needs a certificate to speak TLS at all, generated
	// once and reused the same way, under its own subdirectory so toggling
	// the dashboard's TLS setting never touches it.
	if r.cfg.TunnelListenAddr != "" {
		certFile := filepath.Join(r.cfg.DataDir, "tunnel-tls", "cert.pem")
		keyFile := filepath.Join(r.cfg.DataDir, "tunnel-tls", "key.pem")
		if err := tlscert.EnsureSelfSigned(certFile, keyFile, []string{"nkt-tunnel"}); err != nil {
			return fmt.Errorf("preparing fallback channel certificate: %w", err)
		}
		tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("loading fallback channel certificate: %w", err)
		}
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			if err := tunnel.Run(ctx, tunnel.ListenerConfig{
				ListenAddr: r.cfg.TunnelListenAddr,
				Token:      r.cfg.TunnelToken,
				TLSCert:    tlsCert,
				LocalAddr:  r.cfg.Addr,
				Log:        log,
			}); err != nil && ctx.Err() == nil {
				log.Error("fallback channel: could not start accepting connections", "err", err)
			}
		}()
	}

	ui := webui.FS()
	if ui == nil {
		log.Warn("frontend not built, serving API only (run npm run build in web/)")
	}

	server := api.New(api.Deps{
		Cfg: r.cfg, DB: r.db, Auth: authSvc, Scanner: r.scanner, Scheduler: scheduler,
		Services: r.services, Configs: r.configs, Firewall: r.firewall, Firewalld: r.firewalld, Certs: r.certs,
		Podman: r.podman, LXD: r.lxd, Libvirt: r.libvirt, UI: ui, Log: log,
		Version: version,
	})

	tlsCert, tlsKey, err := ensureTLS(r.cfg, log)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              r.cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server started",
			"addr", r.cfg.Addr, "mode", r.cfg.Mode, "mutations", r.cfg.AllowMutations, "version", version, "tls", tlsCert != "")
		var serveErr error
		if tlsCert != "" {
			serveErr = httpServer.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			serveErr = httpServer.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("получен сигнал завершения, останавливаемся")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("остановка HTTP-сервера", "err", err)
	}
	jobs.Wait()
	log.Info("завершено")
	return nil
}

// ---------------------------------------------------------------------- scan

// printScanReport runs one scan and writes a human-readable summary, which makes
// the tool usable from cron or a CI check without any interface at all.
func (r *runtime) printScanReport(ctx context.Context) error {
	snap, err := r.scanner.Scan(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("Хост:       %s (%s, режим %s)\n", snap.Host.Hostname, snap.Host.OS, snap.Mode)
	fmt.Printf("Скан:       %s за %d мс\n", snap.TS, snap.ScanMS)
	fmt.Printf("Найдено:    %d слушателей в конфигах, %d пулов, %d контейнеров, %d правил firewall\n\n",
		len(snap.Endpoints), len(snap.Upstreams), len(snap.Container), len(snap.Firewall.Rules))

	for _, src := range snap.Sources {
		state := "ok"
		if src.Error != "" {
			state = "ОШИБКА: " + src.Error
		} else if !src.Available {
			state = "недоступен"
		}
		fmt.Printf("  %-12s %-40s %s\n", src.Name, state, src.Version)
	}

	counts := snap.FindingCounts()
	fmt.Printf("\nПроблемы: critical=%d high=%d medium=%d low=%d info=%d\n",
		counts[model.SeverityCritical], counts[model.SeverityHigh], counts[model.SeverityMedium],
		counts[model.SeverityLow], counts[model.SeverityInfo])

	for _, f := range snap.Findings {
		fmt.Printf("\n[%s] %s\n    объект: %s\n    %s\n", f.Severity, f.Title, f.Object, f.Detail)
		if f.Suggestion != "" {
			fmt.Printf("    → %s\n", f.Suggestion)
		}
	}

	// A non-zero exit makes this usable as a gate in a pipeline.
	if counts[model.SeverityCritical] > 0 {
		os.Exit(2)
	}
	return nil
}

// ------------------------------------------------------------------ accounts

// accountsRuntime holds what `nkt users`/`nkt passwd` need — just config and
// the database, the same minimal shape under every NKT_MODE (including hub),
// since neither command ever reads or writes anything a collector would
// produce.
type accountsRuntime struct {
	cfg *config.Config
	db  *store.DB
}

func newAccountsRuntime() (*accountsRuntime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	return &accountsRuntime{cfg: cfg, db: db}, nil
}

func (r *accountsRuntime) close() {
	if r.db != nil {
		_ = r.db.Close()
	}
}

// ----------------------------------------------------------------------- hub

// hubRuntime holds what the hub command needs: the host registry and SSH
// session machinery for managing OTHER hosts, plus — reusing exactly the
// same collect/inventory/control machinery `runtime` above builds for a
// plain local install — a scanner for the machine the hub itself runs on.
// That second half is what lets "localhost" appear pinned in the host list
// with no SSH install of its own (installing a second nkt onto the hub's
// own machine would collide on the same 127.0.0.1:8077 the hub itself
// listens on — see ensureBinary/renderEnv's hardcoded NKT_ADDR). It needs
// the hub's own systemd unit to run with the same privileges a managed
// host's does (see deploy/netknownsthat-hub.service) — a hub deployed
// without them still starts, it just can't read anything on its own host.
type hubRuntime struct {
	cfg *config.Config
	db  *store.DB

	collector collect.Collector
	scanner   *inventory.Scanner
	services  *control.ServiceManager
	configs   *control.ConfigManager
	firewall  *control.FirewallManager
	firewalld *control.FirewalldManager
	certs     *control.CertManager
	podman    *control.PodmanManager
	lxd       *control.LXDManager
	libvirt   *control.LibvirtManager
}

func newHubRuntime() (*hubRuntime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.Mode != config.ModeHub {
		return nil, fmt.Errorf("nkt hub requires NKT_MODE=hub, currently %q", cfg.Mode)
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}

	// The embedded scanner always calls itself "local" regardless of this
	// process's own cfg.Mode ("hub") — collect.New only recognizes
	// "local"/"fixtures" (internal/collect/factory.go), and this is
	// exactly the same host-inspection collector a plain nkt install
	// builds for itself; nothing about scanning the local host is
	// hub-specific.
	collector, err := collect.New("local", cfg.FixturesRoot, cfg.DockerSocket, cfg.PodmanSocket, cfg.CommandTimeout)
	if err != nil {
		return nil, err
	}
	scanner := inventory.New(cfg, collector, db)
	services := control.NewServiceManager(cfg, collector, db)

	return &hubRuntime{
		cfg: cfg, db: db,
		collector: collector,
		scanner:   scanner,
		services:  services,
		configs:   control.NewConfigManager(cfg, collector, db, scanner, services),
		firewall:  control.NewFirewallManager(cfg, collector, db),
		firewalld: control.NewFirewalldManager(cfg, collector, db),
		certs:     control.NewCertManager(cfg, collector, db, services, scanner),
		podman:    control.NewPodmanManager(collector, db),
		lxd:       control.NewLXDManager(collector, db),
		libvirt:   control.NewLibvirtManager(cfg, collector, db, scanner),
	}, nil
}

func (r *hubRuntime) close() {
	if r.db != nil {
		_ = r.db.Close()
	}
}

func (r *hubRuntime) runHub(log *slog.Logger) error {
	key, err := secretbox.ResolveKey(r.cfg.HubMasterKey, r.cfg.HubKeyFile())
	if err != nil {
		return fmt.Errorf("hub secret encryption key: %w", err)
	}

	authSvc := auth.NewService(r.db, r.cfg)
	generated, err := authSvc.Bootstrap(context.Background())
	if err != nil {
		return fmt.Errorf("creating hub admin account: %w", err)
	}
	if generated != "" {
		fmt.Fprintf(os.Stderr,
			"\n=== Hub admin account created ===\n  username: %s\n  password: %s\n"+
				"  Save this password: it is never shown again.\n\n",
			r.cfg.BootstrapAdminUser, generated)
	}

	// Any host still 'installing' belongs to a run this process never
	// finished (a crash, or this very restart) — nothing left alive can
	// ever complete it, so it must not stay stuck forever with its
	// "переустановить"/cancel controls unable to act on anything real.
	// This reason string lands in Host.ErrorMsg, shown in the web UI, not
	// the console — left in Russian like every other install-failure
	// message that field carries today (internal/hub's own error text is
	// entirely Russian still; translating only this one would be a random
	// half-measure, not what console-startup-message English covers).
	if n, err := r.db.ResetStuckInstalls(context.Background(), "установка прервана перезапуском хаба"); err != nil {
		log.Warn("could not reset stuck installs", "err", err)
	} else if n > 0 {
		log.Info("reset stuck host installs", "count", n)
	}

	manager := hub.NewManager(r.cfg, r.db, key, version, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go manager.Run(ctx)

	// The embedded local API server — same api.New/Handler() a plain nkt
	// install uses, sharing the hub's own db and auth.Service so the
	// operator's existing hub session cookie authenticates against it with
	// no separate login. UI is deliberately nil: the hub's own router
	// serves the SPA at "/", this instance is only ever reached at
	// /api/hosts/local/* (internal/hub/server.go), which only needs its
	// API routes.
	localAPI := api.New(api.Deps{
		Cfg: r.cfg, DB: r.db, Auth: authSvc, Scanner: r.scanner,
		Services: r.services, Configs: r.configs, Firewall: r.firewall, Firewalld: r.firewalld, Certs: r.certs,
		Podman: r.podman, LXD: r.lxd, Libvirt: r.libvirt, Log: log, Version: version,
	})

	// Keeps the "localhost" entry's snapshot/findings/history/availability
	// data fresh, exactly the way runServer's scheduler does for a plain
	// local install — without this, nothing would ever call Scan() and
	// the localhost dashboard would stay permanently empty.
	scheduler := monitor.NewScheduler(r.cfg, r.db, r.scanner, r.certs, log)
	var jobs sync.WaitGroup
	scheduler.Start(ctx, &jobs)

	ui := webui.FS()
	if ui == nil {
		log.Warn("frontend not built, serving API only (run npm run build in web/)")
	}

	server := hub.New(hub.Deps{
		Cfg: r.cfg, DB: r.db, Auth: authSvc, Hub: manager,
		Local: localAPI.Handler(), LocalScanner: r.scanner, UI: ui, Log: log,
	})

	tlsCert, tlsKey, err := ensureTLS(r.cfg, log)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              r.cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("hub started", "addr", r.cfg.Addr, "version", version, "tls", tlsCert != "")
		var serveErr error
		if tlsCert != "" {
			serveErr = httpServer.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			serveErr = httpServer.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("получен сигнал завершения, останавливаемся")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("остановка HTTP-сервера", "err", err)
	}
	jobs.Wait()
	log.Info("завершено")
	return nil
}
