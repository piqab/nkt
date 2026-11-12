package vmimage

import (
	"regexp"
	"strings"
	"testing"
)

// Файл сумм зеркала — источник имён: переименование образа на зеркале
// (Debian: genericcloud → generic) не должно ломать скачивание, пока имя
// подходит под один из шаблонов записи.
func TestResolveFromSumsPatterns(t *testing.T) {
	const sums = "aaa111  debian-13-generic-amd64.json\n" +
		"bbb222  debian-13-generic-amd64.qcow2\n" +
		"ccc333  debian-13-genericcloud-amd64.qcow2\n" +
		"ddd444  debian-13-generic-arm64.qcow2\n"
	img := Image{
		ID: "debian-13", Name: "Debian 13 (trixie)", FileName: "debian-13-generic-amd64.qcow2",
		FilePatterns: []string{`^debian-13-generic-amd64\.qcow2$`, `^debian-13-genericcloud-amd64\.qcow2$`},
	}
	name, sum, err := resolveFromSums(sums, img)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if name != "debian-13-generic-amd64.qcow2" || sum != "bbb222" {
		t.Errorf("первый шаблон: %s %s", name, sum)
	}

	// Зеркало оставило только старое имя — работает запасной шаблон.
	const old = "ccc333  debian-13-genericcloud-amd64.qcow2\n"
	name, sum, err = resolveFromSums(old, img)
	if err != nil {
		t.Fatalf("resolve old name: %v", err)
	}
	if name != "debian-13-genericcloud-amd64.qcow2" || sum != "ccc333" {
		t.Errorf("запасной шаблон: %s %s", name, sum)
	}

	// Ни одного подходящего имени — внятный отказ, а не пустая сумма.
	if _, _, err := resolveFromSums("eee555  debian-13-nocloud-amd64.qcow2\n", img); err == nil {
		t.Error("без подходящего имени ждал отказ")
	} else if !strings.Contains(err.Error(), "Debian 13 (trixie)") {
		t.Errorf("в отказе нет имени образа: %v", err)
	}
}

// Без шаблонов имя берётся точно, как у своих образов.
func TestResolveFromSumsExact(t *testing.T) {
	img := Image{ID: "custom:x", Name: "x", FileName: "my.qcow2"}
	sum, err := parseChecksums("abc  *my.qcow2\n", img.FileName)
	if err != nil || sum != "abc" {
		t.Fatalf("точное имя: %q %v", sum, err)
	}
	if _, _, err := resolveFromSums("abc  other.qcow2\n", img); err == nil {
		t.Error("точное имя не должно совпадать по подстроке")
	}
}

// Ссылка на образ собирается из каталога файла сумм плюс разрешённое имя.
func TestSumsDir(t *testing.T) {
	base, err := sumsDir("https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS")
	if err != nil {
		t.Fatal(err)
	}
	if base != "https://cloud.debian.org/images/cloud/trixie/latest/" {
		t.Errorf("каталог сумм: %q", base)
	}
	if got := base + "debian-13-generic-amd64.qcow2"; got != "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.qcow2" {
		t.Errorf("ссылка: %q", got)
	}
}

// У каждой записи каталога имя в ссылке совпадает с FileName, шаблоны
// компилируются, и первый шаблон подходит к FileName: рассинхрон имени и
// ссылки ловится здесь, а не у человека при скачивании.
func TestCatalogSelfConsistent(t *testing.T) {
	for _, img := range Catalog {
		if !strings.HasSuffix(img.URL, "/"+img.FileName) {
			t.Errorf("%s: URL %q не заканчивается именем %q", img.ID, img.URL, img.FileName)
		}
		for _, pat := range img.FilePatterns {
			re, err := regexp.Compile(pat)
			if err != nil {
				t.Errorf("%s: шаблон %q: %v", img.ID, pat, err)
				continue
			}
			if pat == img.FilePatterns[0] && !re.MatchString(img.FileName) {
				t.Errorf("%s: первый шаблон не подходит к FileName %q", img.ID, img.FileName)
			}
		}
	}
}
