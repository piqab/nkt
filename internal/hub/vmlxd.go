package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmnet"
)

// Экземпляры LXD через хаб (1.17.7) — наравне с машинами KVM: создать
// (контейнер или машину LXD), включить, выключить, перезапустить, снимки,
// vCPU, память и диск, удалить. Токену «по правам» с действием «машины» —
// только свои (учёт: вид vm, «lxd/<имя>»: общий счёт машин и общие
// потолки с KVM), в сети токена под правилами nkt_isolation, без
// привилегий и вложенности. {id} — хост с LXD.

// KindLXDCreate — задание хаба «создать экземпляр LXD».
const KindLXDCreate = "lxd.create.hub"

var lxdNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,62}$`)

// lxdRef — ссылка экземпляра в учёте токена.
func lxdRef(name string) string { return "lxd/" + name }

// lxdHost — хост {id} (только управляемый хост, не машина).
func (s *Server) lxdHost(w http.ResponseWriter, r *http.Request) (store.Host, bool) {
	id, err := hostIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return store.Host{}, false
	}
	if id == localHostID {
		return store.Host{ID: localHostID, Name: localHostName}, true
	}
	h, err := s.db.HostByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return store.Host{}, false
	}
	return h, true
}

// lxdOwned — экземпляр токена «по правам» (без токена — любой).
func (s *Server) lxdOwned(ctx context.Context, hostID int64, name string) error {
	t, byToken := tokenFromContext(ctx)
	if !byToken || !t.Limited() {
		return nil
	}
	usage, _ := s.db.ListTokenUsage(ctx, t.ID)
	for _, u := range usage {
		if u.Kind == store.UsageVM && u.HostID == hostID && u.Ref == lxdRef(name) {
			return nil
		}
	}
	return tokenDeny(http.StatusForbidden, "auth.tokenNotOwnVM", name)
}

// tokenLXDCheck — потолки одной машины и суммы токена; при успехе —
// запись в учёт (заменяет прежнюю того же экземпляра).
func (s *Server) tokenLXDCheck(ctx context.Context, t store.APIToken, host store.Host, name string, cpu int, memMB, diskGB int64) error {
	l := t.Perms.Limits
	for _, c := range []struct {
		what       string
		got, limit int64
	}{{"vcpus", int64(cpu), int64(l.VMCPU)}, {"memory_mb", memMB, l.VMMemMB}, {"disk_gb", diskGB, l.VMDiskGB}} {
		if c.limit > 0 && (c.got == 0 || c.got > c.limit) {
			return msgs.Errorf("hub.tokenVMSpec", name, c.what, c.got, c.limit)
		}
	}
	u := store.TokenUsage{TokenID: t.ID, Kind: store.UsageVM, HostID: host.ID, Ref: lxdRef(name), Label: name + "@" + host.Name + " (LXD)",
		CPU: float64(cpu), MemMB: memMB, DiskGB: diskGB}
	if err := s.tokenFits(ctx, t, u); err != nil {
		return err
	}
	return s.db.SetTokenUsage(ctx, u)
}

// handleLXDList — GET /hub/hosts/{id}/lxd: экземпляры хоста (токену —
// свои).
func (s *Server) handleLXDList(w http.ResponseWriter, r *http.Request) {
	h, ok := s.lxdHost(w, r)
	if !ok {
		return
	}
	var out struct {
		Instances []map[string]any `json:"instances"`
	}
	if _, err := s.hostCall(r.Context(), auth.Username(r.Context()), h.ID, "GET", "/api/lxd/instances", nil, &out); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	list := []map[string]any{}
	for _, in := range out.Instances {
		name, _ := in["name"].(string)
		if s.lxdOwned(r.Context(), h.ID, name) == nil {
			list = append(list, in)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": list})
}

// LXDCreateParams — вход задания создания.
type LXDCreateParams struct {
	HostID int64  `json:"host_id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	VM     bool   `json:"vm"`
	control.LXDLaunchOpts
}

