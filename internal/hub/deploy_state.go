package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Где стек конвейера выложен сейчас. Описание можно поменять — другой
// хост, другое имя стека, — и выкладка должна знать, что было до неё:
// старый стек на прежнем хосте убрать, переименованный — заменить новым,
// а его порты и имена контейнеров не считать чужими. Хранится в kv хаба.

// deployedStack — стек, выложенный последней удачной выкладкой.
type deployedStack struct {
	Project string         `json:"project"`
	Hosts   []deployedHost `json:"hosts"`
}

type deployedHost struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// leftoverStack — старый стек, который убрать не удалось (хост недоступен
// или удалён из хаба): виден в строке конвейера с кнопкой «Убрать».
type leftoverStack struct {
	HostID  int64  `json:"host_id"`
	Host    string `json:"host"`
	Project string `json:"project"`
	Reason  string `json:"reason,omitempty"`
	At      string `json:"at"`
}

func deployedKey(id int64) string  { return "pipeline." + strconv.FormatInt(id, 10) + ".deployed" }
func leftoversKey(id int64) string { return "pipeline." + strconv.FormatInt(id, 10) + ".leftovers" }

func (s *Server) loadDeployed(ctx context.Context, id int64) *deployedStack {
	raw, ok, err := s.db.KVGet(ctx, deployedKey(id))
	if err != nil || !ok || raw == "" {
		return nil
	}
	var d deployedStack
	if json.Unmarshal([]byte(raw), &d) != nil || d.Project == "" {
		return nil
	}
	return &d
}

func (s *Server) saveDeployed(ctx context.Context, id int64, d deployedStack) {
	if raw, err := json.Marshal(d); err == nil {
		_ = s.db.KVSet(ctx, deployedKey(id), string(raw))
	}
}

