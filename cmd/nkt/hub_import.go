package main

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"log/slog"
	"os"
	"sort"
	"strings"

	"golang.org/x/term"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/hub"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// runHubImport implements `nkt hub import -file <path>`: the restore half
// of `nkt hub delete`'s export step (or of the web UI's own "экспорт с
// ключом"/"импорт" pair — this reads the same file format either one
// produces). Transparently handles a password-encrypted file
// (secretbox.EncryptWithPassword — see writeHubExport in hub_delete.go) as
// well as a plain one: decryption happens in memory, the plaintext JSON
// never touches disk.
func runHubImport(opts commandOptions, log *slog.Logger) error {
	if opts.file == "" {
		return msgs.Errorf("cli.import.noFile")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Mode != config.ModeHub {
		return msgs.Errorf("cli.needsHubMode", "nkt hub import", cfg.Mode)
	}

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	key, err := secretbox.ResolveKey(cfg.HubMasterKey, cfg.HubKeyFile())
	if err != nil {
		return msgs.Errorf("cli.hubKey", err)
	}
	manager := hub.NewManager(cfg, db, key, version, log)

	data, err := os.ReadFile(opts.file)
	if err != nil {
		return msgs.Errorf("cli.import.read", opts.file, err)
	}

	if secretbox.IsPasswordEncrypted(data) {
		password, err := readImportPassword()
		if err != nil {
			return err
		}
		data, err = secretbox.DecryptWithPassword(password, data)
		if err != nil {
			return err
		}
	}

	export, err := store.DecodeHubExport(data)
	if err != nil {
		return err
	}

	// Из командной строки совпадения по имени пропускаются: заменить
	// существующее можно в окне импорта хаба, там выбор поштучный.
	rep := manager.ImportHosts(msgs.WithLang(context.Background(), cliLang()), export, nil)
	sections := make([]string, 0, len(rep.Sections))
	for name := range rep.Sections {
		sections = append(sections, name)
	}
	sort.Strings(sections)
	for _, name := range sections {
		c := rep.Sections[name]
		fmt.Print(cli("cli.import.section", name, c.Added, c.Replaced, c.Skipped))
	}
	// Ошибки несут имена из файла импорта — без переводов строк, чтобы
	// чужой файл не подделал соседние строки вывода.
	oneLine := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")
	for _, e := range rep.Errors {
		fmt.Print(cli("cli.import.error", oneLine.Replace(e)))
	}
	if len(rep.Errors) > 0 {
		return msgs.Errorf("cli.import.errors", len(rep.Errors))
	}
	return nil
}

// readImportPassword prompts for the password an encrypted export was
// written with — NKT_HUB_EXPORT_PASSWORD first (same variable
// offerExport's non-interactive path in hub_delete.go reads, so a script
// that exported with it can restore with it too, unattended), a hidden
// terminal prompt otherwise.
func readImportPassword() (string, error) {
	if pw := os.Getenv("NKT_HUB_EXPORT_PASSWORD"); pw != "" {
		return pw, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", msgs.Errorf("cli.import.noTerminal")
	}
	fmt.Print(cli("cli.import.askPassword"))
	pw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(pw), nil
}
