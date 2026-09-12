package hub

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"net/url"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/profile"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// groupApplyRequest — какой профиль и к какой группе применить.
type groupApplyRequest struct {
	ProfileID int64  `json:"profile_id"`
	Group     string `json:"group"`
}

// handleGroupApply ставит раскатку профиля по группе в очередь заданий
// хаба и отвечает номером задания. Дальше браузер не нужен: за ходом
// работы смотрят в «Заданиях», в том числе с другой машины и через час.
func (s *Server) handleGroupApply(w http.ResponseWriter, r *http.Request) {
	var req groupApplyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	prof, err := s.db.ProfileByID(r.Context(), req.ProfileID)
	if err != nil {
		fail(w, r, err)
		return
	}
	// Профиль разбирается здесь, до запуска: сломанное описание должно
	// отказать сразу, а не на первом хосте посреди раскатки.
	if _, err := profile.Parse([]byte(prof.Content)); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}

	group := strings.TrimSpace(req.Group)
	hosts, err := s.db.ListHosts(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	var ids []int64
	for _, h := range hosts {
		if strings.TrimSpace(h.Group) == group {
			ids = append(ids, h.ID)
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.groupHasHosts"))
		return
	}

	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind:  KindGroupApply,
		Title: msgs.Tc(r.Context(), "hub.profileGroupJobTitle", prof.Name, groupTitle(r.Context(), group)),
		// Ключ очереди — сама группа: две раскатки по одной группе разом
		// мешали бы друг другу, а по разным группам идут параллельно.
		Queue:  "group:" + group,
		Author: user,
		Steps:  len(ids),
		Params: GroupApplyParams{
			ProfileID: prof.ID, Profile: prof.Name, Group: group,
			Content: prof.Content, Hosts: ids,
		},
	})
	if err != nil {
		s.db.Audit(r.Context(), user, "profile.groupApply", group, "error", err.Error())
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "profile.groupApply", group, "ok", prof.Name)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "hosts": len(ids)})
}

func groupTitle(ctx context.Context, group string) string {
	if group == "" {
		return msgs.Tc(ctx, "hub.noGroup")
	}
	return group
}

// vmProvisionRequest — что и где создавать.
type vmProvisionRequest struct {
	HostID     int64         `json:"host_id"`
	Spec       vmcreate.Spec `json:"spec"`
	InstallNKT bool          `json:"install_nkt"`
	ProfileID  int64         `json:"profile_id"`
}

// handleVMProvision ставит в очередь создание машины на управляемом
// хосте с последующей записью её в список.
func (s *Server) handleVMProvision(w http.ResponseWriter, r *http.Request) {
	var req vmProvisionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := req.Spec.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	host, err := s.db.HostByID(r.Context(), req.HostID)
	if err != nil {
		fail(w, r, err)
		return
	}
	// Профиль применяет сама машина, а для этого на ней должен стоять
	// nkt: молча ставить его «заодно» нельзя, но и обещать применение
	// без него — тоже.
	if req.ProfileID != 0 {
		if _, err := s.db.ProfileByID(r.Context(), req.ProfileID); err != nil {
			fail(w, r, err)
			return
		}
		if !req.InstallNKT {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.applyProfileNktMustInstalled"))
			return
		}
	}

	steps := 4
	if req.InstallNKT {
		steps = 5
	}
	if req.ProfileID != 0 {
		steps = 6
	}

	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind:  KindVMProvision,
		Title: msgs.Tc(r.Context(), "hub.machineOnHostJobTitle", req.Spec.Name, host.Name),
		// Ключ очереди — хост, на котором создаётся машина: копирование
		// образа занимает его диск, и делать это двумя заданиями разом
		// незачем.
		Queue:  fmt.Sprintf("vm:%d", host.ID),
		Author: user,
		Steps:  steps,
		Params: VMProvisionParams{
			HostID: host.ID, Spec: req.Spec,
			InstallNKT: req.InstallNKT, ProfileID: req.ProfileID,
		},
	})
	if err != nil {
		s.db.Audit(r.Context(), user, "vm.provision", req.Spec.Name, "error", err.Error())
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "vm.provision", req.Spec.Name, "ok", host.Name)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// handleDetectAddress выясняет адрес машины у хоста, на котором она
// работает, и записывает его.
//
// Нужно, когда адрес не успел появиться к концу создания: машина ещё
// грузилась или ждала DHCP. Без этого запись хоста осталась бы с
// заглушкой навсегда, а установка на неё — с невнятным отказом
// рукопожатия.
func (s *Server) handleDetectAddress(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	host, err := s.db.HostByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	if host.ParentID == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.addressCanDetectedOnlyMachines"))
		return
	}

	var res struct {
		Address string `json:"address"`
		State   string `json:"state"`
		Reason  string `json:"reason"`
		Detail  string `json:"detail"`
	}
	path := "/api/vm/address?name=" + url.QueryEscape(host.Name)
	if _, err := s.hub.HostAPI(r.Context(), host.ParentID, "GET", path, nil, &res); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if res.Address == "" {
		// Причина уходит кодом: её переводит интерфейс, а сырой ответ
		// virsh идёт рядом — пересказывать его своими словами хуже, чем
		// показать.
		writeJSON(w, http.StatusOK, map[string]any{
			"address": "", "found": false,
			"state": res.State, "reason": res.Reason, "detail": res.Detail,
		})
		return
	}
	if err := s.hub.UpdateHost(r.Context(), host.ID, host.Name, res.Address, host.SSHPort,
		host.SSHUser, host.SSHAuthKind, "", host.TerminalEnabled); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "vm.address", host.Name, "ok", res.Address)
	writeJSON(w, http.StatusOK, map[string]any{"address": res.Address, "found": true})
}