// handleLXDCreate — POST /hub/hosts/{id}/lxd {"name","image","vm","cpu",
// "mem_mb","disk_gb"} → job_id.
func (s *Server) handleLXDCreate(w http.ResponseWriter, r *http.Request) {
	h, ok := s.lxdHost(w, r)
	if !ok {
		return
	}
	var req LXDCreateParams
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req.HostID = h.ID
	req.Network, req.Isolated = "", false
	if _, err := control.LXDLaunchArgsOpts(req.Image, req.Name, req.VM, req.LXDLaunchOpts); err != nil || !lxdNameRe.MatchString(req.Name) {
		if err == nil {
			err = msgs.Errorf("control.invalidInstanceName", req.Name)
		}
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	if t, byToken := tokenFromContext(ctx); byToken && t.Limited() {
		req.Network, req.Isolated = vmnet.IsolatedName(t.ID), true
		if err := s.tokenLXDCheck(ctx, t, h, req.Name, req.CPU, req.MemMB, req.DiskGB); err != nil {
			writeErr(w, r, http.StatusForbidden, err)
			return
		}
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(ctx, "api.backgroundJobsAreUnavailable"))
		return
	}
	user := auth.Username(ctx)
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindLXDCreate, TitleKey: "hub.lxdCreateJobTitle", TitleArgs: []any{req.Name, h.Name},
		Queue: "lxd:" + strconv.FormatInt(h.ID, 10), Author: user, Steps: 2, Params: req})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(ctx, user, "lxd.create", req.Name, "ok", map[string]any{"job_id": id, "host": h.Name, "image": req.Image})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// LXDCreateRunner — задание: сеть токена с правилами, затем lxc launch
// заданием хоста.
type LXDCreateRunner struct{ s *Server }

// NewLXDCreateRunner строит исполнителя.
func NewLXDCreateRunner(s *Server) *LXDCreateRunner { return &LXDCreateRunner{s: s} }

// Resumable — нет.
func (r *LXDCreateRunner) Resumable() bool { return false }

// Run проходит шаги; не создалось — учёт токена снимается.
func (r *LXDCreateRunner) Run(ctx context.Context, jc *jobs.Context) (err error) {
	var p LXDCreateParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	s := r.s
	defer func() {
		if err != nil {
			if _, ok := cutTokenAuthor(jc.Job.Author); ok {
				_ = s.db.DeleteTokenUsage(context.WithoutCancel(ctx), 0, store.UsageVM, p.HostID, lxdRef(p.Name))
			}
		}
	}()
	// Задание токена — его сеть, что бы ни лежало в параметрах.
	if _, ok := cutTokenAuthor(jc.Job.Author); ok {
		tctx, err := tokenAuthorContext(ctx, s.db, jc.Job.Author)
		if err != nil {
			return err
		}
		if t, _ := tokenFromContext(tctx); t.Limited() {
			p.Network, p.Isolated = vmnet.IsolatedName(t.ID), true
		}
	}
	user := jc.Job.Author
	jc.StepKey(1, 2, "hub.lxdStepNetwork")
	if p.Network != "" {
		var out struct {
			Bridges []string `json:"bridges"`
		}
		if _, err := s.hostCall(ctx, user, p.HostID, "POST", "/api/lxd/token-network", map[string]string{"name": p.Network}, &out); err != nil {
			return err
		}
		jc.Log("vmcreate.isolationApplied", p.Network, joinOr(out.Bridges, "—"))
	}
	jc.StepKey(2, 2, "hub.lxdStepLaunch")
	body := map[string]any{"name": p.Name, "image": p.Image, "vm": p.VM, "network": p.Network, "cpu": p.CPU, "mem_mb": p.MemMB, "disk_gb": p.DiskGB, "isolated": p.Isolated}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := s.hostCall(ctx, user, p.HostID, "POST", "/api/lxd/instances?job=1", body, &started); err != nil {
		return err
	}
	return s.waitHostJobVia(ctx, jc, user, p.HostID, started.JobID)
}

