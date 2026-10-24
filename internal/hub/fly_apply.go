package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// KindFlyApply — задание «применить Traffic Fly».
const KindFlyApply = "fly.apply"

// FlyApplyParams — вход задания: какую ревизию применить.
type FlyApplyParams struct {
	Revision int64 `json:"revision"`
	DryRun   bool  `json:"dry_run,omitempty"`
}

// flyApplied — что разложено последним применением.
type flyApplied struct {
	Revision int64   `json:"revision"`
	Hosts    []int64 `json:"hosts"`
	Gateways []int64 `json:"gateways"`
	// HostPush / GatewayPush — что ушло на узел последним: отпечаток
	// конфигурации (без номера версии) и номер. Совпадает, и узел
	// сообщает этот номер — «Применить» его не перекладывает.
	HostPush    map[int64]flyPush `json:"host_push,omitempty"`
	GatewayPush map[int64]flyPush `json:"gateway_push,omitempty"`
}

type flyPush struct {
	Hash string `json:"hash"`
	Rev  int64  `json:"rev"`
}

// flyCfgHash — отпечаток конфигурации без номера версии.
func flyCfgHash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// FlyApplyRunner раскладывает Traffic Fly по шлюзам и хостам.
type FlyApplyRunner struct{ s *Server }

// NewFlyApplyRunner строит исполнителя.
func NewFlyApplyRunner(s *Server) *FlyApplyRunner { return &FlyApplyRunner{s: s} }

func (s *Server) flyAppliedState(ctx context.Context) flyApplied {
	var a flyApplied
	if raw, ok, err := s.db.KVGet(ctx, flyAppliedKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &a)
	}
	return a
}

// Run: 1) ключи шлюзов, 2) ключи хостов и адреса целей, 3) хосты (туннели
// и DNAT), 4) шлюзы (туннели, правила, маршрутизатор). Хосты раньше
// шлюзов: шлюз начинает слать трафик, когда туннели уже готовы. Сухой
// прогон — всё, кроме записи: собранные конфигурации проверяются.
func (r *FlyApplyRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p FlyApplyParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	return r.s.flyApplyRun(ctx, jc, jc.Job.Author, p)
}

// flyLog — куда применение пишет ход: журнал своего задания или чужого
// (выкладка конвейера применяет маршрут внутри своего задания).
type flyLog interface {
	Log(key string, args ...any)
	Logf(format string, args ...any)
	StepKey(n, total int, key string, args ...any)
}

