package hub

import (
	"context"
	"fmt"
	"time"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/store"
)

// События Traffic Fly: раз в минуту хаб смотрит сводки шлюзов и пишет
// переходы — цель упала или вернулась, пул остался без живых целей,
// туннель хоста оборвался или восстановлен. Первое наблюдение событий не
// даёт (сравнивать не с чем).

type flySeen struct {
	alive  map[string]bool // route/target
	empty  map[string]bool // route
	tunnel map[string]bool // gw/host
}

// StartFlyMonitor — наблюдение за шлюзами Traffic Fly.
func (s *Server) StartFlyMonitor(ctx context.Context) {
	go func() {
		var prev *flySeen
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			d := s.flyDoc(ctx)
			if len(d.Routes) == 0 {
				prev = nil
				continue
			}
			now := s.flyObserve(ctx, d)
			if prev != nil {
				s.flyNoteChanges(ctx, d, prev, now)
			}
			prev = now
		}
	}()
}

func (s *Server) flyObserve(ctx context.Context, d FlyDoc) *flySeen {
	seen := &flySeen{alive: map[string]bool{}, empty: map[string]bool{}, tunnel: map[string]bool{}}
	for _, g := range s.flyStatuses(ctx) {
		if g.Status == nil {
			continue
		}
		gw, _ := d.gateway(g.EdgeID)
		for _, rs := range g.Status.Routes {
			live := 0
			for _, ts := range rs.Targets {
				seen.alive[rs.ID+"/"+ts.ID] = ts.Alive
				if ts.Alive && !ts.Drain {
					live++
				}
			}
			seen.empty[rs.ID] = live == 0
		}
		// Туннели: публичный ключ пира → хост (по адресу в туннеле).
		hostByAddr := map[string]int64{}
		for _, r := range d.Routes {
			if r.Gateway != g.EdgeID {
				continue
			}
			for _, t := range r.Targets {
				hostByAddr[d.hostAddr(gw, t.HostID)] = t.HostID
			}
		}
		for _, p := range g.Status.Peers {
			if id, ok := hostByAddr[p.Address]; ok {
				seen.tunnel[flyHostKey(g.EdgeID, id)] = fly.TunnelUp(p.Handshake, g.Status.Now)
			}
		}
	}
	return seen
}

func (s *Server) flyNoteChanges(ctx context.Context, d FlyDoc, prev, now *flySeen) {
	hostOf := func(hostID int64) (store.Host, bool) {
		if hostID == localHostID {
			return store.Host{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"}, true
		}
		h, err := s.db.HostByID(ctx, hostID)
		return h, err == nil
	}
	for _, r := range d.Routes {
		for _, t := range r.Targets {
			k := r.ID + "/" + t.ID
			was, okWas := prev.alive[k]
			is, okIs := now.alive[k]
			if !okWas || !okIs || was == is {
				continue
			}
			h, ok := hostOf(t.HostID)
			if !ok {
				continue
			}
			label := t.Ref
			if label == "" {
				label = t.Kind
			}
			if is {
				s.hub.recordEventLink(ctx, h, store.EventFlyTargetUp, "", "/deploy?tab=fly", "hub.flyTargetUp", r.ID, label, t.Port)
			} else {
				s.hub.recordEventLink(ctx, h, store.EventFlyTargetDown, "", "/deploy?tab=fly", "hub.flyTargetDown", r.ID, label, t.Port)
			}
		}
		if now.empty[r.ID] && !prev.empty[r.ID] && len(r.Targets) > 0 {
			if h, ok := hostOf(r.Targets[0].HostID); ok {
				s.hub.recordEventLink(ctx, h, store.EventFlyPoolEmpty, "critical", "/deploy?tab=fly", "hub.flyPoolEmpty", r.ID)
			}
		}
	}
	for k, is := range now.tunnel {
		was, ok := prev.tunnel[k]
		if !ok || was == is {
			continue
		}
		var gwID, hostID int64
		if _, err := fmt.Sscanf(k, "%d/%d", &gwID, &hostID); err != nil {
			continue
		}
		h, ok := hostOf(hostID)
		if !ok {
			continue
		}
		kind, key := store.EventFlyTunnelDown, "hub.flyTunnelDown"
		if is {
			kind, key = store.EventFlyTunnelUp, "hub.flyTunnelUp"
		}
		s.hub.recordEventLink(ctx, h, kind, "", "/deploy?tab=fly", key, h.Name)
	}
}
