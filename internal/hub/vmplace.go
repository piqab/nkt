package hub

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Автовыбор хоста для новой машины (1.17.6): host_id = 0 — хаб сам
// выбирает хост, где машине хватит места. Кандидаты — управляемые хосты
// в сети (не машины и не сам хаб) в пределах токена; у каждого хаб
// спрашивает /api/vm/preflight: KVM, libvirtd, свободную память и место
// под диски. Подходит хост с запасом по памяти и диску (а токену — ещё и
// в его потолках на хост); из подходящих — с наибольшей свободной
// памятью.

const (
	placeMemReserveMB  = 512
	placeDiskReserveGB = 2
	placePreflightWait = 15 * time.Second
)

// vmPreflight — что хост отвечает о себе для создания машин.
type vmPreflight struct {
	KVM            bool  `json:"kvm"`
	LibvirtdActive bool  `json:"libvirtd_active"`
	ImagesFree     int64 `json:"images_free_bytes"`
	MemAvailable   int64 `json:"mem_available_bytes"`
}

// placePreflight — опрос хоста (тесты подставляют свой).
var placePreflight = func(s *Server, ctx context.Context, user string, hostID int64, out *vmPreflight) error {
	_, err := s.hostCall(ctx, user, hostID, "GET", "/api/vm/preflight", nil, out)
	return err
}

// placeCandidate — хост и почему он подходит или нет.
type placeCandidate struct {
	host   store.Host
	pf     vmPreflight
	reason string
}

// pickVMHost выбирает хост для машины spec.
func (s *Server) pickVMHost(ctx context.Context, spec vmcreate.Spec) (store.Host, error) {
	hosts, err := s.db.ListHosts(ctx)
	if err != nil {
		return store.Host{}, err
	}
	allow := s.scopeFilter(ctx)
	tok, byToken := tokenFromContext(ctx)
	var cands []*placeCandidate
	for _, h := range hosts {
		if h.ParentID != 0 || h.Status != "online" || allow != nil && !allow(h.ID) {
			continue
		}
		cands = append(cands, &placeCandidate{host: h})
	}
	if len(cands) == 0 {
		return store.Host{}, msgs.Errorf("hub.vmPlaceNoHosts")
	}
	pctx, cancel := context.WithTimeout(ctx, placePreflightWait)
	defer cancel()
	user := auth.Username(ctx)
	var wg sync.WaitGroup
	for _, c := range cands {
		wg.Add(1)
		go func(c *placeCandidate) {
			defer wg.Done()
			if err := placePreflight(s, pctx, user, c.host.ID, &c.pf); err != nil {
				c.reason = msgs.Tc(ctx, "hub.vmPlaceUnreachable")
			}
		}(c)
	}
	wg.Wait()
	needMem := int64(spec.MemoryMB+placeMemReserveMB) << 20
	needDisk := int64(spec.DiskGB+placeDiskReserveGB) << 30
	var fit []*placeCandidate
	for _, c := range cands {
		switch {
		case c.reason != "":
		case !c.pf.KVM || !c.pf.LibvirtdActive:
			c.reason = msgs.Tc(ctx, "hub.vmPlaceNoKVM")
		case c.pf.MemAvailable < needMem:
			c.reason = msgs.Tc(ctx, "hub.vmPlaceMem", c.pf.MemAvailable>>20, needMem>>20)
		case c.pf.ImagesFree < needDisk:
			c.reason = msgs.Tc(ctx, "hub.vmPlaceDisk", c.pf.ImagesFree>>30, needDisk>>30)
		case byToken && tok.Limited():
			// Потолки токена на хост: та же проверка, что при создании,
			// но без записи в учёт.
			u := store.TokenUsage{TokenID: tok.ID, Kind: store.UsageVM, HostID: c.host.ID, Ref: spec.Name, Label: spec.Name + "@" + c.host.Name,
				CPU: float64(spec.VCPUs), MemMB: int64(spec.MemoryMB), DiskGB: int64(spec.DiskGB)}
			if err := s.tokenFits(ctx, tok, u); err != nil {
				c.reason = msgs.Localize(msgs.FromContext(ctx), err)
			}
		}
		if c.reason == "" {
			fit = append(fit, c)
		}
	}
	if len(fit) == 0 {
		var why []string
		for _, c := range cands {
			why = append(why, c.host.Name+": "+c.reason)
		}
		sort.Strings(why)
		return store.Host{}, msgs.Errorf("hub.vmPlaceNone", strings.Join(why, "; "))
	}
	sort.SliceStable(fit, func(i, j int) bool { return fit[i].pf.MemAvailable > fit[j].pf.MemAvailable })
	return fit[0].host, nil
}
