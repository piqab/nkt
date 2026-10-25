package hub

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/plugin"
	"github.com/piqab/nkt/internal/store"
)

// DNS через плагин (provides: dns). Хаб ведёт записи «владельцев»:
//
//	fly:<имя>    — имя маршрута Traffic Fly с галочкой «DNS» → адреса его
//	               шлюзов (несколько шлюзов — Failover: живые адреса);
//	site:<конв.> — домены сайта конвейера с site.dns (auto — публичный
//	               адрес хоста, или адреса списком);
//	host:<имя>   — автоимена: <хост>.<зона> → адрес хоста в хабе.
//
// Что поставлено, хаб помнит (dns.owned): плагину уходит и прежнее, чтобы
// он убрал лишнее, не трогая чужие значения у того же имени. Без
// подключённого DNS-плагина ничего этого нет.

const (
	// dnsPluginRecordsKV — записи, которые попросили поставить плагины
	// (кнопка «Поставить через DNS-плагин»): владелец plugin:<имя>:… →
	// записи.
	dnsPluginRecordsKV = "dns.plugin_records"
	dnsOwnedKV         = "dns.owned"
	dnsAutoKV          = "dns.autonames"
	dnsTTL             = 300
	// dnsReconcileEvery — сверка: адрес хоста сменился, плагин был
	// недоступен.
	dnsReconcileEvery = 10 * time.Minute
)

// dnsRecord — записи одного имени и типа (как в _dns_sync).
type dnsRecord struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Values   []string `json:"values"`
	TTL      int      `json:"ttl,omitempty"`
	Failover bool     `json:"failover,omitempty"`
	Port     int      `json:"port,omitempty"`
}

// dnsOwned — что поставлено владельцу и каким плагином.
type dnsOwned struct {
	Plugin  string      `json:"plugin"`
	Records []dnsRecord `json:"records"`
	At      string      `json:"at,omitempty"`
}

// dnsAutonames — автоимена хостов: <хост>.<Zone>.
type dnsAutonames struct {
	Zone    string `json:"zone"`
	Enabled bool   `json:"enabled"`
}

var dnsMu sync.Mutex

func (s *Server) dnsOwnedAll(ctx context.Context) map[string]dnsOwned {
	out := map[string]dnsOwned{}
	if raw, ok, err := s.db.KVGet(ctx, dnsOwnedKV); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) dnsAutonames(ctx context.Context) dnsAutonames {
	var a dnsAutonames
	if raw, ok, err := s.db.KVGet(ctx, dnsAutoKV); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &a)
	}
	return a
}

// dnsPlugins — подключённые плагины, которые ведут DNS.
func (s *Server) dnsPlugins(ctx context.Context) []pluginConf {
	var out []pluginConf
	for _, p := range s.plugins(ctx) {
		if m, err := s.pluginManifest(ctx, p); err == nil && m.Has(plugin.ProvidesDNS) {
			out = append(out, p)
		}
	}
	return out
}

// Зоны плагинов — в памяти на минуту.
var (
	dnsZonesMu    sync.Mutex
	dnsZonesCache = map[string]struct {
		zones []string
		at    time.Time
	}{}
)

func (s *Server) dnsZonesOf(ctx context.Context, p pluginConf) []string {
	dnsZonesMu.Lock()
	e, ok := dnsZonesCache[p.Name]
	dnsZonesMu.Unlock()
	if ok && time.Since(e.at) < time.Minute {
		return e.zones
	}
	var zones []string
	if resp, err := s.pluginCall(ctx, p, plugin.Request{Op: plugin.OpDNSZones}); err == nil {
		var res struct {
			Zones []string `json:"zones"`
		}
		if json.Unmarshal(resp.Result, &res) == nil {
			zones = res.Zones
		}
	}
	dnsZonesMu.Lock()
	dnsZonesCache[p.Name] = struct {
		zones []string
		at    time.Time
	}{zones, time.Now()}
	dnsZonesMu.Unlock()
	return zones
}

// dnsPluginFor — плагин, в чьей зоне имя (самая длинная подходящая).
func (s *Server) dnsPluginFor(ctx context.Context, plugins []pluginConf, name string) (pluginConf, bool) {
	var best pluginConf
	bestLen := 0
	for _, p := range plugins {
		for _, z := range s.dnsZonesOf(ctx, p) {
			if (name == z || strings.HasSuffix(name, "."+z)) && len(z) > bestLen {
				best, bestLen = p, len(z)
			}
		}
	}
	return best, bestLen > 0
}

