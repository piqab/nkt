package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/statediff"
	"github.com/piqab/nkt/internal/store"
)

// «Что изменилось с прошлого захода» — ответ на вопрос, ради которого
// снимки состояния и хранятся.
//
// Снимок пишется при каждом сканировании, отличающемся от предыдущего, а
// сканирование запускает и вход в раздел, и любое действие над хостом.
// Значит история изменений уже собирается сама; здесь она сравнивается.
//
// База сравнения — отметка «докуда я это видел», своя у каждого
// оператора: двое смотрят один сервер по очереди, и «с прошлого захода» у
// них разное. Отметка сдвигается не сама, а кнопкой: иначе открытая на
// втором мониторе вкладка съедала бы изменения, которых никто не читал.

// changesLimit — потолок списка. Сотни строк подряд читать всё равно
// никто не станет, а первый заход после долгого перерыва может дать и
// тысячу.
const changesLimit = 300

func changesSeenKey(user string) string { return "state.changes.seen." + user }

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	latest, err := s.db.LatestSnapshotMeta(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Снимков ещё нет вовсе — сравнивать не с чем, и это не
			// ошибка: первое сканирование только предстоит.
			writeJSON(w, http.StatusOK, map[string]any{"changes": []statediff.Change{}})
			return
		}
		fail(w, r, err)
		return
	}
	_, latestPayload, err := s.db.SnapshotByID(ctx, latest.ID)
	if err != nil {
		fail(w, r, err)
		return
	}

	// Отметка стоит на самом свежем снимке — значит с тех пор ничего не
	// менялось. Отвечать «сравню с предыдущим» здесь нельзя: тогда
	// принятое изменение показывалось бы снова и кнопка «принять» не
	// делала бы ничего.
	if id, ok := s.seenSnapshotID(r); ok && id == latest.ID {
		writeJSON(w, http.StatusOK, map[string]any{
			"changes": []statediff.Change{}, "until": latest,
		})
		return
	}

	baseMeta, basePayload, err := s.baselineSnapshot(r, latest.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Снимок один: сравнивать не с чем, но сказать об этом надо —
			// пустой список иначе читается как «ничего не изменилось».
			writeJSON(w, http.StatusOK, map[string]any{
				"changes": []statediff.Change{}, "until": latest, "first": true,
			})
			return
		}
		fail(w, r, err)
		return
	}

	var prev, cur model.Snapshot
	if err := json.Unmarshal([]byte(basePayload), &prev); err != nil {
		fail(w, r, err)
		return
	}
	if err := json.Unmarshal([]byte(latestPayload), &cur); err != nil {
		fail(w, r, err)
		return
	}

	changes := statediff.Diff(prev, cur)
	truncated := false
	if len(changes) > changesLimit {
		changes, truncated = changes[:changesLimit], true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"since": baseMeta, "until": latest, "changes": changes, "truncated": truncated,
	})
}

// seenSnapshotID — отметка «докуда я это видел» для того, кто спрашивает.
func (s *Server) seenSnapshotID(r *http.Request) (int64, bool) {
	raw, ok, err := s.db.KVGet(r.Context(), changesSeenKey(auth.Username(r.Context())))
	if err != nil || !ok {
		return 0, false
	}
	var id int64
	if _, err := fmt.Sscanf(raw, "%d", &id); err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// baselineSnapshot — снимок, с которым сравнивают: отмеченный оператором
// или, если отметки нет, предыдущий по времени.
func (s *Server) baselineSnapshot(r *http.Request, latestID int64) (store.SnapshotMeta, string, error) {
	if id, ok := s.seenSnapshotID(r); ok && id != latestID {
		meta, payload, err := s.db.SnapshotByID(r.Context(), id)
		if err == nil {
			return meta, payload, nil
		}
		// Отмеченный снимок уже вычищен — не беда, сравним с предыдущим:
		// это меньше того, что человек не видел, но не больше.
	}
	return s.db.SnapshotBefore(r.Context(), latestID)
}

// handleChangesAck сдвигает отметку «видел» на текущий снимок.
func (s *Server) handleChangesAck(w http.ResponseWriter, r *http.Request) {
	latest, err := s.db.LatestSnapshotMeta(r.Context())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		fail(w, r, err)
		return
	}
	user := auth.Username(r.Context())
	if err := s.db.KVSet(r.Context(), changesSeenKey(user), fmt.Sprintf("%d", latest.ID)); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "seen": latest.ID})
}
