package hub

import (
	"context"
	"slices"

	"github.com/piqab/nkt/internal/store"
)

// Сайты выкладок в экспорте хаба: хост и конвейер — по именам, имя сайта —
// первый домен. Хоста с таким именем нет — сайт не импортируется (он
// описывает настройку прокси на конкретной машине); конвейера нет — сайт
// импортируется как заведённый вручную.

func siteName(domains []string) string {
	if len(domains) == 0 {
		return ""
	}
	return domains[0]
}

// exportSites — все сайты хаба.
func (m *Manager) exportSites(ctx context.Context, export *store.HubExport) error {
	sites, err := m.db.ListSites(ctx)
	if err != nil {
		return err
	}
	byID, _ := m.hostNames(ctx)
	pipes := map[int64]string{}
	if list, err := m.db.ListPipelines(ctx); err == nil {
		for _, p := range list {
			pipes[p.ID] = p.Name
		}
	}
	for _, s := range sites {
		host, ok := byID[s.HostID]
		if !ok || siteName(s.Domains) == "" {
			continue
		}
		export.Sites = append(export.Sites, store.SiteExport{Domains: s.Domains, Host: host, Proxy: s.Proxy, Stack: s.Stack,
			Service: s.Service, ContainerPort: s.ContainerPort, Upstream: s.Upstream, OpenFirewall: s.OpenFirewall,
			Pipeline: pipes[s.PipelineID], Cert: s.Cert, CertKey: s.CertKey, Status: s.Status, Error: s.Error, Author: s.Author, CreatedAt: s.CreatedAt})
	}
	return nil
}

// siteIDs — номера сайтов этого хаба по имени.
func (m *Manager) siteIDs(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	if list, err := m.db.ListSites(ctx); err == nil {
		for _, s := range list {
			out[siteName(s.Domains)] = s.ID
		}
	}
	return out
}

// planSites — раздел «Сайты» плана импорта.
func (m *Manager) planSites(ctx context.Context, export store.HubExport) store.PlanSection {
	ids := m.siteIDs(ctx)
	sec := store.PlanSection{Section: store.SectionSites, Items: []store.PlanItem{}}
	for _, s := range export.Sites {
		name := siteName(s.Domains)
		_, taken := ids[name]
		sec.Items = append(sec.Items, store.PlanItem{Name: name, Replaceable: true, Conflict: taken})
	}
	return sec
}

var siteProxies = []string{"nginx", "haproxy", "caddy"}

// importSites — сайты из файла (после хостов и конвейеров).
func (m *Manager) importSites(ctx context.Context, list []store.SiteExport, res store.ImportResolutions, rep *store.ImportReport) {
	if len(list) == 0 {
		return
	}
	cnt := rep.Count(store.SectionSites)
	_, hostByName := m.hostNames(ctx)
	pipes := map[string]int64{}
	if all, err := m.db.ListPipelines(ctx); err == nil {
		for _, p := range all {
			pipes[p.Name] = p.ID
		}
	}
	ids := m.siteIDs(ctx)
	for _, e := range list {
		name := siteName(e.Domains)
		if name == "" || !slices.Contains(siteProxies, e.Proxy) {
			rep.Err("site %q: proxy %q", name, e.Proxy)
			continue
		}
		hostID, ok := hostByName[e.Host]
		if !ok {
			rep.Err("site %q: host %q missing — not imported", name, e.Host)
			continue
		}
		var pipelineID int64
		if e.Pipeline != "" {
			if pipelineID, ok = pipes[e.Pipeline]; !ok {
				rep.Err("site %q: pipeline %q missing — imported as a manual site", name, e.Pipeline)
			}
		}
		s := store.Site{Domains: e.Domains, HostID: hostID, Proxy: e.Proxy, Stack: e.Stack, Service: e.Service,
			ContainerPort: e.ContainerPort, Upstream: e.Upstream, OpenFirewall: e.OpenFirewall, PipelineID: pipelineID, Cert: e.Cert, CertKey: e.CertKey, Author: e.Author}
		if id, taken := ids[name]; taken {
			if !res.Replace(store.SectionSites, name) {
				cnt.Skipped++
				continue
			}
			s.ID = id
		}
		id, err := m.db.SaveSite(ctx, s)
		if err != nil {
			rep.Err("site %q: %v", name, err)
			continue
		}
		if s.ID != 0 {
			cnt.Replaced++
		} else {
			cnt.Added++
		}
		ids[name] = id
		if err := m.db.SetSiteState(ctx, id, e.Status, e.Error, 0); err != nil {
			rep.Err("site %q: %v", name, err)
		}
	}
}
