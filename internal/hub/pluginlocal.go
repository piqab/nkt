package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/catalog"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Свой плагин (1.17.1): администратор загружает архив папки плагина —
// «Плагины» → «Каталог» → «Загрузить свой плагин». Проверка — та же
// статическая, что у каталога (catalog.LintWith, Upload); на хабе плагин
// не запускается. Сохраняется только после просмотра кода (жетон
// проверки + «Код просмотрен»), в настройку plugins.local (экспорт), с
// историей версий кода. Ставится на хост тем же заданием, что плагины
// каталога (ключ «local:<имя>»).

const localPluginsKV = "plugins.local"

func (s *Server) loadLocalPlugins(ctx context.Context) {
	m := map[string]catalog.LocalPlugin{}
	if raw, ok, _ := s.db.KVGet(ctx, localPluginsKV); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	catalog.SetLocal(m)
}

// checkLocalUpload — архив → проверенный плагин и замечания хаба: имя не
// из каталога, версия выше уже загруженной.
func (s *Server) checkLocalUpload(ctx context.Context, data []byte) (*catalog.LocalUpload, *catalog.LocalPlugin, error) {
	files, skipped, err := catalog.ReadUpload(data)
	if err != nil {
		return nil, nil, msgs.Errorf("plugin.localBad", err)
	}
	up, err := catalog.CheckUpload(files, skipped)
	if err != nil {
		return nil, nil, msgs.Errorf("plugin.localBad", err)
	}
	name := up.Plugin.Name
	if snap := catalog.Current(); snap != nil {
		for _, p := range snap.Index.Plugins {
			if p.Name == name || p.Key == name {
				up.Problems = append(up.Problems, msgs.Tc(ctx, "plugin.localNameTaken", name))
			}
		}
	}
	var prev *catalog.LocalPlugin
	if lp, ok := catalog.Local()[name]; ok {
		prev = &lp
		if catalog.NewerOrEqual(lp.Plugin.Version, up.Plugin.Version) {
			up.Problems = append(up.Problems, msgs.Tc(ctx, "plugin.localVersionLow", up.Plugin.Version, lp.Plugin.Version))
		}
	}
	return up, prev, nil
}

// handleLocalPluginCheck — POST /hub/plugins/local/check (тело — архив):
// что в архиве, замечания, код и прежняя версия для диффа; без замечаний
// — жетон для сохранения.
func (s *Server) handleLocalPluginCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, catalog.UploadMaxTotal+catalog.UploadMaxTotal/2))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.localBad", err))
		return
	}
	up, prev, err := s.checkLocalUpload(ctx, data)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	lang := string(msgs.FromContext(ctx))
	type setting struct {
		Env      string `json:"env"`
		Title    string `json:"title"`
		Secret   bool   `json:"secret,omitempty"`
		Required bool   `json:"required,omitempty"`
	}
	settings := []setting{}
	for _, f := range up.Plugin.Settings {
		settings = append(settings, setting{Env: f.Env, Title: catalog.Pick(f.Title, lang), Secret: f.Secret, Required: f.Required})
	}
	out := map[string]any{
		"name": up.Plugin.Name, "version": up.Plugin.Version, "title": catalog.Pick(up.Plugin.Title, lang),
		"description": catalog.Pick(up.Plugin.Description, lang), "port": up.Plugin.Port, "docker": up.Plugin.Docker,
		"settings": settings, "code": up.Code, "sha256": up.SHA256, "problems": up.Problems, "skipped": up.Skipped,
	}
	if prev != nil {
		out["prev_version"] = prev.Plugin.Version
		out["prev_code"] = string(prev.Files["deploy/"+prev.Plugin.File])
	}
	if len(up.Problems) == 0 {
		out["ticket"] = oneTime.Put(data)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLocalPluginSave — POST /hub/plugins/local/save {ticket, reviewed}:
// сохранить проверенный архив (проверка — заново, по тем же байтам).
func (s *Server) handleLocalPluginSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Ticket   string `json:"ticket"`
		Reviewed bool   `json:"reviewed"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !req.Reviewed {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.localNotReviewed"))
		return
	}
	data, ok := oneTime.Take(req.Ticket)
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.ticketGone"))
		return
	}
	up, prev, err := s.checkLocalUpload(ctx, data)
	if err != nil || len(up.Problems) > 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.localBad", firstProblem(up, err)))
		return
	}
	user := auth.Username(ctx)
	lp := catalog.LocalPlugin{Plugin: up.Plugin, Files: map[string][]byte{}}
	for rel, b := range up.Files {
		if _, listed := up.Plugin.Files[rel]; listed {
			lp.Files[rel] = b
		}
	}
	lp.Versions = []catalog.LocalVersion{{Version: up.Plugin.Version, SHA256: up.SHA256, At: store.FormatTime(time.Now()), User: user, Code: up.Code, Manifest: up.Manifest}}
	if prev != nil {
		lp.Versions = append(lp.Versions, prev.Versions...)
	}
	if len(lp.Versions) > catalog.LocalHistory {
		lp.Versions = lp.Versions[:catalog.LocalHistory]
	}
	all := catalog.Local()
	all[up.Plugin.Name] = lp
	raw, _ := json.Marshal(all)
	err = s.db.KVSet(ctx, localPluginsKV, string(raw))
	s.db.Audit(ctx, user, "plugin.localSave", up.Plugin.Name, auditOutcome(err), map[string]any{"version": up.Plugin.Version, "sha256": up.SHA256})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	catalog.SetLocal(all)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "key": up.Plugin.Key, "version": up.Plugin.Version})
}

func firstProblem(up *catalog.LocalUpload, err error) any {
	if err != nil {
		return err
	}
	return up.Problems[0]
}

// handleLocalPluginDelete — DELETE /hub/plugins/local/{name}[?force=1]:
// убрать свой плагин из каталога хаба. Где он поставлен — 409 со списком
// (с force — всё равно убрать; установки остаются до удаления с хоста).
func (s *Server) handleLocalPluginDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")
	all := catalog.Local()
	if _, ok := all[name]; !ok {
		writeErr(w, r, http.StatusNotFound, store.ErrNotFound)
		return
	}
	var used []string
	for _, p := range s.plugins(ctx) {
		if p.Native == catalog.LocalPrefix+name {
			used = append(used, p.Name+"@"+p.Host)
		}
	}
	if len(used) > 0 && r.URL.Query().Get("force") != "1" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": msgs.Tc(ctx, "plugin.localInstalled", name), "installed": used})
		return
	}
	delete(all, name)
	raw, _ := json.Marshal(all)
	err := s.db.KVSet(ctx, localPluginsKV, string(raw))
	s.db.Audit(ctx, auth.Username(ctx), "plugin.localDelete", name, auditOutcome(err), map[string]any{"installed": used})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	catalog.SetLocal(all)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
