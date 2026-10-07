package hub

import (
	"context"
	"net/http"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// «Обновить базу» уязвимостей и ClamAV на «О системе» — заданием хаба (с
// ?job=1): раньше обновление шло горутиной без записи — его не было в
// «Заданиях» и в индикаторе фоновых операций, ход виден был только на
// самой странице. Плановое обновление по расписанию остаётся как было.

// KindDBRefresh — вид задания.
const KindDBRefresh = "hub.dbrefresh"

// DBRefreshParams — какая база: vuln | clam.
type DBRefreshParams struct {
	DB string `json:"db"`
}

// dbRefreshLogEvery — как часто строка хода попадает в журнал задания:
// загрузчики сообщают проценты много раз в секунду.
const dbRefreshLogEvery = 3 * time.Second

func (s *Server) startDBRefreshJob(w http.ResponseWriter, r *http.Request, db string) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	title := "hub.vulnDBRefreshJob"
	if db == "clam" {
		title = "hub.clamDBRefreshJob"
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindDBRefresh, TitleKey: title, Queue: "hub-db-" + db, Author: user, Steps: 1,
		Params: DBRefreshParams{DB: db},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "hub.db_refresh", db, "ok", map[string]any{"job_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"status": "started", "job_id": id})
}

// DBRefreshRunner — исполнитель.
type DBRefreshRunner struct{ m *Manager }

// NewDBRefreshRunner — исполнитель обновления баз хаба.
func NewDBRefreshRunner(m *Manager) *DBRefreshRunner { return &DBRefreshRunner{m: m} }

// Resumable — да: загрузчики сами докачивают и проверяют свежесть.
func (r *DBRefreshRunner) Resumable() bool { return true }

// Run обновляет базу, ход — в журнал. Если она уже обновляется по
// расписанию, ждёт его итога, а не отвечает «готово» сразу.
func (r *DBRefreshRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p DBRefreshParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	var last time.Time
	var lastMsg string
	extra := func(msg string) {
		if msg == lastMsg || time.Since(last) < dbRefreshLogEvery {
			return
		}
		last, lastMsg = time.Now(), msg
		jc.Logf("%s", msg)
	}
	switch p.DB {
	case "vuln":
		jc.StepKey(1, 1, "hub.vulnDBRefreshJob")
		if r.m.VulnDBStatus().Refreshing {
			jc.Log("hub.dbRefreshAlreadyRunning")
			return r.waitDone(ctx, func() (bool, string) { st := r.m.VulnDBStatus(); return st.Refreshing, st.Error })
		}
		if err := r.m.refreshVulnDBWith(ctx, extra); err != nil {
			return err
		}
	case "clam":
		jc.StepKey(1, 1, "hub.clamDBRefreshJob")
		if r.m.ClamDBStatus().Refreshing {
			jc.Log("hub.dbRefreshAlreadyRunning")
			return r.waitDone(ctx, func() (bool, string) { st := r.m.ClamDBStatus(); return st.Refreshing, st.Error })
		}
		if err := r.m.refreshClamDBWith(ctx, extra); err != nil {
			return err
		}
	default:
		return msgs.Errorf("hostop.unknown", p.DB)
	}
	jc.Log("hub.dbRefreshDone")
	return nil
}

// waitDone ждёт конца уже идущего обновления и отдаёт его ошибку.
func (r *DBRefreshRunner) waitDone(ctx context.Context, state func() (bool, string)) error {
	for {
		running, errMsg := state()
		if !running {
			if errMsg != "" {
				return msgs.Errorf("hub.dbRefreshFailed", errMsg)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
