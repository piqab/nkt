package api

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
)

// Удаление потенциально долгих объектов «Контейнеров и ВМ» заданием: один
// или несколько объектов разом, по очереди. Пока объект удаляется, повторный
// запрос на него не заводит нового задания, а возвращает идущее; интерфейс
// по GET /deletions блокирует такие строки даже после перезагрузки страницы.

// KindDelete — задание «удалить объекты».
const KindDelete = "delete.objects"

// Виды удаляемых объектов.
const (
	delDocker      = "docker"
	delPodman      = "podman"
	delLXD         = "lxd"
	delLXDSnapshot = "lxd-snapshot"
	delVM          = "vm"
	delImage       = "image"
	delK8s         = "k8s"
)

// DeleteItem — что удалить.
type DeleteItem struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Snapshot — снимок инстанса LXD (kind lxd-snapshot).
	Snapshot string `json:"snapshot,omitempty"`
	// K8sKind, Namespace — объект Kubernetes (kind k8s).
	K8sKind   string `json:"k8s_kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// Force — запущенный контейнер или инстанс гасится; у образа — даже
	// если на него ссылаются остановленные контейнеры.
	Force bool `json:"force,omitempty"`
	// RemoveStorage — у машины libvirt удалить и её диски.
	RemoveStorage bool `json:"remove_storage,omitempty"`
}

// Key — объект, по которому узнаётся повтор.
func (it DeleteItem) Key() string {
	switch it.Kind {
	case delLXDSnapshot:
		return it.Kind + ":" + it.Name + "/" + it.Snapshot
	case delK8s:
		return it.Kind + ":" + it.K8sKind + ":" + it.Namespace + "/" + it.Name
	}
	return it.Kind + ":" + it.Name
}

func (it DeleteItem) valid() bool {
	if strings.TrimSpace(it.Name) == "" {
		return false
	}
	switch it.Kind {
	case delDocker, delPodman, delLXD, delVM, delImage:
		return true
	case delLXDSnapshot:
		return it.Snapshot != ""
	case delK8s:
		return it.K8sKind != ""
	}
	return false
}

// label — как объект называется в журнале.
func (it DeleteItem) label() string {
	switch it.Kind {
	case delLXDSnapshot:
		return it.Name + "/" + it.Snapshot
	case delK8s:
		return it.K8sKind + " " + strings.TrimPrefix(it.Namespace+"/"+it.Name, "/")
	}
	return it.Name
}

// deletions — какие объекты сейчас удаляются и каким заданием.
type deletions struct {
	mu     sync.Mutex
	active map[string]int64
}

func (d *deletions) snapshot() map[string]int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]int64, len(d.active))
	for k, v := range d.active {
		out[k] = v
	}
	return out
}

func (d *deletions) done(key string) {
	d.mu.Lock()
	delete(d.active, key)
	d.mu.Unlock()
}

// DeleteParams — вход задания.
type DeleteParams struct {
	Items []DeleteItem `json:"items"`
}

// handleDeletions — GET /deletions: что сейчас удаляется {key: job_id}.
func (s *Server) handleDeletions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"active": s.dels.snapshot()})
}

// handleDelete — POST /deletions {items}: удалить заданием. Объекты, которые
// уже удаляются, в новое задание не входят; если новых нет — ответ с
// идущим заданием.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var req DeleteParams
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.dels.mu.Lock()
	if s.dels.active == nil {
		s.dels.active = map[string]int64{}
	}
	var fresh []DeleteItem
	var running int64
	seen := map[string]bool{}
	for _, it := range req.Items {
		if !it.valid() {
			s.dels.mu.Unlock()
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "delete.badItem", it.Kind, it.Name))
			return
		}
		key := it.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		if id, ok := s.dels.active[key]; ok {
			running = id
			continue
		}
		fresh = append(fresh, it)
	}
	if len(fresh) == 0 {
		s.dels.mu.Unlock()
		if running == 0 {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "delete.nothing"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job_id": running, "already": true})
		return
	}
	user := auth.Username(r.Context())
	titleArgs := []any{fresh[0].label()}
	titleKey := "delete.jobTitleOne"
	if len(fresh) > 1 {
		titleKey, titleArgs = "delete.jobTitleMany", []any{len(fresh)}
	}
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindDelete, TitleKey: titleKey, TitleArgs: titleArgs, Queue: "delete", Author: user,
		Steps: len(fresh), Params: DeleteParams{Items: fresh},
	})
	if err != nil {
		s.dels.mu.Unlock()
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	for _, it := range fresh {
		s.dels.active[it.Key()] = id
	}
	s.dels.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// deleteRunner выполняет удаление теми же функциями, что и прежние
// мгновенные запросы.
type deleteRunner struct{ s *Server }

func (d *deleteRunner) Run(ctx context.Context, jc *jobs.Context) error {
	s := d.s
	var p DeleteParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	// Что не успели (отмена, ошибка на полпути) — тоже снять с учёта.
	defer func() {
		for _, it := range p.Items {
			s.dels.done(it.Key())
		}
		s.rescanLater()
	}()
	user := jc.Job.Author
	failed := 0
	for i, it := range p.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		jc.StepKey(i+1, len(p.Items), "delete.step", it.label())
		err := s.deleteOne(ctx, user, it)
		s.db.Audit(ctx, user, "delete."+it.Kind, it.label(), auditResult(err), errText(err))
		s.dels.done(it.Key())
		if err != nil {
			failed++
			jc.Log("delete.failed", it.label(), msgs.Localize(jc.Lang(), err))
			continue
		}
		jc.Log("delete.done", it.label())
	}
	if failed > 0 {
		return msgs.Errorf("delete.failedCount", failed, len(p.Items))
	}
	return nil
}

func (s *Server) deleteOne(ctx context.Context, user string, it DeleteItem) error {
	switch it.Kind {
	case delDocker:
		return s.services.DeleteContainer(ctx, user, it.Name, it.Force)
	case delPodman:
		return s.podman.DeleteContainer(ctx, user, it.Name, it.Force)
	case delLXD:
		err := s.lxd.DeleteInstance(ctx, user, it.Name, it.Force)
		if err == nil && s.guestCreds != nil {
			_ = s.guestCreds.Delete(ctx, "lxd", it.Name)
		}
		return err
	case delLXDSnapshot:
		return s.lxd.SnapshotAction(ctx, user, it.Name, it.Snapshot, "delete", false)
	case delVM:
		err := s.libvirt.UndefineVM(ctx, user, it.Name, it.RemoveStorage, it.Force)
		if err == nil && s.guestCreds != nil {
			_ = s.guestCreds.Delete(ctx, "vm", it.Name)
		}
		return err
	case delImage:
		return s.images.Remove(ctx, it.Name, it.Force)
	case delK8s:
		_, err := s.k8sManager().Act(ctx, k8s.ActionRequest{Kind: it.K8sKind, Namespace: it.Namespace, Name: it.Name, Action: "delete"})
		return err
	}
	return msgs.Errorf("delete.badItem", it.Kind, it.Name)
}
