package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/api"
	"github.com/piqab/nkt/internal/msgs"
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
	version, err := m.SelfUpdateTarget(false)
	if err != nil {
		return err
	}
	return m.applyVersion(ctx, version, nil)
}

// SelfUpdateTarget — версия, на которую встанет хаб: последняя (обновление)
// или предыдущая (откат), с проверкой всех условий заранее — до задания и
// до скачивания.
func (m *Manager) SelfUpdateTarget(rollback bool) (string, error) {
	status := m.VersionStatus()
	if !status.Updatable {
		return "", msgs.Errorf("hub.selfUpdateUnavailableHubRunning")
	}
	if status.Latest == "" {
		return "", msgs.Errorf("hub.versionHasBeenCheckedYet")
	}
	if rollback {
		if status.Previous == "" {
			return "", msgs.Errorf("hub.noPreviousRelease", status.Current)
		}
		return status.Previous, nil
	}
	if !status.UpdateAvailable {
		return "", msgs.Errorf("hub.latestVersionAlreadyInstalled", status.Current)
	}
	return status.Latest, nil
}

// Rollback installs the release right below the running one — the same
// download-verify-stage-restart path as ApplyUpdate, just aimed one
// version back. Hosts follow the hub down the same way they follow it
// up: the hub always installs its own version, and the UI treats any
// version mismatch as "bring the host to the hub's version".
func (m *Manager) Rollback(ctx context.Context) error {
	version, err := m.SelfUpdateTarget(true)
	if err != nil {
		return err
	}
	return m.applyVersion(ctx, version, nil)
}

// selfUpdateLog — куда писать ход обновления: журнал задания
// (*jobs.Context) или, если nil, только журнал службы.
type selfUpdateLog interface {
	Log(key string, args ...any)
	StepKey(n, total int, key string, args ...any)
}

// selfUpdateTimeout — предел всего обновления (скачивание и подготовка).
const selfUpdateTimeout = 30 * time.Minute

// selfUpdateProgressEvery — как часто строка процентов скачивания
// попадает в журнал задания.
const selfUpdateProgressEvery = 5 * time.Second

// applyVersion downloads and verifies release `version`, then replaces
// this hub's own binary and systemd unit and restarts itself. Ход — по
// шагам (сумма, бинарник, юнит, установка), каждый — строкой в out.
func (m *Manager) applyVersion(ctx context.Context, version string, out selfUpdateLog) error {
	ctx, cancel := context.WithTimeout(ctx, selfUpdateTimeout)
	defer cancel()
	const steps = 4
	report := func(key string, args ...any) {
		m.log.Info("hub self-update", "step", key, "args", args)
		if out != nil {
			out.Log(key, args...)
		}
	}
	step := func(n int, key string, args ...any) {
		if out != nil {
			out.StepKey(n, steps, key, args...)
		}
		report(key+"Line", args...)
	}
	var lastProgress time.Time
	progress := func(key string, args ...any) {
		// Строка процентов — не чаще раза в selfUpdateProgressEvery и
		// обязательно последняя (100%).
		if pct, ok := args[0].(int); ok && pct < 100 && time.Since(lastProgress) < selfUpdateProgressEvery {
			return
		}
		lastProgress = time.Now()
		report(key, args...)
	}

	report("hub.selfUpdateFromTo", m.version, version)
	asset := fmt.Sprintf("nkt-%s-%s", runtime.GOOS, runtime.GOARCH)

	step(1, "hub.selfUpdateStepSums")
	want, err := m.releaseSum(ctx, version, asset)
	if err != nil {
		return err
	}
	report("hub.selfUpdateSumFound", asset)

	step(2, "hub.selfUpdateStepBinary", version)
	binPath := filepath.Join(m.cfg.HubBinCacheDir(), fmt.Sprintf("%s-%s", asset, version))
	if got, err := fileSHA256(binPath); err == nil && strings.EqualFold(got, want) {
		// Скачан прошлой попыткой — и сумма та же: качать заново незачем.
		report("hub.selfUpdateFromCache")
	} else {
		if err := m.fetchVerified(ctx, version, asset, want, binPath, report, progress); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return msgs.Errorf("hub.selfUpdateTimedOut", int(selfUpdateTimeout.Minutes()))
			}
			return msgs.Errorf("hub.downloadingBinaryV", version, err)
		}
		report("hub.releaseBinaryVerified", runtime.GOOS, runtime.GOARCH)
	}

	step(3, "hub.selfUpdateStepUnit")
	unitContent, err := m.downloadUnitTemplate(ctx, version, "netknownsthat-hub.service")
	if err != nil {
		return msgs.Errorf("hub.downloadingSystemdUnitV", version, err)
	}

	step(4, "hub.selfUpdateStepInstall")
	stageDir, err := os.MkdirTemp(m.cfg.DataDir, "hub-selfupdate-")
	if err != nil {
		return msgs.Errorf("hub.temporaryDirectory", err)
	}
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	stageUnit := filepath.Join(stageDir, "netknownsthat-hub.service")
	if err := os.WriteFile(stageUnit, []byte(unitContent), 0o644); err != nil {
		return msgs.Errorf("control.writing", stageUnit, err)
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
sleep 3
systemctl restart netknownsthat-hub
`,
		binPath, hubSelfUpdateBinPath,
		stageUnit, hubSelfUpdateServicePath,
		stageDir)

	if err := startSelfUpdateScript(script); err != nil {
		return msgs.Errorf("hub.startingBackgroundUpdateScript", err)
	}
	// Not Wait()'d — see the doc comment above. The stage dir's unit file is
	// cleaned up by the script itself (rm -rf, after install has already
	// copied it into place), not by the defer above, for the same reason:
	// it must still exist when systemctl restart's replacement process
	// starts reading it, well past this function's own return.
	removeStage = false
	report("hub.selfUpdateRestarting", version)
	return nil
}

// startSelfUpdateScript запускает установку с перезапуском вне песочницы
// и не ждёт её (см. applyVersion); подменяется в тестах.
var startSelfUpdateScript = func(script string) error {
	return api.UnrestrictedBackgroundCommand("bash", "-c", script).Start()
}

// fileSHA256 — sha256 файла (hex).
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
