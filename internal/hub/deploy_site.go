package hub

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
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
	want := store.Site{Domains: sp.Domains, HostID: t.ID, Proxy: proxy, Stack: c.Project, Service: sp.Service,
		ContainerPort: sp.Port, OpenFirewall: sp.OpenFirewall(), PipelineID: pl.ID, Author: user}
	if cur != nil {
		want.ID = cur.ID
		if sameSite(*cur, want) && cur.Status == store.SiteOK {
			chk := httpsCheck(ctx, want.Domains[0])
			var full SiteCheck
			_ = json.Unmarshal(cur.Check, &full)
			full.HTTPS = chk
			raw, _ := json.Marshal(full)
			_ = s.db.SetSiteCheck(ctx, cur.ID, raw)
			if chk.OK {
				jc.Log("deploy.siteOK", want.Domains[0], chk.Status, chk.CertDaysLeft)
			} else {
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

// dryRunSite — что сделала бы выкладка с сайтом (только журнал: неудача
// сайта выкладку не отменяет, поэтому здесь — предупреждения).
func (s *Server) dryRunSite(ctx context.Context, jc *jobs.Context, user string, pl store.Pipeline, c *deploy.ComposeSpec, t targetHost, services []string) {
	sp := c.Site
	cur, clash, err := s.pipelineSiteFor(ctx, pl, sp.Domains)
	if err != nil {
		jc.Log("deploy.drySiteWarn", msgs.Localize(jc.Lang(), err))
		return
	}
	if clash != "" {
		jc.Log("deploy.drySiteWarn", msgs.T(jc.Lang(), "hub.siteExists", clash))
	}
	proxy, installed := sp.Proxy, true
	if proxy == "" && cur != nil {
		proxy = cur.Proxy
	}
	if proxy == "" {
		if proxy, installed, err = s.pickProxy(ctx, user, t); err != nil {
			jc.Log("deploy.drySiteWarn", msgs.Localize(jc.Lang(), err))
			return
		}
	}
	if !installed {
		jc.Log("deploy.drySiteProxyInstall", proxy)
	} else {
		jc.Log("deploy.drySiteProxy", proxy)
	}
	if cur != nil && cur.Status == store.SiteOK && sameSite(*cur, store.Site{Domains: sp.Domains, HostID: t.ID, Proxy: proxy,
		Stack: c.Project, Service: sp.Service, ContainerPort: sp.Port, OpenFirewall: sp.OpenFirewall()}) {
		jc.Log("deploy.drySiteSame", sp.Domains[0])
		return
	}
	jc.Log("deploy.drySiteWill", strings.Join(sp.Domains, ", "), sp.Service, sp.Port)
	chk := s.siteOutside(ctx, t, sp.Domains, false)
	for _, d := range chk.DNS {
		switch {
		case d.Match:
			jc.Log("hub.siteDNSOK", d.Domain, strings.Join(d.DomainIPs, ", "))
		case len(d.DomainIPs) == 0:
			jc.Log("deploy.drySiteNoDNS", d.Domain)
		default:
			jc.Log("deploy.drySiteDNS", d.Domain, strings.Join(d.DomainIPs, ", "), strings.Join(d.HostIPs, ", "))
		}
	}
	for _, port := range []string{"80", "443"} {
		jc.Log("hub.sitePort", port, chk.Ports[port])
	}
	if chk.Ports["80"] == "timeout" {
		jc.Log("hub.sitePort80Blocked")
	}
}
