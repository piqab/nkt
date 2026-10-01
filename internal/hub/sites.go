package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/site"
	"github.com/piqab/nkt/internal/store"
)

// «Сайты» в «Выкладках»: домен → проверка DNS и портов с хаба (то есть
// снаружи) → прокси на хосте (поставить nginx, если нет никакого) →
// задание хоста (файрвол, публикация сервиса, certbot, конфигурация
// прокси) → проверка HTTPS снаружи.

// KindSiteSetup — задание хаба «настроить сайт».
const KindSiteSetup = "site.setup"

// SiteSetupParams — вход задания.
type SiteSetupParams struct {
	SiteID       int64 `json:"site_id"`
	InstallProxy bool  `json:"install_proxy"`
}

// SiteCheck — что видно снаружи: DNS, порты, HTTPS.
type SiteCheck struct {
	DNS   []edgeDNSReport   `json:"dns"`
	Ports map[string]string `json:"ports"`
	// LAN — имя ведёт в частную сеть: хаб видит не то, что интернет.
	LAN   bool       `json:"lan,omitempty"`
	HTTPS HTTPSCheck `json:"https"`
}

// siteDNS — указывает ли имя на хост (адрес хаба к хосту, интерфейсы).
func (s *Server) siteDNS(ctx context.Context, t targetHost, domain string) edgeDNSReport {
	if t.ID != localHostID {
		var client *ssh.Client
		if c, err := s.hub.clientFor(ctx, t.ID); err == nil {
			client = c
		}
		return s.checkEdgeDNS(ctx, client, t.Addr, domain)
	}
	rep := s.checkEdgeDNS(ctx, nil, "", domain)
	if ifaces, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ifaces {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
				rep.HostIPs = appendIP(rep.HostIPs, ipn.IP.String())
			}
		}
	}
	rep.Match = ipsOverlap(rep.DomainIPs, rep.HostIPs)
	return rep
}

