// Package monitor collects the time series behind the availability and usage
// schedules: reachability probes, firewall and container counters, and request
// rates derived from access logs.
package monitor

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/store"
)

// maxParallelProbes bounds how many checks run at once. Probes are almost pure
// wait, so a generous number costs nothing and keeps a round short.
const maxParallelProbes = 16

// Prober checks every enabled target and records the outcome.
type Prober struct {
	db  *store.DB
	cfg *config.Config

	client *http.Client
}

// NewProber builds the availability prober.
func NewProber(db *store.DB, cfg *config.Config) *Prober {
	return &Prober{
		db:  db,
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.ProbeTimeout,
			// Internal endpoints routinely use self-signed or internal-CA
			// certificates; the probe answers "is it up", not "is it trusted".
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
				DisableKeepAlives:   true,
				MaxIdleConnsPerHost: 1,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// RunOnce probes every enabled target and stores the results.
func (p *Prober) RunOnce(ctx context.Context) (int, error) {
	targets, err := p.db.ListTargets(ctx, true)
	if err != nil {
		return 0, fmt.Errorf("список целей: %w", err)
	}
	if len(targets) == 0 {
		return 0, nil
	}

	results := make([]store.ProbeResult, len(targets))
	sem := make(chan struct{}, maxParallelProbes)
	var wg sync.WaitGroup

	for i, t := range targets {
		wg.Add(1)
		go func(i int, t store.Target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = p.probe(ctx, t)
		}(i, t)
	}
	wg.Wait()

	if err := p.db.InsertProbeResults(ctx, results); err != nil {
		return 0, fmt.Errorf("сохранение результатов: %w", err)
	}
	return len(results), nil
}

// ProbeTarget runs a single check without storing it, for the "check now" button.
func (p *Prober) ProbeTarget(ctx context.Context, t store.Target) store.ProbeResult {
	return p.probe(ctx, t)
}

// Simulated reports whether probe results are synthetic rather than measured.
func (p *Prober) Simulated() bool { return p.cfg.IsFixtures() }

func (p *Prober) probe(ctx context.Context, t store.Target) store.ProbeResult {
	if p.Simulated() {
		// A canned host snapshot describes sockets that do not exist on this
		// machine; dialling them would only measure the local firewall. The
		// result is shaped instead, and reported to the UI as simulated.
		return simulateProbe(t)
	}

	started := time.Now()
	res := store.ProbeResult{TargetID: t.ID, TS: store.Now()}

	switch t.Kind {
	case "http", "https":
		res.StatusCode, res.Error = p.probeHTTP(ctx, t)
	default:
		res.Error = p.probeTCP(ctx, t)
	}
	res.LatencyMS = float64(time.Since(started).Microseconds()) / 1000
	res.OK = res.Error == ""
	return res
}

func (p *Prober) probeTCP(ctx context.Context, t store.Target) string {
	dialer := net.Dialer{Timeout: p.cfg.ProbeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(t.Host, itoa(t.Port)))
	if err != nil {
		return shortenNetError(err)
	}
	_ = conn.Close()
	return ""
}

func (p *Prober) probeHTTP(ctx context.Context, t store.Target) (int, string) {
	path := t.Path
	if path == "" {
		path = "/"
	}
	url := fmt.Sprintf("%s://%s%s", t.Kind, net.JoinHostPort(t.Host, itoa(t.Port)), path)

	reqCtx, cancel := context.WithTimeout(ctx, p.cfg.ProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("User-Agent", "NetKnownsThat/1.0 (availability probe)")
	if t.HostHeader != "" {
		// Name-based virtual hosts answer differently depending on Host, so a
		// probe without it would test the default server instead of this one.
		req.Host = t.HostHeader
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, shortenNetError(err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// 5xx means the endpoint answered but is broken; that counts as a failure.
	if resp.StatusCode >= 500 {
		return resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, ""
}

// simulateProbe produces a deterministic, plausible check result for fixtures
// mode. Remote backends of the fixture host are unreachable by design, so they
// stay down and the outage views have something real to show.
func simulateProbe(t store.Target) store.ProbeResult {
	now := time.Now()
	res := store.ProbeResult{TargetID: t.ID, TS: store.FormatTime(now), OK: true}

	unreachable := strings.HasPrefix(t.Host, "10.10.0.") && t.Port == 9000
	// One flaky target in every eight, chosen by a stable hash of its key.
	flaky := hashRange(t.Key, 8) == 0 && now.Minute()%17 < 3

	switch {
	case unreachable:
		res.OK = false
		res.Error = "connect: connection refused"
		res.LatencyMS = 5000
	case flaky:
		res.OK = false
		res.Error = "i/o timeout"
		res.LatencyMS = float64(p95Timeout)
	default:
		res.LatencyMS = math.Round((2+dailyShape(now, t.Key)*float64(4+hashRange(t.Key, 30)))*100) / 100
		if t.Kind != "tcp" {
			res.StatusCode = 200
		}
	}
	return res
}

// p95Timeout is the latency recorded for a simulated timeout, in milliseconds.
const p95Timeout = 5000

// shortenNetError trims Go's verbose dial errors down to the part an operator
// actually reads.
func shortenNetError(err error) string {
	msg := err.Error()
	for _, marker := range []string{": connect: ", ": dial tcp ", ": read: ", ": write: "} {
		if i := strings.LastIndex(msg, marker); i >= 0 {
			msg = strings.TrimSpace(msg[i+len(marker):])
		}
	}
	if i := strings.LastIndex(msg, ": "); i >= 0 && len(msg)-i < 60 {
		msg = strings.TrimSpace(msg[i+2:])
	}
	if len(msg) > 160 {
		msg = msg[:160] + "…"
	}
	return msg
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