// publicIP — адрес годится для записи в интернете.
func publicIP(a netip.Addr) bool {
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a.Unmap())
}

// hostPublicIPs — публичные адреса хоста: адрес в хабе и адреса
// интерфейсов (по SSH); машина хаба — свои интерфейсы.
func (s *Server) hostPublicIPs(ctx context.Context, t targetHost) []string {
	rep := s.siteDNS(ctx, t, "localhost")
	var out []string
	for _, ip := range rep.HostIPs {
		if a, err := netip.ParseAddr(ip); err == nil && publicIP(a) {
			out = append(out, a.Unmap().String())
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.Contains(out[j], ":") && !strings.Contains(out[i], ":") })
	return out
}

// recordsFor — адреса → записи A и AAAA имени.
func recordsFor(name string, ips []string, failover bool, port int) []dnsRecord {
	var v4, v6 []string
	for _, ip := range ips {
		a, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		if a.Unmap().Is4() {
			v4 = append(v4, a.Unmap().String())
		} else {
			v6 = append(v6, a.String())
		}
	}
	var out []dnsRecord
	if len(v4) > 0 {
		out = append(out, dnsRecord{Name: name, Type: "A", Values: v4, TTL: dnsTTL, Failover: failover && len(v4) > 1, Port: port})
	}
	if len(v6) > 0 {
		out = append(out, dnsRecord{Name: name, Type: "AAAA", Values: v6, TTL: dnsTTL, Failover: failover && len(v6) > 1, Port: port})
	}
	return out
}

var autonameRe = regexp.MustCompile(`[^a-z0-9-]+`)

// autoname — метка DNS из имени хоста.
func autoname(host string) string {
	n := strings.Trim(autonameRe.ReplaceAllString(strings.ToLower(host), "-"), "-")
	if len(n) > 63 {
		n = n[:63]
	}
	return n
}

// dnsDesired — какие записи должны быть у владельцев сейчас.
func (s *Server) dnsDesired(ctx context.Context) (map[string][]dnsRecord, []string) {
	want := map[string][]dnsRecord{}
	var warns []string
	// Traffic Fly: имя → адреса шлюзов всех маршрутов с DNS.
	d := s.flyDoc(ctx)
	edges := map[int64]EdgeSettings{}
	for _, st := range s.edges(ctx) {
		edges[st.ID] = st
	}
	type nameGW struct {
		ips  []string
		port int
	}
	byName := map[string]*nameGW{}
	for _, r := range d.Routes {
		if !r.DNS {
			continue
		}
		st, ok := edges[r.Gateway]
		if !ok {
			continue
		}
		ep, err := flyEndpoint(ctx, st, 1)
		if err != nil {
			warns = append(warns, msgs.Tc(ctx, "plugin.dnsNoGateway", r.ID, err))
			continue
		}
		ip, _, _ := net.SplitHostPort(ep)
		port := 443
		if r.Kind == "http" {
			port = 80
		} else if r.Kind != "https" && r.Port > 0 {
			port = r.Port
		}
		for _, n := range r.Names {
			n = strings.ToLower(strings.TrimSuffix(n, "."))
			if strings.HasPrefix(n, "*.") {
				continue
			}
			g := byName[n]
			if g == nil {
				g = &nameGW{port: port}
				byName[n] = g
			}
			if !slices.Contains(g.ips, ip) {
				g.ips = append(g.ips, ip)
			}
		}
	}
	for n, g := range byName {
		sort.Strings(g.ips)
		want["fly:"+n] = recordsFor(n, g.ips, true, g.port)
	}
	// Сайты конвейеров с site.dns.
	if pipes, err := s.db.ListPipelines(ctx); err == nil {
		sites, _ := s.db.ListSites(ctx)
		for _, pl := range pipes {
			spec, err := deploy.ParseSpec(pl.Content)
			if err != nil || spec.Compose == nil || spec.Compose.Site == nil || spec.Compose.Site.DNS == "" {
				continue
			}
			sp := spec.Compose.Site
			var ips []string
			if sp.DNS == deploy.SiteDNSAuto {
				i := slices.IndexFunc(sites, func(x store.Site) bool { return x.PipelineID == pl.ID })
				if i < 0 {
					continue // ещё не выложен — адреса нет
				}
				targets, err := s.resolveHostsByID(ctx, sites[i].HostID)
				if err != nil {
					continue
				}
				ips = s.hostPublicIPs(ctx, targets)
				if len(ips) == 0 {
					warns = append(warns, msgs.Tc(ctx, "plugin.dnsNoPublicIP", pl.Name, targets.Name))
					continue
				}
			} else {
				ips = sp.DNSAddrs()
			}
			var recs []dnsRecord
			for _, dom := range sp.Domains {
				recs = append(recs, recordsFor(strings.ToLower(dom), ips, false, 0)...)
			}
			want["site:"+pl.Name] = recs
		}
	}
	// Записи от плагинов (почта и т. п.).
	if raw, ok, err := s.db.KVGet(ctx, dnsPluginRecordsKV); err == nil && ok {
		var extra map[string][]dnsRecord
		if json.Unmarshal([]byte(raw), &extra) == nil {
			for o, recs := range extra {
				want[o] = recs
			}
		}
	}
	// Автоимена хостов.
	if a := s.dnsAutonames(ctx); a.Enabled && a.Zone != "" {
		if hosts, err := s.db.ListHosts(ctx); err == nil {
			for _, h := range hosts {
				addr, err := netip.ParseAddr(h.Addr)
				label := autoname(h.Name)
				if err != nil || label == "" {
					continue
				}
				want["host:"+h.Name] = recordsFor(label+"."+a.Zone, []string{addr.String()}, false, 0)
			}
		}
	}
	return want, warns
}