// flyApplyRun — применение Traffic Fly (тело задания fly.apply).
func (s *Server) flyApplyRun(ctx context.Context, jc flyLog, author string, p FlyApplyParams) error {
	d := s.flyDoc(ctx)
	if p.Revision != 0 && p.Revision != d.Revision {
		return msgs.Errorf("fly.revisionChanged", p.Revision, d.Revision)
	}
	user := s.actingUser(ctx, author, s.firstAdmin(ctx))
	names := map[int64]string{localHostID: "localhost"}
	if list, err := s.db.ListHosts(ctx); err == nil {
		for _, h := range list {
			names[h.ID] = h.Name
		}
	}

	// 1. Шлюзы: туннель edge и ключ.
	jc.StepKey(1, 4, "fly.stepGateways", len(d.Gateways))
	type gwInfo struct {
		g        FlyGateway
		st       EdgeSettings
		pub      string
		endpoint string
		sessOK   bool
	}
	var gws []*gwInfo
	for _, g := range d.Gateways {
		st, sess, ok := s.flySession(ctx, g.EdgeID)
		if !ok {
			return msgs.Errorf("fly.gatewayOffline", edgeName(st), g.EdgeID)
		}
		var key fly.KeyResponse
		if err := flyGatewayCall(ctx, sess, "POST", fly.PathKey, nil, &key); err != nil {
			return msgs.Errorf("fly.gatewayKey", edgeName(st), err)
		}
		ep, err := flyEndpoint(ctx, st, g.ListenPort)
		if err != nil {
			return err
		}
		jc.Log("fly.gatewayReady", edgeName(st), ep)
		if err := flyCheckGatewayTLS(ctx, jc, d, g, edgeName(st), key.Version, ep); err != nil {
			return err
		}
		gws = append(gws, &gwInfo{g: g, st: st, pub: key.PublicKey, endpoint: ep, sessOK: true})
	}

	// 2. Хосты: ключ туннеля к каждому шлюзу, адреса целей.
	jc.StepKey(2, 4, "fly.stepHosts")
	hostKeys := map[string]string{} // "gw/host" → публичный ключ хоста
	hostCfg := map[int64]*fly.HostConfig{}
	cache := map[string]any{}
	for _, gi := range gws {
		g := gi.g
		for _, rt := range d.Routes {
			if rt.Gateway != g.EdgeID {
				continue
			}
			for _, t := range rt.Targets {
				hk := flyHostKey(g.EdgeID, t.HostID)
				iface := flyIface(g.Index)
				if _, ok := hostKeys[hk]; !ok {
					if err := s.flyHostReady(ctx, t.HostID, names[t.HostID]); err != nil {
						return err
					}
					var key fly.KeyResponse
					if err := s.flyHostCall(ctx, user, t.HostID, names[t.HostID], "POST", "/api/fly/key?iface="+iface, nil, &key); err != nil {
						var me *msgs.Err
						if errors.As(err, &me) && me.Key == "fly.hostTimeout" {
							return err
						}
						return msgs.Errorf("fly.hostKey", names[t.HostID], err)
					}
					hostKeys[hk] = key.PublicKey
				}
				addr := d.hostAddr(g, t.HostID)
				ip, err := s.flyResolve(ctx, user, t, addr, cache)
				if err != nil {
					return msgs.Errorf("fly.targetResolve", rt.ID, t.ID, names[t.HostID], err)
				}
				hc := hostCfg[t.HostID]
				if hc == nil {
					hc = &fly.HostConfig{Revision: d.Revision}
					hostCfg[t.HostID] = hc
				}
				var hi *fly.HostIface
				for k := range hc.Ifaces {
					if hc.Ifaces[k].Name == iface {
						hi = &hc.Ifaces[k]
					}
				}
				if hi == nil {
					hc.Ifaces = append(hc.Ifaces, fly.HostIface{Name: iface, Address: addr + "/24",
						Gateway:  fly.Peer{Name: edgeName(gi.st), PublicKey: gi.pub, Address: fmt.Sprintf("10.77.%d.1", g.Index)},
						Endpoint: gi.endpoint, Mark: fly.HostMark(g.Index), Table: fly.HostTableBase + g.Index})
					hi = &hc.Ifaces[len(hc.Ifaces)-1]
				}
				port := d.Alloc.Ports[flyPortKey(g.EdgeID, t.HostID, rt.ID, t.ID)]
				for _, proto := range flyProtos(rt.Kind) {
					if rt.PortTo > rt.Port {
						// Диапазон: те же порты насквозь (N → N).
						hi.Services = append(hi.Services, fly.Service{Port: rt.Port, PortTo: rt.PortTo, Proto: proto, Addr: ip, ToPort: rt.Port, Kind: t.Kind, Ref: t.Ref})
						continue
					}
					hi.Services = append(hi.Services, fly.Service{Port: port, Proto: proto, Addr: ip, ToPort: t.Port, Kind: t.Kind, Ref: t.Ref})
				}
				jc.Log("fly.targetResolved", rt.ID, names[t.HostID], t.Kind, t.Ref, ip, t.Port)
			}
		}
	}
	for id, hc := range hostCfg {
		if err := hc.Validate(); err != nil {
			return msgs.Errorf("fly.hostConfigBad", names[id], err)
		}
	}
	gwCfg := map[int64]fly.GatewayConfig{}
	for _, gi := range gws {
		keys := map[int64]string{}
		for hk, pub := range hostKeys {
			var gwID, hostID int64
			if _, err := fmt.Sscanf(hk, "%d/%d", &gwID, &hostID); err == nil && gwID == gi.g.EdgeID {
				keys[hostID] = pub
			}
		}
		c := d.gatewayConfig(gi.g, keys, names)
		if err := c.Validate(); err != nil {
			return msgs.Errorf("fly.badConfig", err.Error())
		}
		gwCfg[gi.g.EdgeID] = c
	}
	if p.DryRun {
		// Ядро хостов умеет WireGuard? Шлюзы проверены при установке.
		bad := 0
		for id, hc := range hostCfg {
			if len(hc.Ifaces) == 0 {
				continue
			}
			var chk struct {
				WireGuard bool   `json:"wireguard"`
				Virt      string `json:"virt"`
				Error     string `json:"error"`
			}
			if err := s.flyHostCall(ctx, user, id, names[id], "GET", "/api/fly/check", nil, &chk); err != nil {
				continue // старый nkt или хост не ответил — скажет «Применить»
			}
			if !chk.WireGuard {
				jc.Log("fly.hostNoWG", names[id], chk.Virt, chk.Error)
				bad++
			}
		}
		if bad > 0 {
			return msgs.Errorf("fly.dryHostsNoWG", bad)
		}
		jc.Log("fly.dryOK", len(hostCfg), len(gws))
		// DNS-плагин: что поменялось бы в записях имён маршрутов.
		if err := s.dnsSync(ctx, func(o string) bool { return strings.HasPrefix(o, "fly:") }, true, jc.Log); err != nil {
			jc.Log("plugin.dnsWarn", err.Error())
		}
		return nil
	}

	// 3. Хосты; те, где Traffic Fly был, а теперь нет, — пустая
	// конфигурация (туннели снимаются).
	prev := s.flyAppliedState(ctx)
	ids := make([]int64, 0, len(hostCfg))
	for id := range hostCfg {
		ids = append(ids, id)
	}
	for _, id := range prev.Hosts {
		if hostCfg[id] == nil {
			hostCfg[id] = &fly.HostConfig{Revision: d.Revision, Ifaces: []fly.HostIface{}}
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	jc.StepKey(3, 4, "fly.stepPushHosts", len(ids))
	var applied []int64
	hostPush := map[int64]flyPush{}
	for _, id := range ids {
		c := *hostCfg[id]
		c.Revision = 0
		hash := flyCfgHash(c)
		if len(hostCfg[id].Ifaces) > 0 {
			applied = append(applied, id)
		}
		// То же, что уже на хосте (и хост это подтверждает), — не
		// перекладывать: туннели и правила не трогаются вовсе.
		if pp, ok := prev.HostPush[id]; ok && pp.Hash == hash {
			var hs fly.HostStatus
			if err := s.flyHostCall(ctx, user, id, names[id], "GET", "/api/fly/status", nil, &hs); err == nil && hs.Revision == pp.Rev {
				jc.Log("fly.hostUpToDate", names[id])
				hostPush[id] = pp
				continue
			}
		}
		var res struct {
			Log []string `json:"log"`
		}
		if _, err := s.hostCall(ctx, user, id, "PUT", "/api/fly/config", hostCfg[id], &res); err != nil {
			return flyWGHint(msgs.Errorf("fly.hostApply", names[id], err))
		}
		for _, l := range res.Log {
			jc.Logf("    %s: %s", names[id], l)
		}
		hostPush[id] = flyPush{Hash: hash, Rev: d.Revision}
	}

	// 4. Шлюзы.
	jc.StepKey(4, 4, "fly.stepPushGateways", len(gws))
	var gwIDs []int64
	gwPush := map[int64]flyPush{}
	for _, gi := range gws {
		_, sess, ok := s.flySession(ctx, gi.g.EdgeID)
		if !ok {
			return msgs.Errorf("fly.gatewayOffline", edgeName(gi.st), gi.g.EdgeID)
		}
		gwIDs = append(gwIDs, gi.g.EdgeID)
		c := gwCfg[gi.g.EdgeID]
		c.Revision = 0
		hash := flyCfgHash(c)
		if pp, ok := prev.GatewayPush[gi.g.EdgeID]; ok && pp.Hash == hash {
			var st fly.GatewayStatus
			if err := flyGatewayCall(ctx, sess, "GET", fly.PathStatus, nil, &st); err == nil && st.Revision == pp.Rev {
				jc.Log("fly.gatewayUpToDate", edgeName(gi.st))
				gwPush[gi.g.EdgeID] = pp
				s.flyLogCerts(jc, st)
				continue
			}
		}
		if err := flyGatewayCall(ctx, sess, "PUT", fly.PathConfig, gwCfg[gi.g.EdgeID], nil); err != nil {
			return flyWGHint(msgs.Errorf("fly.gatewayApply", edgeName(gi.st), err))
		}
		jc.Log("fly.gatewayApplied", edgeName(gi.st), len(gwCfg[gi.g.EdgeID].Routes))
		gwPush[gi.g.EdgeID] = flyPush{Hash: hash, Rev: d.Revision}
		// Сертификаты: шлюз берёт сохранённые, а к Let's Encrypt идёт
		// только за новыми и за теми, что скоро истекают, — подождать
		// немного и сказать, что с каждым именем.
		if len(c.TLSNames()) > 0 {
			sleepCtx(ctx, 3*time.Second)
			var st fly.GatewayStatus
			if err := flyGatewayCall(ctx, sess, "GET", fly.PathStatus, nil, &st); err == nil {
				s.flyLogCerts(jc, st)
			}
		}
	}
	// Шлюзы без маршрутов в этой ревизии — пустая конфигурация.
	for _, id := range prev.Gateways {
		if _, ok := gwCfg[id]; ok {
			continue
		}
		if st, sess, ok := s.flySession(ctx, id); ok {
			g := FlyGateway{EdgeID: id, Index: 250, ListenPort: flyDefaultWG}
			empty := fly.GatewayConfig{Revision: d.Revision, Iface: "nkt-fly", Address: fmt.Sprintf("10.77.%d.1/24", g.Index), ListenPort: g.ListenPort, Peers: []fly.Peer{}, Routes: []fly.Route{}}
			if err := flyGatewayCall(ctx, sess, "PUT", fly.PathConfig, empty, nil); err != nil {
				jc.Log("fly.gatewayApply", edgeName(st), err)
			}
		}
	}
	raw, _ := json.Marshal(flyApplied{Revision: d.Revision, Hosts: applied, Gateways: gwIDs, HostPush: hostPush, GatewayPush: gwPush})
	if err := s.db.KVSet(ctx, flyAppliedKey, string(raw)); err != nil {
		return err
	}
	jc.Log("fly.applied", d.Revision, len(applied), len(gwIDs))
	// DNS-плагин: имена маршрутов с галочкой «DNS» — на адреса шлюзов.
	// Ошибка DNS не отменяет применённого: она в журнале, сверка повторит.
	if err := s.dnsSync(ctx, func(o string) bool { return strings.HasPrefix(o, "fly:") }, false, jc.Log); err != nil {
		jc.Log("plugin.dnsWarn", err.Error())
	}
	return nil
}

// flyRouteSummary — строка маршрута для журнала.
func flyRouteSummary(r FlyRoute) string {
	if len(r.Names) > 0 {
		return fmt.Sprintf("%s %s:%d", r.Kind, strings.Join(r.Names, ","), r.Port)
	}
	return fmt.Sprintf("%s :%d", r.Kind, r.Port)
}

// flyMinHostVersion — с какой версии nkt на хосте умеет Traffic Fly.
var flyMinHostVersion = [3]int{1, 13, 12}

// flyHostReady — хост-цель годится до первого запроса: nkt на нём есть и
// не старше flyMinHostVersion (dev-сборки и непонятные версии — годятся).
func (s *Server) flyHostReady(ctx context.Context, hostID int64, name string) error {
	if hostID == localHostID {
		return nil
	}
	h, err := s.db.HostByID(ctx, hostID)
	if err != nil {
		return err
	}
	if h.NktVersion == "" {
		return msgs.Errorf("fly.hostNoNkt", name)
	}
	if !flyVersionAtLeast(h.NktVersion, flyMinHostVersion) {
		return msgs.Errorf("fly.hostTooOld", name, h.NktVersion)
	}
	return nil
}

// flyHostCallTimeout — на один запрос Traffic Fly к хосту (ключ, сводка).
// Настройка хоста (PUT /api/fly/config) ставит пакеты — ей предел свой.
const flyHostCallTimeout = 20 * time.Second

// flyHostCall — короткий запрос к хосту с одной повторной попыткой: канал
// к хосту бывает занят или только что переподключился. Не ответил —
// ошибка словами, а не «context deadline exceeded».
func (s *Server) flyHostCall(ctx context.Context, user string, hostID int64, name, method, path string, in, out any) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, flyHostCallTimeout)
		_, err = s.hostCall(cctx, user, hostID, method, path, in, out)
		timedOut := cctx.Err() != nil || isTimeout(err)
		cancel()
		if err == nil || ctx.Err() != nil {
			return err
		}
		if !timedOut {
			return err
		}
	}
	return msgs.Errorf("fly.hostTimeout", name, int(flyHostCallTimeout.Seconds()))
}

