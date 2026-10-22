package api

import (
	"context"
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/svcdesc"
)

// Описания сервисов (svcdesc) на хосте: отчёт, уровень «профиль» (его
// присылает хаб), журнал разбора одного описания и образец источника.

func (s *Server) svcdescEngine() *svcdesc.Engine {
	return svcdesc.NewEngine(s.scanner.Collector(), s.cfg.ServicesDir, s.cfg.DataDir)
}

func (s *Server) svcdescSnapshot() *model.Snapshot {
	if snap := s.scanner.Latest(); snap != nil {
		return snap
	}
	return &model.Snapshot{}
}

// handleSvcdescList — GET /svcdesc: все описания хоста и что они дали;
// profile — имена уровня «профиль» (с хаба).
func (s *Server) handleSvcdescList(w http.ResponseWriter, r *http.Request) {
	lang := msgs.LangFromRequest(r)
	rows := s.svcdescEngine().Report(r.Context(), s.svcdescSnapshot(), lang)
	writeJSON(w, http.StatusOK, map[string]any{
		"descriptions": rows, "profile": svcdesc.ProfileNames(s.cfg.DataDir), "host_dir": s.cfg.ServicesDir,
	})
}

// handleSvcdescProfile — PUT /svcdesc/profile {descriptions}: набор
// уровня «профиль» целиком (лишние удаляются), затем пересканирование.
func (s *Server) handleSvcdescProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Descriptions []svcdesc.Named `json:"descriptions"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(req.Descriptions) > 200 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("svcdesc.tooMany", 200))
		return
	}
	ctx := r.Context()
	err := svcdesc.SetProfile(s.cfg.DataDir, req.Descriptions)
	names := make([]string, 0, len(req.Descriptions))
	for _, d := range req.Descriptions {
		names = append(names, d.Name)
	}
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	s.db.Audit(ctx, auth.Username(ctx), "svcdesc.profile", "", outcome, map[string]any{"names": names})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	go func() { _, _ = s.scanner.Scan(context.Background()) }()
	writeJSON(w, http.StatusOK, map[string]any{"names": names})
}

// handleSvcdescTest — POST /svcdesc/test {content}: журнал разбора
// описания на этом хосте (ничего не сохраняет).
func (s *Server) handleSvcdescTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.svcdescEngine().Test(r.Context(), s.svcdescSnapshot(), req.Content, msgs.LangFromRequest(r)))
}

// handleSvcdescSample — POST /svcdesc/sample {source}: дерево, которое
// даёт источник (конструктор путей). Те же запреты, что у описания.
func (s *Server) handleSvcdescSample(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source svcdesc.Source `json:"source"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	lang := msgs.LangFromRequest(r)
	docs, warnings, err := s.svcdescEngine().Sample(r.Context(), req.Source, lang)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if docs == nil {
		docs = []svcdesc.SampleDoc{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": docs, "warnings": warnings})
}
