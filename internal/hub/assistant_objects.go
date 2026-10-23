package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Объекты хаба в чате ассистента: имена в сообщениях становятся ссылками с
// меню действий, а при наборе вопроса — подсказками. Хосты, группы,
// конвейеры, сценарии, кластеры и маршруты Traffic Fly — из базы хаба;
// контейнеры и службы — из того, что модель прочитала инструментами в
// этом вопросе (у хаба их списков нет, хосты знают их сами).

// assistantObject — объект хаба для ссылки или подсказки.
type assistantObject struct {
	Kind string `json:"kind"` // host | group | pipeline | script | cluster | route | container | service
	Name string `json:"name"`
	ID   int64  `json:"id,omitempty"`
	// Ref — строковый id (маршрут Traffic Fly).
	Ref string `json:"ref,omitempty"`
	// HostID / Host — для контейнера и службы: на каком хосте.
	HostID int64  `json:"host_id,omitempty"`
	Host   string `json:"host,omitempty"`
	// Status — состояние хоста или контейнера (для подсказок).
	Status string `json:"status,omitempty"`
}

func (s *Server) assistantObjects(ctx context.Context) []assistantObject {
	out := []assistantObject{}
	if hosts, err := s.db.ListHosts(ctx); err == nil {
		for _, h := range hosts {
			out = append(out, assistantObject{Kind: "host", Name: h.Name, ID: h.ID, Status: string(h.Status)})
		}
	}
	if groups, err := s.hub.HostGroups(ctx); err == nil {
		for _, g := range groups {
			out = append(out, assistantObject{Kind: "group", Name: g})
		}
	}
	if list, err := s.db.ListPipelines(ctx); err == nil {
		for _, p := range list {
			if s.pipelineInScope(ctx, p) {
				out = append(out, assistantObject{Kind: "pipeline", Name: p.Name, ID: p.ID})
			}
		}
	}
	if list, err := s.db.ListScripts(ctx); err == nil {
		for _, sc := range list {
			out = append(out, assistantObject{Kind: "script", Name: sc.Name, ID: sc.ID})
		}
	}
	if list, err := s.db.ListClusters(ctx); err == nil {
		for _, c := range list {
			out = append(out, assistantObject{Kind: "cluster", Name: c.Name, ID: c.ID})
		}
	}
	for _, r := range s.flyDoc(ctx).Routes {
		out = append(out, assistantObject{Kind: "route", Name: r.ID, Ref: r.ID})
	}
	return out
}

