package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// minPasswordLength matches what the HTTP API enforces.
const minPasswordLength = 10

// listUsers prints the accounts that can sign in to the web dashboard.
func (r *accountsRuntime) listUsers(ctx context.Context) error {
	users, err := r.db.ListUsers(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		fmt.Println(cli("cli.users.none"))
		return nil
	}

	fmt.Printf("%-20s %-8s %-10s %-22s %s\n", cli("cli.users.colLogin"), cli("cli.users.colRole"), cli("cli.users.colState"), cli("cli.users.colCreated"), cli("cli.users.colLast"))
	for _, u := range users {
		state := cli("cli.users.active")
		if u.Disabled {
			state = cli("cli.users.disabled")
		}
		last := u.LastLoginAt
		if last == "" {
			last = cli("cli.users.never")
		}
		fmt.Printf("%-20s %-8s %-10s %-22s %s\n", u.Username, u.Role, state, u.CreatedAt, last)
	}
	return nil
}

// setPassword changes an account's password from the host, which is the way out
// when the printed password was lost. Creating the account is offered only when
// a role is given explicitly, so a typo in the login does not silently make a
// second administrator.
func (r *accountsRuntime) setPassword(ctx context.Context, username, role string, generate bool) error {
	if username == "" {
		username = r.cfg.BootstrapAdminUser
	}
	// Сгенерированный пароль показывается только человеку у терминала:
	// при выводе в файл, CI или журнал он осел бы там открытым текстом.
	// Проверка — до смены пароля, чтобы отказ ничего не менял.
	if generate && !term.IsTerminal(int(os.Stdout.Fd())) {
		return msgs.Errorf("cli.passwd.randomNeedsTerminal")
	}

	_, err := r.db.UserByName(ctx, username)
	switch {
	case errors.Is(err, store.ErrNotFound) && role == "":
		return msgs.Errorf("cli.passwd.noAccount", username, username)
	case errors.Is(err, store.ErrNotFound):
		if role != store.RoleAdmin && role != store.RoleViewer {
			return msgs.Errorf("cli.passwd.badRole", role)
		}
	case err != nil:
		return err
	}
	creating := errors.Is(err, store.ErrNotFound)

	password := ""
	if generate {
		if password, err = auth.GeneratePassword(); err != nil {
			return err
		}
	} else if password, err = promptPassword(); err != nil {
		return err
	}
	// Count characters, not bytes: five Cyrillic letters are ten bytes in UTF-8
	// and would otherwise sail past a byte-based minimum.
	if utf8.RuneCountInString(password) < minPasswordLength {
		return msgs.Errorf("cli.passwd.tooShort", minPasswordLength)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	action := "auth.password.cli"
	if creating {
		if _, err := r.db.CreateUser(ctx, username, hash, role); err != nil {
			return msgs.Errorf("cli.passwd.createFailed", err)
		}
		action = "user.create.cli"
		fmt.Print(cli("cli.passwd.created", username, role))
	} else {
		// Existing sessions are dropped by SetPasswordHash: a password reset
		// should log out whoever was using the old one.
		if err := r.db.SetPasswordHash(ctx, username, hash); err != nil {
			return msgs.Errorf("cli.passwd.setFailed", err)
		}
		if role != "" {
			if err := r.db.SetUserRole(ctx, username, role); err != nil {
				return msgs.Errorf("cli.passwd.roleFailed", err)
			}
			fmt.Print(cli("cli.passwd.roleChanged", username, role))
		}
		fmt.Print(cli("cli.passwd.changed", username))
	}

	if generate {
		// Прямо в терминал (проверен выше), не через логирующий вывод.
		_, _ = os.Stdout.WriteString(cli("cli.passwd.generated", username, password))
	}

	r.db.Audit(ctx, cliActor(), action, username, "ok",
		map[string]any{"generated": generate, "role": role})
	return nil
}

// promptPassword reads a password twice without echoing it.
func promptPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// Piped input: read one line, so the command stays scriptable.
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		// PowerShell prefixes piped text with a UTF-8 byte order mark. Left in
		// place it becomes an invisible first character of the password, and the
		// resulting account cannot be logged into by anyone.
		utf8BOM := string(rune(0xFEFF))
		return strings.TrimRight(strings.TrimPrefix(line, utf8BOM), "\r\n"), nil
	}

	fmt.Print(cli("cli.passwd.prompt"))
	first, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
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

// cliActor names the operator in the audit log the same way the terminal
// interface does.
func cliActor() string {
	name := os.Getenv("SUDO_USER")
	if name == "" {
		name = os.Getenv("USER")
	}
	if name == "" {
		name = os.Getenv("USERNAME")
	}
	if name == "" {
		name = "unknown"
	}
	return "cli:" + name
}
