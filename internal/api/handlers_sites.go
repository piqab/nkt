package api

import (
	"context"
	"encoding/json"
	"github.com/piqab/nkt/internal/config"
	"net/http"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/parse"
	"github.com/piqab/nkt/internal/site"
	"github.com/piqab/nkt/internal/store"
)

// Сайт на хосте (вкладка «Сайты» в «Выкладках» хаба): домен → прокси
// (nginx, HAProxy, Caddy) → сервис compose-стека или адрес:порт, с
// сертификатом. Хаб проверяет DNS и порты снаружи, ставит прокси, если
// его нет, и зовёт задание хоста; всё локальное делает хост: файрвол,
// публикация сервиса на 127.0.0.1, certbot --standalone (прокси на это
// время останавливается), конфигурация прокси общим путём конфигураций
// (проверка, откат, история).

// KindSiteApply — задание «настроить сайт».
const KindSiteApply = "site.apply"

const sitesRegistryKey = "sites.registry"

// HostSite — сайт, настроенный nkt на этом хосте.
type HostSite struct {
	Domains       []string `json:"domains"`
	Proxy         string   `json:"proxy"`
	Upstream      string   `json:"upstream"`
	Stack         string   `json:"stack,omitempty"`
	Service       string   `json:"service,omitempty"`
	ContainerPort int      `json:"container_port,omitempty"`
	HostPort      int      `json:"host_port,omitempty"`
	Lineage       string   `json:"lineage,omitempty"`
	File          string   `json:"file,omitempty"`
	UpdatedAt     string   `json:"updated_at"`
}

func (s *Server) hostSites(ctx context.Context) []HostSite {
	var list []HostSite
	if raw, ok, err := s.db.KVGet(ctx, sitesRegistryKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	return list
}

func (s *Server) saveHostSites(ctx context.Context, list []HostSite) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Domains[0] < list[j].Domains[0] })
	b, _ := json.Marshal(list)
	return s.db.KVSet(ctx, sitesRegistryKey, string(b))
}

// --- предварительная проверка ---------------------------------------------

// SiteProxyState — прокси на хосте.
type SiteProxyState struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
}

