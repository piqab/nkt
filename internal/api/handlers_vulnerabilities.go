package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/parse"
	"github.com/althq/netknownsthat/internal/vuln"
)

// vulnFindingsKVKey is where the previous scan's findings are kept (see
// internal/store's generic kv table) purely so the next scan has something
// to diff against — a rolling one-step comparison, not a history: this key
// always holds exactly the most recent scan, overwritten by the one after
// it.
const vulnFindingsKVKey = "vuln_last_findings"

// findingKey identifies "the same vulnerability" across two scans — a CVE
// against one specific package, against one specific target. The same CVE
// ID can legitimately appear against several packages in one scan (e.g. a
// libc CVE affecting both libc6 and libc6-dev), so ID alone would conflate
// them; Target keeps the host's own packages and each container image's
// packages from colliding with each other too — the identical CVE against
// the identical package name can genuinely exist in both a host package and
// some unrelated container's image at once, and those are different things
// to fix.
func findingKey(f model.VulnFinding) string {
	return f.Target + "\x00" + f.ID + "\x00" + f.Package
}

// applyVulnDiff marks each of findings New relative to whatever scan this
// host kept last (see vulnFindingsKVKey), then persists findings as the new
// "last scan" for the comparison after this one. compared is false only
// when there was no previous scan to compare against at all (this host's
// very first scan) — see model.VulnScan.Compared's own doc comment for why
// that has to be distinguishable from "compared, nothing changed".
func (s *Server) applyVulnDiff(ctx context.Context, findings []model.VulnFinding) (compared bool, newCount, fixedCount int, err error) {
	raw, ok, err := s.db.KVGet(ctx, vulnFindingsKVKey)
	if err != nil {
		return false, 0, 0, err
	}

	if ok {
		var previous []model.VulnFinding
		if err := json.Unmarshal([]byte(raw), &previous); err != nil {
			return false, 0, 0, err
		}
		prevKeys := make(map[string]struct{}, len(previous))
		for _, f := range previous {
			prevKeys[findingKey(f)] = struct{}{}
		}
		currentKeys := make(map[string]struct{}, len(findings))
		for i := range findings {
			currentKeys[findingKey(findings[i])] = struct{}{}
			if _, existed := prevKeys[findingKey(findings[i])]; !existed {
				findings[i].New = true
				newCount++
			}
		}
		for _, f := range previous {
			if _, stillPresent := currentKeys[findingKey(f)]; !stillPresent {
				fixedCount++
			}
		}
		compared = true
	}

	encoded, err := json.Marshal(findings)
	if err != nil {
		return false, 0, 0, err
	}
	if err := s.db.KVSet(ctx, vulnFindingsKVKey, string(encoded)); err != nil {
		return false, 0, 0, err
	}
	return compared, newCount, fixedCount, nil
}

// vulnState is this host's own current vulnerability-scan state — see
// Server.vuln's own doc comment for why this is a single shared value
// rather than a keyed session map the way updateSession is.
type vulnState struct {
	mu       sync.Mutex
	scanning bool
	progress string
	result   *model.VulnScan
	lastErr  string
}

// vulnDir is where this host's own copy of trivy and its vulnerability DB
// live, under the same DataDir every other piece of nkt-owned state
// (SQLite db, TLS cert, ...) is kept — never the scanned host's own package
// data, which only ever exists as the short-lived rootfs vuln.Scan builds
// and immediately discards.
func (s *Server) vulnDir() string {
	return filepath.Join(s.cfg.DataDir, "vuln")
}

