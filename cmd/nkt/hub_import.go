package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/term"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/hub"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
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
		return fmt.Errorf("укажите файл экспорта: nkt hub import -file <файл>")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Mode != config.ModeHub {
		return fmt.Errorf("nkt hub import требует NKT_MODE=hub, сейчас %q", cfg.Mode)
	}

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	key, err := secretbox.ResolveKey(cfg.HubMasterKey, cfg.HubKeyFile())
	if err != nil {
		return fmt.Errorf("ключ шифрования секретов хаба: %w", err)
	}
	manager := hub.NewManager(cfg, db, key, version)

	data, err := os.ReadFile(opts.file)
	if err != nil {
		return fmt.Errorf("чтение %s: %w", opts.file, err)
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

	imported, errs := manager.ImportHosts(context.Background(), export)
	fmt.Printf("Импортировано хостов: %d из %d.\n", imported, len(export.Hosts))
	for _, e := range errs {
		fmt.Printf("  ошибка: %s\n", e)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d хостов не удалось импортировать", len(errs))
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
		return "", fmt.Errorf(
			"файл зашифрован паролем, а стандартный ввод не терминал — задайте NKT_HUB_EXPORT_PASSWORD")
	}
	fmt.Print("Файл зашифрован паролем. Пароль: ")
	pw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(pw), nil
}
