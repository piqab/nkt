package collect

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// lockedDir — каталог, закрытый на запись, как /opt под ProtectSystem=strict,
// и выход из песочницы, который на время команды его открывает.
func lockedDir(t *testing.T) (string, *Local) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("под root права каталога не мешают записи — случай не воспроизвести")
	}
	dir := filepath.Join(t.TempDir(), "opt")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	l := NewLocal("", "", 10*time.Second)
	l.SetEscape(func(ctx context.Context, _ []byte, argv ...string) (CommandResult, error) {
		// Снаружи песочницы всё дерево доступно на запись.
		_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
		defer func() { _ = os.Chmod(dir, 0o500) }()
		out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
		res := CommandResult{Argv: argv, Stderr: string(out)}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, err
	})
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && fi.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	return dir, l
}

// Плагин хаба на хосте с агентом-службой: /opt/nkt-plugins/<имя> создаётся
// и удаляется вне песочницы (было «mkdir …: read-only file system»).
func TestLocalMkdirRemoveAllEscape(t *testing.T) {
	dir, l := lockedDir(t)
	plugin := filepath.Join(dir, "nkt-plugins", "win-11-ii")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := l.Mkdir(plugin); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := l.WriteFile(plugin+"/nkt-plugin.py", []byte("print(1)\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Закрыт и каталог плагинов: удалить win-11-ii изнутри нельзя.
	if err := os.Chmod(filepath.Dir(plugin), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveAll(plugin); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Fatalf("каталог остался: %v", err)
	}
	if err := l.RemoveAll(plugin); err != nil {
		t.Fatalf("повторное удаление несуществующего: %v", err)
	}
}

// Без выхода из песочницы — прежний честный отказ.
func TestLocalMkdirWithoutEscapeFails(t *testing.T) {
	dir, _ := lockedDir(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := NewLocal("", "", 0).Mkdir(filepath.Join(dir, "x")); err == nil {
		t.Fatal("Mkdir: ошибки нет, хотя каталог закрыт")
	}
}

// RemoveAll не удаляет по относительному пути, пути с «..» и выше второго уровня.
func TestLocalRemoveAllRejectsBadPaths(t *testing.T) {
	l := &Local{}
	for _, p := range []string{"", "/", "/opt", "opt/nkt", "/opt/../etc", "/opt//x", "/opt/x/"} {
		if err := l.RemoveAll(p); err == nil {
			t.Errorf("принят %q", p)
		}
	}
}
