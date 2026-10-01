package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/site"
	"github.com/piqab/nkt/internal/store"
)

// Сайт из блока site: конвейера (action: compose). После выкладки стека
// хаб заводит сайт (или находит свой — по конвейеру) и настраивает его
// тем же путём, что мастер «Сайтов». Настроенный и не изменившийся сайт
// только проверяется по HTTPS: сертификат при каждой выкладке заново не
// выпускается. Неудача сайта выкладку не отменяет — стек уже обновлён.

// siteProxyState — прокси на хосте (ответ /api/sites/preflight).
type siteProxyState struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
}

// pickProxy — прокси сайта, если в конвейере не указан: работающий, иначе
// установленный, иначе nginx (будет установлен). second — «установлен ли».
func (s *Server) pickProxy(ctx context.Context, user string, t targetHost) (string, bool, error) {
	var pre struct {
		Proxies []siteProxyState `json:"proxies"`
	}
	if _, err := s.hostCall(ctx, user, t.ID, "GET", "/api/sites/preflight", nil, &pre); err != nil {
		return "", false, err
	}
	for _, p := range pre.Proxies {
		if p.Running {
			return p.Name, true, nil
		}
	}
	for _, p := range pre.Proxies {
		if p.Installed {
			return p.Name, true, nil
		}
	}
	for _, p := range pre.Proxies {
		if p.Name == "nginx" {
			return "nginx", false, nil
		}
	}
	return "nginx", false, nil
}

// pipelineSiteFor — сайт конвейера и чужой сайт на одно из имён (конфликт).
func (s *Server) pipelineSiteFor(ctx context.Context, pl store.Pipeline, domains []string) (cur *store.Site, clash string, err error) {
	list, err := s.db.ListSites(ctx)
	if err != nil {
		return nil, "", err
	}
	for i := range list {
		o := list[i]
		if pl.ID > 0 && o.PipelineID == pl.ID {
			cur = &list[i]
			continue
		}
		for _, d := range o.Domains {
			if slices.Contains(domains, d) {
				clash = o.Domains[0]
			}
		}
	}
	return cur, clash, nil
}

// sameSite — настройки сайта не изменились.
func sameSite(a, b store.Site) bool {
	return slices.Equal(a.Domains, b.Domains) && a.HostID == b.HostID && a.Proxy == b.Proxy && a.Stack == b.Stack &&
		a.Service == b.Service && a.ContainerPort == b.ContainerPort && a.OpenFirewall == b.OpenFirewall && a.Upstream == ""
}

// pipelineSite — сайт после выкладки стека на хост t.
func (s *Server) pipelineSite(ctx context.Context, jc *jobs.Context, user string, pl store.Pipeline, c *deploy.ComposeSpec, t targetHost) error {
	sp := c.Site
	cur, clash, err := s.pipelineSiteFor(ctx, pl, sp.Domains)
	if err != nil {
		return err
	}
	if clash != "" {
		return msgs.Errorf("hub.siteExists", clash)
	}
	proxy := sp.Proxy
	if proxy == "" && cur != nil {
		proxy = cur.Proxy
	}
	if proxy == "" {
		if proxy, _, err = s.pickProxy(ctx, user, t); err != nil {
			return err
		}
	}
	port := sp.Port
	if port == 0 {
		if port, err = s.autoSitePort(ctx, jc, user, t, c.Project, sp.Service); err != nil {
			return err
		}
	}
	want := store.Site{Domains: sp.Domains, HostID: t.ID, Proxy: proxy, Stack: c.Project, Service: sp.Service,
		ContainerPort: port, OpenFirewall: sp.OpenFirewall(), PipelineID: pl.ID, Author: user}
	if cur != nil {
		want.ID = cur.ID
		if sameSite(*cur, want) && cur.Status == store.SiteOK {
			chk := httpsCheck(ctx, want.Domains[0])
			var full SiteCheck
			_ = json.Unmarshal(cur.Check, &full)
			full.HTTPS = chk
			raw, _ := json.Marshal(full)
			_ = s.db.SetSiteCheck(ctx, cur.ID, raw)
			switch {
			case chk.WrongCert:
				err := msgs.Errorf("hub.siteWrongCert", want.Domains[0], strings.Join(chk.CertNames, ", "))
				_ = s.db.SetSiteState(ctx, cur.ID, store.SiteFailed, msgs.Localize(jc.Lang(), err), jc.Job.ID)
				return err
			case chk.OK:
				jc.Log("deploy.siteOK", want.Domains[0], chk.Status, chk.CertDaysLeft)
			default:
				jc.Log("deploy.siteFailed", want.Domains[0], chk.Error)
			}
			return nil
		}
		if cur.HostID != want.HostID && len(cur.Domains) > 0 {
			// Стек переехал на другой хост: конфигурацию прокси на старом
			// убрать (сертификат и публикация там остаются).
			if _, err := s.hostCall(ctx, user, cur.HostID, "POST", "/api/sites/remove", map[string]string{"domain": cur.Domains[0]}, nil); err != nil {
				jc.Log("deploy.siteOldHostLeft", cur.Domains[0], msgs.Localize(jc.Lang(), err))
			}
		}
	}
	id, err := s.db.SaveSite(ctx, want)
	if err != nil {
		return err
	}
	want.ID = id
	jc.Log("deploy.siteSetup", strings.Join(want.Domains, ", "), proxy, t.Name)
	return s.setupSite(ctx, jc, user, want, true, false)
}

