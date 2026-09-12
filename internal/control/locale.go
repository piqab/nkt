package control

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"regexp"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/collect"
)

// Локали. Поменять LANG — мало: локаль должна быть сгенерирована (locale
// -a), иначе каждая программа жалуется «setlocale: No such file or
// directory». Поэтому раздел показывает все известные системе локали из
// /usr/share/i18n/SUPPORTED, отмечает сгенерированные и текущую, и умеет
// и сгенерировать (locale.gen + locale-gen), и назначить основной
// (update-locale или localectl).

// WithEscape даёт менеджеру выход из песочницы: locale-gen пишет в
// /usr/lib/locale, update-locale — в /etc, а юниту туда нельзя.
func (m *SysConfigManager) WithEscape(run PrivilegedRunner) *SysConfigManager {
	m.escape = run
	return m
}

// priv выполняет команду снаружи песочницы, если выход есть; иначе как
// обычно — в fixtures-режиме так и должно быть.
func (m *SysConfigManager) priv(ctx context.Context, argv ...string) (collect.CommandResult, error) {
	if m.escape != nil {
		return m.escape(ctx, argv...)
	}
	return m.c.Run(ctx, argv[0], argv[1:]...)
}

// LocaleInfo — одна локаль.
type LocaleInfo struct {
	Name    string `json:"name"`
	Charset string `json:"charset,omitempty"`
	// Installed — сгенерирована, её можно назначить основной.
	Installed bool `json:"installed"`
	Current   bool `json:"current"`
}

// Locales — все известные системе локали.
type Locales struct {
	Locales []LocaleInfo `json:"locales"`
	Current string       `json:"current,omitempty"`
	// CanGenerate — есть locale-gen (пакет locales); без него новые
	// локали не собрать, только выбирать из уже сгенерированных.
	CanGenerate bool   `json:"can_generate"`
	Note        string `json:"note,omitempty"`
}

const (
	localeSupported = "/usr/share/i18n/SUPPORTED"
	localeGenFile   = "/etc/locale.gen"
)

var localeNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_@.+-]{0,63}$`)

// normLocale приводит имя к виду, в котором его печатает locale -a:
// ru_RU.UTF-8 и ru_RU.utf8 — одна локаль.
func normLocale(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "-", ""))
}

// parseSupported разбирает /usr/share/i18n/SUPPORTED: «имя кодировка».
func parseSupported(raw string) []LocaleInfo {
	var out []LocaleInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, charset, _ := strings.Cut(line, " ")
		out = append(out, LocaleInfo{Name: name, Charset: strings.TrimSpace(charset)})
	}
	return out
}

// Locales собирает список.
func (m *SysConfigManager) Locales(ctx context.Context) Locales {
	out := Locales{Locales: []LocaleInfo{}}
	current := m.Read(ctx).Locale
	out.Current = current

	installed := map[string]bool{}
	if res, err := m.c.Run(ctx, "locale", "-a"); err == nil && res.ExitCode == 0 {
		for _, l := range nonEmptyLines(res.Stdout) {
			installed[normLocale(l)] = true
		}
	}

	seen := map[string]bool{}
	if raw, err := m.c.ReadFile(localeSupported); err == nil {
		for _, l := range parseSupported(string(raw)) {
			l.Installed = installed[normLocale(l.Name)]
			l.Current = current != "" && normLocale(l.Name) == normLocale(current)
			out.Locales = append(out.Locales, l)
			seen[normLocale(l.Name)] = true
		}
		out.CanGenerate = m.c.Exists("/usr/sbin/locale-gen") || m.c.Exists("/usr/bin/locale-gen")
	} else {
		out.Note = msgs.Tc(ctx, "control.localesPackageMissingNote")
	}
	// То, что есть в locale -a, но не в SUPPORTED: C.UTF-8 и ему подобные.
	// Их тоже можно назначить основной.
	for norm := range installed {
		if seen[norm] || norm == "c" || norm == "posix" {
			continue
		}
		name := norm
		if strings.HasSuffix(norm, ".utf8") {
			name = strings.TrimSuffix(norm, ".utf8") + ".UTF-8"
			if name == "c.UTF-8" {
				name = "C.UTF-8"
			}
		}
		out.Locales = append(out.Locales, LocaleInfo{Name: name, Installed: true,
			Current: current != "" && norm == normLocale(current)})
	}
	sort.Slice(out.Locales, func(i, j int) bool {
		a, b := out.Locales[i], out.Locales[j]
		if a.Installed != b.Installed {
			return a.Installed
		}
		return a.Name < b.Name
	})
	return out
}

// enableInLocaleGen раскомментирует (или добавляет) строку локали в
// /etc/locale.gen. Возвращает новый текст и признак, что что-то изменилось.
func enableInLocaleGen(raw string, l LocaleInfo) (string, bool) {
	want := l.Name
	if l.Charset != "" {
		want += " " + l.Charset
	}
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if t == want {
			if strings.TrimSpace(line) == want {
				return raw, false
			}
			lines[i] = want
			return strings.Join(lines, "\n"), true
		}
	}
	if !strings.HasSuffix(raw, "\n") && raw != "" {
		raw += "\n"
	}
	return raw + want + "\n", true
}

// GenerateLocales собирает указанные локали.
func (m *SysConfigManager) GenerateLocales(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	raw, err := m.c.ReadFile(localeSupported)
	if err != nil {
		return msgs.Errorf("control.localesPackageInstalledNothingGenerate")
	}
	known := map[string]LocaleInfo{}
	for _, l := range parseSupported(string(raw)) {
		known[l.Name] = l
	}
	var picked []LocaleInfo
	for _, n := range names {
		l, ok := known[n]
		if !ok || !localeNameRe.MatchString(n) {
			return msgs.Errorf("control.unknownLocale", n)
		}
		picked = append(picked, l)
	}
	if gen, err := m.c.ReadFile(localeGenFile); err == nil {
		text, changed := string(gen), false
		for _, l := range picked {
			var c bool
			text, c = enableInLocaleGen(text, l)
			changed = changed || c
		}
		if changed {
			if err := m.c.WriteFile(localeGenFile, []byte(text), 0o644); err != nil {
				return fmt.Errorf("%s: %w", localeGenFile, err)
			}
		}
		res, err := m.priv(ctx, "locale-gen")
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("locale-gen: %s", strings.TrimSpace(res.Output()))
		}
		return nil
	}
	// Без /etc/locale.gen (не Debian) — localedef напрямую.
	for _, l := range picked {
		input, _, _ := strings.Cut(l.Name, ".")
		res, err := m.priv(ctx, "localedef", "-i", input, "-f", l.Charset, l.Name)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("localedef %s: %s", l.Name, strings.TrimSpace(res.Output()))
		}
	}
	return nil
}

// SetLocale назначает основную локаль системы (LANG). Локаль должна быть
// сгенерирована.
func (m *SysConfigManager) SetLocale(ctx context.Context, name string) error {
	if !localeNameRe.MatchString(name) {
		return msgs.Errorf("control.invalidLocaleName", name)
	}
	if res, err := m.c.Run(ctx, "locale", "-a"); err == nil && res.ExitCode == 0 {
		found := false
		for _, l := range nonEmptyLines(res.Stdout) {
			if normLocale(l) == normLocale(name) {
				found = true
				break
			}
		}
		if !found {
			return msgs.Errorf("control.localeGeneratedGenerateFirst", name)
		}
	}
	var res collect.CommandResult
	var err error
	if m.c.Exists("/usr/sbin/update-locale") {
		// Debian/Ubuntu: правит /etc/default/locale. LC_ALL сбрасывается:
		// заданный где-то раньше, он перекрыл бы новый LANG.
		res, err = m.priv(ctx, "update-locale", "LANG="+name, "LC_ALL=")
	} else {
		res, err = m.priv(ctx, "localectl", "set-locale", "LANG="+name)
	}
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: %s", res.Argv[0], strings.TrimSpace(res.Output()))
	}
	return nil
}
