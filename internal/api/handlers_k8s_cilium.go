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

// handleK8sAddon — POST /k8s/addon {action, name, …}: подготовить узел
// под дополнение (prepare — на любом узле кластера; нужен не всем
// дополнениям) или поставить и обновить его чарт (install — на control
// plane), заданием. Текст команд фиксированный (k8s.AddonPrepareScript,
// k8s.AddonInstallScript), снаружи — только проверенные значения.
func (s *Server) handleK8sAddon(w http.ResponseWriter, r *http.Request) {
	var req k8s.AddonRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m := s.k8sManager()
	st := m.Status(r.Context())
	if !st.Installed {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("k8s.notInstalled"))
		return
	}
	if req.Action == k8s.AddonPrepare {
		cmd := cmdjob.Command{Script: k8s.AddonPrepareScript(req.SkipLocalPath && st.Role == k8s.RoleServer), StepKey: "k8s.addonPrepareStep", StepArgs: []any{req.Name}}
		s.startCmdJob(w, r, "k8s.addonPrepareJob", []any{req.Name}, "k8s:addon", cmdjob.Params{Commands: []cmdjob.Command{cmd}}, "k8s.addon.prepare", req.Name)
		return
	}
	if st.Role != k8s.RoleServer {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("k8s.addonNotCP"))
		return
	}
	// metrics-server уже встроен в k3s — второй экземпляр сломал бы APIService.
	if no := k8s.AddonFlavorConflict(req.Name); no != "" && no == st.Flavor {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("k8s.addonFlavorConflict", req.Name, st.Flavor))
		return
	}
	helm, err := m.HelmArgv(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cmds := addonInstallCommands(s.hubCacheURL(), !m.Helm(r.Context()).Installed, st.Flavor, helm, req)
	s.startCmdJob(w, r, "k8s.addonInstallJob", []any{req.Name, req.Version}, "k8s:addon", cmdjob.Params{Commands: cmds, Refresh: true}, "k8s.addon.install", req.Name+" "+req.Version)
}

// addonInstallCommands — шаги задания установки дополнения: чарт через кэш
// хаба (когда кэш на узле есть; шаг опциональный — не приехал, установка
// сама возьмёт чарт из репозитория через прокси), установка helm, если его
// ещё нет, и сам чарт. Без кэша — прежние два шага, напрямую с узла.
// ChartURL запроса — адрес tarball из index.yaml репозитория (хаб берёт
// его там же, где версии); у Longhorn URL выводится из версии и без него.
func addonInstallCommands(hubCache string, needHelm bool, flavor string, helm []string, req k8s.AddonRequest) []cmdjob.Command {
	var cmds []cmdjob.Command
	if hubCache != "" {
		chartURL := req.ChartURL
		if chartURL == "" && req.Name == k8s.AddonLonghorn {
			chartURL = k8s.LonghornChartURL(req.Version)
		}
		cmds = append(cmds, cmdjob.Command{
			Script: k8s.AddonChartFetchScript(hubCache, req.Name, req.Version, chartURL), StepKey: "k8s.addonChartStep",
			StepArgs: []any{req.Name, req.Version}, Optional: true})
	}
	if needHelm {
		cmds = append(cmds, cmdjob.Command{Script: k8s.HelmSetupScript(hubCache), StepKey: "k8s.helmStep.setup"})
	}
	return append(cmds, cmdjob.Command{Script: k8s.AddonInstallScript(flavor, helm, req, hubCache), StepKey: "k8s.addonInstallStep", StepArgs: []any{req.Name, req.Version}})
}