// autoSitePort — site.port: auto (или не задан): порт, который объявляет
// образ сервиса, если он один.
func (s *Server) autoSitePort(ctx context.Context, jc *jobs.Context, user string, t targetHost, stack, service string) (int, error) {
	var pc struct {
		Checked      bool   `json:"checked"`
		StackMissing bool   `json:"stack_missing"`
		Found        bool   `json:"found"`
		Ports        []int  `json:"ports"`
		Image        string `json:"image"`
	}
	q := "/api/sites/port-check?stack=" + url.QueryEscape(stack) + "&service=" + url.QueryEscape(service)
	code, err := s.hostCall(ctx, user, t.ID, "GET", q, nil, &pc)
	switch {
	case code == http.StatusNotFound || code == http.StatusMethodNotAllowed:
		return 0, msgs.Errorf("deploy.sitePortAutoOld", t.Name)
	case err != nil:
		return 0, err
	case !pc.Checked:
		return 0, msgs.Errorf("deploy.sitePortAutoUnknown")
	case pc.StackMissing:
		return 0, msgs.Errorf("site.stackMissing", stack)
	case !pc.Found:
		return 0, msgs.Errorf("site.serviceMissing", service, stack)
	case len(pc.Ports) != 1:
		return 0, msgs.Errorf("deploy.sitePortAutoMany", pc.Image, portsText(pc.Ports))
	}
	jc.Log("deploy.sitePortAuto", pc.Ports[0], pc.Image)
	return pc.Ports[0], nil
}

func portsText(ports []int) string {
	if len(ports) == 0 {
		return "—"
	}
	out := make([]string, len(ports))
	for i, p := range ports {
		out[i] = strconv.Itoa(p)
	}
	return strings.Join(out, ", ")
}

// dryRunSite — что сделала бы выкладка с сайтом; число проблем (то, на
// чём настройка сайта не пройдёт: выкладка стека при этом удалась бы).
func (s *Server) dryRunSite(ctx context.Context, jc *jobs.Context, user string, pl store.Pipeline, c *deploy.ComposeSpec, t targetHost, port int) int {
	sp := c.Site
	lang := jc.Lang()
	problems := 0
	cur, clash, err := s.pipelineSiteFor(ctx, pl, sp.Domains)
	if err != nil {
		jc.Log("deploy.drySiteWarn", msgs.Localize(lang, err))
		return 1
	}
	if clash != "" {
		problems++
		jc.Log("deploy.drySiteBad", msgs.T(lang, "hub.siteExists", clash))
	}
	proxy, installed := sp.Proxy, true
	if proxy == "" && cur != nil {
		proxy = cur.Proxy
	}
	if proxy == "" {
		if proxy, installed, err = s.pickProxy(ctx, user, t); err != nil {
			jc.Log("deploy.drySiteWarn", msgs.Localize(lang, err))
			return problems + 1
		}
	}
	if !installed {
		jc.Log("deploy.drySiteProxyInstall", proxy)
	} else {
		jc.Log("deploy.drySiteProxy", proxy)
	}
	problems += s.dryRunHostSite(ctx, jc, user, t, sp, proxy, installed)
	if port == 0 {
		port = sp.Port
	}
	if cur != nil && cur.Status == store.SiteOK && sameSite(*cur, store.Site{Domains: sp.Domains, HostID: t.ID, Proxy: proxy,
		Stack: c.Project, Service: sp.Service, ContainerPort: port, OpenFirewall: sp.OpenFirewall()}) {
		jc.Log("deploy.drySiteSame", sp.Domains[0])
		return problems
	}
	jc.Log("deploy.drySiteWill", strings.Join(sp.Domains, ", "), sp.Service, port)
	chk := s.siteOutside(ctx, t, sp.Domains, false)
	for _, d := range chk.DNS {
		switch {
		case d.Match:
			jc.Log("hub.siteDNSOK", d.Domain, strings.Join(d.DomainIPs, ", "))
		case len(d.DomainIPs) == 0:
			problems++
			jc.Log("deploy.drySiteNoDNS", d.Domain)
		default:
			problems++
			jc.Log("deploy.drySiteDNS", d.Domain, strings.Join(d.DomainIPs, ", "), strings.Join(d.HostIPs, ", "))
		}
		// AAAA не на хост: Let's Encrypt предпочитает IPv6 и придёт не туда,
		// даже если A-запись верна.
		if v6 := foreignV6(d.DomainIPs, d.HostIPs); len(v6) > 0 {
			problems++
			jc.Log("deploy.drySiteAAAA", d.Domain, strings.Join(v6, ", "))
		}
	}
	for _, p := range []string{"80", "443"} {
		jc.Log("hub.sitePort", p, chk.Ports[p])
	}
	if chk.Ports["80"] == "timeout" {
		problems++
		jc.Log("deploy.drySitePort80")
	}
	return problems
}

