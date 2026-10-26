package hub

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Цикл машины через хаб (1.17.4): снимки, ресурсы, переустановка и
// удаление — для администратора и для токена «по правам» с действием
// «машины» (только свои машины: их проверяет предел хостов токена).
// {id} — запись самой машины в списке хостов.

// KindVMReinstall — вид задания хаба «переустановить машину».
const KindVMReinstall = "vm.reinstall.hub"

// vmMachine — запись машины по {id} (у неё есть хост-родитель).
func (s *Server) vmMachine(w http.ResponseWriter, r *http.Request) (store.Host, bool) {
	id, err := hostIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return store.Host{}, false
	}
	host, err := s.db.HostByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return store.Host{}, false
	}
	if host.ParentID == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.hostMachineThereNobodyPower"))
		return store.Host{}, false
	}
	return host, true
}

// vmPath — путь API хоста к машине.
func vmPath(h store.Host, tail string) string {
	return "/api/vms/" + url.PathEscape(h.Name) + tail
}

func (s *Server) handleVMSnapshotsList(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	var out struct {
		Snapshots []string `json:"snapshots"`
	}
	if _, err := s.hostCall(r.Context(), auth.Username(r.Context()), vm.ParentID, "GET", vmPath(vm, "/snapshots"), nil, &out); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if out.Snapshots == nil {
		out.Snapshots = []string{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleVMSnapshotCreateHub(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.vmSnapshotAllowed(r.Context(), vm); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	s.vmHostCall(w, r, vm, "POST", vmPath(vm, "/snapshots"), map[string]string{"name": req.Name}, "vm.snapshot")
}

func (s *Server) handleVMSnapshotRevertHub(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	s.vmHostCall(w, r, vm, "POST", vmPath(vm, "/snapshots/"+url.PathEscape(chi.URLParam(r, "snap"))+"/revert"), nil, "vm.snapshot-revert")
}

func (s *Server) handleVMSnapshotDeleteHub(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	s.vmHostCall(w, r, vm, "DELETE", vmPath(vm, "/snapshots/"+url.PathEscape(chi.URLParam(r, "snap"))), nil, "vm.snapshot-delete")
}

// vmSnapshotAllowed — снимков на машину не больше потолка токена (0 —
// нельзя); без токена «по правам» — без потолка.
func (s *Server) vmSnapshotAllowed(ctx context.Context, vm store.Host) error {
	t, byToken := tokenFromContext(ctx)
	if !byToken || !t.Limited() {
		return nil
	}
	var cur struct {
		Snapshots []string `json:"snapshots"`
	}
	if _, err := s.hostCall(ctx, auth.Username(ctx), vm.ParentID, "GET", vmPath(vm, "/snapshots"), nil, &cur); err != nil {
		return err
	}
	if limit := t.Perms.Limits.VMSnapshots; len(cur.Snapshots) >= limit {
		return msgs.Errorf("auth.tokenSnapshots", vm.Name, limit)
	}
	return nil
}

// vmHostCall передаёт вызов хосту машины и пишет журнал.
func (s *Server) vmHostCall(w http.ResponseWriter, r *http.Request, vm store.Host, method, path string, body any, audit string) {
	user := auth.Username(r.Context())
	var out map[string]any
	if _, err := s.hostCall(r.Context(), auth.Username(r.Context()), vm.ParentID, method, path, body, &out); err != nil {
		s.db.Audit(r.Context(), user, audit, vm.Name, "error", err.Error())
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	s.db.Audit(r.Context(), user, audit, vm.Name, "ok", nil)
	if out == nil {
		out = map[string]any{"status": "ok"}
	}
	writeJSON(w, http.StatusOK, out)
}

// vmResizeRequest — новые размеры (0 — не менять).
type vmResizeRequest struct {
	VCPUs    int   `json:"vcpus"`
	MemoryMB int64 `json:"memory_mb"`
	DiskGB   int64 `json:"disk_gb"`
}

// handleVMResize меняет vCPU, память и диск. Токену — в его потолках
// (учёт меняется сразу; хост отказал — прежняя запись возвращается).
func (s *Server) handleVMResizeHub(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	var req vmResizeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.VCPUs < 0 || req.MemoryMB < 0 || req.DiskGB < 0 || req.VCPUs == 0 && req.MemoryMB == 0 && req.DiskGB == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("control.invalidResize"))
		return
	}
	out, code, err := s.vmResize(r.Context(), vm, req)
	if err != nil {
		writeErr(w, r, code, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// vmResize меняет размеры машины; токену — в его потолках (учёт меняется
// сразу; хост отказал — прежняя запись возвращается).
func (s *Server) vmResize(ctx context.Context, vm store.Host, req vmResizeRequest) (map[string]any, int, error) {
	// Границы — до преобразований в int: те же, что у хоста.
	if req.VCPUs < 0 || req.VCPUs > 128 || req.MemoryMB < 0 || req.MemoryMB > 1<<20 || req.DiskGB < 0 || req.DiskGB > 65536 {
		return nil, http.StatusBadRequest, msgs.Errorf("control.invalidResize")
	}
	var restore *store.TokenUsage
	if t, byToken := tokenFromContext(ctx); byToken && t.Limited() {
		old, ok := s.tokenVMUsage(ctx, t, vm)
		if !ok {
			return nil, http.StatusForbidden, tokenDeny(http.StatusForbidden, "auth.tokenNotOwnVM", vm.Name)
		}
		if req.DiskGB > 0 && req.DiskGB < old.DiskGB {
			return nil, http.StatusBadRequest, msgs.Errorf("control.diskShrink", old.DiskGB, req.DiskGB)
		}
		spec := vmcreate.Spec{Name: vm.Name, VCPUs: int(old.CPU), MemoryMB: int(old.MemMB), DiskGB: int(old.DiskGB)}
		if req.VCPUs > 0 {
			spec.VCPUs = req.VCPUs
		}
		if req.MemoryMB > 0 {
			spec.MemoryMB = int(req.MemoryMB)
		}
		if req.DiskGB > 0 {
			spec.DiskGB = int(req.DiskGB)
		}
		parent := store.Host{ID: localHostID, Name: localHostName}
		if vm.ParentID != localHostID {
			var err error
			if parent, err = s.db.HostByID(ctx, vm.ParentID); err != nil {
				return nil, http.StatusBadGateway, err
			}
		}
		if err := s.tokenVMCheck(ctx, t, parent, spec); err != nil {
			return nil, http.StatusForbidden, err
		}
		restore = &old
	}
	user := auth.Username(ctx)
	var out map[string]any
	if _, err := s.hostCall(ctx, auth.Username(ctx), vm.ParentID, "POST", vmPath(vm, "/resize"), req, &out); err != nil {
		if restore != nil {
			_ = s.db.SetTokenUsage(context.WithoutCancel(ctx), *restore)
		}
		s.db.Audit(ctx, user, "vm.resize", vm.Name, "error", err.Error())
		return nil, http.StatusBadGateway, err
	}
	s.db.Audit(ctx, user, "vm.resize", vm.Name, "ok", req)
	return out, http.StatusOK, nil
}

// tokenVMUsage — запись учёта машины токена.
func (s *Server) tokenVMUsage(ctx context.Context, t store.APIToken, vm store.Host) (store.TokenUsage, bool) {
	usage, _ := s.db.ListTokenUsage(ctx, t.ID)
	for _, u := range usage {
		if u.Kind == store.UsageVM && u.HostID == vm.ParentID && u.Ref == vm.Name {
			return u, true
		}
	}
	return store.TokenUsage{}, false
}

// VMReinstallParams — вход задания переустановки.
type VMReinstallParams struct {
	HostID   int64  `json:"host_id"`
	ImageID  string `json:"image_id"`
	SSHKey   string `json:"ssh_key,omitempty"`
	UserData string `json:"user_data,omitempty"`
	// InstallNKT — поставить nkt заново (после переустановки его нет).
	InstallNKT bool `json:"install_nkt"`
}

func (s *Server) handleVMReinstallHub(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	var req VMReinstallParams
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req.HostID = vm.ID
	check := vmcreate.Spec{ImageID: req.ImageID, SSHKey: req.SSHKey, UserData: req.UserData}
	if err := vmReinstallCheck(check); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if t, byToken := tokenFromContext(r.Context()); byToken && t.Limited() {
		if err := tokenVMImage(check); err != nil {
			writeErr(w, r, http.StatusForbidden, err)
			return
		}
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	user := auth.Username(r.Context())
	steps := 3
	if req.InstallNKT {
		steps = 4
	}
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindVMReinstall, TitleKey: "hub.vmReinstallJobTitle", TitleArgs: []any{vm.Name},
		Queue: "vm:" + strconv.FormatInt(vm.ParentID, 10), Author: user, Steps: steps, Params: req,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "vm.reinstall", vm.Name, "ok", map[string]any{"job_id": id, "image_id": req.ImageID})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// vmReinstallCheck — образ, ключ и свой user-data до задания.
func vmReinstallCheck(spec vmcreate.Spec) error {
	if spec.ImageID == "" {
		return msgs.Errorf("vmcreate.imageSelected")
	}
	if k := strings.TrimSpace(spec.SSHKey); k != "" && (strings.ContainsAny(k, "\r\n") || !strings.HasPrefix(k, "ssh-") && !strings.HasPrefix(k, "ecdsa-") && !strings.HasPrefix(k, "sk-")) {
		return msgs.Errorf("vmcreate.keyDoesLookLikeSSH")
	}
	if spec.UserData != "" {
		if _, err := vmcreate.CheckUserData(spec.UserData); err != nil {
			return err
		}
	}
	return nil
}

// handleVMDeleteHub удаляет машину целиком: домен, диски, запись в
// списке и учёт токена — заданием удаления хоста.
func (s *Server) handleVMDeleteHub(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	user := auth.Username(r.Context())
	id, err := s.startVMDelete(r.Context(), vm, user)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "vm.delete", vm.Name, "ok", map[string]any{"job_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// startVMDelete ставит задание удаления машины целиком.
func (s *Server) startVMDelete(ctx context.Context, vm store.Host, author string) (int64, error) {
	// Порты и маршруты Fly токенов на эту машину — до её удаления.
	s.releaseVMPorts(ctx, vm)
	s.releaseVMFly(ctx, vm)
	return s.jobs.Start(ctx, jobs.Spec{
		Kind: KindHostPurge, TitleKey: "hub.hostPurgeJob", TitleArgs: []any{vm.Name},
		Queue: "host-install:" + strconv.FormatInt(vm.ID, 10), Author: author, Steps: 2,
		Params: HostPurgeParams{HostID: vm.ID, Name: vm.Name, Purge: PurgeOptions{VM: true, VMDisks: true}},
	})
}

// VMReinstallRunner — задание хаба: переустановка на хосте, новый адрес,
// забытый ключ SSH машины (он другой после переустановки) и, если
// просили, nkt заново.
type VMReinstallRunner struct{ p *VMProvisionRunner }

// NewVMReinstallRunner строит исполнителя.
func NewVMReinstallRunner(m *Manager) *VMReinstallRunner {
	return &VMReinstallRunner{p: NewVMProvisionRunner(m)}
}

// Resumable — нет: переустановку на хосте повторяют целиком.
func (r *VMReinstallRunner) Resumable() bool { return false }

// Run проходит шаги.
func (r *VMReinstallRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p VMReinstallParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	m := r.p.m
	vm, err := m.db.HostByID(ctx, p.HostID)
	if err != nil {
		return err
	}
	parent, err := m.db.HostByID(ctx, vm.ParentID)
	if err != nil {
		return msgs.Errorf("hub.machineSHostFound", err)
	}
	secret, err := m.hostSecret(vm)
	if err != nil {
		return err
	}
	key := hubPublicKeyLine(vm, secret)
	if key == "" {
		return msgs.Errorf("hub.vmReinstallNoKey", vm.Name)
	}
	spec := vmcreate.Spec{Name: vm.Name, ImageID: p.ImageID, User: vm.SSHUser, SSHKey: p.SSHKey, ExtraKeys: []string{key}, UserData: p.UserData}
	if err := vmReinstallCheck(spec); err != nil {
		return err
	}
	// Машина токена — снова в его сети и с его образами.
	if _, ok := cutTokenAuthor(jc.Job.Author); ok {
		tctx, err := tokenAuthorContext(ctx, m.db, jc.Job.Author)
		if err != nil {
			return err
		}
		if t, _ := tokenFromContext(tctx); t.Limited() {
			if err := tokenVMIsolate(t, &spec); err != nil {
				return err
			}
		}
	}
	steps := 3
	if p.InstallNKT {
		steps = 4
	}
	if err := r.p.ensureHostVersion(ctx, jc, parent); err != nil {
		return err
	}

	// 1. Переустановка на хосте.
	jc.StepKey(1, steps, "hub.vmStepReinstallOn", parent.Name)
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := m.HostAPI(ctx, parent.ID, "POST", "/api/vm/reinstall", vmcreate.ReinstallParams{Spec: spec}, &started); err != nil {
		return msgs.Errorf("hub.startingCreation", parent.Name, err)
	}
	addr, err := r.p.waitVMJob(ctx, jc, parent, started.JobID)
	if err != nil {
		return err
	}
	if addr == "" {
		addr = r.p.pollAddress(ctx, jc, parent, vm.Name)
	}

	// 2. Запись: адрес, ключ машины забыт (сверится заново), nkt нет.
	jc.StepKey(2, steps, "hub.vmStepAddress")
	_ = m.db.SetHostSSHKey(ctx, vm.ID, "")
	_ = m.db.SetHostVersion(ctx, vm.ID, "")
	if addr != "" && addr != vm.Addr {
		if err := m.db.UpdateHost(ctx, vm.ID, vm.Name, addr, vm.SSHPort, vm.SSHUser, vm.SSHAuthKind); err != nil {
			return msgs.Errorf("hub.savingAddress", err)
		}
	}
	m.dropSSHPool(vm.ID)
	jc.Log("hub.vmReinstalled", vm.Name, addr)

	// 3. nkt заново.
	if p.InstallNKT {
		jc.StepKey(3, steps, "hub.vmStepInstall")
		if err := r.p.installNKT(ctx, jc, vm.ID); err != nil {
			return err
		}
	}
	jc.StepKey(steps, steps, "hub.vmStepDone")
	return nil
}
