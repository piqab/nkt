package api

import (
	"strings"
	"testing"
)

// Строки взяты из настоящего /proc/<pid>/status: у root под юнитом с
// CapabilityBoundingSet маска урезана, и именно по ней видно, есть ли
// CAP_SYS_ADMIN — по одному только UID=0 этого не понять.
func TestParseCapEff(t *testing.T) {
	cases := []struct {
		name   string
		status string
		has    bool
		found  bool
	}{
		{"полный набор root", "Name:\tnkt\nCapEff:\t000001ffffffffff\n", true, true},
		{"набор юнита без CAP_SYS_ADMIN", "CapEff:\t0000000000003400\n", false, true},
		{"набор юнита с CAP_SYS_ADMIN", "CapEff:\t0000000000200000\n", true, true},
		{"обычный пользователь", "CapEff:\t0000000000000000\n", false, true},
		{"строки нет вовсе", "Name:\tnkt\n", false, false},
		{"мусор вместо маски", "CapEff:\tнемаска\n", false, false},
	}
	for _, tc := range cases {
		has, found := parseCapEff(tc.status)
		if has != tc.has || found != tc.found {
			t.Errorf("%s: parseCapEff = (%v, %v), want (%v, %v)", tc.name, has, found, tc.has, tc.found)
		}
	}
}

func TestUnitNameFromCgroup(t *testing.T) {
	cases := map[string]string{
		"0::/system.slice/netknownsthat.service\n":                  "netknownsthat.service",
		"0::/system.slice/netknownsthat-hub.service\n":              "netknownsthat-hub.service",
		"0::/system.slice/system-nkt.slice/netknownsthat.service\n": "netknownsthat.service",
		"1:name=systemd:/system.slice/netknownsthat.service\n":      "netknownsthat.service",
		"0::/init.scope\n": "",
		"":                 "",
	}
	for cgroup, want := range cases {
		if got := unitNameFromCgroup(cgroup); got != want {
			t.Errorf("unitNameFromCgroup(%q) = %q, want %q", cgroup, got, want)
		}
	}
}

// Разбор юнита — то, чем диагноз отличает «юнит старый» от «прав нет».
func TestParseUnitDirectives(t *testing.T) {
	current := `[Service]
CapabilityBoundingSet=CAP_NET_ADMIN CAP_SYS_PTRACE CAP_SYS_ADMIN
AmbientCapabilities=CAP_NET_ADMIN CAP_SYS_ADMIN
RestrictNamespaces=mnt
SystemCallFilter=@system-service
SystemCallFilter=setns
`
	d := parseUnitDirectives(current)
	if !d.HasSetnsFilter || !d.HasSysAdminCap || !d.AllowsMountNS {
		t.Errorf("текущий юнит разобран как неполный: %+v", d)
	}

	// Юнит до правки 941ed39: setns не разрешён отдельно, и именно это
	// даёт «reassociate to namespace 'ns/mnt' failed».
	old := `[Service]
AmbientCapabilities=CAP_SYS_ADMIN
RestrictNamespaces=mnt
SystemCallFilter=@system-service
`
	if d := parseUnitDirectives(old); d.HasSetnsFilter {
		t.Error("юнит без строки SystemCallFilter=setns признан пригодным")
	}

	// Закомментированная строка не считается: она ничего не разрешает.
	commented := "[Service]\n# SystemCallFilter=setns\nRestrictNamespaces=mnt\n"
	if d := parseUnitDirectives(commented); d.HasSetnsFilter {
		t.Error("закомментированная директива засчитана как действующая")
	}

	// RestrictNamespaces в форме запрета: mnt разрешён, если его нет в списке.
	if d := parseUnitDirectives("[Service]\nRestrictNamespaces=~net user\n"); !d.AllowsMountNS {
		t.Error("deny-форма RestrictNamespaces разобрана неверно")
	}
	if d := parseUnitDirectives("[Service]\nRestrictNamespaces=~mnt\n"); d.AllowsMountNS {
		t.Error("запрет mnt в deny-форме не замечен")
	}
	if d := parseUnitDirectives("[Service]\nRestrictNamespaces=yes\n"); d.AllowsMountNS {
		t.Error("RestrictNamespaces=yes запрещает всё, включая mnt")
	}
	if d := parseUnitDirectives("[Service]\nRestrictNamespaces=no\n"); !d.AllowsMountNS {
		t.Error("RestrictNamespaces=no не ограничивает ничего")
	}
}

// Команды должны вести к починке в правильном порядке и не предлагать
// трогать юнит, когда дело не в нём.
func TestSandboxFixCommands(t *testing.T) {
	withUnit := sandboxFixCommands(SandboxDiagnosis{
		Problems: []SandboxProblem{{Code: "unit_missing_setns"}},
	})
	joined := strings.Join(withUnit, "\n")
	if !strings.Contains(joined, "install -y dbus") {
		t.Error("установка dbus не предложена")
	}
	if !strings.Contains(joined, "daemon-reload") || !strings.Contains(joined, "netknownsthat.service") {
		t.Error("обновление юнита не предложено, хотя проблема именно в нём")
	}
	if last := withUnit[len(withUnit)-1]; !strings.Contains(last, "restart netknownsthat") {
		t.Errorf("перезапуск должен идти последним, последняя команда: %q", last)
	}

	onlyCaps := sandboxFixCommands(SandboxDiagnosis{
		Problems: []SandboxProblem{{Code: "no_cap_sys_admin"}},
	})
	if strings.Contains(strings.Join(onlyCaps, "\n"), "daemon-reload") {
		t.Error("замена юнита предложена там, где проблема не в его содержимом")
	}
}
