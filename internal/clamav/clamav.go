// Package clamav — сигнатурный поиск вредоносного через ClamAV: состояние
// установки и базы, разбор вывода clamscan, скрипты установки и
// обновления, карантин. Сами команды выполняет вызывающий (API) — вне
// песочницы юнита и с потоковым журналом; здесь только то, что можно
// проверить без хоста.
package clamav

import (
	"context"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// DefaultPaths — что сканировать на хосте по умолчанию: где лежит чужой
// и загруженный код, а не системные каталоги (их проверяет dpkg).
var DefaultPaths = []string{"/home", "/root", "/srv", "/opt", "/var/www", "/tmp", "/var/tmp", "/dev/shm"}

// InstallScript ставит clamav и службу обновления сигнатур; первое
// скачивание базы (~250 МБ) делает сама служба freshclam после запуска.
const InstallScript = "set -e\nexport DEBIAN_FRONTEND=noninteractive\napt-get update\napt-get install -y clamav clamav-freshclam\nsystemctl enable --now clamav-freshclam || true"

// UpdateScript обновляет базу сейчас: пока работает служба freshclam,
// ручной запуск не пускает её лог, поэтому она на время останавливается.
const UpdateScript = "set -e\nsystemctl stop clamav-freshclam 2>/dev/null || true\nfreshclam --stdout\nsystemctl start clamav-freshclam 2>/dev/null || true"

var (
	// «ClamAV 1.0.7/27523/Sun Feb  2 09:00:00 2025»
	reVersion = regexp.MustCompile(`ClamAV\s+(\S+?)(?:/(\d+)/(.+))?$`)
	reFound   = regexp.MustCompile(`^(.*): (\S+) FOUND$`)
	reCount   = regexp.MustCompile(`^(Scanned files|Infected files|Known viruses):\s+(\d+)`)
)

// Status — установлен ли ClamAV, какая версия и база.
func Status(ctx context.Context, c collect.Collector) model.ClamStatus {
	st := model.ClamStatus{}
	if !collect.Which(ctx, c, "clamscan") {
		return st
	}
	st.Installed = true
	if out, err := c.Run(ctx, "clamscan", "--version"); err == nil && out.OK() {
		if m := reVersion.FindStringSubmatch(strings.TrimSpace(out.Stdout)); m != nil {
			st.Version = m[1]
			st.DBVersion = m[2]
			if t, err := time.Parse("Mon Jan _2 15:04:05 2006", strings.TrimSpace(m[3])); err == nil {
				st.DBDate = t.UTC().Format(time.RFC3339)
			}
		}
	}
	if out, err := c.Run(ctx, "systemctl", "is-active", "clamav-freshclam"); err == nil {
		st.FreshclamActive = strings.TrimSpace(out.Stdout) == "active"
	}
	// База ещё не скачана: clamscan --version без сигнатур печатает
	// только версию, а каталог /var/lib/clamav пуст.
	if st.DBVersion == "" {
		if m, err := c.Glob("/var/lib/clamav/*.c[lv]d"); err == nil && len(m) > 0 {
			st.DBPresent = true
		}
	} else {
		st.DBPresent = true
	}
	return st
}

// ParseLine разбирает одну строку clamscan: заражённый файл, счётчик
// сводки или ничего.
func ParseLine(line string) (hit *model.ClamHit, counter string, n int) {
	line = strings.TrimRight(line, "\r")
	if m := reFound.FindStringSubmatch(line); m != nil {
		return &model.ClamHit{Path: m[1], Signature: m[2]}, "", 0
	}
	if m := reCount.FindStringSubmatch(line); m != nil {
		n, _ = strconv.Atoi(m[2])
		return nil, m[1], n
	}
	return nil, "", 0
}

// ScanArgs — аргументы clamscan для каталогов: рекурсивно, печатать
// только заражённые, не останавливаться на нечитаемых, не лезть в
// каталоги ядра и в чужие файловые системы (сетевые монтирования).
func ScanArgs(paths []string) []string {
	args := []string{"clamscan", "-r", "--infected", "--stdout", "--cross-fs=no",
		"--exclude-dir=^/proc", "--exclude-dir=^/sys", "--exclude-dir=^/dev", "--exclude-dir=^/run",
		"--max-filesize=200M", "--max-scansize=400M"}
	return append(args, paths...)
}

// ImageScanScript разворачивает образ в каталог и сканирует его: образ
// нельзя проверить «на месте» — у контейнера своя файловая система, а
// clamscan видит только хостовую. docker export отдаёт слои уже
// сложенными, как их видит контейнер.
func ImageScanScript(tool, ref string) string {
	q := shellQuote(ref)
	return "set -e\n" +
		"d=$(mktemp -d /var/tmp/nkt-clam-XXXXXX)\n" +
		"trap 'rm -rf \"$d\"; [ -n \"$cid\" ] && " + tool + " rm -f \"$cid\" >/dev/null 2>&1 || true' EXIT\n" +
		"cid=$(" + tool + " create " + q + ")\n" +
		tool + " export \"$cid\" | tar -xf - -C \"$d\" 2>/dev/null || true\n" +
		strings.Join(ScanArgs([]string{"\"$d\""}), " ") + " | sed \"s#^$d##\"\n"
}

// QuarantineName — имя файла в карантине: время и исходное имя, чтобы
// два одинаковых имени из разных каталогов не затирали друг друга.
func QuarantineName(p string, now time.Time) string {
	return now.UTC().Format("20060102-150405") + "_" + path.Base(p)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ValidPath — путь для сканирования или карантина: абсолютный, без
// «..» и не корень.
func ValidPath(p string) bool {
	if !strings.HasPrefix(p, "/") || p == "/" || strings.Contains(p, "..") || strings.ContainsAny(p, "\n\x00") {
		return false
	}
	return true
}
