package collect

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A freshly generated file (a self-signed certificate, for instance) needs its
// directory created on first write; editing an existing config never hit this
// because the directory was always already there.
func TestLocalWriteFileCreatesParentDirectory(t *testing.T) {
	root := t.TempDir()
	l := NewLocal("/var/run/docker.sock", "/run/podman/podman.sock", 0)

	target := filepath.Join(root, "nested", "deeper", "file.pem")
	if err := l.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("запись в несуществующий каталог: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("чтение записанного файла: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("содержимое = %q, ожидалось hello", got)
	}
}

// A write into an existing directory must keep working exactly as before.
func TestLocalWriteFileExistingDirectory(t *testing.T) {
	root := t.TempDir()
	l := NewLocal("/var/run/docker.sock", "/run/podman/podman.sock", 0)

	target := filepath.Join(root, "file.txt")
	if err := l.WriteFile(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("первая запись: %v", err)
	}
	if err := l.WriteFile(target, []byte("second"), 0o644); err != nil {
		t.Fatalf("повторная запись: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "second" {
		t.Fatalf("содержимое = %q, err=%v, ожидалось second", got, err)
	}
}

// Каталог, закрытый на запись, — то же, что даёт ProtectSystem=strict на
// живом хосте: ни временного файла рядом, ни записи на месте. Тогда должен
// сработать выход из песочницы, и файл всё равно записаться — не добавляя
// каталог в ReadWritePaths юнита.
func TestLocalWriteFileFallsBackToEscape(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("под root права каталога не мешают записи — случай не воспроизвести")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "etc")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	target := filepath.Join(dir, "app.conf")

	var gotStdin []byte
	var gotArgv []string
	l := NewLocal("/var/run/docker.sock", "/run/podman/podman.sock", 10*time.Second)
	l.SetEscape(func(ctx context.Context, stdin []byte, argv ...string) (CommandResult, error) {
		gotStdin, gotArgv = stdin, argv
		// Снаружи песочницы каталог обычный: тут это изображается снятием
		// прав на время команды — самой песочницы в тесте нет.
		if err := os.Chmod(dir, 0o700); err != nil {
			return CommandResult{}, err
		}
		defer func() { _ = os.Chmod(dir, 0o500) }()

		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		res := CommandResult{Argv: argv, Stderr: string(out)}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, err
	})

	if err := l.WriteFile(target, []byte("listen 8080\n"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "listen 8080\n" {
		t.Errorf("содержимое = %q", got)
	}
	// Содержимое уходит на stdin, а не файлом: у юнита свой /tmp, и
	// промежуточный файл по ту сторону песочницы был бы не виден.
	if string(gotStdin) != "listen 8080\n" {
		t.Errorf("stdin = %q, want содержимое файла", gotStdin)
	}
	if len(gotArgv) == 0 || gotArgv[0] != "sh" {
		t.Errorf("argv = %q, want команду через sh", gotArgv)
	}
	if st, err := os.Stat(target); err != nil {
		t.Fatalf("Stat: %v", err)
	} else if st.Mode().Perm() != 0o640 {
		t.Errorf("права = %o, want 640", st.Mode().Perm())
	}
	if entries, err := os.ReadDir(dir); err != nil {
		t.Fatalf("ReadDir: %v", err)
	} else if len(entries) != 1 {
		t.Errorf("в каталоге %d файлов, ожидался только целевой", len(entries))
	}
}

// Без выхода из песочницы поведение прежнее: честный отказ, а не тишина.
func TestLocalWriteFileWithoutEscapeStillFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("под root права каталога не мешают записи — случай не воспроизвести")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "etc")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	l := NewLocal("/var/run/docker.sock", "/run/podman/podman.sock", 0)
	if err := l.WriteFile(filepath.Join(dir, "app.conf"), []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile: ошибки нет, хотя записать было некуда")
	}
}

// Разборщики этого приложения читают английский вывод: «State: running»,
// «active (running)», «Persistent: yes». На хосте с русской локалью те же
// команды отвечают по-русски, и разбор молча даёт пустоту — список машин
// приходил без состояний, и все они выглядели неактивными.
func TestLocalRunForcesCLocale(t *testing.T) {
	l := NewLocal("", "", 5*time.Second)
	res, err := l.Run(context.Background(), "sh", "-c", `printf '%s|%s' "$LC_ALL" "$LANG"`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "C|C" {
		t.Errorf("окружение команды = %q, а разбор рассчитан на английский вывод", res.Stdout)
	}
}
