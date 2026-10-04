package api

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/monitor"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.db.TargetStatuses(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"targets":   statuses,
		"simulated": s.cfg.IsFixtures(),
		"interval":  s.cfg.ProbeInterval.String(),
	})
}

func (s *Server) handleTargetHistory(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "monitor.invalidTargetId"))
		return
	}
	target, err := s.db.TargetByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	buckets, err := s.db.AvailabilityBuckets(r.Context(), id,
		sinceParam(r, 7*24*time.Hour), granularityParam(r, "hour"), tzParam(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target, "buckets": buckets})
}

func (s *Server) handleAvailabilityHeatmap(w http.ResponseWriter, r *http.Request) {
	id := int64(intParam(r, "target", 0))
	cells, err := s.db.AvailabilityHeatmap(r.Context(), id, sinceParam(r, 14*24*time.Hour), tzParam(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cells": cells, "target": id})
}

func (s *Server) handleOutages(w http.ResponseWriter, r *http.Request) {
	outages, err := s.db.RecentOutages(r.Context(), sinceParam(r, 7*24*time.Hour), intParam(r, "limit", 50))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"outages": outages})
}

// metricQuery builds a usage query from the request parameters.
func metricQuery(r *http.Request) store.MetricQuery {
	q := store.MetricQuery{
		Source:      defaultParam(r, "source", "docker"),
		Metric:      defaultParam(r, "metric", "cpu_pct"),
		Since:       sinceParam(r, 24*time.Hour),
		Granularity: granularityParam(r, "hour"),
		TZOffset:    tzParam(r),
		Aggregate:   defaultParam(r, "agg", "sum"),
	}
	if raw := r.URL.Query().Get("subjects"); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				q.Subjects = append(q.Subjects, s)
			}
		}
	}
	return q
}

// usageTotal returns the reference ceiling the frontend draws as a dashed
// line on the CPU/memory usage charts — "100%" of what, or how many bytes
// of what — computed from the latest scan's HostCapacity (see
// parse.HostCapacity), not a fresh one: a chart annotation is not worth
// forcing a scan for, and total host memory/CPU practically never changes
// between scans anyway. nil (omitted from the response) whenever the
// figure isn't meaningful for this metric or wasn't available from the
// last scan (a container/restricted environment without a real /proc).
func (s *Server) usageTotal(q store.MetricQuery) *float64 {
	if s.scanner == nil {
		return nil
	}
	snap := s.scanner.Latest()
	if snap == nil {
		return nil
	}
	workload := q.Source == "docker" || q.Source == "podman" || q.Source == "lxd" || q.Source == "libvirt"
	switch {
	case workload && q.Metric == "cpu_pct" && snap.Capacity.CPUCores > 0:
		total := float64(snap.Capacity.CPUCores) * 100
		return &total
	case workload && q.Metric == "mem_bytes" && snap.Capacity.MemTotalBytes > 0:
		total := float64(snap.Capacity.MemTotalBytes)
		return &total
	default:
		return nil
	}
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	q := metricQuery(r)
	points, err := s.db.MetricSeries(r.Context(), q)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"points":    points,
		"source":    q.Source,
		"metric":    q.Metric,
		"simulated": s.metricsSimulated(),
		"total":     s.usageTotal(q),
	})
}

// handleUsageSources — источники с данными за сутки: «Нагрузка» открывает
// первый работающий, а не Docker, которого на хосте может не быть.
func (s *Server) handleUsageSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.MetricSources(r.Context(), sinceParam(r, 24*time.Hour))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": list})
}

// k8sCluster — состав кластера для «Нагрузки» и сводки хаба: узлы с
// ролью и узел каждого пода («namespace/под» → узел). Берётся из
// последнего скана — отдельных вызовов kubectl не нужно.
type k8sCluster struct {
	Nodes []model.K8sNode   `json:"nodes"`
	Pods  map[string]string `json:"pods"`
}

func (s *Server) k8sCluster() *k8sCluster {
	if s.scanner == nil {
		return nil
	}
	snap := s.scanner.Latest()
	if snap == nil || snap.K8s == nil {
		return nil
	}
	out := &k8sCluster{Nodes: snap.K8s.Nodes, Pods: map[string]string{}}
	if out.Nodes == nil {
		out.Nodes = []model.K8sNode{}
	}
	for _, p := range snap.K8s.Pods {
		if p.Node != "" {
			out.Pods[p.Namespace+"/"+p.Name] = p.Node
		}
	}
	return out
}

