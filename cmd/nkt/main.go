// Command nkt is NetKnownsThat: it inspects nginx, haproxy, docker and the host
// firewall, and exposes the result three ways — a web dashboard, a terminal
// interface, and a one-shot report for cron or CI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/althq/netknownsthat/internal/api"
	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/monitor"
	"github.com/althq/netknownsthat/internal/store"
	"github.com/althq/netknownsthat/internal/tui"
	"github.com/althq/netknownsthat/internal/webui"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `NetKnownsThat %s — карта сетевых ресурсов и проверка конфигураций хоста.

Использование:
  nkt [serve]         запустить веб-дашборд и фоновый сбор данных (по умолчанию)
  nkt tui             терминальный интерфейс: то же самое без браузера
  nkt scan            разовая проверка, отчёт в stdout, код 2 при критичных находках
  nkt users           показать учётные записи веб-интерфейса
  nkt passwd [логин]  сменить пароль (по умолчанию admin), спросит его без эха
  nkt version         показать версию

Флаги:
  -v                  подробный лог
  -random             в passwd: сгенерировать пароль и напечатать один раз
  -role admin|viewer  в passwd: создать учётную запись с этой ролью, если её нет

Примеры:
  sudo nkt passwd                     сменить пароль администратора
  sudo nkt passwd ops -role viewer    завести учётку только на чтение
  sudo nkt passwd -random             выдать новый случайный пароль admin

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
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if err := dispatch(command, opts, log); err != nil {
		log.Error("не удалось выполнить команду", "команда", command, "err", err)
		os.Exit(1)
	}
}

// commandOptions carries the flags and positional argument the subcommands read.
type commandOptions struct {
	username string
	role     string
	random   bool
}

func dispatch(command string, opts commandOptions, log *slog.Logger) error {
	switch command {
	case "version":
		fmt.Printf("netknownsthat %s\n", version)
		return nil
	case "serve", "tui", "scan", "users", "passwd":
	case "help", "-h", "--help":
		fmt.Fprintf(os.Stderr, usage, version)
		return nil
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
	case "users":
		return app.listUsers(ctx)
	case "passwd":
		return app.setPassword(ctx, opts.username, opts.role, opts.random)
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
	certs     *control.CertManager
}

func newRuntime() (*runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	collector, err := collect.New(string(cfg.Mode), cfg.FixturesRoot, cfg.DockerSocket, cfg.CommandTimeout)
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
		certs:     control.NewCertManager(cfg, collector, db),
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
		Prober:    monitor.NewProber(r.db, r.cfg),
	})
}

// --------------------------------------------------------------------- serve

func (r *runtime) runServer(log *slog.Logger) error {
	authSvc := auth.NewService(r.db, r.cfg)
	generated, err := authSvc.Bootstrap(context.Background())
	if err != nil {
		return fmt.Errorf("создание учётной записи администратора: %w", err)
	}
	if generated != "" {
		// Printed once and never stored in clear text anywhere.
		fmt.Fprintf(os.Stderr,
			"\n=== Создана учётная запись администратора ===\n  логин:  %s\n  пароль: %s\n"+
				"  Сохраните пароль: он больше нигде не отображается.\n\n",
			r.cfg.BootstrapAdminUser, generated)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scheduler := monitor.NewScheduler(r.cfg, r.db, r.scanner, r.certs, log)
	var jobs sync.WaitGroup
	scheduler.Start(ctx, &jobs)

	ui := webui.FS()
	if ui == nil {
		log.Warn("фронтенд не собран, отдаётся только API (соберите web/ через npm run build)")
	}

	server := api.New(api.Deps{
		Cfg: r.cfg, DB: r.db, Auth: authSvc, Scanner: r.scanner, Scheduler: scheduler,
		Services: r.services, Configs: r.configs, Firewall: r.firewall, Certs: r.certs, UI: ui, Log: log,
	})

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
		log.Info("сервер запущен",
			"addr", r.cfg.Addr, "mode", r.cfg.Mode, "mutations", r.cfg.AllowMutations, "version", version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
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
