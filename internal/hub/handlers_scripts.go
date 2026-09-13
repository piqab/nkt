package hub

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// Сценарии хаба: хранение, история, разбор и проверка. Выполнение — в
// scriptrun.go.

const maxScriptBytes = 256 << 10

var scriptColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type scriptRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Note    string `json:"note"`
	Color   string `json:"color"`
}

func (s *Server) scriptByIDParam(r *http.Request) (store.Script, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return store.Script{}, store.ErrNotFound
	}
	return s.db.ScriptByID(r.Context(), id)
}

func cleanScriptRequest(req scriptRequest) (scriptRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return req, msgs.Errorf("script.nameRequired")
	}
	if len(req.Content) > maxScriptBytes {
		return req, msgs.Errorf("script.tooBig", maxScriptBytes>>10)
	}
	req.Color = strings.ToLower(strings.TrimSpace(req.Color))
	if req.Color != "" && !scriptColorRe.MatchString(req.Color) {
		return req, msgs.Errorf("api.profileBadColor", req.Color)
	}
	return req, nil
}

func (s *Server) handleScriptList(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListScripts(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scripts": list})
}

func (s *Server) handleScriptGet(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scriptByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) handleScriptExport(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scriptByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.Map(func(c rune) rune {
		if c == '/' || c == '\\' || c == '"' {
			return '_'
		}
		return c
	}, sc.Name)+`.nkt"`)
	_, _ = w.Write([]byte(sc.Content))
}

func (s *Server) handleScriptCreate(w http.ResponseWriter, r *http.Request) {
	var req scriptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req, err := cleanScriptRequest(req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	id, err := s.db.CreateScript(r.Context(), store.Script{Name: req.Name, Color: req.Color, Content: req.Content, Note: req.Note, Author: user})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "script.create", req.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleScriptUpdate(w http.ResponseWriter, r *http.Request) {
	existing, err := s.scriptByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	var req scriptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req, err = cleanScriptRequest(req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	if err := s.db.UpdateScript(r.Context(), store.Script{ID: existing.ID, Name: req.Name, Color: req.Color, Content: req.Content, Note: req.Note, Author: user}); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), user, "script.update", req.Name, "ok", req.Note)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleScriptDelete(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scriptByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.db.DeleteScript(r.Context(), sc.ID); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "script.delete", sc.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleScriptVersions(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scriptByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	list, err := s.db.ScriptVersions(r.Context(), sc.ID, 50)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list})
}

func (s *Server) handleScriptVersion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "version"), 10, 64)
	if err != nil {
		fail(w, r, store.ErrNotFound)
		return
	}
	v, err := s.db.ScriptVersion(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// scriptIssueJSON — ошибка разбора на языке запроса.
type scriptIssueJSON struct {
	Line  int    `json:"line"`
	Error string `json:"error"`
}

func issuesJSON(lang msgs.Lang, issues []script.Issue) []scriptIssueJSON {
	out := make([]scriptIssueJSON, 0, len(issues))
	for _, i := range issues {
		out = append(out, scriptIssueJSON{Line: i.Line, Error: msgs.Localize(lang, i.Err)})
	}
	return out
}

// refsNow — что известно хабу для проверки ссылок сценария.
func (s *Server) refsNow(r *http.Request) script.Refs {
	refs := script.Refs{Hosts: map[string]bool{}, Groups: map[string]bool{}, Profiles: map[string]bool{}}
	if hosts, err := s.db.ListHosts(r.Context()); err == nil {
		for _, h := range hosts {
			refs.Hosts[h.Name] = true
		}
	}
	if groups, err := s.hub.HostGroups(r.Context()); err == nil {
		for _, g := range groups {
			refs.Groups[g] = true
		}
	}
	if profiles, err := s.db.ListProfiles(r.Context()); err == nil {
		for _, p := range profiles {
			refs.Profiles[p.Name] = true
		}
	}
	return refs
}

// handleScriptCheck разбирает текст и сверяет ссылки — ничего не
// выполняя. Отдаёт план (шаги), ошибки и список хостов, чей пароль
// спросят при запуске.
func (s *Server) handleScriptCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	lang := msgs.FromContext(r.Context())
	sc, issues := script.Parse(req.Content)
	if len(issues) == 0 {
		issues = script.Check(sc, s.refsNow(r))
	}
	steps := sc.Steps
	if steps == nil {
		steps = []script.Step{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"steps":  steps,
		"issues": issuesJSON(lang, issues),
		"asks":   orEmpty(sc.Asks),
		"hosts":  orEmpty(sc.Hosts),
		"groups": orEmpty(sc.Groups),
	})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// handleScriptHelp — справка по командам на языке запроса: та же
// таблица, из которой работает разбор.
func (s *Server) handleScriptHelp(w http.ResponseWriter, r *http.Request) {
	lang := msgs.FromContext(r.Context())
	type argJSON struct {
		Name     string `json:"name"`
		Required bool   `json:"required"`
		Desc     string `json:"desc"`
	}
	type cmdJSON struct {
		Kind    string    `json:"kind"`
		Syntax  string    `json:"syntax"`
		Summary string    `json:"summary"`
		Args    []argJSON `json:"args"`
		Example string    `json:"example"`
		Block   bool      `json:"block,omitempty"`
		OnHost  bool      `json:"on_host,omitempty"`
	}
	out := make([]cmdJSON, 0, len(script.Commands))
	for _, c := range script.Commands {
		cj := cmdJSON{Kind: string(c.Kind), Syntax: c.Syntax, Summary: msgs.T(lang, c.Summary), Example: c.Example, Block: c.Block, OnHost: c.OnHost, Args: []argJSON{}}
		for _, a := range c.Args {
			cj.Args = append(cj.Args, argJSON{Name: a.Name, Required: a.Required, Desc: msgs.T(lang, a.Desc)})
		}
		out = append(out, cj)
	}
	writeJSON(w, http.StatusOK, map[string]any{"commands": out, "intro": msgs.T(lang, "script.doc.intro")})
}