// lxdInstancePath — путь API хоста к экземпляру.
func lxdInstancePath(name, tail string) string {
	return "/api/lxd/instances/" + url.PathEscape(name) + tail
}

// lxdTarget — хост, имя и проверка владения для маршрутов экземпляра.
func (s *Server) lxdTarget(w http.ResponseWriter, r *http.Request) (store.Host, string, bool) {
	h, ok := s.lxdHost(w, r)
	if !ok {
		return h, "", false
	}
	name := chi.URLParam(r, "name")
	if !lxdNameRe.MatchString(name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("control.invalidInstanceName", name))
		return h, "", false
	}
	if err := s.lxdOwned(r.Context(), h.ID, name); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return h, "", false
	}
	return h, name, true
}

// lxdCall — вызов хоста с журналом.
func (s *Server) lxdCall(w http.ResponseWriter, r *http.Request, h store.Host, method, path string, body any, audit, name string) bool {
	user := auth.Username(r.Context())
	var out map[string]any
	if _, err := s.hostCall(r.Context(), user, h.ID, method, path, body, &out); err != nil {
		s.db.Audit(r.Context(), user, audit, name, "error", err.Error())
		writeErr(w, r, http.StatusBadGateway, err)
		return false
	}
	s.db.Audit(r.Context(), user, audit, name, "ok", nil)
	if out == nil {
		out = map[string]any{"status": "ok"}
	}
	writeJSON(w, http.StatusOK, out)
	return true
}

// handleLXDAction — POST /hub/hosts/{id}/lxd/{name}/{action}: start, stop,
// restart.
func (s *Server) handleLXDAction(w http.ResponseWriter, r *http.Request) {
	h, name, ok := s.lxdTarget(w, r)
	if !ok {
		return
	}
	action := chi.URLParam(r, "action")
	if _, err := control.LXDActionArgs(name, action); err != nil || action == "pause" {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.unknownMachineAction"))
		return
	}
	s.lxdCall(w, r, h, "POST", lxdInstancePath(name, "/"+action), nil, "lxd."+action, name)
}

