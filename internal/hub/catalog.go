package hub

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/catalog"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Каталог плагинов и примеров конвейеров (1.16.0): хаб скачивает его с
// GitHub Pages репозитория (или со своего адреса из настроек), проверяет
// подпись и SHA-256 файлов (internal/catalog) и хранит копию в
// DataDir/catalog — установка плагинов и примеры конвейеров работают из
// неё, в том числе без интернета.

// DefaultCatalogURL — каталог по умолчанию (подписан ключом, встроенным в
// nkt: catalog.TrustedKeys).
const DefaultCatalogURL = "https://piqab.github.io/nkt/catalog/"

// catalogSettingsKV — свой адрес каталога и свои ключи (+ история правок).
const catalogSettingsKV = "catalog.settings"

// catalogRefreshEvery — плановое обновление каталога.
const catalogRefreshEvery = 6 * time.Hour

// KindCatalogRefresh — задание «Обновить каталог».
const KindCatalogRefresh = "hub.catalog.refresh"

type catalogSettings struct {
	URL  string `json:"url,omitempty"`
	Keys string `json:"keys,omitempty"`
	// History — прежние значения (новые сверху, до 20).
	History []catalogSettingsVersion `json:"history,omitempty"`
}

type catalogSettingsVersion struct {
	At   string `json:"at"`
	User string `json:"user,omitempty"`
	URL  string `json:"url,omitempty"`
	Keys string `json:"keys,omitempty"`
}

var catalogState struct {
	sync.Mutex
	lastError   string
	lastAttempt time.Time
}

func (s *Server) catalogDir() string { return filepath.Join(s.hub.cfg.DataDir, "catalog") }

func (s *Server) catalogSettings(ctx context.Context) catalogSettings {
	var cs catalogSettings
	if raw, ok, _ := s.db.KVGet(ctx, catalogSettingsKV); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cs)
	}
	return cs
}

func (cs catalogSettings) url() string {
	if cs.URL != "" {
		return cs.URL
	}
	return DefaultCatalogURL
}

// catalogKeys — встроенные ключи и свои из настроек.
func (s *Server) catalogKeys(ctx context.Context) []ed25519.PublicKey {
	keys := catalog.DefaultKeys()
	if extra := s.catalogSettings(ctx).Keys; extra != "" {
		if more, err := catalog.ParsePublicKeys(extra); err == nil {
			keys = append(keys, more...)
		}
	}
	return keys
}

// StartCatalog читает копию каталога и обновляет её по расписанию.
func (s *Server) StartCatalog(ctx context.Context) {
	if snap, err := catalog.Load(s.catalogDir(), s.catalogKeys(ctx)); err == nil {
		catalog.SetCurrent(snap)
	} else if !os.IsNotExist(err) {
		s.hub.log.Warn("catalog: local copy rejected", "err", err)
	}
	go func() {
		for {
			// Копии нет или она старше шести часов — сразу, иначе — когда
			// ей исполнится шесть.
			var wait time.Duration
			if snap := catalog.Current(); snap != nil {
				if t, err := time.Parse(time.RFC3339, snap.FetchedAt); err == nil && time.Since(t) < catalogRefreshEvery {
					wait = catalogRefreshEvery - time.Since(t)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if err := s.refreshCatalog(ctx); err != nil {
				s.hub.log.Warn("catalog: refresh failed", "err", err)
				// Нет сети — повтор через час, а не через шесть.
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Hour):
				}
			}
		}
	}()
}

// refreshCatalog скачивает каталог; ошибка — прежняя копия остаётся.
func (s *Server) refreshCatalog(ctx context.Context) error {
	cs := s.catalogSettings(ctx)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	snap, err := catalog.Fetch(cctx, &http.Client{Timeout: 2 * time.Minute}, cs.url(), s.catalogKeys(ctx), s.catalogDir())
	catalogState.Lock()
	catalogState.lastAttempt = time.Now()
	if err != nil {
		catalogState.lastError = err.Error()
	} else {
		catalogState.lastError = ""
	}
	catalogState.Unlock()
	if err != nil {
		return err
	}
	catalog.SetCurrent(snap)
	return nil
}

// compatibleNKT — версия хаба не ниже min_nkt (сборка без номера — «dev» —
// совместима со всем).
func (s *Server) compatibleNKT(min string) bool {
	v := strings.TrimPrefix(s.hub.Version(), "v")
	if v == "" || v[0] < '0' || v[0] > '9' {
		return true
	}
	return catalog.NewerOrEqual(v, min)
}