// isTimeout — ошибка — истёкшее время (клиента HTTP, контекста, сети).
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "Client.Timeout exceeded")
}

// flyMinTLSGatewayVersion — с какой версии шлюз выпускает сертификаты сам
// (1.13.23 — сертификат на шлюзе, 1.13.25 — исправлен выпуск).
var flyMinTLSGatewayVersion = [3]int{1, 13, 25}

// flyVersionAtLeast — версия не старше min; пустая — старая; непонятная
// (dev-сборка) — годится.
func flyVersionAtLeast(v string, min [3]int) bool {
	if v == "" {
		return false
	}
	p, ok := parseSemver(v)
	if !ok {
		return true
	}
	for i := range p {
		if p[i] != min[i] {
			return p[i] > min[i]
		}
	}
	return true
}

// flyCheckGatewayTLS — у шлюза есть маршруты с сертификатом на шлюзе:
// версия шлюза это умеет (иначе — отказ: старый шлюз галочку молча
// пропустит), а имена указывают на шлюз и его порт 80 открыт снаружи —
// это нужно Let's Encrypt (иначе — предупреждение: хаб может и не
// выходить в интернет).
func flyCheckGatewayTLS(ctx context.Context, jc flyLog, d FlyDoc, g FlyGateway, name, version, endpoint string) error {
	var names []string
	for _, r := range d.Routes {
		if r.Gateway == g.EdgeID && r.TLS {
			names = append(names, r.Names...)
		}
	}
	if len(names) == 0 {
		return nil
	}
	if !flyVersionAtLeast(version, flyMinTLSGatewayVersion) {
		if version == "" {
			version = "?"
		}
		return msgs.Errorf("fly.gatewayTooOld", name, version)
	}
	gwIP, _, _ := net.SplitHostPort(endpoint)
	client := &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, n := range names {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err := net.DefaultResolver.LookupHost(cctx, n)
		cancel()
		if err != nil || !slices.Contains(ips, gwIP) {
			jc.Log("fly.tlsDNS", n, strings.Join(ips, ", "), gwIP)
			continue
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+n+fly.CheckPath80, nil)
		resp, err := client.Do(req)
		if err != nil {
			jc.Log("fly.tlsPort80", n, err.Error())
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if strings.TrimSpace(string(body)) != fly.CheckReply80 {
			jc.Log("fly.tlsPort80", n, resp.Status)
			continue
		}
		jc.Log("fly.tlsReady", n)
	}
	return nil
}

// flyLogCerts — сертификаты шлюза в журнал задания: действует (не
// перевыпускается), выпускается или почему не выпущен.
func (s *Server) flyLogCerts(jc flyLog, st fly.GatewayStatus) {
	for _, c := range st.Certs {
		switch {
		case c.Error != "":
			jc.Log("fly.certFailed", c.Name, c.Error)
		case c.NotAfter > 0:
			jc.Log("fly.certValid", c.Name, time.Unix(c.NotAfter, 0).Format("2006-01-02"))
		default:
			jc.Log("fly.certIssuing", c.Name)
		}
	}
}

// flyWGHint — к ошибке wg-quick на шлюзе или хосте — что с ней делать
// (AppArmor, ядро без WireGuard, занятый порт).
func flyWGHint(err error) error {
	switch fly.WGFailure(msgs.Localize(msgs.EN, err)) {
	case "apparmor":
		return msgs.Errorf("fly.wgHintAppArmor", err)
	case "apparmor-old":
		return msgs.Errorf("fly.wgHintAppArmorOld", err)
	case "kernel":
		return msgs.Errorf("fly.wgHintKernel", err)
	case "port":
		return msgs.Errorf("fly.wgHintPort", err)
	}
	return err
}
