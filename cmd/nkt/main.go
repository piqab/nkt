// Command nkt is the NetKnownsThat server: it inspects nginx, haproxy, docker
// and the host firewall, serves the dashboard, and runs the background
// monitoring jobs.
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
	"github.com/althq/netknownsthat/internal/webui"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		showVersion = flag.Bool("version", false, "показать версию и выйти")
		scanOnce    = flag.Bool("scan", false, "выполнить один скан, напечатать отчёт и выйти")
		verbose     = flag.Bool("v", false, "подробный лог")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("netknownsthat %s\n", version)
		return
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if err := run(log, *scanOnce); err != nil {
		log.Error("запуск не удался", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, scanOnce bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	collector, err := collect.New(string(cfg.Mode), cfg.FixturesRoot, cfg.DockerSocket, cfg.CommandTimeout)
	if err != nil {
		return err
	}

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	scanner := inventory.New(cfg, collector, db)

	if scanOnce {
		return printScanReport(context.Background(), scanner)
	}

	authSvc := auth.NewService(db, cfg)
	generated, err := authSvc.Bootstrap(context.Background())
	if err != nil {
		return fmt.Errorf("создание учётной записи администратора: %w", err)
	}
	if generated != "" {
		// Printed once and never stored in clear text anywhere.
		fmt.Fprintf(os.Stderr,
			"\n=== Создана учётная запись администратора ===\n  логин:  %s\n  пароль: %s\n"+
				"  Сохраните пароль: он больше нигде не отображается.\n\n",
			cfg.BootstrapAdminUser, generated)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scheduler := monitor.NewScheduler(cfg, db, scanner, log)
	var jobs sync.WaitGroup
	scheduler.Start(ctx, &jobs)

	services := control.NewServiceManager(cfg, collector, db)
	configs := control.NewConfigManager(cfg, collector, db, scanner, services)
	firewall := control.NewFirewallManager(cfg, collector, db)

	ui := webui.FS()
	if ui == nil {
		log.Warn("фронтенд не собран, отдаётся только API (соберите web/ через npm run build)")
	}

	server := api.New(api.Deps{
		Cfg: cfg, DB: db, Auth: authSvc, Scanner: scanner, Scheduler: scheduler,
		Services: services, Configs: configs, Firewall: firewall, UI: ui, Log: log,
	})

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("сервер запущен",
			"addr", cfg.Addr, "mode", cfg.Mode, "mutations", cfg.AllowMutations, "version", version)
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

// printScanReport runs one scan and writes a human-readable summary, which makes
// the tool usable from cron or a CI check without the dashboard.
func printScanReport(ctx context.Context, scanner *inventory.Scanner) error {
	snap, err := scanner.Scan(ctx)
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
