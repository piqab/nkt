package api

import (
	"net/http"
)

// handleDisks отдаёт картину дисков хоста: файловые системы, устройства и
// подкачку одним ответом — страница показывает их вместе.
func (s *Server) handleDisks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.disks.Overview(r.Context()))
}

// handleDiskUsage — «что занимает место» в одном каталоге. Отдельным
// запросом и только по требованию: du по большому дереву работает
// минутами и заметно нагружает диск.
func (s *Server) handleDiskUsage(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	entries, err := s.disks.DirUsage(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "entries": entries})
}
