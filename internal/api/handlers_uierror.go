package api

import (
	"net/http"
	"strings"

	"github.com/piqab/nkt/internal/auth"
)

// uiErrorReport — то, что присылает перехватчик ошибок интерфейса.
type uiErrorReport struct {
	Section        string `json:"section"`
	Message        string `json:"message"`
	Stack          string `json:"stack"`
	ComponentStack string `json:"component_stack"`
	URL            string `json:"url"`
}

// uiErrorFieldLimit обрезает каждое поле: отчёт присылает браузер, и
// доверять его размеру нельзя — стек с картой исходников бывает в сотни
// килобайт, а в журнале нужна причина, а не всё дерево.
const uiErrorFieldLimit = 4000

// handleUIError принимает отчёт об ошибке отрисовки и кладёт его в журнал
// хоста.
//
// Смысл ровно один: когда интерфейс падает белым экраном, ответ на вопрос
// «что именно упало» должен оставаться на сервере, а не только на экране
// у того, кто это увидел и уже закрыл вкладку.
func (s *Server) handleUIError(w http.ResponseWriter, r *http.Request) {
	var req uiErrorReport
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	trim := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > uiErrorFieldLimit {
			return s[:uiErrorFieldLimit] + "…"
		}
		return s
	}
	user := auth.Username(r.Context())

	s.log.Warn("ошибка отрисовки интерфейса",
		"user", user,
		"section", trim(req.Section),
		"message", trim(req.Message),
		"url", trim(req.URL),
		"stack", trim(req.Stack),
		"component_stack", trim(req.ComponentStack),
	)
	s.db.Audit(r.Context(), user, "ui.error", trim(req.Section), "error", trim(req.Message))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