// handleCatalog — GET /hub/catalog: каталог на языке запроса, что из него
// поставлено и где.
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lang := string(msgs.FromContext(ctx))
	type installed struct {
		Name     string `json:"name"`
		Host     string `json:"host"`
		Outdated bool   `json:"outdated,omitempty"`
	}
	type plug struct {
		Key         string      `json:"key"`
		Name        string      `json:"name"`
		Version     string      `json:"version"`
		MinNKT      string      `json:"min_nkt,omitempty"`
		Compatible  bool        `json:"compatible"`
		Title       string      `json:"title"`
		Description string      `json:"description"`
		Docker      bool        `json:"docker,omitempty"`
		Readme      bool        `json:"readme"`
		Screens     []string    `json:"screens"`
		Tests       string      `json:"tests,omitempty"`
		Installed   []installed `json:"installed"`
	}
	type example struct {
		Key   string          `json:"key"`
		Title string          `json:"title"`
		UI    json.RawMessage `json:"ui,omitempty"`
	}
	cs := s.catalogSettings(ctx)
	out := map[string]any{"url": cs.url(), "default_url": DefaultCatalogURL, "custom": cs.URL != "" || cs.Keys != ""}
	catalogState.Lock()
	out["error"] = catalogState.lastError
	if !catalogState.lastAttempt.IsZero() {
		out["last_attempt"] = store.FormatTime(catalogState.lastAttempt)
	}
	catalogState.Unlock()
	plugins, examples := []plug{}, []example{}
	if snap := catalog.Current(); snap != nil {
		out["source"], out["fetched_at"], out["commit"], out["generated"] = snap.Source, snap.FetchedAt, snap.Index.Commit, snap.Index.Generated
		have := s.plugins(ctx)
		for _, p := range snap.Index.Plugins {
			item := plug{Key: p.Key, Name: p.Name, Version: p.Version, MinNKT: p.MinNKT, Compatible: s.compatibleNKT(p.MinNKT),
				Title: catalog.Pick(p.Title, lang), Description: catalog.Pick(p.Description, lang), Docker: p.Docker,
				Readme: catalog.Pick(p.Readme, lang) != "", Screens: p.Screens[lang], Tests: p.Tests, Installed: []installed{}}
			if item.Screens == nil {
				item.Screens = p.Screens["ru"]
			}
			if item.Screens == nil {
				item.Screens = []string{}
			}
			for _, c := range have {
				if c.Native == p.Key {
					item.Installed = append(item.Installed, installed{Name: c.Name, Host: c.Host, Outdated: nativeOutdated(c)})
				}
			}
			plugins = append(plugins, item)
		}
		list := append([]catalog.Example(nil), snap.Index.Examples...)
		sort.SliceStable(list, func(i, j int) bool { return exampleOrder(list[i]) < exampleOrder(list[j]) })
		for _, e := range list {
			if len(e.UI) > 0 {
				examples = append(examples, example{Key: e.Key, Title: catalog.Pick(e.Title, lang), UI: e.UI})
			}
		}
	}
	out["plugins"], out["examples"] = plugins, examples
	writeJSON(w, http.StatusOK, out)
}

func exampleOrder(e catalog.Example) int {
	var o struct {
		Order int `json:"order"`
	}
	if json.Unmarshal(e.UI, &o) != nil || o.Order == 0 {
		return 1 << 20
	}
	return o.Order
}