// resolveHostsByID — хост по id как targetHost.
func (s *Server) resolveHostsByID(ctx context.Context, id int64) (targetHost, error) {
	if id == localHostID {
		return targetHost{ID: localHostID, Name: localHostName}, nil
	}
	h, err := s.db.HostByID(ctx, id)
	if err != nil {
		return targetHost{}, err
	}
	return targetHost{ID: h.ID, Name: h.Name, Addr: h.Addr}, nil
}

func sameRecords(a, b []dnsRecord) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return string(ra) == string(rb)
}

// dnsSync — привести записи владельцев (filter — какие; nil — все) к
// желаемым. dry — только спросить плагин, что изменится. logf — строки в
// журнал задания (или nil).
func (s *Server) dnsSync(ctx context.Context, filter func(owner string) bool, dry bool, logf func(key string, args ...any)) error {
	plugins := s.dnsPlugins(ctx)
	if len(plugins) == 0 {
		return nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	dnsMu.Lock()
	defer dnsMu.Unlock()
	want, warns := s.dnsDesired(ctx)
	for _, w := range warns {
		logf("plugin.dnsWarn", w)
	}
	owned := s.dnsOwnedAll(ctx)
	owners := map[string]bool{}
	for o := range want {
		owners[o] = true
	}
	for o := range owned {
		owners[o] = true
	}
	keys := make([]string, 0, len(owners))
	for o := range owners {
		if filter == nil || filter(o) {
			keys = append(keys, o)
		}
	}
	sort.Strings(keys)
	changed := false
	var firstErr error
	for _, o := range keys {
		recs, prev := want[o], owned[o]
		if len(recs) > 0 && len(prev.Records) > 0 && sameRecords(recs, prev.Records) && !dry {
			continue
		}
		if len(recs) == 0 && len(prev.Records) == 0 {
			continue
		}
		// Плагин — по первому имени в его зонах; имена вне них — мимо (в
		// журнал); был другой плагин — прежнее убрать там.
		p, ok := pluginConf{}, false
		var kept []dnsRecord
		for _, r := range recs {
			rp, found := s.dnsPluginFor(ctx, plugins, r.Name)
			switch {
			case !found:
				logf("plugin.dnsNoZone", r.Name)
			case !ok:
				p, ok = rp, true
				kept = append(kept, r)
			case rp.Name == p.Name:
				kept = append(kept, r)
			default:
				logf("plugin.dnsNoZone", r.Name)
			}
		}
		recs = kept
		if len(recs) > 0 && len(prev.Records) > 0 && sameRecords(recs, prev.Records) && !dry {
			continue
		}
		if len(recs) == 0 && len(prev.Records) == 0 {
			continue
		}
		if prev.Plugin != "" && (!ok || prev.Plugin != p.Name) {
			if old, found := s.pluginByName(ctx, prev.Plugin); found {
				if err := s.dnsSyncOne(ctx, old, o, nil, prev.Records, dry, logf); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			prev.Records = nil
		}
		if !ok {
			if !dry {
				delete(owned, o)
				changed = true
			}
			continue
		}
		if err := s.dnsSyncOne(ctx, p, o, recs, prev.Records, dry, logf); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !dry {
			if len(recs) == 0 {
				delete(owned, o)
			} else {
				owned[o] = dnsOwned{Plugin: p.Name, Records: recs, At: store.FormatTime(time.Now())}
			}
			changed = true
		}
	}
	if changed {
		raw, _ := json.Marshal(owned)
		if err := s.db.KVSet(ctx, dnsOwnedKV, string(raw)); err != nil {
			return err
		}
	}
	return firstErr
}

func (s *Server) dnsSyncOne(ctx context.Context, p pluginConf, owner string, recs, prev []dnsRecord, dry bool, logf func(string, ...any)) error {
	if recs == nil {
		recs = []dnsRecord{}
	}
	if prev == nil {
		prev = []dnsRecord{}
	}
	resp, err := s.pluginCall(ctx, p, plugin.Request{Op: plugin.OpDNSSync, DryRun: dry,
		Args: map[string]any{"owner": owner, "records": recs, "previous": prev}})
	if err != nil {
		logf("plugin.dnsFailed", owner, p.Name, err)
		return msgs.Errorf("plugin.dnsFailed", owner, p.Name, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(resp.Text), "\n") {
		if line != "" {
			logf("plugin.dnsLine", owner, line)
		}
	}
	if !dry {
		s.db.Audit(ctx, auth.Username(ctx), "plugin.dns", owner, "ok", map[string]any{"plugin": p.Name, "records": recs})
	}
	return nil
}

// StartDNSReconcile — сверка записей по таймеру (адреса хостов, плагин
// был недоступен).
func (s *Server) StartDNSReconcile(ctx context.Context) {
	go func() {
		t := time.NewTicker(dnsReconcileEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.dnsSync(ctx, nil, false, nil); err != nil {
					s.log.Warn("dns reconcile", "err", err)
				}
			}
		}
	}()
}

// --- «О системе» → «Плагины»: автоимена и что поставлено -----------------

// handleDNSState — GET /hub/dns: плагины DNS, автоимена (с историей) и
// записи хаба по владельцам.
func (s *Server) handleDNSState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	names := []string{}
	for _, p := range s.dnsPlugins(ctx) {
		names = append(names, p.Name)
	}
	var hist []map[string]any
	if raw, ok, _ := s.db.KVGet(ctx, dnsAutoKV+".history"); ok {
		_ = json.Unmarshal([]byte(raw), &hist)
	}
	if hist == nil {
		hist = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": names, "autonames": s.dnsAutonames(ctx), "history": hist, "owned": s.dnsOwnedAll(ctx)})
}

var dnsZoneRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{1,62}$`)

// handleDNSAutonamesSet — PUT /hub/dns/autonames {zone, enabled}:
// прежнее — в историю, записи хостов — сразу.
func (s *Server) handleDNSAutonamesSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var a dnsAutonames
	if err := decodeJSON(r, &a); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	a.Zone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(a.Zone), "."))
	if a.Enabled && !dnsZoneRe.MatchString(a.Zone) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.dnsBadZone", a.Zone))
		return
	}
	user := auth.Username(ctx)
	prev := s.dnsAutonames(ctx)
	var hist []map[string]any
	if raw, ok, _ := s.db.KVGet(ctx, dnsAutoKV+".history"); ok {
		_ = json.Unmarshal([]byte(raw), &hist)
	}
	hist = append(hist, map[string]any{"ts": store.FormatTime(time.Now()), "author": user, "value": prev})
	if len(hist) > 30 {
		hist = hist[len(hist)-30:]
	}
	raw, _ := json.Marshal(a)
	hraw, _ := json.Marshal(hist)
	err := s.db.KVSet(ctx, dnsAutoKV, string(raw))
	if err == nil {
		err = s.db.KVSet(ctx, dnsAutoKV+".history", string(hraw))
	}
	s.db.Audit(ctx, user, "plugin.dnsAutonames", a.Zone, auditOutcome(err), a)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	lines := []string{}
	syncErr := s.dnsSync(ctx, func(o string) bool { return strings.HasPrefix(o, "host:") }, false, func(key string, args ...any) {
		lines = append(lines, msgs.Tc(ctx, key, args...))
	})
	out := map[string]any{"status": "ok", "log": lines}
	if syncErr != nil {
		out["error"] = msgs.Localize(msgs.FromContext(ctx), syncErr)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDNSSync — POST /hub/dns/sync {dry_run}: сверить все записи хаба
// сейчас.
func (s *Server) handleDNSSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	_ = decodeJSON(r, &req)
	lines := []string{}
	err := s.dnsSync(ctx, nil, req.DryRun, func(key string, args ...any) { lines = append(lines, msgs.Tc(ctx, key, args...)) })
	out := map[string]any{"log": lines}
	if err != nil {
		out["error"] = msgs.Localize(msgs.FromContext(ctx), err)
	}
	writeJSON(w, http.StatusOK, out)
}

var (
	dnsOwnerRe = regexp.MustCompile(`^[a-z0-9][a-z0-9:._-]{0,99}$`)
	dnsNameRe  = regexp.MustCompile(`^(?:\*|_?[a-z0-9](?:[a-z0-9_-]{0,61}[a-z0-9])?)(?:\.(?:_?[a-z0-9](?:[a-z0-9_-]{0,61}[a-z0-9])?))+$`)
)

// dnsPluginTypes — какие записи плагин может попросить.
var dnsPluginTypes = map[string]bool{"A": true, "AAAA": true, "CNAME": true, "TXT": true, "MX": true}

// handlePluginDNS — POST /hub/plugins/{name}/dns {owner, records,
// dry_run}: записи, которые нужны плагину (кнопка вида с «dns»), — через
// DNS-плагин хаба от владельца plugin:<имя>:<owner>; пустые records —
// снять. Сухой прогон — что изменится, ничего не меняя.
func (s *Server) handlePluginDNS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := s.pluginFromReq(w, r)
	if !ok {
		return
	}
	if err := s.pluginOpAllowed(ctx, p, plugin.Op{Name: "dns"}); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	var req struct {
		Owner   string      `json:"owner"`
		Records []dnsRecord `json:"records"`
		DryRun  bool        `json:"dry_run"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !dnsOwnerRe.MatchString(req.Owner) || len(req.Records) > 50 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.dnsBadRecords", req.Owner))
		return
	}
	for i := range req.Records {
		rec := &req.Records[i]
		rec.Name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rec.Name), "."))
		rec.Type = strings.ToUpper(rec.Type)
		bad := !dnsNameRe.MatchString(rec.Name) || !dnsPluginTypes[rec.Type] || len(rec.Values) == 0 || len(rec.Values) > 10 ||
			rec.TTL != 0 && (rec.TTL < 60 || rec.TTL > 86400)
		for _, v := range rec.Values {
			bad = bad || v == "" || len(v) > 2048 || strings.ContainsAny(v, "\r\n")
		}
		if bad {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.dnsBadRecords", rec.Name))
			return
		}
		if rec.TTL == 0 {
			rec.TTL = 3600
		}
		rec.Failover, rec.Port = false, 0
	}
	if len(s.dnsPlugins(ctx)) == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.dnsNoProvider"))
		return
	}
	owner := "plugin:" + p.Name + ":" + req.Owner
	// Сухой прогон — сверка с записями, которые будут, без сохранения.
	dnsMu.Lock()
	extra := map[string][]dnsRecord{}
	if raw, ok, err := s.db.KVGet(ctx, dnsPluginRecordsKV); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &extra)
	}
	before, had := extra[owner]
	if len(req.Records) == 0 {
		delete(extra, owner)
	} else {
		extra[owner] = req.Records
	}
	raw, _ := json.Marshal(extra)
	err := s.db.KVSet(ctx, dnsPluginRecordsKV, string(raw))
	dnsMu.Unlock()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	lines := []string{}
	syncErr := s.dnsSync(ctx, func(o string) bool { return o == owner }, req.DryRun, func(key string, args ...any) {
		lines = append(lines, msgs.Tc(ctx, key, args...))
	})
	if req.DryRun {
		// Вернуть, как было: сухой прогон ничего не сохраняет.
		dnsMu.Lock()
		if had {
			extra[owner] = before
		} else {
			delete(extra, owner)
		}
		raw, _ := json.Marshal(extra)
		_ = s.db.KVSet(ctx, dnsPluginRecordsKV, string(raw))
		dnsMu.Unlock()
	} else {
		s.db.Audit(ctx, auth.Username(ctx), "plugin.dnsRecords", owner, auditOutcome(syncErr), req.Records)
	}
	out := map[string]any{"log": lines}
	if syncErr != nil {
		out["error"] = msgs.Localize(msgs.FromContext(ctx), syncErr)
	}
	writeJSON(w, http.StatusOK, out)
}
