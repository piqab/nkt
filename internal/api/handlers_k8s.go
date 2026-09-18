package api

import (
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
)

// Kubernetes на хосте (см. internal/k8s): состояние и узлы, установка
// роли заданием, токен и kubeconfig для хаба, поды для вкладки.

func (s *Server) k8sManager() *k8s.Manager {
	var run control.PrivilegedRunner
	if s.cfg.Mode == config.ModeLocal {
		run = RunUnrestricted
	}
	return k8s.New(s.scanner.Collector(), run)
}

func (s *Server) handleK8sStatus(w http.ResponseWriter, r *http.Request) {
	m := s.k8sManager()
	st := m.Status(r.Context())
	resp := map[string]any{"status": st}
	if st.Installed && st.Role == k8s.RoleServer && st.Active {
		nodes, err := m.Nodes(r.Context())
		if err != nil {
			resp["nodes_error"] = msgs.Localize(msgs.LangFromRequest(r), err)
		} else {
			resp["nodes"] = nodes
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleK8sInstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "k8s.fixturesDisabled"))
		return
	}
	var spec k8s.InstallSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := spec.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.T(msgs.LangFromRequest(r), "api.backgroundJobsAreUnavailable"))
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: k8s.KindInstall, Title: msgs.Tc(r.Context(), "k8s.installJobTitle", spec.Flavor, spec.Role),
		Queue: "host", Author: user, Steps: len(k8s.Steps(spec)), Params: spec,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "k8s.install", spec.Flavor+"/"+spec.Role, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

func (s *Server) handleK8sJoin(w http.ResponseWriter, r *http.Request) {
	info, err := s.k8sManager().Join(r.Context(), r.URL.Query().Get("server"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleK8sKubeconfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.k8sManager().Kubeconfig(r.Context(), r.URL.Query().Get("server"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write([]byte(cfg))
}

func (s *Server) handleK8sPods(w http.ResponseWriter, r *http.Request) {
	raw, err := s.k8sManager().Pods(r.Context(), r.URL.Query().Get("namespace"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (s *Server) handleK8sUninstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "k8s.fixturesDisabled"))
		return
	}
	user := auth.Username(r.Context())
	err := s.k8sManager().Uninstall(r.Context())
	s.db.Audit(r.Context(), user, "k8s.uninstall", "", auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