// handleAssistantObjects — GET /hub/assistant/objects.
func (s *Server) handleAssistantObjects(w http.ResponseWriter, r *http.Request) {
	if !s.experimental(r.Context()).Assistant {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("ai.assistantDisabled"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": s.assistantObjects(r.Context())})
}

var (
	// Обрезанный результат: начало объекта верхнего уровня — у контейнера
	// «{"id":…,"name":…}», у службы «{"name":…,"unit":…}».
	containerHeadRe = regexp.MustCompile(`\{"id":"[^"]*","name":"([^"\\]{1,200})"`)
	serviceHeadRe   = regexp.MustCompile(`\{"name":"([^"\\]{1,200})","unit":"`)
)

// seenObjects — контейнеры и службы из результата инструмента хоста
// (host_containers, host_services). Целый JSON разбирается как есть;
// обрезанный (предел результата для модели) — по началам объектов.
func seenObjects(tool string, hostID int64, host, result string) []assistantObject {
	var parsed struct {
		Containers []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"containers"`
		Services []struct {
			Name        string `json:"name"`
			ActiveState string `json:"active_state"`
		} `json:"services"`
	}
	var out []assistantObject
	add := func(kind, name, status string) {
		name = strings.TrimPrefix(name, "/")
		if name == "" || len(out) >= 300 {
			return
		}
		for _, o := range out {
			if o.Name == name {
				return
			}
		}
		out = append(out, assistantObject{Kind: kind, Name: name, HostID: hostID, Host: host, Status: status})
	}
	whole := json.Unmarshal([]byte(result), &parsed) == nil
	switch tool {
	case "host_containers":
		if whole {
			for _, c := range parsed.Containers {
				add("container", c.Name, c.State)
			}
		} else {
			for _, m := range containerHeadRe.FindAllStringSubmatch(result, -1) {
				add("container", m[1], "")
			}
		}
	case "host_services":
		if whole {
			for _, sv := range parsed.Services {
				add("service", sv.Name, sv.ActiveState)
			}
		} else {
			for _, m := range serviceHeadRe.FindAllStringSubmatch(result, -1) {
				add("service", m[1], "")
			}
		}
	}
	return out
}

// Избранное ассистента — у каждого пользователя своё, на хабе (виден из
// любого браузера): разделы с сохранёнными вопросами.

type assistantFavorite struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type assistantFavSection struct {
	ID    string              `json:"id"`
	Title string              `json:"title"`
	Items []assistantFavorite `json:"items"`
}

type assistantFavorites struct {
	Sections []assistantFavSection `json:"sections"`
}

const (
	favMaxSections = 50
	favMaxItems    = 500
	favMaxText     = 4000
	favMaxTitle    = 100
)

var favIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

func favoritesKey(user string) string { return store.AssistantFavoritesKey(user) }

func (s *Server) loadFavorites(ctx context.Context, user string) assistantFavorites {
	out := assistantFavorites{Sections: []assistantFavSection{}}
	raw, ok, err := s.db.KVGet(ctx, favoritesKey(user))
	if err != nil || !ok || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out.Sections == nil {
		out.Sections = []assistantFavSection{}
	}
	return out
}

func (f assistantFavorites) validate() error {
	if len(f.Sections) > favMaxSections {
		return msgs.Errorf("ai.favTooMany", favMaxSections)
	}
	items := 0
	ids := map[string]bool{}
	for _, sec := range f.Sections {
		if !favIDRe.MatchString(sec.ID) || ids[sec.ID] {
			return msgs.Errorf("ai.favBadID", sec.ID)
		}
		ids[sec.ID] = true
		if strings.TrimSpace(sec.Title) == "" || utf8.RuneCountInString(sec.Title) > favMaxTitle {
			return msgs.Errorf("ai.favBadTitle", favMaxTitle)
		}
		for _, it := range sec.Items {
			if !favIDRe.MatchString(it.ID) || ids[it.ID] {
				return msgs.Errorf("ai.favBadID", it.ID)
			}
			ids[it.ID] = true
			if strings.TrimSpace(it.Text) == "" || len(it.Text) > favMaxText {
				return msgs.Errorf("ai.favBadText", favMaxText)
			}
			items++
		}
	}
	if items > favMaxItems {
		return msgs.Errorf("ai.favTooMany", favMaxItems)
	}
	return nil
}

// handleAssistantFavorites — GET /hub/assistant/favorites: избранное того,
// кто спрашивает.
func (s *Server) handleAssistantFavorites(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadFavorites(r.Context(), auth.Username(r.Context())))
}

// handleAssistantFavoritesPut — PUT /hub/assistant/favorites {sections}:
// избранное целиком (порядок, разделы, удаление — одной записью).
func (s *Server) handleAssistantFavoritesPut(w http.ResponseWriter, r *http.Request) {
	var req assistantFavorites
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.Sections == nil {
		req.Sections = []assistantFavSection{}
	}
	for i := range req.Sections {
		if req.Sections[i].Items == nil {
			req.Sections[i].Items = []assistantFavorite{}
		}
	}
	if err := req.validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	raw, _ := json.Marshal(req)
	if err := s.db.KVSet(r.Context(), favoritesKey(user), string(raw)); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	n := 0
	for _, sec := range req.Sections {
		n += len(sec.Items)
	}
	s.db.Audit(r.Context(), user, "ai.assistant.favorites", user, "ok", map[string]any{"sections": len(req.Sections), "items": n})
	writeJSON(w, http.StatusOK, req)
}
