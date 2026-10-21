package api

import (
	"net/http"
	"strconv"

	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
)

// handleK8sCiliumIngress — POST /k8s/cilium/ingress {port}: включить
// Ingress-контроллер Cilium в режиме host network (порт на каждом узле) —
// заданием на control plane. Текст команд — фиксированный (k8s.
// CiliumIngressScript), снаружи приходит только порт.
func (s *Server) handleK8sCiliumIngress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Port int `json:"port"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.Port == 0 {
		req.Port = 8080
	}
	if req.Port < 1024 || req.Port > 65535 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("script.badK8sIngressPort", strconv.Itoa(req.Port)))
		return
	}
	st := s.k8sManager().Status(r.Context())
	if !st.Installed || st.Role != k8s.RoleServer {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("k8s.ciliumIngressNotCP"))
		return
	}
	cmd := cmdjob.Command{Script: k8s.CiliumIngressScript(st.Flavor, req.Port), StepKey: "k8s.ciliumIngressStep", StepArgs: []any{req.Port}}
	s.startCmdJob(w, r, "k8s.ciliumIngressJob", []any{req.Port}, "k8s:cilium", cmdjob.Params{Commands: []cmdjob.Command{cmd}}, "k8s.cilium.ingress", strconv.Itoa(req.Port))
}