// handleUsageK8s — GET /monitor/usage/k8s: узлы и узлы подов (пусто, если
// это не control plane).
func (s *Server) handleUsageK8s(w http.ResponseWriter, r *http.Request) {
	c := s.k8sCluster()
	if c == nil {
		c = &k8sCluster{Nodes: []model.K8sNode{}, Pods: map[string]string{}}
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleUsageTop(w http.ResponseWriter, r *http.Request) {
	source := defaultParam(r, "source", "docker")
	metric := defaultParam(r, "metric", "net_rx_bytes")
	rows, err := s.db.MetricTop(r.Context(), source, metric,
		sinceParam(r, 24*time.Hour), min(intParam(r, "limit", 10), 2000))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"top": rows, "source": source, "metric": metric})
}

func (s *Server) handleUsageHeatmap(w http.ResponseWriter, r *http.Request) {
	source := defaultParam(r, "source", "iptables")
	metric := defaultParam(r, "metric", "bytes")
	cells, err := s.db.UsageHeatmap(r.Context(), source, metric, r.URL.Query().Get("subject"),
		sinceParam(r, 14*24*time.Hour), tzParam(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cells": cells, "source": source, "metric": metric, "simulated": s.metricsSimulated(),
	})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	var jobs any
	if s.scheduler != nil {
		jobs = s.scheduler.Status()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs": jobs,
		"intervals": map[string]string{
			"probes":    s.cfg.ProbeInterval.String(),
			"metrics":   s.cfg.MetricsInterval.String(),
			"logs":      s.cfg.LogScanInterval.String(),
			"inventory": s.cfg.InventoryInterval.String(),
			"retention": s.cfg.Retention.String(),
		},
		"enabled": s.cfg.SchedulerEnabled,
	})
}

func (s *Server) handleTargetCheck(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "monitor.invalidTargetId"))
		return
	}
	target, err := s.db.TargetByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	if s.scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.T(msgs.LangFromRequest(r), "monitor.schedulerNotRunning"))
		return
	}
	result := s.scheduler.Prober().ProbeTarget(r.Context(), target)
	if err := s.db.InsertProbeResults(r.Context(), []store.ProbeResult{result}); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target, "result": result})
}

type targetPatchRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (s *Server) handleTargetPatch(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "monitor.invalidTargetId"))
		return
	}
	var req targetPatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "monitor.nothingToChange"))
		return
	}
	if err := s.db.SetTargetEnabled(r.Context(), id, *req.Enabled); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "monitor.target", chi.URLParam(r, "id"), "ok",
		map[string]any{"enabled": *req.Enabled})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ------------------------------------------------------------------- firewall

func (s *Server) handleFirewallAdd(w http.ResponseWriter, r *http.Request) {
	var spec control.RuleSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	res, err := s.firewall.AddRule(r.Context(), auth.Username(r.Context()), spec)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
}

type firewallDeleteRequest struct {
	Expected string `json:"expected"`
}

func (s *Server) handleFirewallDelete(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "monitor.invalidRuleNumber"))
		return
	}
	var req firewallDeleteRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	res, err := s.firewall.DeleteRule(r.Context(), auth.Username(r.Context()), number, req.Expected)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
}

// handleFirewallDeleteBySpec removes a rule by the exact specification
// that would have added it, rather than by ufw's positional index —
// DeleteRule's index comes from `ufw status numbered`, which shows
// nothing at all while ufw is inactive, so a rule added while it was off
// has no numbered index to delete by until ufw is turned on.
func (s *Server) handleFirewallDeleteBySpec(w http.ResponseWriter, r *http.Request) {
	var spec control.RuleSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	res, err := s.firewall.DeleteRuleBySpec(r.Context(), auth.Username(r.Context()), spec)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
}

func (s *Server) handleFirewallReload(w http.ResponseWriter, r *http.Request) {
	res, err := s.firewall.Reload(r.Context(), auth.Username(r.Context()))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
}

