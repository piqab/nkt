package api

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/vmimage"
)

// Архив образа Docker или Podman по ссылке — заданием хоста: качает сам
// хост, с докачкой после обрыва и перезапуска nkt, и по желанию сразу
// загружает образ в движок. Закрытое окно браузера ничего не прерывает —
// в отличие от загрузки с компьютера, где файл передаёт сам браузер.

// KindArchiveFetch — вид задания «скачать архив образа по ссылке».
const KindArchiveFetch = "image.archive_fetch"

// dockerArchiveExtRe — что умеет docker (podman) load: tar и сжатые tar.
var dockerArchiveExtRe = regexp.MustCompile(`\.(tar|tar\.gz|tgz|tar\.xz|tar\.zst)$`)

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ArchiveFetchParams — вход задания. Секретов в нём нет: ссылка видна в
// списке заданий так же, как в журнале аудита.
type ArchiveFetchParams struct {
	Engine   string `json:"engine"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	Checksum string `json:"checksum,omitempty"`
	// Load — после скачивания docker (podman) load.
	Load bool `json:"load"`
}

// archiveFetchRequest проверяет запрос и строит параметры задания.
// Имя файла — заданное или последняя часть пути ссылки; к нему
// приставляется движок, чтобы архив попал в список своего раздела.
func archiveFetchRequest(engine, rawURL, fileName, checksum string, load bool) (ArchiveFetchParams, error) {
	engine, err := engineOf(engine)
	if err != nil {
		return ArchiveFetchParams{}, err
	}
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ArchiveFetchParams{}, msgs.Errorf("api.linkMustStartHttpHttps")
	}
	name := strings.TrimSpace(fileName)
	if name == "" {
		name = path.Base(u.Path)
	}
	if !dockerArchiveExtRe.MatchString(name) {
		return ArchiveFetchParams{}, msgs.Errorf("api.archiveFetchBadExt", name)
	}
	if !strings.HasPrefix(name, engine+"__") {
		name = engine + "__" + name
	}
	if !archiveNameRe.MatchString(name) || strings.Contains(name, "..") {
		return ArchiveFetchParams{}, msgs.Errorf("api.archiveBadName", name)
	}
	checksum = strings.ToLower(strings.TrimSpace(checksum))
	if checksum != "" && !sha256Re.MatchString(checksum) {
		return ArchiveFetchParams{}, msgs.Errorf("api.archiveBadChecksum")
	}
	return ArchiveFetchParams{Engine: engine, URL: rawURL, Name: name, Checksum: checksum, Load: load}, nil
}

// handleImageArchiveFetch — POST /images/archives/fetch.
func (s *Server) handleImageArchiveFetch(w http.ResponseWriter, r *http.Request) {
	if s.cfg.IsFixtures() {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	var req struct {
		Engine   string `json:"engine"`
		URL      string `json:"url"`
		FileName string `json:"file_name"`
		Checksum string `json:"checksum"`
		Load     bool   `json:"load"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	p, err := archiveFetchRequest(req.Engine, req.URL, req.FileName, req.Checksum, req.Load)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	// Готовый архив с тем же именем не перезаписывается молча: за ним
	// может стоять другой образ.
	if _, err := os.Stat(filepath.Join(s.archiveDir(), p.Name)); err == nil {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("api.archiveExists", p.Name))
		return
	}
	steps := 1
	if p.Load {
		steps = 2
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindArchiveFetch, TitleKey: "archives.fetchJobTitle", TitleArgs: []any{p.Name},
		// Своя очередь: скачивание не держит общую очередь хоста.
		Queue: "image-archive-fetch", Author: user, Steps: steps, Params: p,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "image.archive_fetch", p.Name, "ok", map[string]any{"job_id": id, "url": p.URL, "load": p.Load})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// archiveFetchRunner качает архив и загружает его в движок.
type archiveFetchRunner struct{ s *Server }

// Resumable — да: недокачанный кусок остаётся рядом, после перезапуска
// запрашивается остаток; повторный load того же архива безвреден.
func (a *archiveFetchRunner) Resumable() bool { return true }

func (a *archiveFetchRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ArchiveFetchParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	// Проверка ещё раз: параметры могли прийти из старой записи задания.
	p2, err := archiveFetchRequest(p.Engine, p.URL, p.Name, p.Checksum, p.Load)
	if err != nil {
		return err
	}
	p = p2
	steps := 1
	if p.Load {
		steps = 2
	}

	jc.StepKey(1, steps, "archives.stepDownload", p.Name)
	jc.Log("vmimage.source", p.URL)
	if p.Checksum == "" {
		jc.Log("vmimage.checksumGivenImageTakenAs")
	}
	store := vmimage.NewStore(a.s.archiveDir())
	// Хост без выхода наружу качает через кэш хаба — как образы машин.
	if a.s.vmimages != nil {
		store.Proxy = a.s.vmimages.Proxy
	}
	img := vmimage.CustomImage(p.Name)
	img.URL = p.URL
	img.Checksum = p.Checksum
	img.ChecksumKind = vmimage.SHA256
	file, err := store.Download(ctx, img, func(pr vmimage.Progress) {
		if pr.Total > 0 {
			jc.Log("vmimage.downloaded", vmimage.HumanBytes(jc.Lang(), pr.Done), vmimage.HumanBytes(jc.Lang(), pr.Total), pr.Done*100/pr.Total)
			return
		}
		jc.Log("vmimage.downloaded2", vmimage.HumanBytes(jc.Lang(), pr.Done))
	})
	if err != nil {
		return err
	}
	jc.Log("archives.saved", file)
	if !p.Load {
		return nil
	}

	engineName := map[string]string{"docker": "Docker", "podman": "Podman"}[p.Engine]
	jc.StepKey(2, steps, "archives.stepLoad", engineName)
	argv := []string{p.Engine, "load", "-i", file}
	jc.Logf("$ %s", strings.Join(argv, " "))
	code, err := RunToolingStream(ctx, func(format string, args ...any) { jc.Logf(format, args...) }, argv...)
	if err == nil && code != 0 {
		err = msgs.Errorf("cmdjob.exitCode", code)
	}
	if err != nil {
		return err
	}
	jc.Log("archives.loaded", engineName)
	return nil
}