// handleLXDSnapshotsHub — GET и POST /hub/hosts/{id}/lxd/{name}/snapshots.
func (s *Server) handleLXDSnapshotsHub(w http.ResponseWriter, r *http.Request) {
	h, name, ok := s.lxdTarget(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		s.lxdCall(w, r, h, "GET", lxdInstancePath(name, "/snapshots"), nil, "lxd.snapshots", name)
		return
	}
	// Имя снимка — name или snapshot (у MCP name — это экземпляр).
	var req struct {
		Name     string `json:"name"`
		Snapshot string `json:"snapshot"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.Snapshot != "" {
		req.Name = req.Snapshot
	}
	if t, byToken := tokenFromContext(r.Context()); byToken && t.Limited() {
		var cur struct {
			Snapshots []json.RawMessage `json:"snapshots"`
		}
		if _, err := s.hostCall(r.Context(), auth.Username(r.Context()), h.ID, "GET", lxdInstancePath(name, "/snapshots"), nil, &cur); err != nil {
			writeErr(w, r, http.StatusBadGateway, err)
			return
		}
		if limit := t.Perms.Limits.VMSnapshots; len(cur.Snapshots) >= limit {
			writeErr(w, r, http.StatusForbidden, msgs.Errorf("auth.tokenSnapshots", name, limit))
			return
		}
	}
	s.lxdCall(w, r, h, "POST", lxdInstancePath(name, "/snapshots"), map[string]any{"name": req.Name}, "lxd.snapshot", name)
}

// handleLXDSnapshotRestoreHub и handleLXDSnapshotDeleteHub — снимок.
func (s *Server) handleLXDSnapshotRestoreHub(w http.ResponseWriter, r *http.Request) {
	h, name, ok := s.lxdTarget(w, r)
	if !ok {
		return
	}
	s.lxdCall(w, r, h, "POST", lxdInstancePath(name, "/snapshots/"+url.PathEscape(chi.URLParam(r, "snap"))+"/restore"), nil, "lxd.snapshot-restore", name)
}

func (s *Server) handleLXDSnapshotDeleteHub(w http.ResponseWriter, r *http.Request) {
	h, name, ok := s.lxdTarget(w, r)
	if !ok {
		return
	}
	s.lxdCall(w, r, h, "DELETE", lxdInstancePath(name, "/snapshots/"+url.PathEscape(chi.URLParam(r, "snap"))), nil, "lxd.snapshot-delete", name)
}

// handleLXDResize — POST /hub/hosts/{id}/lxd/{name}/resize {"cpu","mem_mb",
// "disk_gb"}: токену — в потолках, учёт меняется сразу (отказ хоста —
// прежний).
func (s *Server) handleLXDResize(w http.ResponseWriter, r *http.Request) {
	h, name, ok := s.lxdTarget(w, r)
	if !ok {
		return
	}
	var req struct {
		CPU    int   `json:"cpu"`
		MemMB  int64 `json:"mem_mb"`
		DiskGB int64 `json:"disk_gb"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.CPU == 0 && req.MemMB == 0 && req.DiskGB == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("control.invalidResize"))
		return
	}
	ctx := r.Context()
	var restore *store.TokenUsage
	if t, byToken := tokenFromContext(ctx); byToken && t.Limited() {
		usage, _ := s.db.ListTokenUsage(ctx, t.ID)
		for _, u := range usage {
			if u.Kind == store.UsageVM && u.HostID == h.ID && u.Ref == lxdRef(name) {
				old := u
				restore = &old
			}
		}
		if restore == nil {
			writeErr(w, r, http.StatusForbidden, tokenDeny(http.StatusForbidden, "auth.tokenNotOwnVM", name))
			return
		}
		if req.DiskGB > 0 && req.DiskGB < restore.DiskGB {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("control.diskShrink", restore.DiskGB, req.DiskGB))
			return
		}
		cpu, mem, disk := int(restore.CPU), restore.MemMB, restore.DiskGB
		if req.CPU > 0 {
			cpu = req.CPU
		}
		if req.MemMB > 0 {
			mem = req.MemMB
		}
		if req.DiskGB > 0 {
			disk = req.DiskGB
		}
		if err := s.tokenLXDCheck(ctx, t, h, name, cpu, mem, disk); err != nil {
			writeErr(w, r, http.StatusForbidden, err)
			return
		}
	}
	if !s.lxdCall(w, r, h, "POST", lxdInstancePath(name, "/limits"), req, "lxd.resize", name) && restore != nil {
		_ = s.db.SetTokenUsage(context.WithoutCancel(ctx), *restore)
	}
}

// handleLXDDeleteHub — DELETE /hub/hosts/{id}/lxd/{name}: экземпляр
// (запущенный — гасится) и учёт токена.
func (s *Server) handleLXDDeleteHub(w http.ResponseWriter, r *http.Request) {
	h, name, ok := s.lxdTarget(w, r)
	if !ok {
		return
	}
	if s.lxdCall(w, r, h, "DELETE", lxdInstancePath(name, "?force=true"), nil, "lxd.delete", name) {
		_ = s.db.DeleteTokenUsage(context.WithoutCancel(r.Context()), 0, store.UsageVM, h.ID, lxdRef(name))
	}
}

// joinOr — список через запятую или заглушка.
func joinOr(list []string, empty string) string {
	if len(list) == 0 {
		return empty
	}
	out := list[0]
	for _, s := range list[1:] {
		out += ", " + s
	}
	return out
}
