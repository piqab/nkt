package hub

import (
	"context"
	"path/filepath"
	"time"

	"github.com/piqab/nkt/internal/vuln"
)

// vulnDBRefreshLoop keeps the hub's own centralized trivy DB warm in the
// background, following versionCheckLoop's exact ticker+select shape — so
// an admin clicking "scan" on a managed host's Уязвимости page usually
// finds it already fresh instead of waiting out a ~100MB download inline.
// EnsureDB is its own no-op when already fresh (see internal/vuln's
// dbMaxAge), so a tick that finds nothing stale to do costs nothing.
func (m *Manager) vulnDBRefreshLoop(ctx context.Context) {
	interval := m.cfg.HubVulnDBRefreshInterval
	if interval <= 0 {
		return
	}
	// Same reasoning as versionCheckLoop's own initial delay: nothing here
	// is urgent enough to compete with Run's other goroutines in the first
	// seconds of the process's life.
	initial := time.NewTimer(30 * time.Second)
	defer initial.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initial.C:
		_ = m.RefreshVulnDB(ctx)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.RefreshVulnDB(ctx)
		}
	}
}

// RefreshVulnDB makes sure the hub's own trivy+DB is installed and no older
// than dbMaxAge — both the background loop above and the "О системе" page's
// own "Обновить базу" button call this directly; EnsureDB's own staleness
// check means a manual click when the DB is already fresh just confirms
// that instead of forcing a redundant ~100MB redownload.
func (m *Manager) RefreshVulnDB(ctx context.Context) error {
	m.vulnDBMu.Lock()
	if m.vulnDBRefreshing {
		m.vulnDBMu.Unlock()
		return nil
	}
	m.vulnDBRefreshing = true
	m.vulnDBErr = ""
	m.vulnDBMu.Unlock()

	report := func(msg string) {
		m.vulnDBMu.Lock()
		m.vulnDBProgress = msg
		m.vulnDBMu.Unlock()
	}

	dir := m.vulnDir()
	trivyBin, err := vuln.EnsureTrivy(ctx, filepath.Join(dir, "bin"), report)
	if err == nil {
		err = vuln.EnsureDB(ctx, trivyBin, filepath.Join(dir, "db"), report)
	}

	m.vulnDBMu.Lock()
	m.vulnDBRefreshing = false
	m.vulnDBProgress = ""
	if err != nil {
		m.vulnDBErr = err.Error()
	}
	m.vulnDBMu.Unlock()
	return err
}

// VulnDBInfo is the hub's own centralized-DB status, as handed to callers
// outside this package.
type VulnDBInfo struct {
	Available  bool
	UpdatedAt  time.Time
	Refreshing bool
	Progress   string
	Error      string
}

// VulnDBStatus reports the hub's own trivy DB state without triggering a
// refresh — DBUpdatedAt just reads trivy's own metadata.json (see
// internal/vuln.DBUpdatedAt), so this is cheap enough to call on every "О
// системе" page load.
func (m *Manager) VulnDBStatus() VulnDBInfo {
	updatedAt := vuln.DBUpdatedAt(filepath.Join(m.vulnDir(), "db"))

	m.vulnDBMu.Lock()
	refreshing, progress, errMsg := m.vulnDBRefreshing, m.vulnDBProgress, m.vulnDBErr
	m.vulnDBMu.Unlock()

	return VulnDBInfo{
		Available:  !updatedAt.IsZero(),
		UpdatedAt:  updatedAt,
		Refreshing: refreshing,
		Progress:   progress,
		Error:      errMsg,
	}
}
