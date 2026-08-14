package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/analyze"
	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/store"
	"github.com/althq/netknownsthat/internal/topology"
)

// certSummary condenses the certificate state for the dashboard landing page:
// how many are already broken, and how many will break without intervention.
func certSummary(snap *model.Snapshot) map[string]any {
	var expired, expiring, unreadable, unmanaged int
	soonest := -1
	soonestName := ""

	for _, c := range snap.Certs {
		switch {
		case c.Error != "":
			unreadable++
			continue
		case c.DaysLeft < 0:
			expired++
		case c.DaysLeft <= 30:
			expiring++
		}
		if !c.Renewal.Automatic {
			unmanaged++
		}
		if c.DaysLeft >= 0 && (soonest < 0 || c.DaysLeft < soonest) {
			soonest = c.DaysLeft
			soonestName = strings.Join(c.Names, ", ")
		}
	}

	return map[string]any{
		"total":        len(snap.Certs),
		"expired":      expired,
		"expiring":     expiring,
		"unreadable":   unreadable,
		"unmanaged":    unmanaged,
		"soonest_days": soonest,
		"soonest_name": soonestName,
	}
}

// auditFilter reads the audit query parameters.
func auditFilter(r *http.Request) store.AuditFilter {
	return store.AuditFilter{
		Username: r.URL.Query().Get("username"),
		Action:   r.URL.Query().Get("action"),
		Result:   r.URL.Query().Get("result"),
		Since:    r.URL.Query().Get("from"),
		Limit:    intParam(r, "limit", 200),
		Offset:   intParam(r, "offset", 0),
	}
}

// handleOverview builds the dashboard landing payload in one round trip.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}

	publicEndpoints, tlsEndpoints := 0, 0
	for _, e := range snap.Endpoints {
		if e.Public() {
			publicEndpoints++
		}
		if e.TLS {
			tlsEndpoints++
		}
	}
	running, declared := 0, 0
	for _, c := range snap.Container {
		if c.Running {
			running++
		}
		if c.Declared {
			declared++
		}
	}

	statuses, err := s.db.TargetStatuses(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	up, down, uptimeSum, uptimeN := 0, 0, 0.0, 0
	for _, st := range statuses {
		if st.LastOK != nil {
			if *st.LastOK {
				up++
			} else {
				down++
			}
		}
		if st.Checks24h > 0 {
			uptimeSum += st.Uptime24h
			uptimeN++
		}
	}
	avgUptime := 0.0
	if uptimeN > 0 {
		avgUptime = uptimeSum / float64(uptimeN)
	}

	outages, err := s.db.RecentOutages(r.Context(), sinceParam(r, 24*time.Hour), 5)
	if err != nil {
		fail(w, err)
		return
	}

	top := snap.Findings
	if len(top) > 12 {
		top = top[:12]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"host":      snap.Host,
		"mode":      snap.Mode,
		"scanned":   snap.TS,
		"scan_ms":   snap.ScanMS,
		"simulated": s.cfg.IsFixtures(),
		"counts": map[string]int{
			"endpoints":           len(snap.Endpoints),
			"endpoints_public":    publicEndpoints,
			"endpoints_tls":       tlsEndpoints,
			"upstreams":           len(snap.Upstreams),
			"containers":          len(snap.Container),
			"containers_running":  running,
			"containers_declared": declared,
			"networks":            len(snap.Networks),
			"firewall_rules":      len(snap.Firewall.Rules),
			"listeners":           len(snap.Listeners),
			"config_files":        len(snap.Files),
			"certificates":        len(snap.Certs),
		},
		"certificates":    certSummary(snap),
		"package_updates": snap.Packages,
		// The version actually serving this — the hub's poller reads it
		// from here to show what is really running on a host, as opposed
		// to what it recorded having installed there.
		"version":      s.version,
		"findings":     snap.FindingCounts(),
		"top_findings": top,
		"services":     snap.Services,
		"sources":      snap.Sources,
		"firewall": map[string]any{
			"ufw_active": snap.Firewall.UFWActive,
			"ufw_policy": snap.Firewall.UFWPolicy,
			"backends":   snap.Firewall.Backends,
			"policies":   snap.Firewall.Policies,
		},
		"availability": map[string]any{
			"targets":           len(statuses),
			"up":                up,
			"down":              down,
			"avg_uptime":        avgUptime,
			"outages":           outages,
			"metrics_simulated": s.scheduler != nil && s.scheduler.MetricsSimulated(),
		},
	})
}

