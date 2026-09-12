package api

import (
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/profile"
	"github.com/piqab/nkt/internal/store"
)

// Профиль описывает, каким хост должен быть, а план показывает, чем
// действительность от этого отличается. План строится по запросу и ничего
// не меняет — сначала показать, потом менять, как и везде в nkt.

// maxProfileBytes — потолок описания. Профиль правят в браузере и хранят
// в базе; описание длиннее этого — признак того, что в него положили не
// то.
const maxProfileBytes = 1 << 20

type profileRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Note    string `json:"note"`
}

func (s *Server) handleProfileList(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListProfiles(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list})
}

func (s *Server) handleProfileGet(w http.ResponseWriter, r *http.Request) {
	p, err := s.profileByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleProfileExport отдаёт описание файлом — тем же YAML, что лежит в
// базе. Это вторая половина выбранного хранения: в хабе правят, в git
// хранят.
func (s *Server) handleProfileExport(w http.ResponseWriter, r *http.Request) {
	p, err := s.profileByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeFileName(p.Name)+`.yaml"`)
	_, _ = w.Write([]byte(p.Content))
}

func (s *Server) handleProfileCreate(w http.ResponseWriter, r *http.Request) {
	var req profileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	parsed, err := parseProfileRequest(req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	id, err := s.db.CreateProfile(r.Context(), store.Profile{
		Name: parsed.Name, Content: req.Content, Note: req.Note, Author: user,
	})
	if err != nil {
		s.db.Audit(r.Context(), user, "profile.create", parsed.Name, "error", err.Error())
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "profile.create", parsed.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	existing, err := s.profileByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	var req profileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	parsed, err := parseProfileRequest(req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	err = s.db.UpdateProfile(r.Context(), store.Profile{
		ID: existing.ID, Name: parsed.Name, Content: req.Content, Note: req.Note, Author: user,
	})
	if err != nil {
		s.db.Audit(r.Context(), user, "profile.update", parsed.Name, "error", err.Error())
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), user, "profile.update", parsed.Name, "ok", req.Note)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleProfileDelete(w http.ResponseWriter, r *http.Request) {
	p, err := s.profileByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	user := auth.Username(r.Context())
	if err := s.db.DeleteProfile(r.Context(), p.ID); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "profile.delete", p.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleProfileVersions(w http.ResponseWriter, r *http.Request) {
	p, err := s.profileByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.db.ProfileVersions(r.Context(), p.ID, limit)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list})
}

func (s *Server) handleProfileVersion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "version"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "api.invalidRevisionNumber"))
		return
	}
	v, err := s.db.ProfileVersion(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleProfilePlan строит план: что на хосте разошлось с описанием.
// Ничего не меняет — это и есть смысл шага.
func (s *Server) handleProfilePlan(w http.ResponseWriter, r *http.Request) {
	p, err := s.profileByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	parsed, err := profile.Parse([]byte(p.Content))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	reader := s.profileReader()
	if reader == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.hostStateReadingUnavailable"))
		return
	}
	plan := profile.Build(r.Context(), parsed, reader)
	plan.TS = store.FormatTime(time.Now())
	writeJSON(w, http.StatusOK, plan)
}

// handleProfilePlanPreview строит план по присланному описанию, не
// сохраняя его. Нужен при правке: посмотреть, что даст изменение, до
// того, как оно записано.
func (s *Server) handleProfilePlanPreview(w http.ResponseWriter, r *http.Request) {
	var req profileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	parsed, err := parseProfileRequest(req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	reader := s.profileReader()
	if reader == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.hostStateReadingUnavailable"))
		return
	}
	plan := profile.Build(r.Context(), parsed, reader)
	plan.TS = store.FormatTime(time.Now())
	writeJSON(w, http.StatusOK, plan)
}

// applyRequest — что применять. План приходит от браузера целиком, а не
// перестраивается здесь заново: между показом и нажатием состояние могло
// измениться, а применить надо ровно то, что человек видел и одобрил.
type applyRequest struct {
	// Name — заголовок задания, когда применяют не сохранённый здесь
	// профиль, а присланный извне (хаб применяет свой профиль к группе
	// хостов; на самих хостах его копии нет).
	Name    string           `json:"name"`
	Changes []profile.Change `json:"changes"`
}

// handleProfileApply ставит применение в очередь фоновых заданий и сразу
// отвечает его номером. Дальше браузер не нужен: задание живёт в базе,
// а его журнал доступен и через час, и с другой машины.
func (s *Server) handleProfileApply(w http.ResponseWriter, r *http.Request) {
	// Идентификатор необязателен: без него применяется присланный план,
	// а профиль остаётся там, где его хранят.
	var p store.Profile
	if chi.URLParam(r, "id") != "" {
		var err error
		if p, err = s.profileByIDParam(r); err != nil {
			fail(w, r, err)
			return
		}
	}
	var req applyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(req.Changes) == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "api.planItemsSelected"))
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	name := p.Name
	if name == "" {
		name = strings.TrimSpace(req.Name)
	}
	if name == "" {
		name = msgs.Tc(r.Context(), "api.profileUntitled")
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind:  profile.KindApply,
		Title: msgs.Tc(r.Context(), "api.profileJobTitle", name),
		// Ключ очереди один на весь хост: два применения разом (или
		// применение вместе с установкой пакетов) кончаются беспорядком.
		Queue:  "host",
		Author: user,
		Steps:  len(req.Changes),
		Params: profile.ApplyParams{ProfileID: p.ID, Name: name, Changes: req.Changes},
	})
	if err != nil {
		s.db.Audit(r.Context(), user, "profile.apply", name, "error", err.Error())
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "profile.apply", name, "ok",
		msgs.Tc(r.Context(), "api.itemsJob", len(req.Changes), id))
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// profileReader строит читателя состояния хоста.
func (s *Server) profileReader() profile.Reader {
	if s.scanner == nil {
		return nil
	}
	return profile.NewHostReader(s.scanner.Collector(), s.scanner, s.osusers, s.sysconfig)
}

func (s *Server) profileByIDParam(r *http.Request) (store.Profile, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return store.Profile{}, control.ErrNotFound
	}
	return s.db.ProfileByID(r.Context(), id)
}

// parseProfileRequest проверяет описание до записи: сохранённый профиль,
// который не разбирается, — это ловушка на потом.
func parseProfileRequest(req profileRequest) (profile.Profile, error) {
	if len(req.Content) > maxProfileBytes {
		return profile.Profile{}, errTooBigProfile
	}
	p, err := profile.Parse([]byte(req.Content))
	if err != nil {
		return profile.Profile{}, err
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		p.Name = name
	}
	if strings.TrimSpace(p.Name) == "" {
		return profile.Profile{}, errNoProfileName
	}
	return p, nil
}

// safeFileName оставляет от имени профиля то, что годится для имени
// файла: имя приходит от оператора и уходит в заголовок ответа.
func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "profile"
	}
	return out
}

var (
	errTooBigProfile = errProfile("api.profileTooBig")
	errNoProfileName = errProfile("api.profileNoName")
)

type errProfile string

// errProfile — ключ каталога msgs: текст берётся по нему.
func (e errProfile) Error() string { return msgs.T(msgs.DefaultLang, string(e)) }

// Unwrap отдаёт каталожную ошибку, чтобы writeErr показал её на языке запроса.
func (e errProfile) Unwrap() error { return msgs.Errorf(string(e)) }
