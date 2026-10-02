package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Раскладка бокового меню, общая для всех, кто входит в хаб: порядок
// разделов меню хаба и порядок со скрытыми разделами меню хоста (одна на
// все хосты, открытые через хаб). Правит администратор в «О системе»;
// у каждой раскладки — история версий. Скрытие убирает раздел только из
// меню: по прямому адресу он открывается, права не меняются.

// NavLayout — порядок разделов (ключи меню) и скрытые. Разделов, которых
// нет в порядке (новые в следующих версиях), интерфейс ставит в конец по
// умолчанию; ключи, которых нет в интерфейсе, он пропускает.
type NavLayout struct {
	Order  []string `json:"order"`
	Hidden []string `json:"hidden"`
}

type navVersion struct {
	TS     string `json:"ts"`
	Author string `json:"author,omitempty"`
	NavLayout
}

// NavKinds — чьё меню: хаба или хоста.
var NavKinds = []string{"hub", "host"}

// navUnhideable — разделы, которые скрыть нельзя: без них не вернуться.
var navUnhideable = map[string][]string{"host": {"/"}}

var navKeyRe = regexp.MustCompile(`^[a-z0-9/_-]{1,40}$`)

func navKVKey(kind string) string        { return "ui.nav." + kind }
func navHistoryKVKey(kind string) string { return "ui.nav." + kind + ".history" }

// NavLayoutFor — раскладка (пустая — по умолчанию) и история.
func (m *Manager) NavLayoutFor(ctx context.Context, kind string) (NavLayout, []navVersion) {
	l := NavLayout{Order: []string{}, Hidden: []string{}}
	if raw, ok, _ := m.db.KVGet(ctx, navKVKey(kind)); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &l)
	}
	hist := []navVersion{}
	if raw, ok, _ := m.db.KVGet(ctx, navHistoryKVKey(kind)); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &hist)
	}
	return l, hist
}

// cleanNav — проверка и нормализация: известный вид, допустимые ключи без
// повторов, у меню хаба ничего не скрыто, неприкосновенные не скрыты.
func cleanNav(kind string, l NavLayout) (NavLayout, error) {
	if !slices.Contains(NavKinds, kind) {
		return l, msgs.Errorf("hub.navBadKind", kind)
	}
	if len(l.Order) > 80 || len(l.Hidden) > 80 {
		return l, msgs.Errorf("hub.navTooMany")
	}
	uniq := func(list []string) ([]string, error) {
		out := []string{}
		for _, k := range list {
			if !navKeyRe.MatchString(k) {
				return nil, msgs.Errorf("hub.navBadKey", k)
			}
			if !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
		return out, nil
	}
	var err error
	if l.Order, err = uniq(l.Order); err != nil {
		return l, err
	}
	if l.Hidden, err = uniq(l.Hidden); err != nil {
		return l, err
	}
	if kind == "hub" && len(l.Hidden) > 0 {
		return l, msgs.Errorf("hub.navHubNoHide")
	}
	for _, k := range navUnhideable[kind] {
		if slices.Contains(l.Hidden, k) {
			return l, msgs.Errorf("hub.navUnhideable", k)
		}
	}
	return l, nil
}

// SaveNavLayout — новая раскладка; прежняя — в историю (50 версий).
func (m *Manager) SaveNavLayout(ctx context.Context, kind, user string, l NavLayout) (NavLayout, error) {
	l, err := cleanNav(kind, l)
	if err != nil {
		return l, err
	}
	prev, hist := m.NavLayoutFor(ctx, kind)
	if slices.Equal(prev.Order, l.Order) && slices.Equal(prev.Hidden, l.Hidden) {
		return l, nil
	}
	hist = append([]navVersion{{TS: store.Now(), Author: user, NavLayout: prev}}, hist...)
	if len(hist) > 50 {
		hist = hist[:50]
	}
	rawHist, _ := json.Marshal(hist)
	if err := m.db.KVSet(ctx, navHistoryKVKey(kind), string(rawHist)); err != nil {
		return l, err
	}
	raw, _ := json.Marshal(l)
	return l, m.db.KVSet(ctx, navKVKey(kind), string(raw))
}

// handleNavLayout — GET/PUT /hub/ui/nav/{kind}: раскладка меню хаба или
// хоста с историей.
func (s *Server) handleNavLayout(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if !slices.Contains(NavKinds, kind) {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.navBadKind", kind))
		return
	}
	ctx := r.Context()
	if r.Method == http.MethodPut {
		var l NavLayout
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&l); err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		user := auth.Username(ctx)
		saved, err := s.hub.SaveNavLayout(ctx, kind, user, l)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		s.db.Audit(ctx, user, "ui.nav."+kind, "", "ok", map[string]any{"order": saved.Order, "hidden": saved.Hidden})
	}
	l, hist := s.hub.NavLayoutFor(ctx, kind)
	writeJSON(w, http.StatusOK, map[string]any{"layout": l, "history": hist, "unhideable": navUnhideable[kind]})
}
