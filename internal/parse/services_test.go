package parse

import (
	"testing"

	"github.com/piqab/nkt/internal/model"
)

// TestInstallTarget locks in the three services whose real apt/snap
// package doesn't match the logical/unit Name InstallTarget would
// otherwise fall back to, plus one that does — a regression here would
// mean handleServiceInstallWS either fails outright (bare "docker" and
// "libvirt" aren't real apt packages) or tries apt-get against a package
// that current Debian/Ubuntu don't ship (LXD, snap-only upstream).
func TestInstallTarget(t *testing.T) {
	cases := []struct {
		service     string
		wantMethod  ServiceInstallMethod
		wantPackage string
	}{
		{"docker", InstallViaAPT, "docker.io"},
		{"lxd", InstallViaSnap, "lxd"},
		{"libvirt", InstallViaAPT, "libvirt-daemon-system"},
		{"nginx", InstallViaAPT, "nginx"}, // no override: Name already is the real apt package
	}
	for _, c := range cases {
		t.Run(c.service, func(t *testing.T) {
			info, ok := InstallTarget(c.service)
			if !ok {
				t.Fatalf("InstallTarget(%q) ok = false, want true", c.service)
			}
			if info.Method != c.wantMethod {
				t.Errorf("Method = %q, want %q", info.Method, c.wantMethod)
			}
			if info.Package != c.wantPackage {
				t.Errorf("Package = %q, want %q", info.Package, c.wantPackage)
			}
			if info.Binary == "" {
				t.Error("Binary is empty — the already-installed check has nothing to collect.Which against")
			}
		})
	}

	t.Run("unknown service refused", func(t *testing.T) {
		if _, ok := InstallTarget("rm-rf-everything"); ok {
			t.Error("InstallTarget on an unmanaged name returned ok = true, want the allowlist to refuse it")
		}
	})
}

// Вывод systemctl show снят с настоящей машины. «Установлен» не сводится
// к наличию бинарника в PATH: у не-root процесса в PATH нет /usr/sbin, а
// у части служб исполняемый файл называется не так, как служба. После
// установки из-за этого чип оставался жёлтым — «не установлен» — при
// работающей службе.
func TestApplyUnitPropertiesInstalledDetection(t *testing.T) {
	t.Run("работающая служба установлена по определению", func(t *testing.T) {
		unit := model.ServiceUnit{ActiveState: "unknown"} // бинарник не нашёлся
		applyUnitProperties(&unit, "Description=nginx\nActiveState=active\nSubState=running\nUnitFileState=enabled\n")
		if !unit.Installed {
			t.Error("активная служба помечена как не установленная")
		}
		if unit.ActiveState != "active" {
			t.Errorf("ActiveState = %q", unit.ActiveState)
		}
	})

	t.Run("установлена, но остановлена", func(t *testing.T) {
		unit := model.ServiceUnit{ActiveState: "unknown"}
		applyUnitProperties(&unit, "ActiveState=inactive\nSubState=dead\nUnitFileState=disabled\n")
		if !unit.Installed {
			t.Error("остановленная, но известная systemd служба помечена как не установленная")
		}
	})

	t.Run("юнита нет вовсе", func(t *testing.T) {
		// Такой ответ systemctl даёт для отсутствующей службы — и вот
		// тогда «не установлена» верно, даже если одноимённый бинарник
		// откуда-то нашёлся.
		unit := model.ServiceUnit{Installed: true, ActiveState: "unknown"}
		applyUnitProperties(&unit, "ActiveState=inactive\nSubState=dead\nUnitFileState=not-found\n")
		if unit.Installed {
			t.Error("отсутствующий юнит признан установленным")
		}
	})

	t.Run("systemctl не ответил — решает бинарник", func(t *testing.T) {
		// Пустой вывод: состояние неизвестно, и переписывать признак,
		// полученный от command -v, нечем.
		unit := model.ServiceUnit{Installed: true, ActiveState: "unknown"}
		applyUnitProperties(&unit, "")
		if !unit.Installed {
			t.Error("признак от command -v потерян при пустом ответе systemctl")
		}
	})
}
