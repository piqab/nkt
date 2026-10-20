package hub

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Экспериментальные функции («О системе»): что показывать в интерфейсе.
// Галочки только прячут — сами функции работают: созданные кластеры
// живут, задания идут, API отвечает. Настройка общая для хаба, с историей,
// уходит в экспорт (store.ExportedSettingKeys).

const (
	experimentalKey        = "ui.experimental"
	experimentalHistoryKey = "ui.experimental.history"
)

// Experimental — что показывать.
type Experimental struct {
	// Map — переключатель «Карта» над списком хостов (и перенос с карты).
	Map bool `json:"map"`
	// Clusters — карточка «Кластеры» в списке хостов, «Новый кластер» в
	// строке хоста, пункт меню «Кластеры».
	Clusters bool `json:"clusters"`
}

type experimentalVersion struct {
	TS     string       `json:"ts"`
	Author string       `json:"author,omitempty"`
	Value  Experimental `json:"value"`
}

// experimental — настройка; не задана — всё скрыто, кроме кластеров у
// хаба, где они уже заведены (иначе после обновления пропали бы с экрана).
func (s *Server) experimental(ctx context.Context) Experimental {
	var e Experimental
	if raw, ok, err := s.db.KVGet(ctx, experimentalKey); err == nil && ok && raw != "" {
		if json.Unmarshal([]byte(raw), &e) == nil {
			return e
		}
	}
	if list, err := s.db.ListClusters(ctx); err == nil && len(list) > 0 {
		e.Clusters = true
	}
	return e
}

func (s *Server) experimentalState(ctx context.Context) map[string]any {
	var hist []experimentalVersion
	if raw, ok, _ := s.db.KVGet(ctx, experimentalHistoryKey); ok {
		_ = json.Unmarshal([]byte(raw), &hist)
	}
	if hist == nil {
		hist = []experimentalVersion{}
	}
	return map[string]any{"value": s.experimental(ctx), "history": hist}
}

// handleExperimentalGet — GET /hub/experimental.
func (s *Server) handleExperimentalGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.experimentalState(r.Context()))
}

// handleExperimentalSet — PUT /hub/experimental {map, clusters}
// (администратор); прежнее значение — в историю.
func (s *Server) handleExperimentalSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if u, ok := auth.UserFromContext(ctx); !ok || !u.IsAdmin() {
		writeError(w, http.StatusForbidden, msgs.Tc(ctx, "auth.adminOnly"))
		return
	}
	var next Experimental
	if err := decodeJSON(r, &next); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	prev := s.experimental(ctx)
	if prev != next {
		user := auth.Username(ctx)
		var hist []experimentalVersion
		if raw, ok, _ := s.db.KVGet(ctx, experimentalHistoryKey); ok {
			_ = json.Unmarshal([]byte(raw), &hist)
		}
		hist = append([]experimentalVersion{{TS: store.Now(), Author: user, Value: prev}}, hist...)
		if len(hist) > 50 {
			hist = hist[:50]
		}
		rawH, _ := json.Marshal(hist)
		rawV, _ := json.Marshal(next)
		if err := s.db.KVSet(ctx, experimentalHistoryKey, string(rawH)); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		if err := s.db.KVSet(ctx, experimentalKey, string(rawV)); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		s.db.Audit(ctx, user, "ui.experimental", string(rawV), "ok", map[string]any{"from": prev})
	}
	writeJSON(w, http.StatusOK, s.experimentalState(ctx))
}