func (s *Server) loadLeftovers(ctx context.Context, id int64) []leftoverStack {
	raw, ok, err := s.db.KVGet(ctx, leftoversKey(id))
	if err != nil || !ok || raw == "" {
		return nil
	}
	var out []leftoverStack
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func (s *Server) saveLeftovers(ctx context.Context, id int64, list []leftoverStack) {
	raw := ""
	if len(list) > 0 {
		if b, err := json.Marshal(list); err == nil {
			raw = string(b)
		}
	}
	_ = s.db.KVSet(ctx, leftoversKey(id), raw)
}

// deployedFromContent — где стек по описанию (для конвейеров, выложенных
// до того, как хаб начал это запоминать: берётся описание, бывшее до
// правки). Хосты — по имени, без проверки, что они в сети.
func (s *Server) deployedFromContent(ctx context.Context, content string) *deployedStack {
	spec, err := deploy.ParseSpec(content)
	if err != nil || spec.Action != deploy.ActionCompose || spec.Compose == nil {
		return nil
	}
	d := &deployedStack{Project: spec.Compose.Project}
	hosts, _ := s.db.ListHosts(ctx)
	for _, n := range spec.Compose.Hosts {
		if strings.TrimSpace(n) == "localhost" && s.local != nil {
			d.Hosts = append(d.Hosts, deployedHost{ID: localHostID, Name: "localhost"})
			continue
		}
		for _, h := range hosts {
			if sameHostName(h.Name, n) {
				d.Hosts = append(d.Hosts, deployedHost{ID: h.ID, Name: h.Name})
			}
		}
	}
	if spec.Compose.Group != "" {
		for _, h := range hosts {
			if h.Group == spec.Compose.Group {
				d.Hosts = append(d.Hosts, deployedHost{ID: h.ID, Name: h.Name})
			}
		}
	}
	return d
}

// rememberDeployedBefore — правка описания выложенного конвейера, о
// котором хаб ещё ничего не запомнил: зафиксировать прежнее описание как
// «выложено сейчас», иначе следующая выкладка не узнает, что убирать.
func (s *Server) rememberDeployedBefore(ctx context.Context, p store.Pipeline) {
	if p.LastCommit == "" || s.loadDeployed(ctx, p.ID) != nil {
		return
	}
	if d := s.deployedFromContent(ctx, p.Content); d != nil && len(d.Hosts) > 0 {
		s.saveDeployed(ctx, p.ID, *d)
	}
}

// replaceOn — прежнее имя стека, если на этом хосте он был выложен под
// другим (переименование project): хост заменит его новым.
func replaceOn(prev *deployedStack, hostID int64, project string) string {
	if prev == nil || prev.Project == project {
		return ""
	}
	for _, h := range prev.Hosts {
		if h.ID == hostID {
			return prev.Project
		}
	}
	return ""
}

// stackMovesFrom — прежние хосты, где стек остался бы после выкладки на
// targets (их в новом описании нет).
func stackMovesFrom(prev *deployedStack, targets []targetHost) []deployedHost {
	if prev == nil {
		return nil
	}
	var out []deployedHost
	for _, h := range prev.Hosts {
		if !slices.ContainsFunc(targets, func(t targetHost) bool { return t.ID == h.ID }) {
			out = append(out, h)
		}
	}
	return out
}

// hostUsable — хост есть на хабе, в сети и отвечает (для «убрать старый
// стек»); причина — словами, если нет.
func (s *Server) hostUsable(ctx context.Context, id int64) (bool, string) {
	if id == localHostID {
		return s.local != nil, ""
	}
	h, err := s.db.HostByID(ctx, id)
	if err != nil {
		return false, msgs.Tc(ctx, "deploy.leftoverHostGone")
	}
	if h.Status != store.HostStatusOnline {
		return false, msgs.Tc(ctx, "hub.hostReadyYetStatus", h.Name, h.Status)
	}
	if ov, ok := s.hub.overviewOf(id); ok && !ov.reachable {
		return false, msgs.Tc(ctx, "deploy.leftoverUnreachable")
	}
	return true, ""
}

// cleanupMovedStacks — после удачной выкладки: старый стек с прежних
// хостов убрать (каталог с данными — в .nkt-removed на хосте); не вышло —
// запомнить «остался» и не ронять выкладку: новый стек уже работает.
func (s *Server) cleanupMovedStacks(ctx context.Context, jc *jobs.Context, user string, pl store.Pipeline, project string, prev *deployedStack, targets []targetHost) {
	if prev == nil {
		return
	}
	lang := jc.Lang()
	left := s.loadLeftovers(ctx, pl.ID)
	for _, h := range stackMovesFrom(prev, targets) {
		ok, why := s.hostUsable(ctx, h.ID)
		if ok {
			t := targetHost{ID: h.ID, Name: h.Name}
			var started struct {
				JobID int64 `json:"job_id"`
			}
			_, err := s.composeHostPost(ctx, jc, user, t, "/api/compose/stacks/remove", map[string]any{"project": prev.Project}, &started)
			if err == nil {
				err = s.waitHostJobVia(ctx, jc, user, h.ID, started.JobID)
			}
			if err == nil {
				jc.Log("deploy.oldStackRemoved", prev.Project, h.Name)
				continue
			}
			why = msgs.Localize(lang, err)
		}
		jc.Log("deploy.oldStackLeft", prev.Project, h.Name, why)
		left = slices.DeleteFunc(left, func(l leftoverStack) bool { return l.HostID == h.ID && l.Project == prev.Project })
		left = append(left, leftoverStack{HostID: h.ID, Host: h.Name, Project: prev.Project, Reason: why, At: store.Now()})
	}
	// Стек снова там, где числился оставшимся, — он больше не «остался».
	left = slices.DeleteFunc(left, func(l leftoverStack) bool {
		return l.Project == project && slices.ContainsFunc(targets, func(t targetHost) bool { return t.ID == l.HostID })
	})
	s.saveLeftovers(ctx, pl.ID, left)
}

// handlePipelineLeftoverRemove — POST /hub/pipelines/{id}/leftovers/remove
// {host_id, project}: убрать оставшийся старый стек, когда хост снова
// доступен (задание хоста; запись «остался» снимается сразу), или
// просто забыть о нём (forget: хост удалён или стек убран вручную).
func (s *Server) handlePipelineLeftoverRemove(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		HostID  int64  `json:"host_id"`
		Project string `json:"project"`
		Forget  bool   `json:"forget"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	left := s.loadLeftovers(ctx, pl.ID)
	idx := slices.IndexFunc(left, func(l leftoverStack) bool { return l.HostID == req.HostID && l.Project == req.Project })
	if idx < 0 {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("deploy.leftoverUnknown"))
		return
	}
	user := auth.Username(ctx)
	out := map[string]any{"status": "forgotten"}
	if !req.Forget {
		if ok, why := s.hostUsable(ctx, req.HostID); !ok {
			writeErr(w, r, http.StatusConflict, msgs.Errorf("deploy.leftoverStill", left[idx].Host, why))
			return
		}
		var started struct {
			JobID int64 `json:"job_id"`
		}
		if _, err := s.hostCall(ctx, user, req.HostID, http.MethodPost, "/api/compose/stacks/remove", map[string]any{"project": req.Project}, &started); err != nil {
			writeErr(w, r, http.StatusBadGateway, err)
			return
		}
		out = map[string]any{"status": "started", "job_id": started.JobID, "host_id": req.HostID}
	}
	s.saveLeftovers(ctx, pl.ID, append(left[:idx:idx], left[idx+1:]...))
	s.db.Audit(ctx, user, "pipeline.leftover_remove", pl.Name, "ok", map[string]any{"host": left[idx].Host, "project": req.Project, "forget": req.Forget})
	writeJSON(w, http.StatusOK, out)
}

// sameHostName — имя хоста в описании и на хабе: без учёта регистра и
// пробелов по краям.
func sameHostName(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