// handleVulnerabilities reports this host's current scan state — the last
// completed scan (if any), whether one is running right now, and its
// progress message. Never triggers a scan itself: EnsureTrivy/EnsureDB can
// mean downloading a ~50MB binary and a ~100MB (nearly 1GB uncompressed)
// database, not something a plain page-open GET should risk doing by
// accident (see handleVulnScanStart, which the frontend calls explicitly).
func (s *Server) handleVulnerabilities(w http.ResponseWriter, r *http.Request) {
	s.vuln.mu.Lock()
	resp := map[string]any{
		"scanning": s.vuln.scanning,
		"progress": s.vuln.progress,
	}
	if s.vuln.result != nil {
		resp["scan"] = s.vuln.result
	}
	if s.vuln.lastErr != "" {
		resp["error"] = s.vuln.lastErr
	}
	s.vuln.mu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

// handleVulnScanStart kicks off a scan in the background and returns
// immediately — like handleRenewCertbot/handleDbusInstallWS, the actual
// work (possibly downloading trivy + its DB, then scanning) can take
// anywhere from seconds (everything already cached) to several minutes
// (first run on a fresh host), so the frontend polls handleVulnerabilities
// for progress instead of holding one long request open.
func (s *Server) handleVulnScanStart(w http.ResponseWriter, r *http.Request) {
	s.vuln.mu.Lock()
	if s.vuln.scanning {
		s.vuln.mu.Unlock()
		writeError(w, http.StatusConflict, "сканирование уже выполняется")
		return
	}
	s.vuln.scanning = true
	s.vuln.lastErr = ""
	s.vuln.progress = "Запуск..."
	s.vuln.mu.Unlock()

	// context.Background(), not r.Context(): a scan that can legitimately
	// take minutes must not be cancelled just because the request that
	// started it (a fire-and-forget POST the frontend doesn't hold open)
	// has already returned.
	go s.runVulnScan(context.Background())

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) runVulnScan(ctx context.Context) {
	report := func(msg string) {
		s.vuln.mu.Lock()
		s.vuln.progress = msg
		s.vuln.mu.Unlock()
	}
	fail := func(err error) {
		s.vuln.mu.Lock()
		s.vuln.lastErr = err.Error()
		s.vuln.scanning = false
		s.vuln.mu.Unlock()
	}

	report("Собираю список установленных пакетов...")
	manifest := parse.Manifest(s.scanner.Collector())
	images := s.runningImages(ctx)

	if !manifest.Available && len(images) == 0 {
		// Not a dpkg-based host and no Docker/Podman containers running —
		// nothing trivy could scan here at all, and no point downloading a
		// ~1GB database to learn that. Same "not applicable, not an error"
		// shape PackageUpdates uses.
		s.vuln.mu.Lock()
		s.vuln.result = &model.VulnScan{Available: false, ScannedAt: time.Now()}
		s.vuln.scanning = false
		s.vuln.progress = ""
		s.vuln.mu.Unlock()
		return
	}

	dir := s.vulnDir()
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

	var findings []model.VulnFinding
	if manifest.Available {
		report("Сканирую пакеты ОС на уязвимости...")
		osFindings, err := vuln.Scan(ctx, trivyBin, dbDir, manifest)
		if err != nil {
			fail(err)
			return
		}
		findings = append(findings, osFindings...)
	}

	var warnings []string
	for _, image := range images {
		report(fmt.Sprintf("Сканирую образ %s...", image))
		imageFindings, err := vuln.ScanImage(ctx, trivyBin, dbDir, image, s.cfg.DockerSocket, s.cfg.PodmanSocket)
		if err != nil {
			// One image gone missing (removed between the container list
			// being read and the scan reaching it) or unreachable must not
			// throw away everything else already scanned — noted instead,
			// same as any other SourceStatus.Warnings-style degrade.
			warnings = append(warnings, fmt.Sprintf("%s: %s", image, err.Error()))
			continue
		}
		for i := range imageFindings {
			imageFindings[i].Target = image
		}
		findings = append(findings, imageFindings...)
	}

	compared, newCount, fixedCount, err := s.applyVulnDiff(ctx, findings)
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
		Warnings:   warnings,
		DBUpdated:  vuln.DBUpdatedAt(dbDir),
		ScannedAt:  time.Now(),
	}
	s.vuln.mu.Lock()
	s.vuln.result = result
	s.vuln.scanning = false
	s.vuln.progress = ""
	s.vuln.mu.Unlock()
}

// runningImages returns the deduplicated set of image references currently
// in use by a Docker or Podman container on this host, from the latest
// scan snapshot — already collected for the regular dashboard (Containers/
// Docker page), so this adds no extra work to gather.
func (s *Server) runningImages(ctx context.Context) []string {
	snap, err := s.scanner.LatestOrScan(ctx)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var images []string
	add := func(ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		images = append(images, ref)
	}
	for _, c := range snap.Container {
		add(c.Image)
	}
	for _, c := range snap.Podman {
		add(c.Image)
	}
	return images
}
