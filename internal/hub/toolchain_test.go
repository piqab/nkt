package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// Имя записи архива приводится к пути внутри корня: абсолютное и «..»
// наружу отвергаются, обычное остаётся относительным.
func TestArchiveEntryName(t *testing.T) {
	for name, want := range map[string]string{
		"go/bin/go":        "go/bin/go",
		"./go/bin/go":      "go/bin/go",
		"/etc/passwd":      "etc/passwd", // ведущий «/» срезается, наружу не ведёт
		"../../etc/shadow": "etc/shadow",
	} {
		got, err := archiveEntryName(name)
		if err != nil || got != want {
			t.Errorf("archiveEntryName(%q) = %q, %v; ждали %q", name, got, err, want)
		}
	}
	for _, bad := range []string{"", ".", "/", "../.."} {
		if got, err := archiveEntryName(bad); err == nil {
			t.Errorf("archiveEntryName(%q) = %q, ждали отказ", bad, got)
		}
	}
}

// tarGz собирает архив из записей для теста распаковки.
type entry struct {
	name     string
	typeflag byte
	linkname string
	body     string
}

func tarGz(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Linkname: e.linkname, Mode: 0o755, Size: int64(len(e.body))}
		if e.typeflag == tar.TypeDir {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Обычный архив распаковывается как есть, вместе с symlink внутри.
func TestExtractTarGzNormal(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "toolchain")
	arc := tarGz(t, []entry{
		{name: "go/", typeflag: tar.TypeDir},
		{name: "go/bin/go", typeflag: tar.TypeReg, body: "binary"},
		{name: "go/bin/gofmt", typeflag: tar.TypeSymlink, linkname: "go"},
	})
	if err := extractTarGz(bytes.NewReader(arc), dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "go/bin/go")); err != nil || string(data) != "binary" {
		t.Errorf("файл архива = %q, %v", data, err)
	}
	if target, err := os.Readlink(filepath.Join(dest, "go/bin/gofmt")); err != nil || target != "go" {
		t.Errorf("ссылка = %q, %v", target, err)
	}
}

// Двухшаговый обход: сначала symlink «внутрь» на каталог снаружи, потом
// запись через него. Лексическая проверка имени такое пропускала —
// os.Root нет.
func TestExtractTarGzSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	dest := filepath.Join(t.TempDir(), "toolchain")
	arc := tarGz(t, []entry{
		{name: "go/", typeflag: tar.TypeDir},
		{name: "go/esc", typeflag: tar.TypeSymlink, linkname: outside},
		{name: "go/esc/pwned", typeflag: tar.TypeReg, body: "owned"},
	})
	if err := extractTarGz(bytes.NewReader(arc), dest); err == nil {
		t.Fatal("запись через symlink наружу прошла")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); err == nil {
		t.Fatalf("файл записан за пределами каталога распаковки: %s/pwned", outside)
	}
}

// Запись с «..» в имени наружу не выходит: она попадает внутрь корня.
func TestExtractTarGzDotDotName(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "toolchain")
	arc := tarGz(t, []entry{{name: "../../escaped.txt", typeflag: tar.TypeReg, body: "x"}})
	if err := extractTarGz(bytes.NewReader(arc), dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.txt")); err == nil {
		t.Error("файл записан выше каталога распаковки")
	}
	if _, err := os.Stat(filepath.Join(dest, "escaped.txt")); err != nil {
		t.Errorf("запись не попала внутрь корня: %v", err)
	}
}