// handleCatalogFile — GET /hub/catalog/files/{key}/*: справка и скриншоты
// плагина из копии каталога (браузер к GitHub не ходит). Отдаётся только
// то, что перечислено в каталоге, и только README и картинки.
func (s *Server) handleCatalogFile(w http.ResponseWriter, r *http.Request) {
	key, rel := chi.URLParam(r, "key"), chi.URLParam(r, "*")
	ext := strings.ToLower(path.Ext(rel))
	if !catalog.SafePath(rel) || (ext != ".png" && ext != ".md") {
		http.NotFound(w, r)
		return
	}
	b, ok := catalog.Current().File(key, rel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(ext)
	if ext == ".md" {
		ct = "text/markdown; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(b)
}

// handleCatalogRefresh — POST /hub/catalog/refresh: задание «Обновить каталог».
func (s *Server) handleCatalogRefresh(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{Kind: KindCatalogRefresh, TitleKey: "catalog.refreshJob", Queue: "hub-catalog", Author: user, Steps: 1})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "catalog.refresh", "", "ok", map[string]any{"job_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"status": "started", "job_id": id})
}

// CatalogRefreshRunner — исполнитель «Обновить каталог».
type CatalogRefreshRunner struct{ s *Server }

// NewCatalogRefreshRunner — исполнитель обновления каталога.
func NewCatalogRefreshRunner(s *Server) *CatalogRefreshRunner { return &CatalogRefreshRunner{s: s} }

// Resumable — да: скачивание целиком повторяемо.
func (r *CatalogRefreshRunner) Resumable() bool { return true }

// Run скачивает и проверяет каталог.
func (r *CatalogRefreshRunner) Run(ctx context.Context, jc *jobs.Context) error {
	cs := r.s.catalogSettings(ctx)
	jc.StepKey(1, 1, "catalog.stepFetch", cs.url())
	if err := r.s.refreshCatalog(ctx); err != nil {
		return msgs.Errorf("catalog.fetchFailed", err)
	}
	snap := catalog.Current()
	jc.Log("catalog.fetched", len(snap.Index.Plugins), len(snap.Index.Examples), snap.Index.Commit)
	return nil
}

// handleCatalogUpload — POST /hub/catalog/upload (тело — catalog.tar.gz):
// каталог без интернета. Проверка та же, что у скачанного.
func (s *Server) handleCatalogUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := catalog.Import(http.MaxBytesReader(w, r.Body, catalog.MaxTotal+catalog.MaxIndex), s.catalogKeys(ctx), s.catalogDir())
	s.db.Audit(ctx, auth.Username(ctx), "catalog.upload", "", auditOutcome(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("catalog.uploadFailed", err))
		return
	}
	catalog.SetCurrent(snap)
	writeJSON(w, http.StatusOK, map[string]any{"plugins": len(snap.Index.Plugins), "examples": len(snap.Index.Examples), "commit": snap.Index.Commit})
}

// handleCatalogSettings — GET /hub/catalog/settings: свой адрес и ключи с
// историей.
func (s *Server) handleCatalogSettings(w http.ResponseWriter, r *http.Request) {
	cs := s.catalogSettings(r.Context())
	if cs.History == nil {
		cs.History = []catalogSettingsVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": cs.URL, "keys": cs.Keys, "default_url": DefaultCatalogURL, "history": cs.History})
}

// handleCatalogSettingsSave — POST /hub/catalog/settings {url, keys,
// dry_run}: «Сверить» — что изменится, «Сохранить» — с историей. Пусто —
// каталог и ключи по умолчанию.
func (s *Server) handleCatalogSettingsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		URL    string `json:"url"`
		Keys   string `json:"keys"`
		DryRun bool   `json:"dry_run"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req.URL, req.Keys = strings.TrimSpace(req.URL), strings.TrimSpace(req.Keys)
	if req.URL == DefaultCatalogURL {
		req.URL = ""
	}
	if req.URL != "" {
		if err := checkCatalogURL(req.URL); err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	if req.Keys != "" {
		if _, err := catalog.ParsePublicKeys(req.Keys); err != nil {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("catalog.badKeys", err))
			return
		}
	}
	cs := s.catalogSettings(ctx)
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"changed": cs.URL != req.URL || cs.Keys != req.Keys})
		return
	}
	user := auth.Username(ctx)
	if cs.URL != req.URL || cs.Keys != req.Keys {
		cs.History = append([]catalogSettingsVersion{{At: store.FormatTime(time.Now()), User: user, URL: cs.URL, Keys: cs.Keys}}, cs.History...)
		if len(cs.History) > 20 {
			cs.History = cs.History[:20]
		}
	}
	cs.URL, cs.Keys = req.URL, req.Keys
	raw, _ := json.Marshal(cs)
	err := s.db.KVSet(ctx, catalogSettingsKV, string(raw))
	s.db.Audit(ctx, user, "catalog.settings", cs.url(), auditOutcome(err), map[string]any{"custom_keys": cs.Keys != ""})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func checkCatalogURL(u string) error {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") || strings.ContainsAny(u, " \t\r\n@") || len(u) > 500 {
		return msgs.Errorf("catalog.badURL", u)
	}
	return nil
}

// catalogUnavailable — понятная ошибка, когда плагина нет в каталоге:
// каталог ещё не загружен или плагин из него убран.
func catalogUnavailable(key string) error {
	if catalog.Current() == nil {
		return msgs.Errorf("catalog.notLoaded")
	}
	return msgs.Errorf("plugin.unknownPackage", key)
}