// foreignV6 — IPv6-адреса имени, которых нет у хоста (пусто — AAAA нет
// или они на хост).
func foreignV6(domainIPs, hostIPs []string) []string {
	var v6, out []string
	for _, ip := range domainIPs {
		if a, err := netip.ParseAddr(ip); err == nil && a.Is6() && !a.Is4In6() {
			v6 = append(v6, ip)
		}
	}
	for _, ip := range v6 {
		if !slices.Contains(hostIPs, ip) {
			out = append(out, ip)
		}
	}
	if len(out) == len(v6) {
		return out
	}
	return nil // хотя бы один AAAA ведёт на хост
}

// dryRunHostSite — хост глазами настройки сайта: certbot, файрвол, кто
// держит 80/443, сертификат, конфигурация nginx. Число проблем.
func (s *Server) dryRunHostSite(ctx context.Context, jc *jobs.Context, user string, t targetHost, sp *deploy.SiteSpec, proxy string, proxyInstalled bool) int {
	problems := 0
	var pre struct {
		Certbot bool `json:"certbot"`
		Holders []struct {
			Port      int    `json:"port"`
			Process   string `json:"process"`
			Unit      string `json:"unit"`
			Container string `json:"container"`
		} `json:"holders"`
		Firewall *struct {
			Manager  string `json:"manager"`
			Active   bool   `json:"active"`
			Open80   bool   `json:"open80"`
			Open443  bool   `json:"open443"`
			Writable *bool  `json:"writable"`
		} `json:"firewall"`
	}
	if _, err := s.hostCall(ctx, user, t.ID, "GET", "/api/sites/preflight", nil, &pre); err != nil || pre.Firewall == nil {
		return 0
	}
	if !pre.Certbot && proxy != site.ProxyCaddy {
		jc.Log("deploy.dryCertbotInstall")
	}
	fw := pre.Firewall
	switch {
	case !fw.Active:
		jc.Log("deploy.dryFirewallOff")
	case fw.Open80 && fw.Open443:
		jc.Log("deploy.dryFirewallOpen", fw.Manager)
	case !sp.OpenFirewall():
		jc.Log("deploy.dryFirewallClosedSkip", fw.Manager)
	case fw.Writable != nil && !*fw.Writable:
		jc.Log("deploy.dryFirewallNotWritable", fw.Manager)
	default:
		jc.Log("deploy.dryFirewallWillOpen", fw.Manager)
	}
	// 80/443 держит не наш прокси: certbot --standalone не займёт порт,
	// прокси сайта не встанет.
	seen := map[string]bool{}
	for _, h := range pre.Holders {
		name := h.Process
		if h.Container != "" {
			name = "container " + h.Container
		} else if name == "" {
			name = h.Unit
		}
		if h.Container == "" && (name == proxy || (h.Unit != "" && strings.HasPrefix(h.Unit, proxy))) {
			continue
		}
		key := strconv.Itoa(h.Port) + name
		if seen[key] {
			continue
		}
		seen[key] = true
		problems++
		jc.Log("deploy.drySiteHolder", h.Port, name, proxy)
	}
	// С хоста: сертификат, conf.d у nginx, чужие server_name.
	var dry struct {
		Cert *struct {
			Lineage  string `json:"lineage"`
			DaysLeft int    `json:"days_left"`
		} `json:"cert"`
		NginxConfD *bool    `json:"nginx_conf_d"`
		Conflicts  []string `json:"conflicts"`
	}
	q := "/api/sites/dry?domains=" + url.QueryEscape(strings.Join(sp.Domains, ",")) + "&proxy=" + url.QueryEscape(proxy)
	code, err := s.hostCall(ctx, user, t.ID, "GET", q, nil, &dry)
	switch {
	case code == http.StatusNotFound || code == http.StatusMethodNotAllowed:
		jc.Log("deploy.drySiteOldHost", t.Name)
		return problems
	case err != nil:
		jc.Log("deploy.drySiteWarn", msgs.Localize(jc.Lang(), err))
		return problems
	}
	switch {
	case proxy == site.ProxyCaddy:
		jc.Log("deploy.drySiteCertCaddy")
	case dry.Cert != nil && dry.Cert.DaysLeft > 20:
		jc.Log("deploy.drySiteCertReuse", dry.Cert.Lineage, dry.Cert.DaysLeft)
	default:
		jc.Log("deploy.drySiteCertIssue", strings.Join(sp.Domains, ", "))
	}
	if dry.NginxConfD != nil && !*dry.NginxConfD && proxyInstalled {
		problems++
		jc.Log("deploy.drySiteNoConfD")
	}
	for _, c := range dry.Conflicts {
		problems++
		jc.Log("deploy.drySiteConflict", c)
	}
	return problems
}
