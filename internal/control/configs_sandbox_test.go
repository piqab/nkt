package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Открытые каталоги накапливаются: второй файл в другом каталоге не должен
// закрывать первый, а повторное открытие того же — приводить к перезапуску
// службы впустую.
func TestMergeWritablePaths(t *testing.T) {
	first, changed := mergeWritablePaths("", "/etc/fail2ban/action.d")
	if !changed {
		t.Fatal("первый каталог не отмечен как изменение")
	}
	if !strings.Contains(first, "ReadWritePaths=-/etc/fail2ban/action.d") {
		t.Fatalf("drop-in без нужного пути:\n%s", first)
	}
	if !strings.Contains(first, "[Service]") {
		t.Errorf("drop-in без секции [Service]:\n%s", first)
	}

	second, changed := mergeWritablePaths(first, "/etc/logrotate.d")
	if !changed {
		t.Fatal("второй каталог не отмечен как изменение")
	}
	for _, want := range []string{"/etc/fail2ban/action.d", "/etc/logrotate.d"} {
		if !strings.Contains(second, "ReadWritePaths=-"+want) {
			t.Errorf("после второго открытия потерян %s:\n%s", want, second)
		}
	}

	again, changed := mergeWritablePaths(second, "/etc/logrotate.d")
	if changed {
		t.Error("повторное открытие того же каталога выдано за изменение")
	}
	if again != second {
		t.Errorf("файл переписан без нужды:\n%s", again)
	}

	// Путь, записанный без ведущего «-», — тот же путь.
	plain, changed := mergeWritablePaths("[Service]\nReadWritePaths=/etc/logrotate.d\n", "/etc/logrotate.d")
	if changed {
		t.Errorf("путь без ведущего дефиса не распознан как уже открытый:\n%s", plain)
	}
}

// AllowWrite пишет drop-in для каталога того файла, который правят, и
// повторное открытие того же каталога не выдаёт себя за изменение — иначе
// каждое нажатие кнопки перезапускало бы службу впустую.
func TestAllowWriteWritesDropIn(t *testing.T) {
	m := categoriesSetup(t)

	dropIn, changed, err := m.AllowWrite("/etc/ssh/sshd_config")
	if err != nil {
		t.Fatalf("AllowWrite: %v", err)
	}
	if !changed {
		t.Fatal("первое открытие каталога не отмечено как изменение")
	}
	if dropIn != WritablePathsDropIn {
		t.Errorf("drop-in = %q, want %q", dropIn, WritablePathsDropIn)
	}

	raw, err := os.ReadFile(filepath.Join(m.cfg.FixturesRoot, WritablePathsDropIn))
	if err != nil {
		t.Fatalf("drop-in не записан: %v", err)
	}
	if !strings.Contains(string(raw), "ReadWritePaths=-/etc/ssh") {
		t.Errorf("в drop-in нет каталога файла:\n%s", raw)
	}

	if _, changed, err = m.AllowWrite("/etc/ssh/sshd_config"); err != nil {
		t.Fatalf("повторный AllowWrite: %v", err)
	} else if changed {
		t.Error("повторное открытие того же каталога выдано за изменение")
	}

	// Путь вне разрешённых каталогов не должен открывать ничего: иначе
	// кнопкой из интерфейса можно было бы отдать юниту произвольный
	// каталог.
	if _, _, err := m.AllowWrite("/etc/shadow"); err == nil {
		t.Error("AllowWrite принял путь вне разрешённых каталогов")
	}
}
