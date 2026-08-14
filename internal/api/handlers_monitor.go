package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/store"
)

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.db.TargetStatuses(r.Context())
	if err != nil {
		fail(w, err)
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
		writeError(w, http.StatusBadRequest, "Некорректный идентификатор цели")
		return
	}
	target, err := s.db.TargetByID(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	buckets, err := s.db.AvailabilityBuckets(r.Context(), id,
		sinceParam(r, 7*24*time.Hour), granularityParam(r, "hour"), tzParam(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target, "buckets": buckets})
}

func (s *Server) handleAvailabilityHeatmap(w http.ResponseWriter, r *http.Request) {
	id := int64(intParam(r, "target", 0))
	cells, err := s.db.AvailabilityHeatmap(r.Context(), id, sinceParam(r, 14*24*time.Hour), tzParam(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cells": cells, "target": id})
}

func (s *Server) handleOutages(w http.ResponseWriter, r *http.Request) {
	outages, err := s.db.RecentOutages(r.Context(), sinceParam(r, 7*24*time.Hour), intParam(r, "limit", 50))
	if err != nil {
		fail(w, err)
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

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	q := metricQuery(r)
	points, err := s.db.MetricSeries(r.Context(), q)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"points":    points,
		"source":    q.Source,
		"metric":    q.Metric,
		"simulated": s.metricsSimulated(),
	})
}

func (s *Server) handleUsageTop(w http.ResponseWriter, r *http.Request) {
	source := defaultParam(r, "source", "docker")
	metric := defaultParam(r, "metric", "net_rx_bytes")
	rows, err := s.db.MetricTop(r.Context(), source, metric,
		sinceParam(r, 24*time.Hour), intParam(r, "limit", 10))
	if err != nil {
		fail(w, err)
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
		fail(w, err)
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
		writeError(w, http.StatusBadRequest, "Некорректный идентификатор цели")
		return
	}
	target, err := s.db.TargetByID(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if s.scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "Планировщик не запущен")
		return
	}
	result := s.scheduler.Prober().ProbeTarget(r.Context(), target)
	if err := s.db.InsertProbeResults(r.Context(), []store.ProbeResult{result}); err != nil {
		fail(w, err)
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
		writeError(w, http.StatusBadRequest, "Некорректный идентификатор цели")
		return
	}
	var req targetPatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "Нечего менять")
		return
	}
	if err := s.db.SetTargetEnabled(r.Context(), id, *req.Enabled); err != nil {
		fail(w, err)
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.firewall.AddRule(r.Context(), auth.Username(r.Context()), spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, "Некорректный номер правила")
		return
	}
	var req firewallDeleteRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	res, err := s.firewall.DeleteRule(r.Context(), auth.Username(r.Context()), number, req.Expected)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.firewall.DeleteRuleBySpec(r.Context(), auth.Username(r.Context()), spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
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
