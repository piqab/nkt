package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// «Остановить» и «Запустить» выкладку конвейера compose — на всех хостах,
// где стек выложен последней удачной выкладкой. Остановка — compose stop
// (контейнеры, тома и .env остаются), запуск — та же версия: файлы не
// пишутся, образы, которых нет на хосте, скачиваются с ключом конвейера
// (ключ остаётся в рамках действий конвейера, а не произвольного
// сценария). Остановленная выкладка помечается: ассистент и находки не
// принимают её за поломку, оповещения по её целям приходят с пометкой.

// KindPipelinePower — задание «остановить/запустить выкладку».
const KindPipelinePower = "pipeline.power"

// PipelinePowerParams — вход задания.
type PipelinePowerParams struct {
	PipelineID int64  `json:"pipeline_id"`
	Action     string `json:"action"` // start | stop
}

// pipelineStopped — пометка остановленной выкладки (kv хаба).
type pipelineStopped struct {
	By      string         `json:"by"`
	At      string         `json:"at"`
	Project string         `json:"project"`
	Hosts   []deployedHost `json:"hosts"`
	// Containers — контейнеры стека по хостам (id хоста строкой): по ним
	// цели Traffic Fly узнают, что лежат вместе с выкладкой.
	Containers map[string][]string `json:"containers,omitempty"`
}

func stoppedKey(id int64) string { return "deploy.stopped." + strconv.FormatInt(id, 10) }

func (s *Server) loadStopped(ctx context.Context, id int64) *pipelineStopped {
	raw, ok, err := s.db.KVGet(ctx, stoppedKey(id))
	if err != nil || !ok || raw == "" {
		return nil
	}
	var st pipelineStopped
	if json.Unmarshal([]byte(raw), &st) != nil {
		return nil
	}
	return &st
}

func (s *Server) saveStopped(ctx context.Context, id int64, st *pipelineStopped) {
	if st == nil {
		_ = s.db.KVSet(ctx, stoppedKey(id), "")
		return
	}
	if raw, err := json.Marshal(st); err == nil {
		_ = s.db.KVSet(ctx, stoppedKey(id), string(raw))
	}
}

// stoppedTarget — выкладка, остановленная вместе с этим контейнером
// хоста (цель Traffic Fly), или nil.
func (s *Server) stoppedTarget(ctx context.Context, hostID int64, container string) (*store.Pipeline, *pipelineStopped) {
	if container == "" {
		return nil, nil
	}
	list, err := s.db.ListPipelines(ctx)
	if err != nil {
		return nil, nil
	}
	for i := range list {
		st := s.loadStopped(ctx, list[i].ID)
		if st == nil {
			continue
		}
		if slices.Contains(st.Containers[strconv.FormatInt(hostID, 10)], container) {
			return &list[i], st
		}
	}
	return nil, nil
}

// powerPlan — что затронет действие: хосты выкладки и маршруты Traffic Fly
// с целями-контейнерами этого стека на них.
type powerPlan struct {
	Project string           `json:"project"`
	Hosts   []deployedHost   `json:"hosts"`
	Routes  []string         `json:"routes"`
	Stopped *pipelineStopped `json:"stopped,omitempty"`
}

func (s *Server) pipelinePowerPlan(ctx context.Context, pl store.Pipeline) (powerPlan, error) {
	spec, err := deploy.ParseSpec(pl.Content)
	if err != nil || spec.Action != deploy.ActionCompose || spec.Compose == nil {
		return powerPlan{}, msgs.Errorf("deploy.powerOnlyCompose")
	}
	d := s.loadDeployed(ctx, pl.ID)
	if d == nil || len(d.Hosts) == 0 {
		return powerPlan{}, msgs.Errorf("deploy.powerNotDeployed", pl.Name)
	}
	plan := powerPlan{Project: d.Project, Hosts: d.Hosts, Routes: []string{}, Stopped: s.loadStopped(ctx, pl.ID)}
	// Контейнер стека compose — «стек-сервис-1» или свой container_name;
	// для плана хватает имени стека в начале (точный список — у
	// остановки, с хоста).
	{
		for _, r := range s.flyDoc(ctx).Routes {
			for _, t := range r.Targets {
				onHost := slices.ContainsFunc(d.Hosts, func(h deployedHost) bool { return h.ID == t.HostID })
				if onHost && t.Kind == "container" && (t.Ref == d.Project || strings.HasPrefix(t.Ref, d.Project+"-") || strings.HasPrefix(t.Ref, d.Project+"_")) {
					if !slices.Contains(plan.Routes, r.ID) {
						plan.Routes = append(plan.Routes, r.ID)
					}
				}
			}
		}
	}
	return plan, nil
}

