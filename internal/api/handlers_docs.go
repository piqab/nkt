package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Адрес сайта документации для кнопки «Справка»: по умолчанию — сайт
// проекта, можно заменить своим (копия сайта в локальной сети). Хранится
// у машины, чей интерфейс открыт (у хаба — у машины хаба), с историей.

// DefaultDocsURL — сайт документации проекта.
const DefaultDocsURL = "https://piqab.github.io/nkt/"

const (
	docsURLKey     = "ui.docs_url"
	docsHistoryKey = "ui.docs_url.history"
)

// docsVersion — прежнее значение адреса.
type docsVersion struct {
	TS     string `json:"ts"`
	Author string `json:"author,omitempty"`
	URL    string `json:"url"`
}

// validDocsURL — http(s), без логина в адресе, оканчивается на «/».
func validDocsURL(raw string) bool {
	if len(raw) > 300 || !strings.HasSuffix(raw, "/") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == ""
}

func (s *Server) docsState(r *http.Request) map[string]any {
	ctx := r.Context()
	cur, ok, _ := s.db.KVGet(ctx, docsURLKey)
	if !ok || cur == "" {
		cur = DefaultDocsURL
	}
	var hist []docsVersion
	if raw, ok, _ := s.db.KVGet(ctx, docsHistoryKey); ok {
		_ = json.Unmarshal([]byte(raw), &hist)
	}
	if hist == nil {
		hist = []docsVersion{}
	}
	return map[string]any{"url": cur, "default": DefaultDocsURL, "custom": cur != DefaultDocsURL, "history": hist}
}

// handleDocsGet — GET /ui/docs: адрес справки.
func (s *Server) handleDocsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.docsState(r))
}

// handleDocsSet — PUT /ui/docs {url}: новый адрес (пусто — сайт проекта);
// прежний — в историю.
func (s *Server) handleDocsSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	next := strings.TrimSpace(req.URL)
	if next == "" {
		next = DefaultDocsURL
	}
	if !validDocsURL(next) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ui.docsURLBad", next))
		return
	}
	user := auth.Username(ctx)
	prev, ok, _ := s.db.KVGet(ctx, docsURLKey)
	if !ok || prev == "" {
		prev = DefaultDocsURL
	}
	if prev != next {
		var hist []docsVersion
		if raw, ok, _ := s.db.KVGet(ctx, docsHistoryKey); ok {
			_ = json.Unmarshal([]byte(raw), &hist)
		}
		hist = append([]docsVersion{{TS: store.Now(), Author: user, URL: prev}}, hist...)
		if len(hist) > 50 {
			hist = hist[:50]
		}
		raw, _ := json.Marshal(hist)
		if err := s.db.KVSet(ctx, docsHistoryKey, string(raw)); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		if err := s.db.KVSet(ctx, docsURLKey, next); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		s.db.Audit(ctx, user, "ui.docs_url", next, "ok", map[string]any{"from": prev})
	}
	writeJSON(w, http.StatusOK, s.docsState(r))
}
