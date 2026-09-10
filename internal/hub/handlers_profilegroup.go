package hub

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/profile"
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "фоновые задания недоступны")
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	group := strings.TrimSpace(req.Group)
	hosts, err := s.db.ListHosts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var ids []int64
	for _, h := range hosts {
		if strings.TrimSpace(h.Group) == group {
			ids = append(ids, h.ID)
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "в группе нет хостов")
		return
	}

	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind:  KindGroupApply,
		Title: "профиль " + prof.Name + " → группа " + groupTitle(group),
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "profile.groupApply", group, "ok", prof.Name)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "hosts": len(ids)})
}

func groupTitle(group string) string {
	if group == "" {
		return "без группы"
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.Spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "фоновые задания недоступны")
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
			writeError(w, http.StatusBadRequest,
				"чтобы применить профиль, на новую машину нужно поставить nkt — он и применяет профиль")
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
		Title: "машина " + req.Spec.Name + " на " + host.Name,
		// Ключ очереди — хост, на котором создаётся машина: копирование
		// образа занимает его диск, и делать это двумя заданиями разом
		// незачем.
		Queue:  fmt.Sprintf("vm:%d", host.ID),
		Author: user,
		Steps: steps,
		Params: VMProvisionParams{
			HostID: host.ID, Spec: req.Spec,
			InstallNKT: req.InstallNKT, ProfileID: req.ProfileID,
		},
	})
	if err != nil {
		s.db.Audit(r.Context(), user, "vm.provision", req.Spec.Name, "error", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	host, err := s.db.HostByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	if host.ParentID == 0 {
		writeError(w, http.StatusBadRequest, "адрес определяется только у машин, созданных на управляемом хосте")
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
		writeError(w, http.StatusBadGateway, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "vm.address", host.Name, "ok", res.Address)
	writeJSON(w, http.StatusOK, map[string]any{"address": res.Address, "found": true})
}
