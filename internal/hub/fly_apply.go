package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	s := r.s
	var p FlyApplyParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	d := s.flyDoc(ctx)
	if p.Revision != 0 && p.Revision != d.Revision {
		return msgs.Errorf("fly.revisionChanged", p.Revision, d.Revision)
	}
	user := s.actingUser(ctx, jc.Job.Author, s.firstAdmin(ctx))
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
					var key fly.KeyResponse
					if _, err := s.hostCall(ctx, user, t.HostID, "POST", "/api/fly/key?iface="+iface, nil, &key); err != nil {
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
						Endpoint: gi.endpoint, Mark: fly.HostMarkBase + uint32(g.Index), Table: fly.HostTableBase + g.Index})
					hi = &hc.Ifaces[len(hc.Ifaces)-1]
				}
				port := d.Alloc.Ports[flyPortKey(g.EdgeID, t.HostID, rt.ID, t.ID)]
				for _, proto := range flyProtos(rt.Kind) {
					hi.Services = append(hi.Services, fly.Service{Port: port, Proto: proto, Addr: ip, ToPort: t.Port})
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
		jc.Log("fly.dryOK", len(hostCfg), len(gws))
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
	for _, id := range ids {
		var res struct {
			Log []string `json:"log"`
		}
		if _, err := s.hostCall(ctx, user, id, "PUT", "/api/fly/config", hostCfg[id], &res); err != nil {
			return msgs.Errorf("fly.hostApply", names[id], err)
		}
		for _, l := range res.Log {
			jc.Logf("    %s: %s", names[id], l)
		}
		if len(hostCfg[id].Ifaces) > 0 {
			applied = append(applied, id)
		}
	}

	// 4. Шлюзы.
	jc.StepKey(4, 4, "fly.stepPushGateways", len(gws))
	var gwIDs []int64
	for _, gi := range gws {
		_, sess, ok := s.flySession(ctx, gi.g.EdgeID)
		if !ok {
			return msgs.Errorf("fly.gatewayOffline", edgeName(gi.st), gi.g.EdgeID)
		}
		if err := flyGatewayCall(ctx, sess, "PUT", fly.PathConfig, gwCfg[gi.g.EdgeID], nil); err != nil {
			return msgs.Errorf("fly.gatewayApply", edgeName(gi.st), err)
		}
		jc.Log("fly.gatewayApplied", edgeName(gi.st), len(gwCfg[gi.g.EdgeID].Routes))
		gwIDs = append(gwIDs, gi.g.EdgeID)
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
	raw, _ := json.Marshal(flyApplied{Revision: d.Revision, Hosts: applied, Gateways: gwIDs})
	if err := s.db.KVSet(ctx, flyAppliedKey, string(raw)); err != nil {
		return err
	}
	jc.Log("fly.applied", d.Revision, len(applied), len(gwIDs))
	return nil
}

// flyRouteSummary — строка маршрута для журнала.
func flyRouteSummary(r FlyRoute) string {
	if len(r.Names) > 0 {
		return fmt.Sprintf("%s %s:%d", r.Kind, strings.Join(r.Names, ","), r.Port)
	}
	return fmt.Sprintf("%s :%d", r.Kind, r.Port)
}
