package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/hub"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// runHubDelete implements `nkt hub delete`: irreversibly wipes everything
// this hub instance knows — the secretbox master key, the host registry
// (including every stored SSH and admin secret), the audit log, cached
// cross-compiled binaries and Go toolchain, and config-edit history — so
// that someone who later gets access to this machine's disk (a stolen
// drive, a compromised backup, a next tenant of a reused VPS) cannot
// recover any of it. Deliberately CLI-only, run by hand over SSH/console —
// unlike every other mutating action this project exposes, there is no web
// UI button for this: it must never be reachable by anything short of
// already having a shell on the box the hub itself runs on.
//
// The installed binary and systemd unit are left alone: `sudo systemctl
// start netknownsthat-hub` right after this bootstraps a brand new hub
// (new master key, new admin account) with no separate reinstall step —
// this command clears the hub's STATE, not its installation.
func runHubDelete(opts commandOptions, log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Mode != config.ModeHub {
		return fmt.Errorf("nkt hub delete требует NKT_MODE=hub, сейчас %q", cfg.Mode)
	}

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = db.Close()
		}
	}()

	key, err := secretbox.ResolveKey(cfg.HubMasterKey, cfg.HubKeyFile())
	if err != nil {
		return fmt.Errorf("ключ шифрования секретов хаба: %w", err)
	}
	manager := hub.NewManager(cfg, db, key, version)

	ctx := context.Background()
	stdin := bufio.NewReader(os.Stdin)
	if err := offerExport(ctx, manager, opts, stdin); err != nil {
		return err
	}

	if !opts.yes {
		ok, err := confirmHubDelete(cfg, stdin)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Отменено, ничего не удалено.")
			return nil
		}
	}

	// Best-effort, before the wipe below: a still-running hub service
	// holding these same files open should not be racing this command's
	// overwrite-then-remove pass. Failure here (no systemd, no permission,
	// a Docker-based deployment with no unit at all) is logged, never
	// fatal — it must not block the actual security-critical step.
	stopHubUnit(log)

	if err := db.Close(); err != nil {
		log.Warn("закрытие базы перед удалением", "err", err)
	}
	closed = true

	fmt.Printf("Стираю %s…\n", cfg.DataDir)
	if err := secretbox.SecureWipeDir(cfg.DataDir); err != nil {
		return fmt.Errorf("удаление данных хаба: %w", err)
	}

	fmt.Printf(
		"Готово: ключ шифрования, база (включая SSH- и admin-секреты всех подключённых "+
			"хостов), кэш бинарников и история конфигов в %s безвозвратно стёрты.\n"+
			"Сами хосты и установленный на них nkt это не трогает — только то, что хаб о них знал.\n"+
			"Бинарник и systemd-юнит хаба остались на месте: `sudo systemctl start "+
			"netknownsthat-hub` поднимет полностью новый хаб — новый ключ, новая учётная "+
			"запись администратора.\n",
		cfg.DataDir)
	return nil
}

// offerExport handles the three ways the operator can decide whether to
// back the host registry up (with the master key embedded, so it can be
// restored onto a fresh hub via import — see Manager.ExportHosts) before
// everything is wiped: an explicit path via -export, an explicit skip via
// -no-export, or — the only case that needs a live terminal — an
// interactive prompt. -yes without either flag is refused outright rather
// than silently skipping the backup: a scripted, unattended run is exactly
// the situation where nobody would notice a lost export until it's too late.
func offerExport(ctx context.Context, manager *hub.Manager, opts commandOptions, stdin *bufio.Reader) error {
	switch {
	case opts.noExport:
		return nil
	case opts.export != "":
		return writeHubExport(ctx, manager, opts.export)
	case opts.yes:
		return fmt.Errorf(
			"с -yes нужно явно указать -export <файл> или -no-export — без терминала нечем " +
				"спросить, нужен ли бэкап перед необратимым удалением")
	case !term.IsTerminal(int(os.Stdin.Fd())):
		return fmt.Errorf("стандартный ввод не терминал — укажите -export <файл> или -no-export явно")
	}

	fmt.Print("Сохранить экспорт хостов с ключом перед удалением, для восстановления? [Y/n]: ")
	answer, err := stdin.ReadString('\n')
	if err != nil && answer == "" {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(answer), "n") {
		return nil
	}

	fmt.Print("Куда сохранить файл экспорта: ")
	path, err := stdin.ReadString('\n')
	if err != nil && path == "" {
		return err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("путь для экспорта не указан")
	}
	return writeHubExport(ctx, manager, path)
}

func writeHubExport(ctx context.Context, manager *hub.Manager, path string) error {
	export, err := manager.ExportHosts(ctx, true)
	if err != nil {
		return fmt.Errorf("подготовка экспорта: %w", err)
	}
	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return fmt.Errorf("сериализация экспорта: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("запись %s: %w", path, err)
	}
	fmt.Printf(
		"Экспорт с ключом сохранён в %s (%d хостов). Пока ключ в файле, его одного достаточно, "+
			"чтобы расшифровать секреты всех хостов — храните и удалите его так же осторожно, "+
			"как пароли.\n",
		path, len(export.Hosts))
	return nil
}

// confirmHubDelete requires the operator to type out a specific word,
// deliberately more friction than a bare y/N — every other destructive
// action in this project (window.confirm in the web UI) is at most one
// click to undo-by-not-clicking; this one has nothing to undo at all once
// it runs.
func confirmHubDelete(cfg *config.Config, stdin *bufio.Reader) (bool, error) {
	fmt.Printf(
		"\nВНИМАНИЕ: необратимо сотрёт (с затиранием содержимого, не просто rm) ключ "+
			"шифрования, базу хаба — включая SSH- и admin-секреты ВСЕХ подключённых хостов — "+
			"кэш бинарников и историю конфигов в %s.\n"+
			"Сами управляемые хосты и nkt на них не трогает — только то, что этот хаб о них знал.\n\n"+
			"Наберите «удалить», чтобы продолжить: ",
		cfg.DataDir)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	return strings.TrimSpace(line) == "удалить", nil
}

// stopHubUnit best-effort stops and disables the hub's own systemd unit.
// See runHubDelete's own comment on why a failure here never aborts the
// command — it is not a Docker-based deployment's fault that it has no
// systemd unit to stop, and that must not leave its data un-wiped.
func stopHubUnit(log *slog.Logger) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	for _, args := range [][]string{
		{"stop", "netknownsthat-hub"},
		{"disable", "netknownsthat-hub"},
	} {
		out, err := exec.Command("systemctl", args...).CombinedOutput()
		if err != nil {
			log.Warn("systemctl "+strings.Join(args, " "), "err", err, "output", strings.TrimSpace(string(out)))
		}
	}
}
