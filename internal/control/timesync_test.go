package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// Стенд для локалей и времени: свой снимок с нужными файлами и
// заготовленными ответами команд, escape записывает команды в журнал.
func sysconfigFixture(t *testing.T, commands []map[string]any, files map[string]string) (*SysConfigManager, *[][]string) {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	idx, _ := json.Marshal(map[string]any{"commands": commands})
	if err := os.WriteFile(filepath.Join(root, ".commands", "index.json"), idx, 0o644); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	m := NewSysConfigManager(collect.NewFixtures(root)).WithEscape(
		func(_ context.Context, argv ...string) (collect.CommandResult, error) {
			calls = append(calls, argv)
			return collect.CommandResult{Argv: argv}, nil
		})
	return m, &calls
}

func cmd(match []string, stdout string) map[string]any {
	return map[string]any{"match": match, "stdout": stdout, "simulated": true}
}

func TestTimeSyncTimesyncd(t *testing.T) {
	m, calls := sysconfigFixture(t, []map[string]any{
		cmd([]string{"timedatectl", "show"}, "NTP=yes\nNTPSynchronized=yes\n"),
		cmd([]string{"dpkg-query", "-W", "-f=${Status}", "systemd-timesyncd"}, "install ok installed"),
		cmd([]string{"dpkg-query"}, "unknown ok not-installed"),
		cmd([]string{"timedatectl", "show-timesync", "--all"},
			"SystemNTPServers=\nServerName=time.google.com\nNTPMessage={ Leap=0, Stratum=1, Offset=+0.5ms, Delay=9ms }\n"),
	}, map[string]string{
		"etc/systemd/timesyncd.conf":            "[Time]\n#NTP=\n",
		"etc/systemd/timesyncd.conf.d/nkt.conf": "[Time]\nNTP=pool.ntp.org time.cloudflare.com\n",
	})
	st := m.TimeSync(context.Background())
	if st.Service != "systemd-timesyncd" || !st.Installed || !st.CanConfigure {
		t.Fatalf("служба: %+v", st)
	}
	if strings.Join(st.Servers, " ") != "pool.ntp.org time.cloudflare.com" || st.Server != "time.google.com" || st.Stratum != "1" || st.Offset != "+0.5ms" {
		t.Errorf("состояние: %+v", st)
	}

	if err := m.SetTimeSyncServers(context.Background(), []string{"ntp.ubuntu.com"}); err != nil {
		t.Fatalf("SetTimeSyncServers: %v", err)
	}
	st = m.TimeSync(context.Background())
	if strings.Join(st.Servers, " ") != "ntp.ubuntu.com" {
		t.Errorf("после записи серверы = %v", st.Servers)
	}
	last := (*calls)[len(*calls)-1]
	if strings.Join(last, " ") != "systemctl restart systemd-timesyncd" {
		t.Errorf("служба не перезапущена: %v", last)
	}
	if err := m.SetTimeSyncServers(context.Background(), []string{"bad host"}); err == nil {
		t.Error("имя с пробелом принято")
	}
}

func TestTimeSyncChronyAndMissing(t *testing.T) {
	m, _ := sysconfigFixture(t, []map[string]any{
		cmd([]string{"timedatectl", "show"}, "NTP=yes\nNTPSynchronized=no\n"),
		cmd([]string{"dpkg-query", "-W", "-f=${Status}", "chrony"}, "install ok installed"),
		cmd([]string{"dpkg-query"}, "unknown ok not-installed"),
		cmd([]string{"chronyc", "-n", "tracking"}, "Reference ID    : 0A000001 (10.0.0.1)\nStratum         : 3\nSystem time     : 0.002 seconds slow of NTP time\n"),
	}, map[string]string{
		"etc/chrony/chrony.conf": "pool 2.debian.pool.ntp.org iburst\nserver 10.0.0.1 iburst\n",
	})
	st := m.TimeSync(context.Background())
	if st.Service != "chrony" || strings.Join(st.Servers, " ") != "2.debian.pool.ntp.org 10.0.0.1" || st.Server != "10.0.0.1" || st.Stratum != "3" {
		t.Errorf("chrony: %+v", st)
	}

	none, _ := sysconfigFixture(t, []map[string]any{
		cmd([]string{"timedatectl", "show"}, "NTP=no\n"),
		cmd([]string{"dpkg-query"}, "unknown ok not-installed"),
	}, nil)
	st = none.TimeSync(context.Background())
	if st.Installed || st.InstallPackage != "systemd-timesyncd" || st.Note == "" {
		t.Errorf("без службы: %+v", st)
	}
	if err := none.SetTimeSyncServers(context.Background(), []string{"pool.ntp.org"}); err == nil {
		t.Error("запись серверов без службы прошла")
	}
}

func TestLocalesGenerateAndSet(t *testing.T) {
	m, calls := sysconfigFixture(t, []map[string]any{
		cmd([]string{"locale", "-a"}, "C\nC.utf8\nen_US.utf8\nPOSIX\n"),
		cmd([]string{"hostnamectl"}, "{}"),
		cmd([]string{"timedatectl"}, ""),
		cmd([]string{"dpkg-query"}, "unknown ok not-installed"),
	}, map[string]string{
		"usr/share/i18n/SUPPORTED": "en_US.UTF-8 UTF-8\nru_RU.UTF-8 UTF-8\nde_DE.UTF-8 UTF-8\n",
		"etc/locale.gen":           "# ru_RU.UTF-8 UTF-8\nen_US.UTF-8 UTF-8\n",
		"etc/default/locale":       "LANG=en_US.UTF-8\n",
		"usr/sbin/locale-gen":      "#!/bin/sh\n",
		"usr/sbin/update-locale":   "#!/bin/sh\n",
	})
	ls := m.Locales(context.Background())
	if ls.Current != "en_US.UTF-8" || !ls.CanGenerate {
		t.Fatalf("список: %+v", ls)
	}
	byName := map[string]LocaleInfo{}
	for _, l := range ls.Locales {
		byName[l.Name] = l
	}
	if !byName["en_US.UTF-8"].Current || !byName["en_US.UTF-8"].Installed || byName["ru_RU.UTF-8"].Installed || !byName["C.UTF-8"].Installed {
		t.Errorf("отметки: %+v", byName)
	}
	if err := m.GenerateLocales(context.Background(), []string{"ru_RU.UTF-8"}); err != nil {
		t.Fatalf("GenerateLocales: %v", err)
	}
	gen, _ := m.c.ReadFile("/etc/locale.gen")
	if !strings.HasPrefix(string(gen), "ru_RU.UTF-8 UTF-8\n") || strings.Contains(string(gen), "# ru_RU") {
		t.Errorf("locale.gen: %q", gen)
	}
	if last := (*calls)[len(*calls)-1]; last[0] != "locale-gen" {
		t.Errorf("locale-gen не запущен: %v", last)
	}
	if err := m.GenerateLocales(context.Background(), []string{"xx_XX"}); err == nil {
		t.Error("неизвестная локаль принята")
	}
	if err := m.SetLocale(context.Background(), "ru_RU.UTF-8"); err == nil {
		t.Error("несгенерированная локаль назначена")
	}
	if err := m.SetLocale(context.Background(), "C.UTF-8"); err != nil {
		t.Fatalf("SetLocale: %v", err)
	}
	if last := (*calls)[len(*calls)-1]; strings.Join(last, " ") != "update-locale LANG=C.UTF-8 LC_ALL=" {
		t.Errorf("update-locale: %v", last)
	}
}
