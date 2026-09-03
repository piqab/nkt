package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/vuln"
)

// hubVulnState is one managed host's own current vulnerability-scan state,
// as seen by the hub — the exact shape internal/api's own vulnState carries
// for a standalone nkt, moved up a level because one hub process serves
// every managed host's Уязвимости page, not just its own.
type hubVulnState struct {
	mu       sync.Mutex
	scanning bool
	progress string
	result   *model.VulnScan
	lastErr  string
}

// vulnScanTimeout bounds one host's whole scan — fetching its manifest,
// scanning OS packages against the hub's own DB, and asking it to scan its
// own container images (which can itself mean that host self-installing
// trivy+DB for the first time). Generous: a cold DB download over a slow
// link is exactly the case this must not cut off partway through.
const vulnScanTimeout = 10 * time.Minute

// vulnDir is where the hub's own copy of trivy and its vulnerability DB
// live — the exact same subpath convention internal/api's own vulnDir()
// uses under a plain nkt's DataDir, so the hub's *own* "localhost" row
// (scanned in-process through the embedded api.Server, see server.go's
// proxyLocal) and this file's centralized scanning of every *other* managed
// host share one cache instead of each keeping an independent ~1GB copy.
func (m *Manager) vulnDir() string {
	return filepath.Join(m.cfg.DataDir, "vuln")
}

// vulnScanKVKeyFor namespaces internal/api's own vulnScanKVKey constant per
// host — the hub's store.DB is shared across every managed host it scans,
// unlike a standalone nkt's, where that key alone is already unambiguous.
func vulnScanKVKeyFor(hostID int64) string {
	return "vuln_last_scan_host_" + strconv.FormatInt(hostID, 10)
}

// StartHostVulnScan kicks off a centralized scan for hostID in the
// background and returns immediately — handleHostVulnScanStart's own
// counterpart to a standalone nkt's handleVulnScanStart, one level up.
func (m *Manager) StartHostVulnScan(hostID int64) error {
	state := m.vulnStateFor(hostID)

	state.mu.Lock()
	if state.scanning {
		state.mu.Unlock()
		return errVulnScanAlreadyRunning
	}
	state.scanning = true
	state.lastErr = ""
	state.progress = "Запуск..."
	state.mu.Unlock()

	// context.Background(), not a request context: a scan spanning an SSH
	// round trip to the host plus a possible first-time DB download can
	// legitimately outlive the fire-and-forget POST that started it.
	go m.runHostVulnScan(context.Background(), hostID)
	return nil
}

var errVulnScanAlreadyRunning = fmt.Errorf("сканирование уже выполняется")

// vulnStateFor returns hostID's own state, creating it on first use —
// mirrors the lazy-creation shape connsMu/conns already uses, just for a
// map of pointers (each host's own mutex) instead of values.
func (m *Manager) vulnStateFor(hostID int64) *hubVulnState {
	m.vulnMu.Lock()
	defer m.vulnMu.Unlock()
	state, ok := m.vulnScans[hostID]
	if !ok {
		state = &hubVulnState{}
		m.vulnScans[hostID] = state
	}
	return state
}

// HostVulnStatus reports hostID's current scan state — internal/api's own
// handleVulnerabilities logic, moved up a level: falls back to whatever was
// last persisted under vulnScanKVKeyFor when nothing is cached in memory
// yet, so a hub restart does not masquerade as "this host was never
// scanned" any more than a standalone nkt's own restart does.
func (m *Manager) HostVulnStatus(ctx context.Context, hostID int64) (scanning bool, progress string, result *model.VulnScan, lastErr string) {
	state := m.vulnStateFor(hostID)

	state.mu.Lock()
	scanning, progress, result, lastErr = state.scanning, state.progress, state.result, state.lastErr
	state.mu.Unlock()

	if result == nil && !scanning {
		if persisted, err := m.loadPersistedVulnScan(ctx, hostID); err == nil && persisted != nil {
			state.mu.Lock()
			if state.result == nil {
				state.result = persisted
			}
			result = state.result
			state.mu.Unlock()
		}
	}
	return scanning, progress, result, lastErr
}

func (m *Manager) loadPersistedVulnScan(ctx context.Context, hostID int64) (*model.VulnScan, error) {
	raw, ok, err := m.db.KVGet(ctx, vulnScanKVKeyFor(hostID))
	if err != nil || !ok {
		return nil, err
	}
	var scan model.VulnScan
	if err := json.Unmarshal([]byte(raw), &scan); err != nil {
		return nil, err
	}
	return &scan, nil
}

