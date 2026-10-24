package hub

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Токен «по правам»: какие объекты ему можно (конвейеры, сценарии,
// кластеры по именам, только свои машины), сколько действий в час и
// потолки ресурсов — суммой по учёту (token_usage) и на хост.

// tokenObjectAllowed — объект маршрута в правах токена.
func (s *Server) tokenObjectAllowed(r *http.Request, t store.APIToken, pattern string) error {
	ctx := r.Context()
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	switch pattern {
	case "/hub/pipelines/{id}/deploy", "/hub/pipelines/{id}/rollback", "/hub/pipelines/{id}/start", "/hub/pipelines/{id}/stop":
		p, err := s.db.PipelineByID(ctx, id)
		if err != nil {
			return tokenDeny(http.StatusNotFound, "err.notFound")
		}
		return tokenPipelineAllowed(t, p)
	case "/hub/pipelines/{id}", "/hub/pipelines/{id}/remove":
		// Править и удалять — только свои.
		p, err := s.db.PipelineByID(ctx, id)
		if err != nil {
			return tokenDeny(http.StatusNotFound, "err.notFound")
		}
		if !t.Owns(p.Author) {
			return tokenDeny(http.StatusForbidden, "auth.tokenNotOwnPipeline", p.Name)
		}
	case "/hub/scripts/{id}/run":
		sc, err := s.db.ScriptByID(ctx, id)
		if err != nil {
			return tokenDeny(http.StatusNotFound, "err.notFound")
		}
		if len(t.Perms.Scripts) > 0 && !slices.Contains(t.Perms.Scripts, sc.Name) {
			return tokenDeny(http.StatusForbidden, "auth.tokenObjectDenied", sc.Name)
		}
	case "/hub/hosts/{id}/vm/{action}":
		h, err := s.db.HostByID(ctx, id)
		if err != nil || h.ParentID == 0 {
			return tokenDeny(http.StatusNotFound, "err.notFound")
		}
		return s.tokenOwnsVM(ctx, t, strconv.FormatInt(h.ParentID, 10), h.Name)
	}
	return nil
}

// tokenPipelineAllowed — конвейер в списке токена (пусто — любой в
// пределах его хостов) или создан им самим.
func tokenPipelineAllowed(t store.APIToken, p store.Pipeline) error {
	if t.Limited() && len(t.Perms.Pipelines) > 0 && !slices.Contains(t.Perms.Pipelines, p.Name) && !t.Owns(p.Author) {
		return tokenDeny(http.StatusForbidden, "auth.tokenObjectDenied", p.Name)
	}
	return nil
}

// tokenContentAllowed — описание, которое токен с пределами или «по
// правам» создаёт, правит или прогоняет: «по правам» — только compose, и
// все хосты описания — в пределах токена.
func (s *Server) tokenContentAllowed(ctx context.Context, t store.APIToken, content string) error {
	if !t.Scoped() && !t.Limited() {
		return nil
	}
	spec, err := deploy.ParseSpec(content)
	if err != nil {
		return err
	}
	if spec.Action != deploy.ActionCompose || spec.Compose == nil {
		return msgs.Errorf("auth.tokenPipelineCompose")
	}
	targets, err := s.resolveHosts(ctx, spec.Compose.Hosts, spec.Compose.Group)
	if err != nil {
		return err
	}
	if allow := s.scopeFilter(ctx); allow != nil {
		for _, h := range targets {
			if !allow(h.ID) {
				return tokenDeny(http.StatusForbidden, "auth.tokenHostDenied")
			}
		}
	}
	return nil
}

// tokenOwnsVM — машину создал этот токен (она в его учёте).
func (s *Server) tokenOwnsVM(ctx context.Context, t store.APIToken, hostRef, name string) error {
	hostID := int64(localHostID)
	if hostRef != "local" {
		var err error
		if hostID, err = strconv.ParseInt(hostRef, 10, 64); err != nil {
			return tokenDeny(http.StatusForbidden, "auth.tokenHostDenied")
		}
	}
	usage, _ := s.db.ListTokenUsage(ctx, t.ID)
	for _, u := range usage {
		if u.Kind == store.UsageVM && u.HostID == hostID && u.Ref == name {
			return nil
		}
	}
	return tokenDeny(http.StatusForbidden, "auth.tokenNotOwnVM", name)
}

// tokenRater — действия токенов «по правам» за последний час (в памяти:
// после перезапуска хаба счёт начинается заново).
type tokenRater struct {
	mu   sync.Mutex
	seen map[int64][]time.Time
}

var rater = &tokenRater{seen: map[int64][]time.Time{}}

// tokenRate — не больше DeploysPerHour выкладок, запусков и применений в
// час (прочие действия не считаются).
func (s *Server) tokenRate(t store.APIToken, action string) error {
	if t.Perms.Limits.DeploysPerHour <= 0 {
		return nil
	}
	switch action {
	case store.TokenActPipelineDeploy, store.TokenActPipelinePower, store.TokenActK8sApply, store.TokenActScriptRun, store.TokenActVMManage:
	default:
		return nil
	}
	rater.mu.Lock()
	defer rater.mu.Unlock()
	now := time.Now()
	list := slices.DeleteFunc(rater.seen[t.ID], func(ts time.Time) bool { return now.Sub(ts) > time.Hour })
	if len(list) >= t.Perms.Limits.DeploysPerHour {
		rater.seen[t.ID] = list
		return tokenDeny(http.StatusTooManyRequests, "auth.tokenRateLimited", t.Perms.Limits.DeploysPerHour)
	}
	rater.seen[t.ID] = append(list, now)
	return nil
}

// tokenFits — новые записи учёта влезают в потолки токена: суммы по
// остальным записям (такие же — заменяются, не складываются) плюс новые —
// не выше общих потолков; стеки и машины на одном хосте — не выше
// потолков хоста; машин — не больше VMs.
func (s *Server) tokenFits(ctx context.Context, t store.APIToken, adds ...store.TokenUsage) error {
	l := t.Perms.Limits
	usage, err := s.db.ListTokenUsage(ctx, t.ID)
	if err != nil {
		return err
	}
	same := func(u store.TokenUsage) bool {
		return slices.ContainsFunc(adds, func(a store.TokenUsage) bool { return a.Kind == u.Kind && a.HostID == u.HostID && a.Ref == u.Ref })
	}
	var cpu float64
	var mem, disk int64
	vms := 0
	hostCPU := map[int64]float64{}
	hostMem := map[int64]int64{}
	count := func(u store.TokenUsage) {
		cpu += u.CPU
		mem += u.MemMB
		disk += u.DiskGB
		if u.Kind == store.UsageVM {
			vms++
		}
		if u.Kind != store.UsageK8sNS {
			hostCPU[u.HostID] += u.CPU
			hostMem[u.HostID] += u.MemMB
		}
	}
	for _, u := range usage {
		if !same(u) {
			count(u)
		}
	}
	before := struct {
		cpu       float64
		mem, disk int64
		host      map[int64]float64
		hostMem   map[int64]int64
	}{cpu, mem, disk, maps.Clone(hostCPU), maps.Clone(hostMem)}
	var label string
	var needCPU float64
	var needMem, needDisk int64
	for _, a := range adds {
		count(a)
		needCPU += a.CPU
		needMem += a.MemMB
		needDisk += a.DiskGB
		if label == "" {
			label = a.Label
		}
	}
	switch {
	case l.CPU > 0 && cpu > l.CPU+1e-9:
		return msgs.Errorf("hub.tokenOverCPU", label, needCPU, l.CPU-before.cpu, l.CPU)
	case l.MemMB > 0 && mem > l.MemMB:
		return msgs.Errorf("hub.tokenOverMem", label, needMem, l.MemMB-before.mem, l.MemMB)
	case l.DiskGB > 0 && disk > l.DiskGB:
		return msgs.Errorf("hub.tokenOverDisk", label, needDisk, l.DiskGB-before.disk, l.DiskGB)
	case l.VMs > 0 && vms > l.VMs:
		return msgs.Errorf("hub.tokenOverVMs", l.VMs)
	}
	for _, a := range adds {
		if a.Kind == store.UsageK8sNS {
			continue
		}
		switch {
		case l.HostCPU > 0 && hostCPU[a.HostID] > l.HostCPU+1e-9:
			return msgs.Errorf("hub.tokenOverHostCPU", a.Label, a.CPU, l.HostCPU-before.host[a.HostID], l.HostCPU)
		case l.HostMemMB > 0 && hostMem[a.HostID] > l.HostMemMB:
			return msgs.Errorf("hub.tokenOverHostMem", a.Label, a.MemMB, l.HostMemMB-before.hostMem[a.HostID], l.HostMemMB)
		}
	}
	return nil
}

// tokenByName — токен по имени автора задания («token:<имя>»): задания
// выкладки и машины узнают, с какими потолками их начали.
func (s *Server) tokenByAuthor(ctx context.Context, author string) (store.APIToken, bool) {
	name, ok := cutTokenAuthor(author)
	if !ok {
		return store.APIToken{}, false
	}
	list, err := s.db.ListAPITokens(ctx)
	if err != nil {
		return store.APIToken{}, false
	}
	for _, t := range list {
		if t.Name == name {
			return t, t.Limited()
		}
	}
	return store.APIToken{}, false
}

func cutTokenAuthor(author string) (string, bool) {
	const prefix = "token:"
	if len(author) > len(prefix) && author[:len(prefix)] == prefix {
		return author[len(prefix):], true
	}
	return "", false
}

// tokenVMCheck — машина в потолках токена «по правам»: vCPU, память и
// диск одной машины, суммы и хост; при успехе — запись в учёт.
func (s *Server) tokenVMCheck(ctx context.Context, t store.APIToken, host store.Host, spec vmcreate.Spec) error {
	l := t.Perms.Limits
	for _, c := range []struct {
		what       string
		got, limit int64
	}{{"vcpus", int64(spec.VCPUs), int64(l.VMCPU)}, {"memory_mb", int64(spec.MemoryMB), l.VMMemMB}, {"disk_gb", int64(spec.DiskGB), l.VMDiskGB}} {
		if c.limit > 0 && c.got > c.limit {
			return msgs.Errorf("hub.tokenVMSpec", spec.Name, c.what, c.got, c.limit)
		}
	}
	u := store.TokenUsage{TokenID: t.ID, Kind: store.UsageVM, HostID: host.ID, Ref: spec.Name, Label: spec.Name + "@" + host.Name,
		CPU: float64(spec.VCPUs), MemMB: int64(spec.MemoryMB), DiskGB: int64(spec.DiskGB)}
	if err := s.tokenFits(ctx, t, u); err != nil {
		return err
	}
	return s.db.SetTokenUsage(ctx, u)
}
