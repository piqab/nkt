package control

import (
	"context"
	"errors"
	"fmt"
	gopath "path"
	"strings"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/store"
)

// Errors returned by the config manager.
var (
	ErrPathNotAllowed = errors.New("файл вне разрешённых каталогов")
	ErrNotFound       = errors.New("файл не найден")
	ErrTooLarge       = errors.New("файл слишком большой для редактора")
)

// maxEditableBytes caps what the editor will load. Config files are small; a
// multi-megabyte file is a sign the path is wrong.
const maxEditableBytes = 2 << 20

// ConfigManager reads and edits managed configuration files.
type ConfigManager struct {
	cfg     *config.Config
	c       collect.Collector
	db      *store.DB
	scanner *inventory.Scanner
	hist    *history
	svc     *ServiceManager
}

// NewConfigManager builds the config editor.
func NewConfigManager(cfg *config.Config, c collect.Collector, db *store.DB,
	scanner *inventory.Scanner, svc *ServiceManager) *ConfigManager {
	return &ConfigManager{
		cfg: cfg, c: c, db: db, scanner: scanner,
		hist: newHistory(cfg.HistoryDir()),
		svc:  svc,
	}
}

// FileContent is a config file plus its bytes.
type FileContent struct {
	model.ManagedFile
	Content string `json:"content"`
}

// WriteResult describes what happened to an edit.
type WriteResult struct {
	Path       string                 `json:"path"`
	VersionID  int64                  `json:"version_id"`
	Validated  bool                   `json:"validated"`
	Validation *collect.CommandResult `json:"validation,omitempty"`
	RolledBack bool                   `json:"rolled_back"`
	Message    string                 `json:"message"`
	Applied    bool                   `json:"applied"`
	Apply      *collect.CommandResult `json:"apply,omitempty"`
}

// serviceForPath decides which service owns a path, and therefore which
// validator and reload command apply to it.
func (m *ConfigManager) serviceForPath(path string) string {
	switch {
	case underRoot(path, m.cfg.NginxRoot):
		return model.ServiceNginx
	case underRoot(path, m.cfg.HAProxyRoot):
		return model.ServiceHAProxy
	}
	for _, p := range m.cfg.ComposeFiles {
		if p == path {
			return model.ServiceDocker
		}
	}
	if snap := m.scanner.Latest(); snap != nil {
		for _, f := range snap.Files {
			if f.Path == path {
				return f.Service
			}
		}
	}
	return ""
}

func underRoot(path, root string) bool {
	root = strings.TrimSuffix(root, "/")
	return path == root || strings.HasPrefix(path, root+"/")
}

// checkPath enforces the allowlist. Editing is confined to the directories the
// application is configured to manage, so a crafted path cannot reach /etc/shadow.
func (m *ConfigManager) checkPath(path string) (service string, err error) {
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return "", ErrPathNotAllowed
	}
	if gopath.Clean(path) != path {
		return "", ErrPathNotAllowed
	}
	service = m.serviceForPath(path)
	if service == "" {
		return "", ErrPathNotAllowed
	}
	return service, nil
}

// List returns every config file the dashboard knows about.
func (m *ConfigManager) List(ctx context.Context) ([]model.ManagedFile, error) {
	snap, err := m.scanner.LatestOrScan(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.ManagedFile, 0, len(snap.Files))
	for _, f := range snap.Files {
		if svc, err := m.checkPath(f.Path); err == nil {
			f.Service = svc
			f.Editable = f.Readable
		} else {
			f.Editable = false
		}
		out = append(out, f)
	}
	return out, nil
}

// Read loads one config file.
func (m *ConfigManager) Read(path string) (FileContent, error) {
	service, err := m.checkPath(path)
	if err != nil {
		return FileContent{}, err
	}
	st, err := m.c.Stat(path)
	if err != nil {
		return FileContent{}, ErrNotFound
	}
	if st.Size > maxEditableBytes {
		return FileContent{}, ErrTooLarge
	}
	raw, err := m.c.ReadFile(path)
	if err != nil {
		return FileContent{}, err
	}
	return FileContent{
		ManagedFile: model.ManagedFile{
			Path: path, Service: service, Size: st.Size, ModTime: st.ModTime,
			SHA256: sha256Hex(raw), Editable: true, Readable: true,
		},
		Content: string(raw),
	}, nil
}