// runHostVulnScan does the actual work: fetches hostID's package manifest
// over the tunnel already used for everything else the hub does with this
// host, scans it against the hub's *own* trivy+DB (never downloaded per
// host — see vulnDir's own doc comment), asks the host itself to scan
// whatever container images it has running (the one part that genuinely
// cannot move off the host — internal/vuln.ScanImage needs a live local
// Docker/Podman socket), and persists the combined result exactly the way
// internal/api's own runVulnScan does for a standalone host, just keyed per
// host instead of being the only scan this process will ever run.
func (m *Manager) runHostVulnScan(ctx context.Context, hostID int64) {
	ctx, cancel := context.WithTimeout(ctx, vulnScanTimeout)
	defer cancel()

	state := m.vulnStateFor(hostID)
	report := func(msg string) {
		state.mu.Lock()
		state.progress = msg
		state.mu.Unlock()
	}
	fail := func(err error) {
		state.mu.Lock()
		state.lastErr = err.Error()
		state.scanning = false
		state.mu.Unlock()
	}

	dial, _, onFail, err := m.dialerFor(ctx, hostID)
	if err != nil {
		fail(err)
		return
	}
	cookie, err := m.cookieFor(ctx, hostID, dial)
	if err != nil {
		onFail()
		fail(err)
		return
	}
	client := tunnelHTTPClientNoTimeout(dial)

	report("Забираю список установленных пакетов с хоста...")
	manifest, err := fetchHostManifest(ctx, client, cookie)
	if err != nil {
		onFail()
		fail(err)
		return
	}

	report("Прошу хост просканировать образы контейнеров...")
	imgFindings, imgWarnings, imgDBUpdated, err := fetchHostImageScan(ctx, client, cookie)
	if err != nil {
		// A host that cannot scan its own images (trivy self-install
		// failed there, say) must not throw away the OS-package half the
		// hub can still do perfectly well itself — noted as a warning
		// instead, same "degrade, don't abort" shape as a single
		// unreachable image within one host's own scan.
		imgWarnings = append(imgWarnings, "сканирование образов на хосте: "+err.Error())
	}

	var findings []model.VulnFinding
	dbUpdated := imgDBUpdated
	if manifest.Available {
		dir := m.vulnDir()
		trivyBin, err := vuln.EnsureTrivy(ctx, filepath.Join(dir, "bin"), report)
		if err != nil {
			fail(err)
			return
		}
		dbDir := filepath.Join(dir, "db")
		if err := vuln.EnsureDB(ctx, trivyBin, dbDir, report); err != nil {
			fail(err)
			return
		}

		report("Сканирую пакеты ОС на уязвимости...")
		osFindings, err := vuln.Scan(ctx, trivyBin, dbDir, manifest)
		if err != nil {
			fail(err)
			return
		}
		findings = append(findings, osFindings...)

		hubDBUpdated := vuln.DBUpdatedAt(dbDir)
		// The older of the two DB freshness timestamps is the honest
		// answer for a combined result — reporting only the newer one
		// would overstate how current the *other* half of these findings
		// actually is.
		if dbUpdated.IsZero() || hubDBUpdated.Before(dbUpdated) {
			dbUpdated = hubDBUpdated
		}
	}
	findings = append(findings, imgFindings...)

	if !manifest.Available && len(imgFindings) == 0 && len(imgWarnings) == 0 {
		state.mu.Lock()
		state.result = &model.VulnScan{Available: false, ScannedAt: time.Now()}
		state.scanning = false
		state.progress = ""
		state.mu.Unlock()
		return
	}

	compared, newCount, fixedCount, err := m.applyHostVulnDiff(ctx, hostID, findings)
	if err != nil {
		fail(err)
		return
	}

	result := &model.VulnScan{
		Available:  true,
		Findings:   findings,
		Compared:   compared,
		NewCount:   newCount,
		FixedCount: fixedCount,
		Warnings:   imgWarnings,
		DBUpdated:  dbUpdated,
		ScannedAt:  time.Now(),
	}

	if encoded, err := json.Marshal(result); err != nil {
		m.log.Warn("could not serialize vulnerability scan result", "host_id", hostID, "error", err)
	} else if err := m.db.KVSet(ctx, vulnScanKVKeyFor(hostID), string(encoded)); err != nil {
		m.log.Warn("could not save vulnerability scan result", "host_id", hostID, "error", err)
	}

	state.mu.Lock()
	state.result = result
	state.scanning = false
	state.progress = ""
	state.mu.Unlock()
}