// SiteHolder — кто слушает 80/443.
type SiteHolder struct {
	Port      int    `json:"port"`
	Process   string `json:"process,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Container string `json:"container,omitempty"`
}

// SiteStack — compose-стек и его сервисы с опубликованными портами.
type SiteStack struct {
	Name     string        `json:"name"`
	File     string        `json:"file"`
	Services []SiteService `json:"services"`
	Error    string        `json:"error,omitempty"`
}

// SiteService — сервис стека.
type SiteService struct {
	Name   string      `json:"name"`
	Ports  []site.Port `json:"ports"`
	Expose []int       `json:"expose,omitempty"`
}

// SiteFirewall — файрвол хоста и открыты ли 80/443 (по правилам).
type SiteFirewall struct {
	Manager string `json:"manager,omitempty"`
	Active  bool   `json:"active"`
	Open80  bool   `json:"open80"`
	Open443 bool   `json:"open443"`
	// Writable — nkt сможет добавить правило (ufw: /etc/ufw пишется
	// изнутри песочницы или в обход неё; firewalld — через D-Bus).
	Writable bool `json:"writable"`
}

func (s *Server) siteProxies(ctx context.Context, snap *model.Snapshot) []SiteProxyState {
	c := s.scanner.Collector()
	var out []SiteProxyState
	for _, name := range site.Proxies {
		st := SiteProxyState{Name: name, Installed: collect.Which(ctx, c, name)}
		if snap != nil {
			for _, svc := range snap.Services {
				if svc.Name == name && svc.ActiveState == "active" {
					st.Running = true
				}
			}
		}
		out = append(out, st)
	}
	return out
}

// composeFileIn — compose-файл каталога стека.
func composeFileIn(c collect.Collector, dir string) string {
	for _, n := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		if c.Exists(dir + "/" + n) {
			return n
		}
	}
	return ""
}

// stackServices — сервисы стека с портами (compose config --format json).
func stackServices(ctx context.Context, c collect.Collector, engine, name, file string) ([]SiteService, error) {
	res, err := c.RunTimeout(ctx, time.Minute, engine, append(composeArgs(c, name, file), "config", "--format", "json")...)
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, msgs.Errorf("compose.configRejected", strings.TrimSpace(res.Output()))
	}
	var doc struct {
		Services map[string]struct {
			Ports []struct {
				Target    int    `json:"target"`
				Published any    `json:"published"`
				HostIP    string `json:"host_ip"`
			} `json:"ports"`
			Expose []any `json:"expose"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		return nil, msgs.Errorf("compose.configRejected", err.Error())
	}
	var out []SiteService
	for svcName, svc := range doc.Services {
		ss := SiteService{Name: svcName, Ports: []site.Port{}}
		for _, p := range svc.Ports {
			pub := 0
			switch v := p.Published.(type) {
			case float64:
				pub = int(v)
			case string:
				pub, _ = strconv.Atoi(v)
			}
			ss.Ports = append(ss.Ports, site.Port{Target: p.Target, Published: pub, HostIP: p.HostIP})
		}
		for _, e := range svc.Expose {
			switch v := e.(type) {
			case float64:
				ss.Expose = append(ss.Expose, int(v))
			case string:
				if n, err := strconv.Atoi(strings.SplitN(v, "/", 2)[0]); err == nil {
					ss.Expose = append(ss.Expose, n)
				}
			}
		}
		out = append(out, ss)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// serviceDeclaredPorts — порты контейнера, которые объявляет сервис: в
// compose (expose, target у ports) и в образе (EXPOSE). found — сервис
// есть в стеке; пусто — образ портов не объявляет (проверять не с чем).
func serviceDeclaredPorts(ctx context.Context, c collect.Collector, engine, dir, project, file, service string) (ports []int, image string, found bool, err error) {
	ports, image, _, found, err = serviceDeclaredPortsFrom(ctx, c, engine, dir, project, file, service)
	return
}

// Откуда известны порты сервиса: из EXPOSE скачанного образа или только из
// compose (ports/expose) — второе не доказывает, что процесс их слушает.
const (
	PortsFromImage   = "image"
	PortsFromCompose = "compose"
)

// serviceDeclaredPortsFrom — serviceDeclaredPorts и источник портов.
func serviceDeclaredPortsFrom(ctx context.Context, c collect.Collector, engine, dir, project, file, service string) (ports []int, image, from string, found bool, err error) {
	res, err := c.RunTimeout(ctx, time.Minute, engine, append(composeArgsIn(c, dir, project, file), "config", "--format", "json")...)
	if err != nil {
		return nil, "", "", false, err
	}
	if !res.OK() {
		return nil, "", "", false, msgs.Errorf("compose.configRejected", strings.TrimSpace(res.Output()))
	}
	var doc struct {
		Services map[string]struct {
			Image string `json:"image"`
			Ports []struct {
				Target int `json:"target"`
			} `json:"ports"`
			Expose []any `json:"expose"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		return nil, "", "", false, msgs.Errorf("compose.configRejected", err.Error())
	}
	svc, ok := doc.Services[service]
	if !ok {
		return nil, "", "", false, nil
	}
	// Образ объявляет порты (EXPOSE) — сверка только с ними: expose в
	// compose — лишь пометка, процесс в контейнере она не заставит
	// слушать (пример httpbin с чужим образом: expose 8080, образ — 80).
	imageSet := map[int]bool{}
	set := map[int]bool{}
	for _, p := range svc.Ports {
		set[p.Target] = true
	}
	for _, e := range svc.Expose {
		switch v := e.(type) {
		case float64:
			set[int(v)] = true
		case string:
			if n, err := strconv.Atoi(strings.SplitN(v, "/", 2)[0]); err == nil {
				set[n] = true
			}
		}
	}
	if svc.Image != "" {
		// Образ ещё не скачан — EXPOSE не узнать (без скачивания).
		if out, err := c.RunTimeout(ctx, 20*time.Second, engine, "image", "inspect", "--format", "{{json .Config.ExposedPorts}}", svc.Image); err == nil && out.OK() {
			var exposed map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(out.Stdout)), &exposed) == nil {
				for k := range exposed {
					if n, err := strconv.Atoi(strings.SplitN(k, "/", 2)[0]); err == nil {
						imageSet[n] = true
					}
				}
			}
		}
	}
	from = PortsFromCompose
	if len(imageSet) > 0 {
		set, from = imageSet, PortsFromImage
	}
	for n := range set {
		ports = append(ports, n)
	}
	sort.Ints(ports)
	return ports, svc.Image, from, true, nil
}

// portList — «80, 443» для сообщений.
func portList(ports []int) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ", ")
}

func (s *Server) siteStacks(ctx context.Context, engine string) []SiteStack {
	c := s.scanner.Collector()
	dirs, _ := c.Glob(parse.ComposeStacksDir + "/*")
	sort.Strings(dirs)
	var out []SiteStack
	for _, dir := range dirs {
		name := path.Base(dir)
		if !site.ValidName(name) {
			continue
		}
		file := composeFileIn(c, dir)
		if file == "" {
			continue
		}
		st := SiteStack{Name: name, File: file, Services: []SiteService{}}
		if engine != "" {
			if svcs, err := stackServices(ctx, c, engine, name, file); err != nil {
				st.Error = msgs.Localize(msgs.FromContext(ctx), err)
			} else {
				st.Services = svcs
			}
		}
		out = append(out, st)
	}
	return out
}

// siteFirewallState — файрвол хоста и сможет ли nkt открыть в нём порты.
func (s *Server) siteFirewallState(snap *model.Snapshot) SiteFirewall {
	fw := siteFirewall(snap)
	fw.Writable = fw.Manager != "ufw" || s.firewall == nil || s.firewall.Writable()
	return fw
}

func siteFirewall(snap *model.Snapshot) SiteFirewall {
	var fw SiteFirewall
	if snap == nil {
		return fw
	}
	for _, name := range []string{"ufw", "firewalld"} {
		if m := snap.Firewall.Manager(name); m.Active {
			fw.Manager, fw.Active = name, true
		}
	}
	if !fw.Active {
		fw.Open80, fw.Open443 = true, true
		return fw
	}
	for _, r := range snap.Firewall.Rules {
		if r.Backend != fw.Manager {
			continue
		}
		act := strings.ToUpper(r.Action)
		if !strings.Contains(act, "ALLOW") && !strings.Contains(act, "ACCEPT") {
			continue
		}
		spec := strings.ToLower(r.PortSpec + " " + r.Comment)
		if slices.Contains(r.Ports, 80) || strings.Contains(spec, "http") && !strings.Contains(spec, "https") {
			fw.Open80 = true
		}
		if slices.Contains(r.Ports, 443) || strings.Contains(spec, "https") {
			fw.Open443 = true
		}
	}
	return fw
}

// handleSitePreflight — GET /sites/preflight: что есть на хосте для сайта.
func (s *Server) handleSitePreflight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, _ := s.scanner.LatestOrScan(ctx)
	c := s.scanner.Collector()
	engine := composeEngine(ctx, c)
	var holders []SiteHolder
	if snap != nil {
		for _, l := range snap.Listeners {
			if l.Port != 80 && l.Port != 443 {
				continue
			}
			holders = append(holders, SiteHolder{Port: l.Port, Process: l.Process, Unit: l.Unit, Container: l.ContainerID})
		}
	}
	sites := s.hostSites(ctx)
	if sites == nil {
		sites = []HostSite{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proxies":  s.siteProxies(ctx, snap),
		"holders":  holders,
		"stacks":   s.siteStacks(ctx, engine),
		"firewall": s.siteFirewallState(snap),
		"engine":   engine,
		"certbot":  collect.Which(ctx, c, "certbot"),
		"sites":    sites,
	})
}

// handleSitePortCheck — GET /sites/port-check?stack=&service=: есть ли
// сервис в стеке и какие порты контейнера он объявляет (хаб спрашивает до
// установки прокси и выпуска сертификата).
func (s *Server) handleSitePortCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	stack, service := q.Get("stack"), q.Get("service")
	if !site.ValidName(stack) || service == "" || len(service) > 64 || strings.ContainsAny(service, "/ \t\n") {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("site.badTarget"))
		return
	}
	out := map[string]any{"checked": false}
	if s.cfg.Mode == config.ModeFixtures {
		writeJSON(w, http.StatusOK, out)
		return
	}
	c := s.scanner.Collector()
	dir := composeStackDir(stack)
	file := composeFileIn(c, dir)
	engine := composeEngine(ctx, c)
	if file == "" || engine == "" {
		out["checked"], out["stack_missing"] = true, true
		writeJSON(w, http.StatusOK, out)
		return
	}
	ports, image, found, err := serviceDeclaredPorts(ctx, c, engine, dir, stack, file, service)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	out["checked"], out["found"], out["ports"], out["image"] = true, found, ports, image
	writeJSON(w, http.StatusOK, out)
}

// handleSiteDry — GET /sites/dry?domains=a,b&proxy=nginx: что увидит
// настройка сайта на хосте (для сухого прогона): годный сертификат на эти
// имена, читает ли nginx conf.d и не занято ли имя чужой конфигурацией.
func (s *Server) handleSiteDry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	domains, err := site.NormalizeDomains(strings.Split(q.Get("domains"), ","))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	proxy := q.Get("proxy")
	if !slices.Contains(site.Proxies, proxy) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("site.badProxy", proxy))
		return
	}
	out := map[string]any{}
	if s.certs != nil {
		if list, err := s.certs.ListLetsEncryptLineages(); err == nil {
			for _, l := range list {
				covers := true
				for _, d := range domains {
					if !slices.Contains(l.Names, d) {
						covers = false
					}
				}
				if covers {
					out["cert"] = map[string]any{"lineage": l.Name, "days_left": l.DaysLeft}
					break
				}
			}
		}
	}
	c := s.scanner.Collector()
	if proxy == site.ProxyNginx && collect.Which(ctx, c, "nginx") {
		main, _ := c.ReadFile(s.cfg.NginxMainConfig)
		out["nginx_conf_d"] = strings.Contains(string(main), "conf.d/*.conf")
		// Чужие server_name с этими именами: тогда наш server не действует.
		if res, err := c.RunTimeout(ctx, 30*time.Second, "nginx", "-T"); err == nil {
			out["conflicts"] = nginxNameConflicts(res.Stdout, domains)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// nginxNameConflicts — «файл: server_name …» с нашими именами вне файла
// сайта nkt (вывод nginx -T помечает файлы «# configuration file …:»).
func nginxNameConflicts(dump string, domains []string) []string {
	var out []string
	file := ""
	for _, line := range strings.Split(dump, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# configuration file ") {
			file = strings.TrimSuffix(strings.TrimPrefix(t, "# configuration file "), ":")
			continue
		}
		if !strings.HasPrefix(t, "server_name") || strings.Contains(path.Base(file), "nkt-") {
			continue
		}
		names := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(t, "server_name"), ";"))
		for _, d := range domains {
			if slices.Contains(names, d) {
				out = append(out, file+": "+t)
				break
			}
		}
	}
	return out
}

// --- настройка ----------------------------------------------------------

// SiteApplyParams — вход задания.
type SiteApplyParams struct {
	Domains       []string `json:"domains"`
	Proxy         string   `json:"proxy"`
	Stack         string   `json:"stack,omitempty"`
	Service       string   `json:"service,omitempty"`
	ContainerPort int      `json:"container_port,omitempty"`
	Upstream      string   `json:"upstream,omitempty"`
	OpenFirewall  bool     `json:"open_firewall"`
	// Force — имя указывает не на интерфейсы хоста (NAT), но хаб уже
	// проверил, что снаружи оно ведёт сюда.
	Force bool `json:"force"`
}

func (p *SiteApplyParams) check() error {
	d, err := site.NormalizeDomains(p.Domains)
	if err != nil {
		return err
	}
	p.Domains = d
	if !slices.Contains(site.Proxies, p.Proxy) {
		return msgs.Errorf("site.badProxy", p.Proxy)
	}
	if p.Stack != "" {
		if !site.ValidName(p.Stack) || !site.ValidName(p.Service) || p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return msgs.Errorf("site.badTarget")
		}
		p.Upstream = ""
		return nil
	}
	up, err := site.ValidUpstream(p.Upstream)
	if err != nil {
		return err
	}
	p.Upstream = up
	return nil
}

// handleSiteApply — POST /sites/apply: задание хоста.
func (s *Server) handleSiteApply(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var p SiteApplyParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := p.check(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindSiteApply, TitleKey: "site.jobTitle", TitleArgs: []any{p.Domains[0]},
		Queue: "site", Author: auth.Username(r.Context()), Steps: 5, Params: p,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

type siteRunner struct{ s *Server }

func (sr *siteRunner) Run(ctx context.Context, jc *jobs.Context) error {
	s := sr.s
	var p SiteApplyParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	if err := p.check(); err != nil {
		return err
	}
	c := s.scanner.Collector()
	user := jc.Job.Author
	lang := jc.Lang()
	snap, _ := s.scanner.LatestOrScan(ctx)
	defer s.rescanLater()

	// 1. Прокси установлен.
	jc.StepKey(1, 5, "site.stepCheck", p.Proxy)
	// Порт сервиса — до файрвола и сертификата: сайт на порт, который
	// контейнер не слушает, дал бы 502 уже после выпуска сертификата.
	if p.Stack != "" && s.cfg.Mode != config.ModeFixtures {
		dir := composeStackDir(p.Stack)
		file := composeFileIn(c, dir)
		engine := composeEngine(ctx, c)
		if file == "" || engine == "" {
			return msgs.Errorf("site.stackMissing", p.Stack)
		}
		ports, image, found, err := serviceDeclaredPorts(ctx, c, engine, dir, p.Stack, file, p.Service)
		switch {
		case err != nil:
			return err
		case !found:
			return msgs.Errorf("site.serviceMissing", p.Service, p.Stack)
		case len(ports) == 0:
			jc.Log("site.portUnverified", image)
		case !slices.Contains(ports, p.ContainerPort):
			return msgs.Errorf("site.portNotExposed", p.ContainerPort, p.Service, image, portList(ports))
		default:
			jc.Log("site.portOK", p.ContainerPort, portList(ports))
		}
	}

	if !collect.Which(ctx, c, p.Proxy) {
		return msgs.Errorf("site.proxyMissing", p.Proxy)
	}
	if p.Proxy == site.ProxyNginx {
		main, _ := c.ReadFile(s.cfg.NginxMainConfig)
		if !strings.Contains(string(main), "conf.d/*.conf") {
			return msgs.Errorf("site.nginxNoConfD", s.cfg.NginxMainConfig)
		}
	}

	// 2. Файрвол.
	jc.StepKey(2, 5, "site.stepFirewall")
	fw := siteFirewall(snap)
	switch {
	case !fw.Active:
		jc.Log("site.firewallOff")
	case fw.Open80 && fw.Open443:
		jc.Log("site.firewallOpen", fw.Manager)
	case !p.OpenFirewall:
		jc.Log("site.firewallClosedSkip", fw.Manager)
	default:
		// Правило не добавилось — не повод бросать сайт: хаб снаружи уже
		// проверил порт 80; если он закрыт, это скажет выпуск сертификата.
		if err := s.siteOpenFirewall(ctx, user, fw.Manager, snap); err != nil {
			jc.Log("site.firewallFailed", fw.Manager, msgs.Localize(lang, err))
		} else {
			jc.Log("site.firewallOpened", fw.Manager)
		}
	}

	// 3. Цель: сервис стека — на 127.0.0.1.
	jc.StepKey(3, 5, "site.stepTarget")
	rec := HostSite{Domains: p.Domains, Proxy: p.Proxy, Upstream: p.Upstream, Stack: p.Stack, Service: p.Service, ContainerPort: p.ContainerPort}
	if p.Stack != "" {
		up, hostPort, err := s.sitePublish(ctx, jc, snap, p)
		if err != nil {
			return err
		}
		rec.Upstream, rec.HostPort = up, hostPort
	}
	jc.Log("site.upstream", rec.Upstream)

	// 4. Сертификат (Caddy получает свой сам).
	jc.StepKey(4, 5, "site.stepCert", strings.Join(p.Domains, ", "))
	if p.Proxy != site.ProxyCaddy {
		lineage, err := s.siteCertificate(ctx, jc, user, p.Domains, p.Force)
		if err != nil {
			return err
		}
		rec.Lineage = lineage
	} else {
		jc.Log("site.caddyOwnCert")
	}

	// 5. Прокси.
	jc.StepKey(5, 5, "site.stepProxy", p.Proxy)
	all := s.hostSites(ctx)
	kept := all[:0]
	for _, old := range all {
		if old.Domains[0] != rec.Domains[0] {
			kept = append(kept, old)
		}
	}
	file, err := s.siteWriteProxy(ctx, jc, user, lang, rec, kept)
	if err != nil {
		return err
	}
	rec.File = file
	rec.UpdatedAt = store.Now()
	if err := s.saveHostSites(ctx, append(kept, rec)); err != nil {
		return err
	}
	s.db.Audit(ctx, user, "site.apply", rec.Domains[0], "ok", map[string]any{"proxy": rec.Proxy, "upstream": rec.Upstream})
	jc.Log("site.done", rec.Domains[0], rec.Upstream)
	return nil
}

func (s *Server) siteOpenFirewall(ctx context.Context, user, manager string, snap *model.Snapshot) error {
	switch manager {
	case "ufw":
		for _, port := range []int{80, 443} {
			if _, err := s.firewall.AddRule(ctx, user, control.RuleSpec{Action: "allow", Port: port, Protocol: "tcp", Comment: "nkt site"}); err != nil {
				return err
			}
		}
	case "firewalld":
		zone := snap.Firewall.Manager("firewalld").Policy
		if zone == "" {
			zone = "public"
		}
		for _, svc := range []string{"http", "https"} {
			if _, err := s.firewalld.AddRule(ctx, user, control.FirewalldPortSpec{Zone: zone, Service: svc, Permanent: true, Runtime: true}); err != nil {
				return err
			}
		}
	}
	return nil
}

// sitePublish — адрес, по которому прокси достаёт сервис стека. Уже
// опубликованный порт используется как есть (предупреждение, если он
// открыт наружу); иначе — файл публикации nkt с 127.0.0.1:<свободный>.
func (s *Server) sitePublish(ctx context.Context, jc *jobs.Context, snap *model.Snapshot, p SiteApplyParams) (string, int, error) {
	c := s.scanner.Collector()
	engine := composeEngine(ctx, c)
	if engine == "" {
		return "", 0, msgs.Errorf("compose.noEngine")
	}
	dir := composeStackDir(p.Stack)
	file := composeFileIn(c, dir)
	if file == "" {
		return "", 0, msgs.Errorf("site.stackMissing", p.Stack)
	}
	svcs, err := stackServices(ctx, c, engine, p.Stack, file)
	if err != nil {
		return "", 0, err
	}
	var svc *SiteService
	for i := range svcs {
		if svcs[i].Name == p.Service {
			svc = &svcs[i]
		}
	}
	if svc == nil {
		return "", 0, msgs.Errorf("site.serviceMissing", p.Service, p.Stack)
	}
	for _, port := range svc.Ports {
		if port.Target == p.ContainerPort && port.Published > 0 {
			ip := port.HostIP
			if ip == "" || ip == "0.0.0.0" || ip == "::" {
				jc.Log("site.portPublic", p.Service, port.Published)
				ip = "127.0.0.1"
			}
			return ip + ":" + strconv.Itoa(port.Published), port.Published, nil
		}
	}
	// Свободный локальный порт: не слушается и не занят другим сайтом.
	used := map[int]bool{}
	if snap != nil {
		for _, l := range snap.Listeners {
			used[l.Port] = true
		}
	}
	for _, other := range s.hostSites(ctx) {
		used[other.HostPort] = true
	}
	port := 0
	for n := site.PortRangeStart; n <= site.PortRangeEnd; n++ {
		if !used[n] {
			port = n
			break
		}
	}
	if port == 0 {
		return "", 0, msgs.Errorf("site.noFreePort")
	}
	ovPath := dir + "/" + site.OverrideFile
	old, _ := c.ReadFile(ovPath)
	content, err := site.Override(string(old), p.Service, port, p.ContainerPort)
	if err != nil {
		return "", 0, err
	}
	if err := c.WriteFile(ovPath, []byte(content), 0o644); err != nil {
		return "", 0, err
	}
	if s.configs != nil {
		_, _ = s.configs.RecordDoc(ctx, ovPath, "docker", jc.Job.Author, store.ActionEdit, msgs.T(jc.Lang(), "site.noteOverride", p.Domains[0]), old, []byte(content))
	}
	jc.Log("site.published", p.Service, port, p.ContainerPort)
	args := append(composeArgs(c, p.Stack, file), "up", "-d", p.Service)
	res, err := c.RunTimeout(ctx, 10*time.Minute, engine, args...)
	if err == nil && !res.OK() {
		err = msgs.Errorf("compose.commandFailed", "up -d "+p.Service, res.ExitCode)
	}
	if err != nil {
		if len(old) > 0 {
			_ = c.WriteFile(ovPath, old, 0o644)
		} else {
			_ = c.DeleteFile(ovPath)
		}
		for _, l := range strings.Split(strings.TrimSpace(res.Output()), "\n") {
			jc.Logf("      %s", l)
		}
		return "", 0, err
	}
	return "127.0.0.1:" + strconv.Itoa(port), port, nil
}

// siteCertificate — lineage certbot на все имена: действующий (дольше
// 20 дней) — как есть, иначе выпуск certbot --standalone.
func (s *Server) siteCertificate(ctx context.Context, jc *jobs.Context, user string, domains []string, force bool) (string, error) {
	find := func() (string, int) {
		list, _ := s.certs.ListLetsEncryptLineages()
		for _, l := range list {
			covers := true
			for _, d := range domains {
				if !slices.Contains(l.Names, d) {
					covers = false
				}
			}
			if covers {
				return l.Name, l.DaysLeft
			}
		}
		return "", 0
	}
	if name, days := find(); name != "" && days > 20 {
		jc.Log("site.certExisting", name, days)
		return name, nil
	}
	if !collect.Which(ctx, s.scanner.Collector(), "certbot") {
		return "", msgs.Errorf("site.noCertbot")
	}
	err := s.certs.IssueCertbotSync(ctx, user, domains, force,
		func(key string, args ...any) { jc.Log(key, args...) },
		func(text string) {
			for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
				jc.Logf("      %s", l)
			}
		})
	if err != nil {
		return "", err
	}
	name, _ := find()
	if name == "" {
		return "", msgs.Errorf("site.certNotFound", domains[0])
	}
	return name, nil
}

// siteWriteProxy пишет конфигурацию прокси (проверка, откат, история,
// перезагрузка) и возвращает путь файла сайта.
func (s *Server) siteWriteProxy(ctx context.Context, jc *jobs.Context, user string, lang msgs.Lang, rec HostSite, others []HostSite) (string, error) {
	if s.configs == nil {
		return "", msgs.Errorf("f2b.configsUnavailable")
	}
	c := s.scanner.Collector()
	note := msgs.T(lang, "site.noteProxy", rec.Domains[0])
	cfg := site.Config{Domains: rec.Domains, Upstream: rec.Upstream, Lineage: rec.Lineage}
	write := func(p, content string) error {
		res, err := s.configs.Write(ctx, lang, user, p, content, note, true)
		if err != nil {
			return err
		}
		if res.RolledBack {
			return msgs.Errorf("site.proxyRejected", p, res.Message)
		}
		jc.Log("site.proxyWritten", p)
		return nil
	}
	var file string
	switch rec.Proxy {
	case site.ProxyNginx:
		file = site.NginxFile(s.cfg.NginxRoot, rec.Domains[0])
		if err := write(file, site.Nginx(cfg)); err != nil {
			return "", err
		}
	case site.ProxyCaddy:
		mainPath := s.cfg.CaddyMainConfig
		main, _ := c.ReadFile(mainPath)
		file = site.CaddyFile(s.cfg.CaddyRoot, rec.Domains[0])
		// Файл сайта — до подключения: caddy validate на import пустого
		// каталога тоже не жалуется, но так порядок очевиднее.
		if err := c.WriteFile(file, []byte(site.Caddy(cfg)), 0o644); err != nil {
			return "", err
		}
		if updated, changed := site.EnsureCaddyImport(string(main), s.cfg.CaddyRoot); changed {
			if err := write(mainPath, updated); err != nil {
				_ = c.DeleteFile(file)
				return "", err
			}
		} else if err := write(file, site.Caddy(cfg)); err != nil {
			return "", err
		}
	case site.ProxyHAProxy:
		mainPath := s.cfg.HAProxyMainConf
		main, err := c.ReadFile(mainPath)
		if err != nil {
			return "", err
		}
		if port, busy := site.HAProxyForeignBind(string(main)); busy {
			return "", msgs.Errorf("site.haproxyForeignBind", port)
		}
		pem, err := s.siteCombinedPEM(rec.Lineage)
		if err != nil {
			return "", err
		}
		file = site.HAProxyPEM(rec.Domains[0])
		if err := c.WriteFile(file, pem, 0o600); err != nil {
			return "", err
		}
		var sites []site.Config
		for _, o := range append(others, rec) {
			if o.Proxy == site.ProxyHAProxy {
				sites = append(sites, site.Config{Domains: o.Domains, Upstream: o.Upstream, Lineage: o.Lineage})
			}
		}
		if err := write(mainPath, site.ReplaceHAProxyBlock(string(main), site.HAProxyBlock(sites))); err != nil {
			return "", err
		}
		file = mainPath
	}
	// Прокси мог быть остановлен (только что поставлен) — запустить.
	if res, err := c.Run(ctx, "systemctl", "is-active", rec.Proxy); err == nil && strings.TrimSpace(res.Stdout) != "active" {
		if _, err := s.services.Action(ctx, user, rec.Proxy, "start"); err != nil {
			return "", err
		}
		jc.Log("site.proxyStarted", rec.Proxy)
	}
	return file, nil
}

// siteCombinedPEM — сертификат и ключ lineage одним файлом (HAProxy);
// продление nkt пересобирает такие копии само (по отпечатку).
func (s *Server) siteCombinedPEM(lineage string) ([]byte, error) {
	c := s.scanner.Collector()
	chain, err := c.ReadFile(parse.LetsEncryptLive + lineage + "/fullchain.pem")
	if err != nil {
		return nil, err
	}
	key, err := c.ReadFile(parse.LetsEncryptLive + lineage + "/privkey.pem")
	if err != nil {
		return nil, err
	}
	return append(append(append([]byte{}, chain...), '\n'), key...), nil
}

// handleSiteRemove — POST /sites/remove {domain}: убрать конфигурацию
// прокси сайта. Сертификат и публикация сервиса остаются.
func (s *Server) handleSiteRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
		// Unpublish — снять публикацию сервиса на 127.0.0.1 (файл
		// compose.nkt.yml) и пересоздать сервис без неё.
		Unpublish bool `json:"unpublish"`
		// DeleteCert — удалить и сертификат (certbot delete), если его не
		// использует другой сайт.
		DeleteCert bool `json:"delete_cert"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	lang := msgs.LangFromRequest(r)
	c := s.scanner.Collector()
	all := s.hostSites(ctx)
	var rec *HostSite
	var kept []HostSite
	for i := range all {
		if all[i].Domains[0] == req.Domain {
			rec = &all[i]
			continue
		}
		kept = append(kept, all[i])
	}
	if rec == nil {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("site.notFound", req.Domain))
		return
	}
	var err error
	switch rec.Proxy {
	case site.ProxyNginx, site.ProxyCaddy:
		if raw, rerr := c.ReadFile(rec.File); rerr == nil && s.configs != nil {
			_, _ = s.configs.RecordDoc(ctx, rec.File, rec.Proxy, user, store.ActionEdit, msgs.T(lang, "site.noteRemoved", req.Domain), raw, []byte{})
		}
		if err = c.DeleteFile(rec.File); err == nil {
			_, err = s.services.Action(ctx, user, rec.Proxy, "reload")
		}
	case site.ProxyHAProxy:
		main, rerr := c.ReadFile(s.cfg.HAProxyMainConf)
		if rerr != nil {
			err = rerr
			break
		}
		var sites []site.Config
		for _, o := range kept {
			if o.Proxy == site.ProxyHAProxy {
				sites = append(sites, site.Config{Domains: o.Domains, Upstream: o.Upstream, Lineage: o.Lineage})
			}
		}
		var res control.WriteResult
		res, err = s.configs.Write(ctx, lang, user, s.cfg.HAProxyMainConf, site.ReplaceHAProxyBlock(string(main), site.HAProxyBlock(sites)), msgs.T(lang, "site.noteRemoved", req.Domain), true)
		if err == nil && res.RolledBack {
			err = msgs.Errorf("site.proxyRejected", s.cfg.HAProxyMainConf, res.Message)
		}
		if err == nil {
			_ = c.DeleteFile(site.HAProxyPEM(rec.Domains[0]))
		}
	}
	if err != nil {
		s.db.Audit(ctx, user, "site.remove", req.Domain, "error", err.Error())
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if kept == nil {
		kept = []HostSite{}
	}
	_ = s.saveHostSites(ctx, kept)
	out := map[string]any{"ok": true}
	if req.Unpublish && rec.Stack != "" && rec.HostPort > 0 {
		done, err := s.siteUnpublish(ctx, user, lang, *rec)
		if err != nil {
			out["unpublish_error"] = msgs.Localize(lang, err)
		}
		out["unpublished"] = done
	}
	if req.DeleteCert && rec.Lineage != "" {
		shared := false
		for _, o := range kept {
			if o.Lineage == rec.Lineage {
				shared = true
			}
		}
		switch {
		case shared:
			out["cert_shared"] = true
		case !lineageRe.MatchString(rec.Lineage):
			out["cert_error"] = msgs.T(lang, "site.badLineage", rec.Lineage)
		default:
			res, err := c.RunTimeout(ctx, 2*time.Minute, "certbot", "delete", "--cert-name", rec.Lineage, "--non-interactive")
			if err == nil && !res.OK() {
				err = msgs.Errorf("compose.commandFailed", "certbot delete", res.ExitCode)
			}
			if err != nil {
				out["cert_error"] = msgs.Localize(lang, err) + " " + strings.TrimSpace(res.Output())
			} else {
				out["cert_deleted"] = rec.Lineage
			}
		}
	}
	s.db.Audit(ctx, user, "site.remove", req.Domain, "ok", out)
	s.rescanLater()
	writeJSON(w, http.StatusOK, out)
}

// lineageRe — имя сертификата certbot (домен, возможно с «-0001»).
var lineageRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

// siteUnpublish убирает публикацию сервиса сайта из compose.nkt.yml и
// пересоздаёт сервис (up -d) без неё. false — публиковал не nkt.
func (s *Server) siteUnpublish(ctx context.Context, user string, lang msgs.Lang, rec HostSite) (bool, error) {
	c := s.scanner.Collector()
	dir := composeStackDir(rec.Stack)
	ovPath := dir + "/" + site.OverrideFile
	old, err := c.ReadFile(ovPath)
	if err != nil {
		return false, nil
	}
	content, changed, err := site.RemoveOverride(string(old), rec.Service, rec.HostPort, rec.ContainerPort)
	if err != nil || !changed {
		return false, err
	}
	if content == "" {
		err = c.DeleteFile(ovPath)
	} else {
		err = c.WriteFile(ovPath, []byte(content), 0o644)
	}
	if err != nil {
		return false, err
	}
	if s.configs != nil {
		_, _ = s.configs.RecordDoc(ctx, ovPath, "docker", user, store.ActionEdit, msgs.T(lang, "site.noteRemoved", rec.Domains[0]), old, []byte(content))
	}
	file := composeFileIn(c, dir)
	engine := composeEngine(ctx, c)
	if file == "" || engine == "" {
		return true, nil
	}
	res, err := c.RunTimeout(ctx, 10*time.Minute, engine, append(composeArgs(c, rec.Stack, file), "up", "-d", rec.Service)...)
	if err == nil && !res.OK() {
		err = msgs.Errorf("compose.commandFailed", "up -d "+rec.Service, res.ExitCode)
	}
	return true, err
}
