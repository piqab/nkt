package hub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/piqab/nkt/internal/api"
)

// hubSelfUpdatePaths mirror handleSelfUpdate's own constants
// (internal/api/handlers_selfupdate.go) one level up — the hub's own
// binary and unit, not a managed host's. hub.env is deliberately absent
// from this list: unlike a managed host's nkt.env (regenerated from
// scratch by every install/update — see provision.go's renderEnv), the
// hub's own hub.env is operator-owned configuration this process must
// never overwrite on its own initiative.
const (
	hubSelfUpdateBinPath     = "/usr/local/bin/nkt"
	hubSelfUpdateServicePath = "/etc/systemd/system/netknownsthat-hub.service"
)

// ApplyUpdate downloads and verifies the latest release VersionStatus last
// found, then replaces this hub's own binary and systemd unit and restarts
// itself — the local, no-SSH-involved counterpart to what install() does
// for a managed host, and structurally identical to handleSelfUpdate's own
// download-verify-stage-restart shape for a managed host reached over the
// tunnel channel. Returns once the background restart script has actually
// started, not once it finishes — by design, since the script's own
// `systemctl restart netknownsthat-hub` near the end kills this very
// process partway through, well before any Wait() here could return.
//
// Refuses outright (before downloading anything) unless
// VersionStatus().Updatable is true and a newer version is actually known
// — this is the one path in the whole hub that intentionally overwrites
// its own running binary, so every precondition is checked up front rather
// than discovered halfway through a script no one is watching run.
func (m *Manager) ApplyUpdate(ctx context.Context) error {
	status := m.VersionStatus()
	if !status.Updatable {
		return fmt.Errorf(
			"самообновление недоступно: хаб сейчас не запущен как systemd-юнит (нет INVOCATION_ID в " +
				"окружении) — либо это Docker/Kubernetes-развёртывание (обновите образ и пересоздайте " +
				"контейнер: docker compose pull && docker compose up -d), либо бинарник запущен вручную, " +
				"не через systemd (установите и запустите как сервис: sudo make hub-install && " +
				"sudo systemctl enable --now netknownsthat-hub)")
	}
	if status.Latest == "" {
		return fmt.Errorf("версия ещё не проверялась — сначала выполните проверку обновлений")
	}
	if !status.UpdateAvailable {
		return fmt.Errorf("уже установлена последняя версия (%s)", status.Current)
	}
	version := status.Latest

	report := func(key string, args ...any) {
		m.log.Info("hub self-update", "step", key, "args", args)
	}

	name := fmt.Sprintf("nkt-%s-%s-%s", runtime.GOOS, runtime.GOARCH, version)
	binPath := filepath.Join(m.cfg.HubBinCacheDir(), name)
	if _, err := os.Stat(binPath); err != nil {
		if err := m.downloadReleaseBinary(ctx, runtime.GOOS, runtime.GOARCH, version, binPath, report); err != nil {
			return fmt.Errorf("скачивание бинарника v%s: %w", version, err)
		}
	}

	unitContent, err := m.downloadUnitTemplate(ctx, version, "netknownsthat-hub.service")
	if err != nil {
		return fmt.Errorf("скачивание systemd-юнита v%s: %w", version, err)
	}

	stageDir, err := os.MkdirTemp(m.cfg.DataDir, "hub-selfupdate-")
	if err != nil {
		return fmt.Errorf("временный каталог: %w", err)
	}
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	stageUnit := filepath.Join(stageDir, "netknownsthat-hub.service")
	if err := os.WriteFile(stageUnit, []byte(unitContent), 0o644); err != nil {
		return fmt.Errorf("запись %s: %w", stageUnit, err)
	}

	// Same escape-the-sandbox reasoning as handleSelfUpdate: this process's
	// own ProtectSystem=strict makes /usr/local/bin and
	// /etc/systemd/system read-only to it directly, so the actual install +
	// restart runs in a separate, unrestricted transient unit via
	// api.UnrestrictedBackgroundCommand — the same mechanism the terminal
	// and OS package updates already use to leave this process's own
	// sandbox untouched while still getting real root access to the host.
	script := fmt.Sprintf(`set -e
install -D -m 0755 %s %s
install -D -m 0644 %s %s
systemctl daemon-reload
rm -rf %s
sleep 1
systemctl restart netknownsthat-hub
`,
		binPath, hubSelfUpdateBinPath,
		stageUnit, hubSelfUpdateServicePath,
		stageDir)

	cmd := api.UnrestrictedBackgroundCommand("bash", "-c", script)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск фонового скрипта обновления: %w", err)
	}
	// Not Wait()'d — see the doc comment above. The stage dir's unit file is
	// cleaned up by the script itself (rm -rf, after install has already
	// copied it into place), not by the defer above, for the same reason:
	// it must still exist when systemctl restart's replacement process
	// starts reading it, well past this function's own return.
	removeStage = false
	return nil
}