// applyHostVulnDiff is internal/api's own applyVulnDiff, moved up a level —
// findingKey's own de-duplication logic (Target/ID/Package) is identical,
// just diffed against this host's own persisted key instead of the one
// shared key a standalone nkt's Server keeps.
func (m *Manager) applyHostVulnDiff(ctx context.Context, hostID int64, findings []model.VulnFinding) (compared bool, newCount, fixedCount int, err error) {
	previous, err := m.loadPersistedVulnScan(ctx, hostID)
	if err != nil {
		return false, 0, 0, err
	}
	if previous == nil {
		return false, 0, 0, nil
	}

	prevKeys := make(map[string]struct{}, len(previous.Findings))
	for _, f := range previous.Findings {
		prevKeys[vulnFindingKey(f)] = struct{}{}
	}
	currentKeys := make(map[string]struct{}, len(findings))
	for i := range findings {
		currentKeys[vulnFindingKey(findings[i])] = struct{}{}
		if _, existed := prevKeys[vulnFindingKey(findings[i])]; !existed {
			findings[i].New = true
			newCount++
		}
	}
	for _, f := range previous.Findings {
		if _, stillPresent := currentKeys[vulnFindingKey(f)]; !stillPresent {
			fixedCount++
		}
	}
	return true, newCount, fixedCount, nil
}

// vulnFindingKey mirrors internal/api's own findingKey exactly — duplicated
// rather than imported (internal/hub already imports internal/api for the
// embedded Server, so the import itself would be fine, but reaching into
// another package for one unexported-shaped helper this small is more
// indirection than the four-line function is worth).
func vulnFindingKey(f model.VulnFinding) string {
	return f.Target + "\x00" + f.ID + "\x00" + f.Package
}

// dropVulnScan forgets hostID's cached scan state — called alongside
// CloseHost's other per-host cleanup when a host is removed from the
// registry. Deliberately does not delete the persisted vulnScanKVKeyFor
// entry: if the same host is re-added later (same address, a fresh row),
// there is no harm in still remembering its last scan for diffing purposes,
// same as store.DB itself never scrubs old KV rows on host removal today.
func (m *Manager) dropVulnScan(hostID int64) {
	m.vulnMu.Lock()
	delete(m.vulnScans, hostID)
	m.vulnMu.Unlock()
}

// fetchHostManifest asks hostID's own nkt for its package manifest — the
// new, cheap GET handleVulnManifest (internal/api/handlers_vulnerabilities.go)
// exists specifically so a hub never has to run trivy on the host's own
// filesystem to get this.
func fetchHostManifest(ctx context.Context, client *http.Client, cookie string) (model.PackageManifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+remoteAPIAddr+"/api/vulnerabilities/manifest", nil)
	if err != nil {
		return model.PackageManifest{}, err
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	resp, err := client.Do(req)
	if err != nil {
		return model.PackageManifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return model.PackageManifest{}, fmt.Errorf("хост вернул код %d на /vulnerabilities/manifest", resp.StatusCode)
	}
	var body struct {
		Manifest model.PackageManifest `json:"manifest"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return model.PackageManifest{}, err
	}
	return body.Manifest, nil
}

// fetchHostImageScan asks hostID's own nkt to scan whatever container
// images it currently has running — the one half of vulnerability scanning
// that genuinely cannot centralize onto the hub (see runHostVulnScan's own
// doc comment). Posts to the new handleVulnScanImages
// (internal/api/handlers_vulnerabilities.go), which self-installs trivy+DB
// on the host only if it actually has images to scan.
func fetchHostImageScan(ctx context.Context, client *http.Client, cookie string) ([]model.VulnFinding, []string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+remoteAPIAddr+"/api/vulnerabilities/scan-images", nil)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, time.Time{}, fmt.Errorf("хост вернул код %d на /vulnerabilities/scan-images", resp.StatusCode)
	}
	var body struct {
		Findings  []model.VulnFinding `json:"findings"`
		Warnings  []string            `json:"warnings"`
		DBUpdated time.Time           `json:"db_updated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, nil, time.Time{}, err
	}
	return body.Findings, body.Warnings, body.DBUpdated, nil
}

// tunnelHTTPClientNoTimeout is tunnelHTTPClient (tunnel.go) without its
// blanket 30s client-level Timeout — a first-time image scan on the host
// (self-installing trivy + downloading its own ~1GB DB, see
// handleVulnScanImages) can legitimately take longer than that, and this
// call's own deadline already comes from vulnScanTimeout via ctx instead.
func tunnelHTTPClientNoTimeout(dial dialFunc) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return dial("tcp", remoteAPIAddr)
			},
		},
	}
}