// handlePipelinePowerPlan — GET /hub/pipelines/{id}/power: хосты и
// маршруты для окна подтверждения.
func (s *Server) handlePipelinePowerPlan(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	plan, err := s.pipelinePowerPlan(r.Context(), pl)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handlePipelineStart / handlePipelineStop — POST /hub/pipelines/{id}/start
// и /stop: задание хаба.
func (s *Server) handlePipelineStart(w http.ResponseWriter, r *http.Request) {
	s.handlePipelinePower(w, r, "start")
}

func (s *Server) handlePipelineStop(w http.ResponseWriter, r *http.Request) {
	s.handlePipelinePower(w, r, "stop")
}

func (s *Server) handlePipelinePower(w http.ResponseWriter, r *http.Request, action string) {
	pl, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	id, err := s.startPipelinePower(r.Context(), pl, action, auth.Username(r.Context()))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// startPipelinePower — задание остановки/запуска (кнопка, API, сценарий).
func (s *Server) startPipelinePower(ctx context.Context, pl store.Pipeline, action, user string) (int64, error) {
	if _, err := s.pipelinePowerPlan(ctx, pl); err != nil {
		return 0, err
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindPipelinePower, TitleKey: "deploy.power." + action + "Title", TitleArgs: []any{pl.Name},
		Queue: fmt.Sprintf("deploy:%d", pl.ID), Author: user, Steps: 1,
		Params: PipelinePowerParams{PipelineID: pl.ID, Action: action},
	})
	if err != nil {
		return 0, err
	}
	s.db.Audit(ctx, user, "pipeline."+action, pl.Name, "ok", map[string]any{"job_id": id})
	return id, nil
}

// PipelinePowerRunner — задание остановки/запуска выкладки.
type PipelinePowerRunner struct{ s *Server }

// NewPipelinePowerRunner — исполнитель.
func NewPipelinePowerRunner(s *Server) *PipelinePowerRunner { return &PipelinePowerRunner{s: s} }

// Run: стек на каждом хосте выкладки по очереди; пометка «остановлена» —
// по мере остановки (остановленное на части хостов тоже помечено),
// снимается удачным запуском или новой выкладкой.
func (r *PipelinePowerRunner) Run(ctx context.Context, jc *jobs.Context) error {
	s := r.s
	var p PipelinePowerParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	if p.Action != "start" && p.Action != "stop" {
		return msgs.Errorf("deploy.powerBadAction", p.Action)
	}
	pl, err := s.db.PipelineByID(ctx, p.PipelineID)
	if err != nil {
		return err
	}
	plan, err := s.pipelinePowerPlan(ctx, pl)
	if err != nil {
		return err
	}
	spec, _ := deploy.ParseSpec(pl.Content)
	c := spec.Compose
	user := s.actingUser(ctx, jc.Job.Author, pl.Author, s.firstAdmin(ctx))
	body := map[string]any{"project": plan.Project, "action": p.Action, "file": path.Base(c.File), "wait_timeout": int(c.Wait().Seconds())}
	if p.Action == "start" {
		// Ключи конвейера — на случай, если образа на хосте нет: хост
		// скачает только недостающее и только с ними.
		var images []string
		for _, img := range c.Images {
			images = append(images, img)
		}
		keys := s.registriesFor(pl, spec, images)
		for _, k := range s.pipelineRegistries(pl) {
			if _, ok := keys[k.Host]; !ok && k.Host != "" {
				if keys == nil {
					keys = map[string]registryKey{}
				}
				keys[k.Host] = k
			}
		}
		if regs := registryList(keys); len(regs) > 0 {
			body["registry_auths"] = regs
		}
	}
	jc.StepKey(1, 1, "deploy.power."+p.Action+"Step", len(plan.Hosts))
	stopped := plan.Stopped
	if p.Action == "stop" {
		if stopped == nil {
			stopped = &pipelineStopped{Project: plan.Project, Containers: map[string][]string{}}
		}
		stopped.By, stopped.At = jc.Job.Author, store.Now()
		if stopped.Containers == nil {
			stopped.Containers = map[string][]string{}
		}
	}
	for _, h := range plan.Hosts {
		jc.Log("deploy.power."+p.Action+"Host", h.Name, plan.Project)
		var started struct {
			JobID      int64    `json:"job_id"`
			Containers []string `json:"containers"`
		}
		if _, err := s.hostCall(ctx, user, h.ID, "POST", "/api/compose/stacks/power", body, &started); err != nil {
			return msgs.Errorf("deploy.powerHostFailed", h.Name, msgs.Localize(jc.Lang(), err))
		}
		if p.Action == "stop" {
			// Пометка — до остановки: цели, упавшие во время неё, уже
			// с пометкой.
			stopped.Containers[strconv.FormatInt(h.ID, 10)] = started.Containers
			if !slices.ContainsFunc(stopped.Hosts, func(x deployedHost) bool { return x.ID == h.ID }) {
				stopped.Hosts = append(stopped.Hosts, h)
			}
			s.saveStopped(ctx, pl.ID, stopped)
		}
		if err := s.waitHostJobVia(ctx, jc, user, h.ID, started.JobID); err != nil {
			return msgs.Errorf("deploy.powerHostFailed", h.Name, msgs.Localize(jc.Lang(), err))
		}
		if p.Action == "start" && stopped != nil {
			stopped.Hosts = slices.DeleteFunc(stopped.Hosts, func(x deployedHost) bool { return x.ID == h.ID })
			delete(stopped.Containers, strconv.FormatInt(h.ID, 10))
			if len(stopped.Hosts) == 0 {
				stopped = nil
			}
			s.saveStopped(ctx, pl.ID, stopped)
		}
	}
	jc.Log("deploy.power."+p.Action+"Done", pl.Name, len(plan.Hosts))
	return nil
}
