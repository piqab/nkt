package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/hub"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// runHubDelete implements `nkt hub delete`: irreversibly clears everything
// this hub instance knows — shredding (overwrite, not just unlink) the
// secretbox master key, the host registry (including every stored SSH and
// admin secret), the audit log, config-edit history and the self-signed
// TLS private key, so that someone who later gets access to this machine's
// disk (a stolen drive, a compromised backup, a next tenant of a reused
// VPS) cannot recover any of it — then plainly removing everything else
// (cached cross-compiled binaries, the self-installed Go toolchain), which
// hold nothing secret to begin with (see wipeHubData). Deliberately
// CLI-only, run by hand over SSH/console — unlike every other mutating
// action this project exposes, there is no web UI button for this: it must
// never be reachable by anything short of already having a shell on the
// box the hub itself runs on.
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
		return msgs.Errorf("cli.needsHubMode", "nkt hub delete", cfg.Mode)
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
		return msgs.Errorf("cli.hubKey", err)
	}
	manager := hub.NewManager(cfg, db, key, version, log)

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
			fmt.Println(cli("cli.delete.canceled"))
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
		log.Warn("closing the database before deletion", "err", err)
	}
	closed = true

	fmt.Print(cli("cli.delete.wiping", cfg.DataDir))
	if err := wipeHubData(cfg); err != nil {
		return msgs.Errorf("cli.delete.failed", err)
	}

	fmt.Print(cli("cli.delete.done", cfg.DataDir))
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
		// A password can't safely arrive as a flag (visible in `ps`) or get
		// typed hidden in a non-interactive run — NKT_HUB_EXPORT_PASSWORD
		// is the only way to encrypt on this path. Left unset, the export
		// is written in the clear: loud about it (see writeHubExport),
		// never silent, but not blocked — an unattended/scripted delete
		// must still be able to finish.
		return writeHubExport(ctx, manager, opts.export, os.Getenv("NKT_HUB_EXPORT_PASSWORD"))
	case opts.yes:
		return msgs.Errorf("cli.delete.yesNeedsExport")
	case !term.IsTerminal(int(os.Stdin.Fd())):
		return msgs.Errorf("cli.delete.noTerminal")
	}

	fmt.Print(cli("cli.delete.askExport"))
	answer, err := stdin.ReadString('\n')
	if err != nil && answer == "" {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(answer), "n") {
		return nil
	}

	fmt.Print(cli("cli.delete.askPath"))
	path, err := stdin.ReadString('\n')
	if err != nil && path == "" {
		return err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return msgs.Errorf("cli.delete.noPath")
	}

	password, err := promptExportPassword(stdin)
	if err != nil {
		return err
	}
	return writeHubExport(ctx, manager, path, password)
}

// promptExportPassword asks whether to encrypt the export file and, if so,
// reads the password twice (hidden, via term.ReadPassword — falling back to
// a visible line read only when stdin isn't a real terminal, same as
// accounts.go's own promptPassword). An empty answer opts out of
// encryption — writeHubExport prints its own loud warning when that
// happens, this function's job is just collecting the choice.
func promptExportPassword(stdin *bufio.Reader) (string, error) {
	fmt.Println()
	fmt.Println(cli("cli.export.warn"))

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Print(cli("cli.export.askPassword"))
		line, err := stdin.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}

	fmt.Print(cli("cli.export.askPassword"))
	first, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	if len(first) == 0 {
		return "", nil
	}
	fmt.Print(cli("cli.passwordRepeat"))
	second, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", msgs.Errorf("cli.passwordMismatch")
	}
	return string(first), nil
}

// writeHubExport writes the host registry (with the master key embedded —
// see Manager.ExportHosts) to path, encrypted under password when one is
// given (see secretbox.EncryptWithPassword) — and, either way, prints the
// same reminder about how sensitive the file is: encrypted, because a weak
// or reused password is still a real risk; unencrypted, because there is
// nothing else standing between this file and whoever finds it.
func writeHubExport(ctx context.Context, manager *hub.Manager, path, password string) error {
	export, err := manager.ExportHosts(ctx, true)
	if err != nil {
		return msgs.Errorf("cli.export.prepare", err)
	}
	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return msgs.Errorf("cli.export.marshal", err)
	}

	encrypted := password != ""
	if encrypted {
		data, err = secretbox.EncryptWithPassword(password, data)
		if err != nil {
			return msgs.Errorf("cli.export.encrypt", err)
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return msgs.Errorf("cli.export.write", path, err)
	}

	if encrypted {
		fmt.Print(cli("cli.export.savedEncrypted", path, len(export.Hosts), path))
		return nil
	}
	fmt.Print(cli("cli.export.savedPlain", path, len(export.Hosts)))
	return nil
}

// confirmHubDelete requires the operator to type out a specific word,
// deliberately more friction than a bare y/N — every other destructive
// action in this project (window.confirm in the web UI) is at most one
// click to undo-by-not-clicking; this one has nothing to undo at all once
// it runs.
func confirmHubDelete(cfg *config.Config, stdin *bufio.Reader) (bool, error) {
	fmt.Print(cli("cli.delete.confirm", cfg.DataDir, cli("cli.delete.word")))
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	return strings.TrimSpace(line) == cli("cli.delete.word"), nil
}

// wipeHubData shreds only what can actually hold a secret, then removes
// everything else in DataDir with a plain (fast) recursive delete.
//
// An earlier version secretbox.SecureWipeDir'd the whole DataDir tree
// uniformly, including HubGoToolchainDir — the Go toolchain the hub
// self-installs when no working `go` is configured (see
// internal/hub/toolchain.go). That directory is nothing but an unpacked
// copy of the public Go distribution: thousands of small source/package
// files, none of them secret, and the resulting open+write+fsync+close+
// unlink cycle on every single one of them turned what should be an
// instant operation into one that could run for minutes — reported as the
// command "hanging" with no way to tell it was almost done versus stuck.
// HubBinCacheDir (this project's own cross-compiled binaries — also public,
// also potentially many files across several architectures) has the same
// problem for the same reason.
func wipeHubData(cfg *config.Config) error {
	dbPath := cfg.DBPath()
	shred := []string{
		cfg.HubKeyFile(),
		dbPath,
		dbPath + "-wal",
		dbPath + "-shm",
		dbPath + "-journal",
		// The self-signed TLS private key (see internal/tlscert) — small,
		// but it IS key material, unlike everything wiped via RemoveAll below.
		filepath.Join(cfg.DataDir, "tls"),
		// Every version of every config edit this hub's own local scanner
		// (the "localhost" self-monitoring entry — see internal/hub's
		// runHub) has made — can hold sensitive config content (e.g. an
		// inline haproxy stats password) even though it isn't encryption
		// key material.
		cfg.HistoryDir(),
	}
	for _, path := range shred {
		if err := secretbox.SecureWipeDir(path); err != nil {
			return msgs.Errorf("cli.delete.shred", path, err)
		}
	}

	// Public, non-secret, and — for the Go toolchain especially — made of
	// enough small files that shredding them one by one is what made this
	// command feel hung. A plain recursive delete is the entire point.
	for _, path := range []string{cfg.HubBinCacheDir(), cfg.HubGoToolchainDir()} {
		if err := os.RemoveAll(path); err != nil {
			return msgs.Errorf("cli.delete.remove", path, err)
		}
	}

	// Whatever's left is empty directories plus anything not explicitly
	// listed above — removed outright as a catch-all, not shredded, since
	// everything actually known to be sensitive was already handled above.
	return os.RemoveAll(cfg.DataDir)
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
