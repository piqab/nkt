package hub

import (
	"context"
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Обновление и откат самого хаба — заданием: двоичный файл релиза
// (десятки мегабайт) качался внутри запроса, и на медленной сети браузер
// сообщал об ошибке, пока скачивание шло дальше, а хаб потом сам
// перезапускался. Задание пишет ход в журнал; перезапуск начинается через
// несколько секунд после успеха — когда запись о задании уже сделана.

// KindHubSelfUpdate — вид задания.
const KindHubSelfUpdate = "hub.selfupdate"

// HubSelfUpdateParams — вход задания.
type HubSelfUpdateParams struct {
	Version  string `json:"version"`
	Rollback bool   `json:"rollback,omitempty"`
}

// startSelfUpdate — POST /hub/update и /hub/rollback.
func (s *Server) startSelfUpdate(w http.ResponseWriter, r *http.Request, rollback bool) {
	version, err := s.hub.SelfUpdateTarget(rollback)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	title := "hub.selfUpdateJob"
	if rollback {
		title = "hub.selfRollbackJob"
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindHubSelfUpdate, TitleKey: title, TitleArgs: []any{version},
		Queue: "hub-selfupdate", Author: user, Steps: 4,
		Params: HubSelfUpdateParams{Version: version, Rollback: rollback},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	action := "hub.update"
	if rollback {
		action = "hub.rollback"
	}
	s.db.Audit(r.Context(), user, action, version, "ok", map[string]any{"job_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "job_id": id})
}

// HubSelfUpdateRunner — исполнитель.
type HubSelfUpdateRunner struct{ m *Manager }

// NewHubSelfUpdateRunner — исполнитель обновления хаба.
func NewHubSelfUpdateRunner(m *Manager) *HubSelfUpdateRunner { return &HubSelfUpdateRunner{m: m} }

// Resumable — нет: после перезапуска хаб уже другой версии, повторять
// нечего.
func (r *HubSelfUpdateRunner) Resumable() bool { return false }

// Run качает и проверяет релиз, затем запускает установку с перезапуском.
func (r *HubSelfUpdateRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p HubSelfUpdateParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	return r.m.applyVersion(ctx, p.Version, jc)
}
