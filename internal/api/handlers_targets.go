package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Ручные цели доступности: адрес, которого нет в конфигурациях (внешний
// сайт, машина на другом хосте, порт без веб-сервера). Скан их не трогает.

var targetHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.:-]{0,252})$`)

type targetCreateRequest struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Path  string `json:"path"`
}

// manualTarget проверяет запрос и строит цель.
func manualTarget(req targetCreateRequest) (store.Target, bool) {
	req.Host = strings.Trim(strings.TrimSpace(req.Host), "[]")
	req.Label = strings.TrimSpace(req.Label)
	if !targetHostRe.MatchString(req.Host) || len(req.Label) > 120 {
		return store.Target{}, false
	}
	switch req.Kind {
	case "icmp":
		req.Port, req.Path = 0, ""
	case "tcp":
		req.Path = ""
	case "http", "https":
		if req.Path == "" {
			req.Path = "/"
		}
		if !strings.HasPrefix(req.Path, "/") || strings.ContainsAny(req.Path, " \t\r\n") {
			return store.Target{}, false
		}
	default:
		return store.Target{}, false
	}
	if req.Kind != "icmp" && (req.Port < 1 || req.Port > 65535) {
		return store.Target{}, false
	}
	label := req.Label
	if label == "" {
		label = req.Host
		if req.Port > 0 {
			label = fmt.Sprintf("%s:%d", req.Host, req.Port)
		}
	}
	return store.Target{
		Key:   fmt.Sprintf("manual:%s:%s:%d:%s", req.Kind, req.Host, req.Port, req.Path),
		Label: label, Kind: req.Kind, Host: req.Host, Port: req.Port, Path: req.Path,
		Source: "manual", Service: "manual",
	}, true
}

// handleTargetCreate — POST /monitor/targets {label, kind, host, port, path}.
func (s *Server) handleTargetCreate(w http.ResponseWriter, r *http.Request) {
	var req targetCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	t, ok := manualTarget(req)
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "monitor.badManualTarget"))
		return
	}
	id, err := s.db.UpsertTarget(r.Context(), t)
	if err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "monitor.target.add", t.Key, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// handleTargetDelete — DELETE /monitor/targets/{id}: только ручные.
func (s *Server) handleTargetDelete(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "monitor.invalidTargetId"))
		return
	}
	if err := s.db.DeleteManualTarget(r.Context(), id); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "monitor.target.delete", chi.URLParam(r, "id"), "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
