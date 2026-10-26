package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
)

// Пространство имён клиента (1.17.8): то, что хаб спрашивает у control
// plane, заводя namespace токена и выдавая его kubeconfig.

// handleK8sNamespaceExists — POST /k8s/namespace-exists {"namespace"}.
func (s *Server) handleK8sNamespaceExists(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace string `json:"namespace"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ok, err := s.k8sManager().NamespaceExists(r.Context(), req.Namespace)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"exists": ok})
}

// handleK8sSAKubeconfig — POST /k8s/sa-kubeconfig {"namespace","secret",
// "server"}: kubeconfig учётной записи (секрет в ответе — только хабу).
func (s *Server) handleK8sSAKubeconfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace string `json:"namespace"`
		Secret    string `json:"secret"`
		Server    string `json:"server"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.k8sManager().ServiceAccountKubeconfig(r.Context(), req.Namespace, req.Secret, req.Server)
	s.db.Audit(r.Context(), auth.Username(r.Context()), "k8s.sa_kubeconfig", req.Namespace, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"kubeconfig": cfg})
}

// handleK8sPodLog — GET /k8s/pods/log?namespace=&pod=&container=&tail=.
func (s *Server) handleK8sPodLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	out, err := s.k8sManager().PodLogs(r.Context(), q.Get("namespace"), q.Get("pod"), q.Get("container"), tail)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"log": out})
}

// handleHelmTemplate — POST /k8s/helm/template: манифесты чарта (helm
// template) без установки — хаб проверяет и применяет их сам.
func (s *Server) handleHelmTemplate(w http.ResponseWriter, r *http.Request) {
	var req k8s.HelmInstallRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(req.Values) > k8s.MaxManifest {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("k8s.manifestTooBig", k8s.MaxManifest>>10))
		return
	}
	valuesFile := ""
	if req.Values != "" {
		dir := filepath.Join(s.cfg.DataDir, ".run")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fail(w, r, err)
			return
		}
		f, err := os.CreateTemp(dir, "helm-values-*.yaml")
		if err != nil {
			fail(w, r, err)
			return
		}
		valuesFile = f.Name()
		defer os.Remove(valuesFile)
		_, err = f.WriteString(req.Values)
		_ = f.Close()
		if err != nil {
			fail(w, r, err)
			return
		}
	}
	argv, err := s.k8sManager().HelmTemplateArgv(r.Context(), req, valuesFile)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	res, err := RunTooling(r.Context(), argv...)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if !res.OK() {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("k8s.kubectl", res.Stderr))
		return
	}
	if len(res.Stdout) > k8s.MaxManifest {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("k8s.manifestTooBig", k8s.MaxManifest>>10))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"manifest": res.Stdout})
}
