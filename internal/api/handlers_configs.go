package api

import (
	"errors"
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

func (s *Server) handleConfigList(w http.ResponseWriter, r *http.Request) {
	files, err := s.configs.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	// ConfigManager.List walks disk directly (live, for versioning/editing)
	// and knows nothing about server_name/host — that's parsed separately,
	// into Endpoint.Names, by the inventory scanner. Cross-referencing the
	// latest scan here (best-effort: a scan failure just means no site
	// names on this response, not that the file list itself failed) is
	// what lets "Файлы" show which sites each file declares.
	if snap, err := s.scanner.LatestOrScan(r.Context()); err == nil {
		model.AttachSiteNames(files, snap.Endpoints)
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// handleConfigRoots отдаёт корни категорий — «новый файл» начинает путь с
// корня выбранной категории и не выпускает наружу.
func (s *Server) handleConfigRoots(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"roots": s.configs.CategoryRoots()})
}

func (s *Server) handleConfigBrowse(w http.ResponseWriter, r *http.Request) {
	entries, err := s.configs.BrowseDir(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

type mkdirRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleConfigMkdir(w http.ResponseWriter, r *http.Request) {
	var req mkdirRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	if err := s.configs.Mkdir(req.Path); err != nil {
		s.db.Audit(r.Context(), user, "config.mkdir", req.Path, "error", err.Error())
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), user, "config.mkdir", req.Path, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleConfigRead(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	file, err := s.configs.Read(path)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

type configWriteRequest struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Note     string `json:"note"`
	Apply    bool   `json:"apply"`
	Expected string `json:"expected_sha256"`
	// Force снимает запрет на правку sshd_config без резервного канала —
	// осознанное «у меня есть консоль», а не значение по умолчанию.
	Force bool `json:"force"`
}

// HeaderVia и ViaHubTunnel помечают запрос, прошедший через SSH-туннель
// хаба; заголовок ставит прокси хаба (internal/hub/proxy.go), затирая
// одноимённый заголовок браузера — подделать его снаружи нельзя. Константы
// живут здесь, а не в internal/hub: хаб импортирует api, обратное
// невозможно.
const (
	HeaderVia    = "X-NKT-Via"
	ViaHubTunnel = "hub-tunnel"
)

// viaHubTunnel сообщает, пришёл ли запрос по SSH-туннелю хаба.
func viaHubTunnel(r *http.Request) bool {
	return r.Header.Get(HeaderVia) == ViaHubTunnel
}

// handleSSHPreflight отвечает на вопрос, можно ли сейчас безопасно править
// конфигурацию sshd: отвечает ли демон на новое соединение и останется ли
// путь к хосту, если после правки он не поднимется.
func (s *Server) handleSSHPreflight(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"probe":   s.configs.ProbeSSHD(r.Context()),
		"reserve": s.configs.SSHReserveChannel(r.Context(), viaHubTunnel(r)),
	})
}

func (s *Server) handleConfigWrite(w http.ResponseWriter, r *http.Request) {
	var req configWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())

	// Правка sshd_config — единственная, способная отрезать доступ к хосту
	// навсегда, поэтому она требует пути в обход sshd (см.
	// control.SSHReserveChannel). Обойти запрет можно только явным force —
	// у оператора может быть консоль, о которой отсюда никак не узнать.
	if svc, err := s.configs.ServiceForPath(req.Path); err == nil && svc == model.ServiceSSH && !req.Force {
		if reserve := s.configs.SSHReserveChannel(r.Context(), viaHubTunnel(r)); !reserve.OK {
			writeJSON(w, http.StatusPreconditionRequired, map[string]any{
				"error":   msgs.Tc(r.Context(), "api.sshdEditBlocked", reserve.Detail),
				"reserve": reserve,
				"probe":   s.configs.ProbeSSHD(r.Context()),
			})
			return
		}
	}

	// Whether this write creates the file decides how the response ends: a
	// brand-new file is not in the cached snapshot the file list is built
	// from, so answering before a rescan shows the operator a list without
	// the file they just made.
	isNew := !snapshotHasFile(s.scanner.Latest(), req.Path)

	// Optimistic locking: refuse to silently overwrite a file that changed on
	// disk after the editor loaded it.
	if req.Expected != "" {
		current, err := s.configs.Read(req.Path)
		if err != nil {
			fail(w, r, err)
			return
		}
		if current.SHA256 != req.Expected {
			writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "configs.staleContent"))
			return
		}
	}

	res, err := s.configs.Write(r.Context(), msgs.LangFromRequest(r), user, req.Path, req.Content, req.Note, req.Apply)
	if err != nil {
		s.db.Audit(r.Context(), user, "config.write", req.Path, "error", err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "result": res})
		return
	}
	s.db.Audit(r.Context(), user, "config.write", req.Path, "ok", map[string]any{
		"version": res.VersionID, "applied": res.Applied, "note": req.Note,
	})

	if isNew {
		// Synchronous on purpose. Creating a file is rare and the wait is
		// the scan's own duration; editing an existing one — the common
		// case — stays as fast as it was.
		_, _ = s.scanner.Scan(r.Context())
	} else {
		s.rescanLater()
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleConfigBlocks(w http.ResponseWriter, r *http.Request) {
	blocks, err := s.configs.ListBlocks(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks})
}

type blockWriteRequest struct {
	Path string `json:"path"`
	control.BlockWriteRequest
}

func (s *Server) handleConfigBlockWrite(w http.ResponseWriter, r *http.Request) {
	var req blockWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())

	res, err := s.configs.WriteBlock(r.Context(), msgs.LangFromRequest(r), user, req.Path, req.BlockWriteRequest)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, control.ErrStaleContent) {
			status = http.StatusConflict
		}
		s.db.Audit(r.Context(), user, "config.block."+req.Op, req.Path, "error", err.Error())
		writeJSON(w, status, map[string]any{"error": err.Error(), "result": res})
		return
	}
	s.db.Audit(r.Context(), user, "config.block."+req.Op, req.Path, "ok", map[string]any{
		"kind": req.Kind, "version": res.VersionID, "applied": res.Applied,
	})
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleConfigVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.configs.Versions(r.Context(), r.URL.Query().Get("path"), intParam(r, "limit", 100))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (s *Server) handleConfigVersion(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	version, content, err := s.configs.VersionContent(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "content": content})
}

func (s *Server) handleConfigDiff(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	diff, err := s.configs.Diff(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": diff})
}

type rollbackRequest struct {
	Apply bool `json:"apply"`
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	var req rollbackRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	user := auth.Username(r.Context())

	res, err := s.configs.Rollback(r.Context(), msgs.LangFromRequest(r), user, id, req.Apply)
	if err != nil {
		s.db.Audit(r.Context(), user, "config.rollback", res.Path, "error", err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "result": res})
		return
	}
	s.db.Audit(r.Context(), user, "config.rollback", res.Path, "ok",
		map[string]any{"restored_from": id, "new_version": res.VersionID})
	writeJSON(w, http.StatusOK, res)
}

// snapshotHasFile reports whether the last scan already knew this path.
func snapshotHasFile(snap *model.Snapshot, path string) bool {
	if snap == nil {
		return false
	}
	for _, f := range snap.Files {
		if f.Path == path {
			return true
		}
	}
	return false
}
