package api

import (
	"net/http"
	"os"
	"os/exec"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// wantsJob — клиент просит фоновое задание (?job=1). Старый хост этот
// флаг не знает и выполняет операцию сразу, как раньше: новый интерфейс
// по ответу без job_id понимает, что всё уже сделано.
func wantsJob(r *http.Request) bool { return r.URL.Query().Get("job") == "1" }

// startCmdJob запускает задание «выполнить команды» и отвечает {job_id}.
func (s *Server) startCmdJob(w http.ResponseWriter, r *http.Request, titleKey string, titleArgs []any, queue string, p cmdjob.Params, auditAction, target string) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: cmdjob.Kind, TitleKey: titleKey, TitleArgs: titleArgs, Queue: queue, Author: user,
		Steps: len(p.Commands), Params: p,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, auditAction, target, "ok", map[string]any{"job_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// hostTool — полный путь программы: вне песочницы команда идёт через
// systemd-run с коротким PATH, где нет /snap/bin.
func hostTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/snap/bin", "/usr/local/bin", "/usr/bin"} {
		if _, err := os.Stat(dir + "/" + name); err == nil {
			return dir + "/" + name
		}
	}
	return name
}
