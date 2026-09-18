package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Проверки перед созданием машин и кластера (сухой прогон хаба): всё,
// что можно узнать, ничего не меняя.

func (s *Server) handleVMPreflight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.scanner.Collector()
	out := map[string]any{
		"kvm":             c.Exists("/dev/kvm"),
		"libvirtd_active": false,
	}
	if res, err := c.Run(ctx, "systemctl", "is-active", "libvirtd"); err == nil {
		out["libvirtd_active"] = strings.TrimSpace(res.Stdout) == "active"
	}
	// Свободное место там, где лежат диски машин.
	if res, err := c.Run(ctx, "df", "-B1", "--output=avail", "/var/lib/libvirt/images"); err == nil && res.OK() {
		lines := strings.Fields(res.Stdout)
		if n, err := strconv.ParseInt(lines[len(lines)-1], 10, 64); err == nil {
			out["images_free_bytes"] = n
		}
	}
	if snap, err := s.scanner.LatestOrScan(ctx); err == nil {
		out["mem_total_bytes"] = snap.Capacity.MemTotalBytes
		out["cpu_cores"] = snap.Capacity.CPUCores
		ports := []int{}
		for _, l := range snap.Listeners {
			ports = append(ports, l.Port)
		}
		out["listening_ports"] = ports
	}
	tools := vmcreate.CheckTools(ctx, RunTooling)
	missing := []string{}
	for _, t := range vmcreate.MissingTools(tools) {
		missing = append(missing, t.Package)
	}
	out["missing_packages"] = missing
	if s.vmimages != nil {
		downloaded := []string{}
		for _, l := range s.vmimages.Status() {
			if l.Downloaded {
				downloaded = append(downloaded, l.ID)
			}
		}
		out["images_downloaded"] = downloaded
	}
	// Мосты хоста — для кластеров на нескольких хостах.
	bridges := []string{}
	if res, err := c.Run(ctx, "sh", "-c", "ip -o link show type bridge 2>/dev/null | awk -F': ' '{print $2}'"); err == nil && res.OK() {
		for _, l := range strings.Fields(res.Stdout) {
			bridges = append(bridges, strings.SplitN(l, "@", 2)[0])
		}
	}
	out["bridges"] = bridges
	if mgr := s.vmnets(); mgr != nil {
		if nets, err := mgr.List(ctx); err == nil {
			out["networks"] = nets
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleNetCheck — доступен ли адрес с хоста: HEAD с таймаутом из самого
// процесса (сеть у юнита есть). Нужно сухому прогону: с хоста должны
// открываться get.k3s.io, pkgs.k8s.io и ссылка на образ.
func (s *Server) handleNetCheck(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "api.linkMustStartHttpHttps"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	req.Header.Set("User-Agent", "nkt/"+s.version)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	out := map[string]any{"url": raw, "ms": time.Since(started).Milliseconds()}
	if err != nil {
		out["ok"] = false
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	resp.Body.Close()
	out["ok"] = resp.StatusCode < 400
	out["status"] = resp.StatusCode
	writeJSON(w, http.StatusOK, out)
}

// handleAptDownload — скачать пакеты в кэш apt без установки: подготовка
// к настоящей установке, не меняющая систему.
func (s *Server) handleAptDownload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	var req struct {
		Packages []string `json:"packages"`
	}
	if err := decodeJSON(r, &req); err != nil || len(req.Packages) == 0 {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.noPackagesSelected"))
		return
	}
	for _, p := range req.Packages {
		if !aptPackageNameRe.MatchString(p) {
			writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.unknownPackage", p))
			return
		}
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	res, err := RunUnrestrictedEnv(ctx, map[string]string{"DEBIAN_FRONTEND": "noninteractive"}, nil,
		"sh", "-c", "apt-get update -qq && apt-get install -y -qq --download-only "+strings.Join(req.Packages, " "))
	user := auth.Username(r.Context())
	if err == nil && res.ExitCode != 0 {
		err = msgs.Errorf("pkgInstall.downloadFailed", lastLineOf(res.Output()))
	}
	s.db.Audit(r.Context(), user, "apt.download", strings.Join(req.Packages, " "), auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "packages": req.Packages})
}
