package api

import (
	"context"
	"encoding/json"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/store"
)

// Задание переживает и вкладку, и сеть, и перезапуск службы (см.
// internal/jobs), поэтому у интерфейса две разные потребности: узнать
// «что вообще происходит» после возвращения — и следить за идущим прямо
// сейчас. Первое закрывают обычные запросы списка и журнала с указанием
// уже полученной строки, второе — веб-сокет, который лишь досылает новое.
// Сокет здесь необязателен: без него всё то же самое собирается опросом.

// jobLogLimit — сколько строк журнала отдаётся за один запрос.
const jobLogLimit = 2000

func (s *Server) handleJobList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.db.ListJobs(r.Context(), limit)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	active, err := s.db.CountActiveJobs(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": list, "active": active})
}

func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleJobLog(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	lines, err := s.db.JobLog(r.Context(), job.ID, after, jobLogLimit)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "lines": lines})
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	user := auth.Username(r.Context())
	if err := s.jobs.Cancel(r.Context(), job.ID); err != nil {
		s.db.Audit(r.Context(), user, "job.cancel", job.Kind, "error", err.Error())
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), user, "job.cancel", job.Kind, "ok", job.Title)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleJobWS досылает наблюдателю новые строки и смену состояния.
//
// Соединение здесь только ускоряет доставку: журнал уже записан в базу, и
// разрыв ничего не теряет — вернувшийся браузер дочитывает недостающее
// обычным запросом с номером последней полученной строки.
func (s *Server) handleJobWS(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ch, unwatch := s.jobs.Watch(job.ID)
	defer unwatch()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	startWSKeepalive(ctx, cancel, conn, wsKeepalivePingInterval, wsKeepalivePingTimeout, wsKeepaliveMaxMissed)

	// Уже завершённому заданию слать нечего: наблюдателя закрываем сразу,
	// а всё, что было, он и так получил журналом.
	if job.Done() {
		_ = writeJobEvent(ctx, conn, map[string]any{"job": job})
		conn.Close(websocket.StatusNormalClosure, msgs.Tc(r.Context(), "api.jobFinished"))
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-ch:
			if !ok {
				fresh, err := s.db.JobByID(context.Background(), job.ID)
				if err == nil {
					_ = writeJobEvent(ctx, conn, map[string]any{"job": fresh})
				}
				conn.Close(websocket.StatusNormalClosure, msgs.Tc(r.Context(), "api.jobFinished"))
				return
			}
			payload := map[string]any{}
			if u.Line != nil {
				payload["line"] = u.Line.Text
			}
			if u.Job != nil {
				payload["job"] = *u.Job
			}
			if err := writeJobEvent(ctx, conn, payload); err != nil {
				return
			}
		}
	}
}

func writeJobEvent(ctx context.Context, conn *websocket.Conn, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, raw)
}

func (s *Server) jobByIDParam(r *http.Request) (store.Job, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return store.Job{}, control.ErrNotFound
	}
	return s.db.JobByID(r.Context(), id)
}