// Импорт машин, которые уже есть на хосте: хаб видит домены libvirt тем
// же вызовом, что и их состояние, и может завести их в список с
// родителем — как созданные им самим, только ключ внутрь чужой машины
// положить сам не может. Дальше — как с обычным хостом: пароль или
// ключ, установка nkt.

type discoveredVM struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Address string `json:"address,omitempty"`
}

// discoverVMs — домены хоста, которых ещё нет в списке.
func (s *Server) discoverVMs(ctx context.Context, hostID int64) ([]discoveredVM, error) {
	hosts, err := s.db.ListHosts(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, h := range hosts {
		if h.ParentID == hostID {
			known[h.Name] = true
		}
	}
	var list struct {
		VMs []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"vms"`
	}
	if _, err := s.hub.HostAPI(ctx, hostID, "GET", "/api/vms", nil, &list); err != nil {
		return nil, err
	}
	out := []discoveredVM{}
	for _, vm := range list.VMs {
		if known[vm.Name] {
			continue
		}
		d := discoveredVM{Name: vm.Name, State: vm.State}
		if vm.State == "running" {
			// Адрес — только у работающей: у выключенной его не у кого
			// спросить, а ждать здесь незачем — определится после старта.
			var res struct {
				Address string `json:"address"`
			}
			path := "/api/vm/address?name=" + url.QueryEscape(vm.Name)
			if _, err := s.hub.HostAPI(ctx, hostID, "GET", path, nil, &res); err == nil {
				d.Address = res.Address
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// handleVMDiscover отдаёт домены хоста, которых ещё нет в списке.
func (s *Server) handleVMDiscover(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	vms, err := s.discoverVMs(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": vms})
}

type vmImportRequest struct {
	Names   []string `json:"names"`
	SSHPort int      `json:"ssh_port"`
	SSHUser string   `json:"ssh_user"`
	// Password — вход по паролю; пусто — заводится ключ хаба, и его надо
	// положить в машину руками (кнопка «публичный ключ» в строке).
	Password string `json:"password,omitempty"`
}

// handleVMImport заводит выбранные домены записями с родителем.
func (s *Server) handleVMImport(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var req vmImportRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(req.Names) == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.pickLeastOneMachine"))
		return
	}
	if req.SSHPort <= 0 {
		req.SSHPort = 22
	}
	if strings.TrimSpace(req.SSHUser) == "" {
		req.SSHUser = "root"
	}
	parent, err := s.db.HostByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	// Адреса и состояния — свежие, тем же способом, что и при поиске.
	discovered, err := s.discoverVMs(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	found := map[string]discoveredVM{}
	for _, vm := range discovered {
		found[vm.Name] = vm
	}

	user := auth.Username(r.Context())
	type imported struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		PublicKey string `json:"public_key,omitempty"`
	}
	// Пустые срезы, а не nil: nil уезжает в JSON как null, и «errors.length»
	// в браузере роняло окно ровно после удачного импорта.
	done := []imported{}
	errs := []string{}
	for _, name := range req.Names {
		vm, ok := found[name]
		if !ok {
			errs = append(errs, msgs.Tc(r.Context(), "hub.vmNotOnHostOrListed", name))
			continue
		}
		addr := vm.Address
		if addr == "" {
			addr = PlaceholderAddr
		}
		var newID int64
		var pub string
		if req.Password != "" {
			newID, err = s.hub.AddHost(r.Context(), name, addr, req.SSHPort, req.SSHUser, store.HostAuthPassword, req.Password, false)
		} else {
			newID, pub, err = s.hub.AddHostGenerated(r.Context(), name, addr, req.SSHPort, req.SSHUser, false)
		}
		if err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
		if err := s.db.SetHostParent(r.Context(), newID, parent.ID); err != nil {
			errs = append(errs, msgs.Tc(r.Context(), "hub.vmBindFailed", name, err))
			continue
		}
		s.db.Audit(r.Context(), user, "vm.import", name, "ok", parent.Name)
		done = append(done, imported{ID: newID, Name: name, PublicKey: pub})
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": done, "errors": errs})
}