func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.Scan(r.Context())
	if err != nil {
		s.db.Audit(r.Context(), auth.Username(r.Context()), "inventory.refresh", "", "error", err.Error())
		fail(w, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "inventory.refresh", "", "ok",
		map[string]any{"findings": len(snap.Findings), "scan_ms": snap.ScanMS})
	writeJSON(w, http.StatusOK, map[string]any{
		"scanned":  snap.TS,
		"scan_ms":  snap.ScanMS,
		"findings": snap.FindingCounts(),
		"digest":   snap.Digest,
	})
}

func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	severity := r.URL.Query().Get("severity")
	service := r.URL.Query().Get("service")
	rule := r.URL.Query().Get("rule")

	out := make([]model.Finding, 0, len(snap.Findings))
	for _, f := range snap.Findings {
		if severity != "" && f.Severity != severity {
			continue
		}
		if service != "" && f.Service != service {
			continue
		}
		if rule != "" && f.Rule != rule {
			continue
		}
		out = append(out, f)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"findings": out,
		"counts":   snap.FindingCounts(),
		"total":    len(snap.Findings),
	})
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, topology.Build(snap))
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"services":        snap.Services,
		"allow_mutations": s.cfg.AllowMutations,
	})
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"containers": snap.Container,
		"networks":   snap.Networks,
	})
}

// handleMisc lists sockets something on the host is actually listening on
// that don't match any endpoint the config parsers found — the same set
// analyze.ruleListeningNotDeclared turns into findings, here as a plain
// service list instead ("Разное" tab: services the configs don't know
// about, not a problem report about them).
func (s *Server) handleMisc(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	listeners := analyze.UndeclaredListeners(snap)
	writeJSON(w, http.StatusOK, map[string]any{
		"listeners": listeners,
	})
}

// handleInterfaces lists every network interface on the host — physical
// NICs, bridges, VLANs, tunnels, loopback — as a plain inventory. Deliberately
// no "public" verdict here: Listener.Public() already answers that per
// socket, and guessing which interface counts as "the" public one at the
// interface level would just be a second, coarser answer to a question
// already answered precisely elsewhere.
func (s *Server) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"interfaces": snap.Interfaces,
	})
}

func (s *Server) handleFirewall(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	backend := r.URL.Query().Get("backend")
	rules := snap.Firewall.Rules
	if backend != "" {
		filtered := make([]model.FirewallRule, 0, len(rules))
		for _, rule := range rules {
			if rule.Backend == backend {
				filtered = append(filtered, rule)
			}
		}
		rules = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ufw_active": snap.Firewall.UFWActive,
		"ufw_policy": snap.Firewall.UFWPolicy,
		"backends":   snap.Firewall.Backends,
		"policies":   snap.Firewall.Policies,
		"rules":      rules,
		"listeners":  snap.Listeners,
	})
}

// handleFirewallNumbered lists ufw's rules two ways: numbered (what
// DeleteRule's index refers to — empty while ufw is inactive, since
// `ufw status numbered` prints nothing but "Status: inactive" then) and
// added (what ufw actually has stored regardless of active state — the
// only way to know a rule exists at all on a host where ufw isn't
// currently turned on).
func (s *Server) handleFirewallNumbered(w http.ResponseWriter, r *http.Request) {
	rules, err := s.firewall.NumberedRules(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	added, err := s.firewall.AddedRules(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules, "added": added})
}

// handleCertificates returns the TLS certificates with their renewal state,
// ordered so that whatever needs attention soonest comes first.
func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}

	expired, expiring, unreadable, unmanaged := 0, 0, 0, 0
	for _, c := range snap.Certs {
		switch {
		case c.Error != "":
			unreadable++
		case c.DaysLeft < 0:
			expired++
		case c.DaysLeft <= 30:
			expiring++
		}
		if c.Error == "" && !c.Renewal.Automatic {
			unmanaged++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"certificates": snap.Certs,
		"summary": map[string]int{
			"total":      len(snap.Certs),
			"expired":    expired,
			"expiring":   expiring,
			"unreadable": unreadable,
			"unmanaged":  unmanaged,
		},
	})
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListSnapshots(r.Context(), intParam(r, "limit", 30))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.db.ListAudit(r.Context(), auditFilter(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// ------------------------------------------------------------------- actions

func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	action := chi.URLParam(r, "action")
	user := auth.Username(r.Context())

	res, err := s.services.Action(r.Context(), user, name, action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"output":    strings.TrimSpace(res.Output()),
		"simulated": res.Simulated,
	})
}

func (s *Server) handleServiceValidate(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user := auth.Username(r.Context())

	res, ok, err := s.services.ValidateOnly(r.Context(), user, name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"validated": ok,
		"valid":     res.OK(),
		"output":    strings.TrimSpace(res.Output()),
		"simulated": res.Simulated,
	})
}

func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	action := chi.URLParam(r, "action")
	user := auth.Username(r.Context())

	if err := s.services.ContainerAction(r.Context(), user, name, action); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