// rescanLater refreshes the inventory after a change, detached from the request
// so the response is not held up and the scan is not cancelled with it.
func (s *Server) rescanLater() {
	go func() { _, _ = s.scanner.Scan(context.Background()) }()
}

func (s *Server) metricsSimulated() bool {
	return s.scheduler != nil && s.scheduler.MetricsSimulated()
}

func defaultParam(r *http.Request, name, def string) string {
	if v := r.URL.Query().Get(name); v != "" {
		return v
	}
	return def
}

// summarySources — ряды, которые «Мониторинг» хаба берёт с хоста.
var summarySources = []string{monitor.SourceHost, monitor.SourceDisk, monitor.SourceDocker, monitor.SourcePodman,
	monitor.SourceLXD, monitor.SourceLibvirt, monitor.SourceK8s, monitor.SourceK8sNode}

// summaryMaxSpan — сколько часов отдаётся за раз (хаб дозабирает частями).
const summaryMaxSpan = 31 * 24 * time.Hour

type summarySeries struct {
	Source  string `json:"source"`
	Subject string `json:"subject"`
	Metric  string `json:"metric"`
	// Points — [час, среднее, пик, сумма].
	Points [][4]any `json:"points"`
}

type summaryTarget struct {
	ID      int64  `json:"id"`
	Key     string `json:"key"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Service string `json:"service"`
	Enabled bool   `json:"enabled"`
	// Points — [час, удачных, всего, средняя задержка мс].
	Points [][4]any `json:"points"`
}

// handleMonitorSummary — GET /monitor/summary?since=&until=: почасовые
// сводки рядов хоста, контейнеров и машин и проверок доступности за
// полные часы [since, until) (по умолчанию — последние сутки).
func (s *Server) handleMonitorSummary(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC().Truncate(time.Hour)
	until := now
	if v := r.URL.Query().Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		until = t.UTC().Truncate(time.Hour)
		if until.After(now) {
			until = now
		}
	}
	since := until.Add(-24 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		since = t.UTC().Truncate(time.Hour)
	}
	if until.Sub(since) > summaryMaxSpan {
		since = until.Add(-summaryMaxSpan)
	}
	ctx := r.Context()
	fromS, toS := store.FormatTime(since), store.FormatTime(until)
	hm, err := s.db.MetricHourly(ctx, fromS, toS, summarySources)
	if err != nil {
		fail(w, r, err)
		return
	}
	series := []summarySeries{}
	for _, m := range hm {
		n := len(series)
		if n == 0 || series[n-1].Source != m.Source || series[n-1].Subject != m.Subject || series[n-1].Metric != m.Metric {
			series = append(series, summarySeries{Source: m.Source, Subject: m.Subject, Metric: m.Metric})
			n++
		}
		series[n-1].Points = append(series[n-1].Points, [4]any{m.Hour, round2(m.Avg), round2(m.Max), round2(m.Sum)})
	}
	targets, err := s.db.ListTargets(ctx, false)
	if err != nil {
		fail(w, r, err)
		return
	}
	hp, err := s.db.ProbeHourly(ctx, fromS, toS)
	if err != nil {
		fail(w, r, err)
		return
	}
	byID := map[int64]*summaryTarget{}
	outT := make([]summaryTarget, 0, len(targets))
	for _, t := range targets {
		outT = append(outT, summaryTarget{ID: t.ID, Key: t.Key, Label: t.Label, Kind: t.Kind, Host: t.Host, Port: t.Port,
			Service: t.Service, Enabled: t.Enabled, Points: [][4]any{}})
	}
	for i := range outT {
		byID[outT[i].ID] = &outT[i]
	}
	for _, p := range hp {
		if t := byID[p.TargetID]; t != nil {
			t.Points = append(t.Points, [4]any{p.Hour, p.OK, p.Total, round2(p.LatencyMS)})
		}
	}
	out := map[string]any{
		"version": 1, "since": fromS, "until": toS, "series": series, "targets": outT,
		"simulated": s.metricsSimulated(),
	}
	if snap := s.scanner.Latest(); snap != nil {
		out["capacity"] = snap.Capacity
	}
	if c := s.k8sCluster(); c != nil {
		out["k8s"] = c
	}
	writeJSON(w, http.StatusOK, out)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
