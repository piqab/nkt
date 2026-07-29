package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/store"
)

// minPasswordLength matches what the HTTP API enforces.
const minPasswordLength = 10

// listUsers prints the accounts that can sign in to the web dashboard.
func (r *runtime) listUsers(ctx context.Context) error {
	users, err := r.db.ListUsers(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		fmt.Println("Учётных записей нет. Запустите nkt — администратор создастся при первом старте.")
		return nil
	}

	fmt.Printf("%-20s %-8s %-10s %-22s %s\n", "ЛОГИН", "РОЛЬ", "СОСТОЯНИЕ", "СОЗДАН", "ПОСЛЕДНИЙ ВХОД")
	for _, u := range users {
		state := "активна"
		if u.Disabled {
			state = "отключена"
		}
		last := u.LastLoginAt
		if last == "" {
			last = "не входил"
		}
		fmt.Printf("%-20s %-8s %-10s %-22s %s\n", u.Username, u.Role, state, u.CreatedAt, last)
	}
	return nil
}

// setPassword changes an account's password from the host, which is the way out
// when the printed password was lost. Creating the account is offered only when
// a role is given explicitly, so a typo in the login does not silently make a
// second administrator.
func (r *runtime) setPassword(ctx context.Context, username, role string, generate bool) error {
	if username == "" {
		username = r.cfg.BootstrapAdminUser
	}

	_, err := r.db.UserByName(ctx, username)
	switch {
	case errors.Is(err, store.ErrNotFound) && role == "":
		return fmt.Errorf(
			"учётной записи %q нет. Существующие: nkt users. "+
				"Чтобы создать новую, укажите роль: nkt passwd %s -role admin",
			username, username)
	case errors.Is(err, store.ErrNotFound):
		if role != store.RoleAdmin && role != store.RoleViewer {
			return fmt.Errorf("роль должна быть admin или viewer, получено %q", role)
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
		return fmt.Errorf("пароль должен быть не короче %d символов", minPasswordLength)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	action := "auth.password.cli"
	if creating {
		if _, err := r.db.CreateUser(ctx, username, hash, role); err != nil {
			return fmt.Errorf("создание учётной записи: %w", err)
		}
		action = "user.create.cli"
		fmt.Printf("Создана учётная запись %s с ролью %s.\n", username, role)
	} else {
		// Existing sessions are dropped by SetPasswordHash: a password reset
		// should log out whoever was using the old one.
		if err := r.db.SetPasswordHash(ctx, username, hash); err != nil {
			return fmt.Errorf("смена пароля: %w", err)
		}
		if role != "" {
			if err := r.db.SetUserRole(ctx, username, role); err != nil {
				return fmt.Errorf("смена роли: %w", err)
			}
			fmt.Printf("Роль %s изменена на %s.\n", username, role)
		}
		fmt.Printf("Пароль %s изменён, все его сессии завершены.\n", username)
	}

	if generate {
		fmt.Printf("\n  логин:  %s\n  пароль: %s\n\n  Сохраните пароль: он больше нигде не отображается.\n",
			username, password)
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

	fmt.Print("Новый пароль: ")
	first, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	fmt.Print("Повторите пароль: ")
	second, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("пароли не совпадают")
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