// probePort — соединение с хаба: open (принято), refused (путь открыт,
// никто не слушает — certbot --standalone встанет сам), timeout (похоже,
// фильтруется снаружи).
func probePort(ctx context.Context, host string, port int) string {
	d := net.Dialer{Timeout: 6 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		_ = conn.Close()
		return "open"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "refused"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "error: " + err.Error()
}

func (s *Server) siteOutside(ctx context.Context, t targetHost, domains []string, https bool) SiteCheck {
	chk := SiteCheck{Ports: map[string]string{}}
	for _, d := range domains {
		rep := s.siteDNS(ctx, t, d)
		for _, ip := range rep.DomainIPs {
			if p := net.ParseIP(ip); p != nil && (p.IsPrivate() || p.IsLoopback()) {
				chk.LAN = true
			}
		}
		chk.DNS = append(chk.DNS, rep)
	}
	for _, port := range []int{80, 443} {
		chk.Ports[strconv.Itoa(port)] = probePort(ctx, domains[0], port)
	}
	if https {
		chk.HTTPS = httpsCheck(ctx, domains[0])
	}
	return chk
}

func (s *Server) siteTarget(ctx context.Context, hostID int64) (targetHost, error) {
	if hostID == localHostID {
		if s.local == nil {
			return targetHost{}, msgs.Errorf("hub.f2bNoLocal")
		}
		return targetHost{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"}, nil
	}
	h, err := s.db.HostByID(ctx, hostID)
	if err != nil {
		return targetHost{}, err
	}
	return targetHost{ID: h.ID, Name: h.Name, Addr: h.Addr}, nil
}

// siteJSON — сайт для интерфейса.
type siteJSON struct {
	store.Site
	HostName string `json:"host_name"`
	// PipelineName — конвейер, чей блок site: описывает сайт.
	PipelineName string `json:"pipeline_name,omitempty"`
}

// handleSites — GET /hub/sites.
func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListSites(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	out := make([]siteJSON, 0, len(list))
	allow := s.scopeFilter(r.Context())
	for _, st := range list {
		if allow != nil && !allow(st.HostID) {
			continue
		}
		name := "localhost"
		if st.HostID != localHostID {
			if h, err := s.db.HostByID(r.Context(), st.HostID); err == nil {
				name = h.Name
			} else {
				name = "#" + strconv.FormatInt(st.HostID, 10)
			}
		}
		item := siteJSON{Site: st, HostName: name}
		if st.PipelineID > 0 {
			if pl, err := s.db.PipelineByID(r.Context(), st.PipelineID); err == nil {
				item.PipelineName = pl.Name
			}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out})
}

// handleSitePreflight — POST /hub/sites/preflight {host_id, domains}:
// DNS и порты снаружи и что есть на хосте (прокси, стеки, файрвол).
func (s *Server) handleSitePreflight(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HostID  int64    `json:"host_id"`
		Domains []string `json:"domains"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	t, err := s.siteTarget(ctx, req.HostID)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	out := map[string]any{}
	if len(req.Domains) > 0 {
		domains, err := site.NormalizeDomains(req.Domains)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		out["outside"] = s.siteOutside(ctx, t, domains, false)
	}
	var host json.RawMessage
	if _, err := s.hostCall(ctx, auth.Username(ctx), t.ID, "GET", "/api/sites/preflight", nil, &host); err != nil {
		out["host_error"] = msgs.Localize(msgs.FromContext(ctx), err)
	} else {
		out["host"] = host
	}
	writeJSON(w, http.StatusOK, out)
}

type siteRequest struct {
	Domains       []string `json:"domains"`
	HostID        int64    `json:"host_id"`
	Proxy         string   `json:"proxy"`
	Stack         string   `json:"stack"`
	Service       string   `json:"service"`
	ContainerPort int      `json:"container_port"`
	Upstream      string   `json:"upstream"`
	OpenFirewall  bool     `json:"open_firewall"`
	InstallProxy  bool     `json:"install_proxy"`
}

// handleSiteSave — POST /hub/sites (новый) и PUT /hub/sites/{id}:
// сохранить и сразу настроить (задание хаба).
func (s *Server) handleSiteSave(w http.ResponseWriter, r *http.Request) {
	var req siteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	domains, err := site.NormalizeDomains(req.Domains)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !slices.Contains(site.Proxies, req.Proxy) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("site.badProxy", req.Proxy))
		return
	}
	if req.Stack != "" {
		if !site.ValidName(req.Stack) || !site.ValidName(req.Service) || req.ContainerPort < 1 || req.ContainerPort > 65535 {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("site.badTarget"))
			return
		}
		req.Upstream = ""
	} else if req.Upstream, err = site.ValidUpstream(req.Upstream); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if _, err := s.siteTarget(ctx, req.HostID); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var id, pipelineID int64
	if raw := chi.URLParam(r, "id"); raw != "" {
		id, _ = strconv.ParseInt(raw, 10, 64)
		prev, err := s.db.SiteByID(ctx, id)
		if err != nil {
			writeErr(w, r, http.StatusNotFound, err)
			return
		}
		// Правка в мастере не отвязывает сайт от конвейера.
		pipelineID = prev.PipelineID
	} else {
		// Имя уже занято другим сайтом хаба — второй на то же имя не
		// заводится.
		if list, err := s.db.ListSites(ctx); err == nil {
			for _, other := range list {
				if len(other.Domains) > 0 && slices.Contains(domains, other.Domains[0]) {
					writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.siteExists", other.Domains[0]))
					return
				}
			}
		}
	}
	user := auth.Username(ctx)
	id, err = s.db.SaveSite(ctx, store.Site{ID: id, Domains: domains, HostID: req.HostID, Proxy: req.Proxy, Stack: req.Stack,
		Service: req.Service, ContainerPort: req.ContainerPort, Upstream: req.Upstream, OpenFirewall: req.OpenFirewall, PipelineID: pipelineID, Author: user})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	jobID, err := s.startSiteSetup(ctx, user, id, domains[0], req.InstallProxy)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "job_id": jobID})
}

func (s *Server) startSiteSetup(ctx context.Context, user string, id int64, name string, installProxy bool) (int64, error) {
	if s.jobs == nil {
		return 0, msgs.Errorf("api.backgroundJobsAreUnavailable")
	}
	jobID, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindSiteSetup, TitleKey: "hub.siteJobTitle", TitleArgs: []any{name}, Queue: "site:" + name,
		Author: user, Steps: 5, Params: SiteSetupParams{SiteID: id, InstallProxy: installProxy},
	})
	if err == nil {
		_ = s.db.SetSiteState(ctx, id, store.SiteSettingUp, "", jobID)
		s.db.Audit(ctx, user, "site.setup", name, "ok", map[string]any{"job": jobID})
	}
	return jobID, err
}

// handleSiteApply — POST /hub/sites/{id}/apply {install_proxy}: настроить
// заново (после правки на хосте, смены стека и т. п.).
func (s *Server) handleSiteApply(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	st, err := s.db.SiteByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	var req struct {
		InstallProxy bool `json:"install_proxy"`
	}
	_ = decodeJSON(r, &req)
	jobID, err := s.startSiteSetup(r.Context(), auth.Username(r.Context()), id, st.Domains[0], req.InstallProxy)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

// handleSiteCheck — POST /hub/sites/{id}/check: DNS, порты и HTTPS снаружи.
func (s *Server) handleSiteCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	st, err := s.db.SiteByID(ctx, id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	t, err := s.siteTarget(ctx, st.HostID)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	chk := s.siteOutside(ctx, t, st.Domains, true)
	raw, _ := json.Marshal(chk)
	_ = s.db.SetSiteCheck(ctx, id, raw)
	writeJSON(w, http.StatusOK, chk)
}

// handleSiteDelete — DELETE /hub/sites/{id}?remove=1: забыть сайт; с
// remove=1 — ещё и убрать конфигурацию прокси на хосте (сертификат и
// публикация сервиса остаются).
func (s *Server) handleSiteDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	st, err := s.db.SiteByID(ctx, id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	user := auth.Username(ctx)
	if r.URL.Query().Get("remove") == "1" {
		if _, err := s.hostCall(ctx, user, st.HostID, "POST", "/api/sites/remove", map[string]string{"domain": st.Domains[0]}, nil); err != nil {
			writeErr(w, r, http.StatusBadGateway, err)
			return
		}
	}
	if err := s.db.DeleteSite(ctx, id); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(ctx, user, "site.delete", st.Domains[0], "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// installHostPackage — пакет дистрибутива на хосте фоновым заданием хоста
// (apt-get); журнал установки — в журнал задания сайта.
func (s *Server) installHostPackage(ctx context.Context, jc *jobs.Context, user string, t targetHost, pkg string) error {
	var started struct {
		JobID int64 `json:"job_id"`
	}
	code, err := s.hostCall(ctx, user, t.ID, "POST", "/api/system/apt/packages/"+pkg+"/install/ws?job=1", nil, &started)
	switch {
	case code == http.StatusForbidden:
		// Нет apt-get (не Debian/Ubuntu) или режим фикстур.
		return msgs.Errorf("site.packageNoApt", pkg, msgs.Localize(jc.Lang(), err))
	case err != nil:
		return err
	case started.JobID == 0:
		return msgs.Errorf("site.packageNotInstalled", pkg)
	}
	return s.waitHostJobVia(ctx, jc, user, t.ID, started.JobID)
}

// SiteSetupRunner — задание настройки сайта.
type SiteSetupRunner struct{ s *Server }

// NewSiteSetupRunner — исполнитель.
func NewSiteSetupRunner(s *Server) *SiteSetupRunner { return &SiteSetupRunner{s: s} }

// Run: DNS и порты снаружи → прокси (поставить, если нужно) → задание
// хоста → HTTPS снаружи.
func (r *SiteSetupRunner) Run(ctx context.Context, jc *jobs.Context) (err error) {
	s := r.s
	var p SiteSetupParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	st, err := s.db.SiteByID(ctx, p.SiteID)
	if err != nil {
		return err
	}
	return s.setupSite(ctx, jc, jc.Job.Author, st, p.InstallProxy, true)
}

// setupSite — настройка сайта: DNS и порты снаружи → прокси (поставить,
// если можно) → задание хоста → HTTPS снаружи. steps — шаги задания
// (своё задание сайта); иначе — строки журнала (шаг выкладки конвейера).
// Состояние сайта (ok или ошибка) записывается в любом случае.
func (s *Server) setupSite(ctx context.Context, jc *jobs.Context, user string, st store.Site, installProxy, steps bool) (err error) {
	defer func() {
		status, text := store.SiteOK, ""
		if err != nil {
			status, text = store.SiteFailed, msgs.Localize(jc.Lang(), err)
		}
		_ = s.db.SetSiteState(context.WithoutCancel(ctx), st.ID, status, text, jc.Job.ID)
	}()
	step := func(i int, key string, args ...any) {
		if steps {
			jc.StepKey(i, 5, key, args...)
		} else {
			jc.Log(key, args...)
		}
	}
	t, err := s.siteTarget(ctx, st.HostID)
	if err != nil {
		return err
	}

	// 1. DNS.
	step(1, "hub.siteStepDNS", strings.Join(st.Domains, ", "))
	chk := s.siteOutside(ctx, t, st.Domains, false)
	force := true
	for _, d := range chk.DNS {
		if d.Match {
			jc.Log("hub.siteDNSOK", d.Domain, strings.Join(d.DomainIPs, ", "))
		} else {
			force = false
			jc.Log("hub.siteDNSMismatch", d.Domain, strings.Join(d.DomainIPs, ", "), strings.Join(d.HostIPs, ", "))
		}
	}

	// 2. Порты снаружи.
	step(2, "hub.siteStepPorts")
	for _, port := range []string{"80", "443"} {
		jc.Log("hub.sitePort", port, chk.Ports[port])
	}
	if chk.Ports["80"] == "timeout" {
		jc.Log("hub.sitePort80Blocked")
	}
	if chk.LAN {
		jc.Log("hub.siteLAN")
	}

	// Порт сервиса — до установки прокси и сертификата (старый хост
	// маршрута не знает — проверит задание хоста).
	if st.Stack != "" {
		var pc struct {
			Checked      bool   `json:"checked"`
			StackMissing bool   `json:"stack_missing"`
			Found        bool   `json:"found"`
			Ports        []int  `json:"ports"`
			Image        string `json:"image"`
		}
		q := "/api/sites/port-check?stack=" + url.QueryEscape(st.Stack) + "&service=" + url.QueryEscape(st.Service)
		if code, err := s.hostCall(ctx, user, t.ID, "GET", q, nil, &pc); err == nil && pc.Checked {
			switch {
			case pc.StackMissing:
				return msgs.Errorf("site.stackMissing", st.Stack)
			case !pc.Found:
				return msgs.Errorf("site.serviceMissing", st.Service, st.Stack)
			case len(pc.Ports) > 0 && !slices.Contains(pc.Ports, st.ContainerPort):
				list := make([]string, len(pc.Ports))
				for i, p := range pc.Ports {
					list[i] = strconv.Itoa(p)
				}
				return msgs.Errorf("site.portNotExposed", st.ContainerPort, st.Service, pc.Image, strings.Join(list, ", "))
			}
		} else if err != nil && code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			return err
		}
	}

	// 3. Прокси на хосте.
	step(3, "hub.siteStepProxy", st.Proxy)
	var pre struct {
		Proxies []struct {
			Name      string `json:"name"`
			Installed bool   `json:"installed"`
		} `json:"proxies"`
		Certbot bool `json:"certbot"`
	}
	if _, err := s.hostCall(ctx, user, t.ID, "GET", "/api/sites/preflight", nil, &pre); err != nil {
		return err
	}
	installed := false
	for _, px := range pre.Proxies {
		if px.Name == st.Proxy && px.Installed {
			installed = true
		}
	}
	if !installed {
		if !installProxy {
			return msgs.Errorf("site.proxyMissing", st.Proxy)
		}
		jc.Log("hub.siteInstallingProxy", st.Proxy)
		if err := s.installHostPackage(ctx, jc, user, t, st.Proxy); err != nil {
			return err
		}
	}
	// Сертификат выпускает и продлевает certbot (Caddy — свой сам): нет
	// его — ставится так же, как прокси. Без разрешения ставить не
	// требуется заранее: годный сертификат хост возьмёт и без certbot, а
	// нужен будет выпуск — скажет сам.
	if st.Proxy != site.ProxyCaddy && !pre.Certbot && installProxy {
		jc.Log("hub.siteInstallingCertbot")
		if err := s.installHostPackage(ctx, jc, user, t, "certbot"); err != nil {
			// Не поставился — дальше: годный сертификат хост возьмёт и так,
			// а для выпуска нового сам скажет, что certbot нужен.
			jc.Log("hub.siteCertbotInstallFailed", msgs.Localize(jc.Lang(), err))
		}
	}

	// 4. Хост: файрвол, публикация, сертификат, прокси.
	step(4, "hub.siteStepHost", t.Name)
	var started struct {
		JobID int64 `json:"job_id"`
	}
	body := map[string]any{"domains": st.Domains, "proxy": st.Proxy, "stack": st.Stack, "service": st.Service,
		"container_port": st.ContainerPort, "upstream": st.Upstream, "open_firewall": st.OpenFirewall, "force": force}
	if _, err := s.hostCall(ctx, user, t.ID, "POST", "/api/sites/apply", body, &started); err != nil {
		return err
	}
	_ = s.db.SetSiteState(ctx, st.ID, store.SiteSettingUp, "", 0)
	if err := s.waitHostJobVia(ctx, jc, user, t.ID, started.JobID); err != nil {
		return err
	}

	// 5. HTTPS снаружи.
	step(5, "hub.siteStepHTTPS", st.Domains[0])
	chk.HTTPS = httpsCheck(ctx, st.Domains[0])
	raw, _ := json.Marshal(chk)
	_ = s.db.SetSiteCheck(ctx, st.ID, raw)
	if chk.HTTPS.WrongCert {
		// Прокси отвечает чужим сертификатом — конфигурация этого сайта не
		// действует (не подхватилась, перекрыта другим server): не «готово».
		return msgs.Errorf("hub.siteWrongCert", st.Domains[0], strings.Join(chk.HTTPS.CertNames, ", "))
	}
	if chk.HTTPS.OK {
		jc.Log("hub.siteHTTPSOK", st.Domains[0], chk.HTTPS.Status, chk.HTTPS.CertDaysLeft)
	} else {
		// Сайт настроен, но снаружи не ответил: DNS ещё не разошёлся,
		// сервис стартует, фильтр провайдера — это видно в журнале, а не
		// повод откатывать уже сделанное.
		jc.Log("hub.siteHTTPSFailed", st.Domains[0], chk.HTTPS.Error)
		if chk.HTTPS.Status == http.StatusBadGateway && st.Stack != "" {
			// Прокси работает, а сервис за ним не ответил — чаще всего
			// порт контейнера не тот, что слушает образ.
			jc.Log("hub.site502Hint", st.Service, st.ContainerPort)
		}
	}
	return nil
}
