// Package inventory runs every parser, assembles one snapshot of the host and
// keeps the most recent result in memory for the API to serve.
package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/althq/netknownsthat/internal/analyze"
	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/parse"
	"github.com/althq/netknownsthat/internal/store"
)

// Scanner produces host snapshots.
type Scanner struct {
	cfg *config.Config
	c   collect.Collector
	db  *store.DB

	mu      sync.RWMutex
	latest  *model.Snapshot
	scanned time.Time
}

// New builds a scanner.
func New(cfg *config.Config, c collect.Collector, db *store.DB) *Scanner {
	return &Scanner{cfg: cfg, c: c, db: db}
}

// Latest returns the cached snapshot, or nil when no scan has finished yet.
func (s *Scanner) Latest() *model.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest
}

// LatestOrScan returns the cached snapshot, running a scan when there is none.
func (s *Scanner) LatestOrScan(ctx context.Context) (*model.Snapshot, error) {
	if snap := s.Latest(); snap != nil {
		return snap, nil
	}
	return s.Scan(ctx)
}

// Collector exposes the host access layer to other packages.
func (s *Scanner) Collector() collect.Collector { return s.c }

// Scan reads the whole host and stores the result.
func (s *Scanner) Scan(ctx context.Context) (*model.Snapshot, error) {
	started := time.Now()
	hostInfo := s.c.HostInfo(ctx)

	snap := &model.Snapshot{
		TS:   store.Now(),
		Mode: s.c.Mode(),
		Host: model.HostInfo{
			Mode:     hostInfo.Mode,
			Hostname: hostInfo.Hostname,
			Kernel:   hostInfo.Kernel,
			OS:       hostInfo.OS,
			Notes:    hostInfo.Notes,
		},
	}

	// The parsers touch disjoint parts of the host, so run them concurrently.
	var (
		wg        sync.WaitGroup
		nginxRes  parse.NginxResult
		hapRes    parse.HAProxyResult
		dockerRes parse.DockerResult
		fwRes     parse.FirewallResult
		listeners []model.Listener
		lisStatus model.SourceStatus
	)
	wg.Add(5)
	go func() { defer wg.Done(); nginxRes = parse.Nginx(ctx, s.c, s.cfg.NginxMainConfig) }()
	go func() { defer wg.Done(); hapRes = parse.HAProxy(ctx, s.c, s.cfg.HAProxyMainConf) }()
	go func() { defer wg.Done(); dockerRes = parse.Docker(ctx, s.c, s.cfg.ComposeFiles) }()
	go func() { defer wg.Done(); fwRes = parse.Firewall(ctx, s.c) }()
	go func() { defer wg.Done(); listeners, lisStatus = parse.Listeners(ctx, s.c) }()
	wg.Wait()

	snap.Sources = []model.SourceStatus{
		nginxRes.Status, hapRes.Status, dockerRes.Status, fwRes.Status, lisStatus,
	}
	snap.Endpoints = append(append(append([]model.Endpoint{},
		nginxRes.Endpoints...), hapRes.Endpoints...), dockerRes.Endpoints...)
	snap.Upstreams = append(append([]model.Upstream{}, nginxRes.Upstreams...), hapRes.Upstreams...)
	snap.Files = append(append([]model.ManagedFile{}, nginxRes.Files...), hapRes.Files...)
	snap.Files = append(snap.Files, dockerRes.Files...)
	snap.Container = dockerRes.Containers
	snap.Networks = dockerRes.Networks
	snap.Firewall = fwRes.State
	snap.Listeners = listeners

	// Global nginx settings live outside any server block but still matter.
	applyGlobalNginx(snap, nginxRes.Global)

	// Certificates are read after the endpoints, since the paths come from them.
	certs := parse.Certificates(ctx, s.c, snap.Endpoints)
	if !s.cfg.IsFixtures() {
		// A snapshot has no real sockets to dial; on a real host this is what
		// catches a renewed file nobody reloaded into the service.
		checkLiveCertificates(ctx, certs.Certs, snap.Endpoints, s.cfg.ProbeTimeout)
	}
	snap.Certs = certs.Certs
	snap.Sources = append(snap.Sources, certs.Status)

	// Service catalogue, with the config files each service actually uses.
	specs := parse.DefaultServiceSpecs()
	for i := range specs {
		switch specs[i].Name {
		case model.ServiceNginx:
			specs[i].ConfigFiles = fileList(nginxRes.Files)
		case model.ServiceHAProxy:
			specs[i].ConfigFiles = fileList(hapRes.Files)
		case model.ServiceDocker:
			specs[i].ConfigFiles = fileList(dockerRes.Files)
		}
	}
	services, svcStatus := parse.Services(ctx, s.c, specs)
	snap.Services = services
	snap.Sources = append(snap.Sources, svcStatus)

	sort.Slice(snap.Endpoints, func(i, j int) bool {
		if snap.Endpoints[i].Port != snap.Endpoints[j].Port {
			return snap.Endpoints[i].Port < snap.Endpoints[j].Port
		}
		return snap.Endpoints[i].Service < snap.Endpoints[j].Service
	})
	sort.Slice(snap.Files, func(i, j int) bool { return snap.Files[i].Path < snap.Files[j].Path })

	snap.Findings = analyze.Run(snap)
	snap.ScanMS = time.Since(started).Milliseconds()
	snap.Digest = digest(snap)

	s.mu.Lock()
	s.latest = snap
	s.scanned = time.Now()
	s.mu.Unlock()

	if s.db != nil {
		if err := s.persist(ctx, snap); err != nil {
			return snap, fmt.Errorf("сохранение снапшота: %w", err)
		}
	}
	return snap, nil
}

// applyGlobalNginx propagates http-level directives onto the endpoints that
// inherit them, so the analyzer can reason per listener.
func applyGlobalNginx(snap *model.Snapshot, global map[string]string) {
	if len(global) == 0 {
		return
	}
	for i := range snap.Endpoints {
		e := &snap.Endpoints[i]
		if e.Service != model.ServiceNginx {
			continue
		}
		if e.Extra == nil {
			e.Extra = map[string]string{}
		}
		if e.Extra["ssl_protocols"] == "" && global["ssl_protocols"] != "" && e.TLS {
			e.Extra["ssl_protocols"] = global["ssl_protocols"]
		}
		if global["server_tokens"] != "" {
			e.Extra["server_tokens"] = global["server_tokens"]
		}
	}
}

func fileList(files []model.ManagedFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// digest fingerprints everything except the timestamp, so an unchanged host
// produces an unchanged digest and the snapshot history stays meaningful.
func digest(snap *model.Snapshot) string {
	clone := *snap
	clone.TS = ""
	clone.ScanMS = 0
	clone.Digest = ""
	raw, err := json.Marshal(clone)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Scanner) persist(ctx context.Context, snap *model.Snapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if _, _, err := s.db.SaveSnapshot(ctx, snap.Digest, string(raw)); err != nil {
		return err
	}
	return s.syncTargets(ctx, snap)
}