// Write replaces a config file. The previous content is captured first, the new
// content is validated by the owning service, and a failed validation restores
// the file before returning — the host is never left with a config it rejects.
func (m *ConfigManager) Write(ctx context.Context, user, path, content, note string, apply bool) (WriteResult, error) {
	service, err := m.checkPath(path)
	if err != nil {
		return WriteResult{}, err
	}
	if len(content) > maxEditableBytes {
		return WriteResult{}, ErrTooLarge
	}

	previous, hadPrevious := m.snapshotCurrent(ctx, path, service, user)
	newBytes := []byte(normaliseNewlines(content))

	if err := m.c.WriteFile(path, newBytes, 0o644); err != nil {
		return WriteResult{}, fmt.Errorf("запись %s: %w", path, err)
	}

	res := WriteResult{Path: path}
	validation, validated := m.svc.Validate(ctx, service, path)
	res.Validated = validated
	if validated {
		res.Validation = &validation
		if !validation.OK() {
			// Put the file back exactly as it was before returning the error.
			if hadPrevious {
				if rbErr := m.c.WriteFile(path, previous, 0o644); rbErr != nil {
					return res, fmt.Errorf("конфиг не прошёл проверку, и откат не удался: %v (проверка: %s)",
						rbErr, strings.TrimSpace(validation.Output()))
				}
				res.RolledBack = true
			}
			res.Message = "Конфигурация не прошла проверку, изменения отменены."
			return res, fmt.Errorf("%s отклонил конфигурацию: %s",
				service, strings.TrimSpace(validation.Output()))
		}
	}

	versionID, err := m.recordVersion(ctx, path, service, user, store.ActionEdit, note, newBytes)
	if err != nil {
		return res, err
	}
	res.VersionID = versionID
	res.Message = "Файл сохранён."

	if apply {
		out, err := m.svc.Action(ctx, user, service, "reload")
		if err == nil {
			res.Applied = out.OK()
			res.Apply = &out
			if out.OK() {
				res.Message = "Файл сохранён и конфигурация перезагружена."
			}
		}
	}

	// The host changed, so the cached inventory is stale.
	go func() { _, _ = m.scanner.Scan(context.Background()) }()
	return res, nil
}

// snapshotCurrent records the pre-edit content so a rollback target always exists.
func (m *ConfigManager) snapshotCurrent(ctx context.Context, path, service, user string) ([]byte, bool) {
	raw, err := m.c.ReadFile(path)
	if err != nil {
		return nil, false
	}
	if _, err := m.db.LatestVersion(ctx, path); errors.Is(err, store.ErrNotFound) {
		// First time we touch this file: keep the original as the baseline.
		_, _ = m.recordVersion(ctx, path, service, user, store.ActionObserved,
			"состояние до первой правки", raw)
	}
	return raw, true
}

func (m *ConfigManager) recordVersion(ctx context.Context, path, service, user, action, note string,
	content []byte) (int64, error) {

	name, sum, err := m.hist.put(path, content)
	if err != nil {
		return 0, err
	}
	return m.db.AddVersion(ctx, store.ConfigVersion{
		Path: path, Service: service, Author: user, Action: action, Note: note,
		Size: int64(len(content)), SHA256: sum, BlobName: name,
	})
}

// Versions lists the revision history of a file.
func (m *ConfigManager) Versions(ctx context.Context, path string, limit int) ([]store.ConfigVersion, error) {
	if path != "" {
		if _, err := m.checkPath(path); err != nil {
			return nil, err
		}
	}
	return m.db.ListVersions(ctx, path, limit)
}

// VersionContent returns the bytes of one stored revision.
func (m *ConfigManager) VersionContent(ctx context.Context, id int64) (store.ConfigVersion, string, error) {
	v, err := m.db.VersionByID(ctx, id)
	if err != nil {
		return v, "", err
	}
	raw, err := m.hist.get(v.BlobName)
	if err != nil {
		return v, "", fmt.Errorf("версия %d недоступна: %w", id, err)
	}
	return v, string(raw), nil
}

// Rollback restores a previous revision, going through the same validation path
// as a normal edit.
func (m *ConfigManager) Rollback(ctx context.Context, user string, id int64, apply bool) (WriteResult, error) {
	v, content, err := m.VersionContent(ctx, id)
	if err != nil {
		return WriteResult{}, err
	}
	note := fmt.Sprintf("откат к версии #%d от %s", v.ID, v.TS)
	res, err := m.Write(ctx, user, v.Path, content, note, apply)
	if err != nil {
		return res, err
	}
	// Mark the freshly written version as a rollback rather than a plain edit.
	if res.VersionID > 0 {
		_, _ = m.db.ExecContext(ctx, `UPDATE config_versions SET action = ? WHERE id = ?`,
			store.ActionRollback, res.VersionID)
	}
	res.Message = fmt.Sprintf("Восстановлена версия #%d.", v.ID)
	return res, nil
}

// Diff renders a unified diff between a stored revision and the current file.
func (m *ConfigManager) Diff(ctx context.Context, id int64) (string, error) {
	v, old, err := m.VersionContent(ctx, id)
	if err != nil {
		return "", err
	}
	current, err := m.Read(v.Path)
	if err != nil {
		return "", err
	}
	return UnifiedDiff(fmt.Sprintf("версия #%d (%s)", v.ID, v.TS), "текущий файл",
		old, current.Content), nil
}

func normaliseNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}
