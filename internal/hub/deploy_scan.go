package hub

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// «Заполнить описание» по самому файлу: хаб скачивает compose-файл по
// ссылке (неглубоко, один коммит; закрытый репозиторий — ключами
// сохранённого конвейера) и разбирает его — сервисы, порты образов из
// registry, переменные, файлы рядом.

// handlePipelineScan — POST /hub/pipelines/scan {repo, ref, file,
// pipeline_id}.
func (s *Server) handlePipelineScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Repo       string `json:"repo"`
		Ref        string `json:"ref"`
		File       string `json:"file"`
		PipelineID int64  `json:"pipeline_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := deploy.ValidRepo(req.Repo); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !deploy.ValidRef(req.Ref) || !deploy.ValidPath(req.File) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("deploy.specBad", "file", req.File))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	lang := msgs.FromContext(ctx)

	g := deploy.Git{}
	if req.PipelineID > 0 {
		pl, err := s.db.PipelineByID(ctx, req.PipelineID)
		if err != nil || !s.pipelineInScope(ctx, pl) {
			writeErr(w, r, http.StatusNotFound, store.ErrNotFound)
			return
		}
		g = s.pipelineGit(pl, "")
	}
	work, err := os.MkdirTemp(filepath.Join(s.hub.cfg.DataDir), "scan-")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(work)
	if g.Dir == "" {
		g.Dir = filepath.Join(work, "git")
	}
	fail := func(err error) {
		text := quietSSH(msgs.Localize(lang, err))
		writeJSON(w, http.StatusOK, map[string]any{"error": text, "reason": deploy.ClassifyGitError(text, g.Cred.Token != "", g.Cred.SSHKey != ""),
			"has_access": len(g.Cred.Token)+len(g.Cred.SSHKey) > 0})
	}
	refs, err := g.Remote(ctx, req.Repo)
	if err != nil {
		fail(err)
		return
	}
	sha := refs["refs/heads/"+req.Ref]
	if sha == "" {
		sha = refs["refs/tags/"+req.Ref]
	}
	if sha == "" {
		fail(msgs.Errorf("deploy.refMissing", req.Ref))
		return
	}
	dest := filepath.Join(work, "src")
	if err := g.Checkout(ctx, req.Repo, req.Ref, sha, dest); err != nil {
		fail(err)
		return
	}
	text, err := deploy.ReadFile(dest, req.File)
	if err != nil {
		fail(err)
		return
	}
	// Порты образов — из registry, параллельно (не дольше общего срока).
	var mu sync.Mutex
	cache := map[string][]int{}
	images := map[string]bool{}
	if pre, err := deploy.ScanCompose(text, req.File, nil); err == nil {
		for _, svc := range pre.Services {
			if svc.Image != "" {
				images[svc.Image] = true
			}
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for img := range images {
		wg.Add(1)
		go func(img string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ports, err := imageExposedPorts(ctx, registryHTTP, img, "amd64"); err == nil {
				mu.Lock()
				cache[img] = ports
				mu.Unlock()
			}
		}(img)
	}
	wg.Wait()
	scan, err := deploy.ScanCompose(text, req.File, func(img string) []int { return cache[img] })
	if err != nil {
		fail(msgs.Errorf("compose.configRejected", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scan": scan, "commit": sha, "has_access": len(g.Cred.Token)+len(g.Cred.SSHKey) > 0})
}
