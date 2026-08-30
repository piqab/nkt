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
	"github.com/althq/netknownsthat/internal/msgs"
	"github.com/althq/netknownsthat/internal/parse"
	"github.com/althq/netknownsthat/internal/store"
)

// Errors returned by the config manager.
var (
	ErrPathNotAllowed = errors.New("файл вне разрешённых каталогов")
	ErrNotFound       = errors.New("файл не найден")
	ErrTooLarge       = errors.New("файл слишком большой для редактора")
	// ErrStaleContent is returned when a BlockWriteRequest's expected_sha256
	// no longer matches the file — the same optimistic-lock contract Write's
	// callers already implement at the handler layer, enforced once here so
	// every block-write caller gets it for free.
	ErrStaleContent = errors.New("файл изменился с момента загрузки страницы, обновите список блоков и повторите")
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
	case underRoot(path, m.cfg.CaddyRoot):
		return model.ServiceCaddy
	case underRoot(path, m.cfg.Fail2banRoot):
		return model.ServiceFail2ban
	case underRoot(path, parse.LibvirtQEMUDir):
		// libvirt itself stores a defined domain's persistent XML here — this
		// is not a config file some other service merely reads, it IS
		// libvirt's own storage for the definition, which is exactly why
		// writing here can double as "create/edit a VM" (see Write's apply
		// step) without a bespoke VM-creation code path.
		return model.ServiceLibvirt
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
		// A running container's own compose label is as trustworthy as the
		// configured NKT_COMPOSE_FILES list — docker reported it, not the
		// request — and it is often the ONLY way to reach a stack that
		// wasn't in that list to begin with (a second project, a path that
		// doesn't match the default guesses). Editing a container's config
		// via "редактировать конфиг" depends on this: that link is built
		// from exactly this same ComposeFile field.
		for _, c := range snap.Container {
			if c.ComposeFile == path {
				return model.ServiceDocker
			}
		}
	}
	// A compose-named file under /home is trusted even before it exists on
	// disk — a brand-new stack a user is about to create has, by definition,
	// no prior scan or running container to vouch for it. The filename
	// pattern is the only signal available, same as docker compose itself
	// uses to recognise its own config with no -f flag given.
	if underRoot(path, "/home") && isComposeFileName(path) {
		return model.ServiceDocker
	}
	return ""
}

// composeFileNames are the filenames `docker compose` itself recognises
// without an explicit -f flag.
var composeFileNames = map[string]bool{
	"docker-compose.yml": true, "docker-compose.yaml": true,
	"compose.yml": true, "compose.yaml": true,
}

func isComposeFileName(path string) bool {
	return composeFileNames[gopath.Base(path)]
}

// underRoot reports whether path is root itself or somewhere beneath it. An
// empty root never matches anything — without this guard, root="" would
// trim to "" and root+"/" would be "/", which every absolute path starts
// with, silently claiming the entire filesystem for whichever service's
// root config field happened to be left unset (e.g. a test config that
// predates a newer service and never got the new field added).
func underRoot(path, root string) bool {
	if root == "" {
		return false
	}
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

// BrowseDir lists a directory under /home, for the "новый контейнер" path
// picker — a directory being browsed is not itself a file this app manages
// (checkPath would reject most of them), only the compose file the operator
// eventually picks or creates inside it goes through the normal write path.
// /home is a deliberately narrow root: shared hosts conventionally give each
// operator their own home directory to keep their compose stacks in, and
// nothing else on the host needs browsing this way.
func (m *ConfigManager) BrowseDir(path string) ([]collect.FileInfo, error) {
	if path == "" {
		path = "/home"
	}
	if err := checkHomePath(path); err != nil {
		return nil, err
	}
	return m.c.ListDir(path)
}

// Mkdir creates a new directory under /home — laying out a fresh compose
// stack's folder before any file exists in it, same sandboxing as BrowseDir.
func (m *ConfigManager) Mkdir(path string) error {
	if err := checkHomePath(path); err != nil {
		return err
	}
	return m.c.Mkdir(path)
}

func checkHomePath(path string) error {
	if path == "" || !underRoot(path, "/home") || strings.Contains(path, "..") || gopath.Clean(path) != path {
		return ErrPathNotAllowed
	}
	return nil
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

// ListBlocks builds the structural block tree of one config file — the
// nginx server{}/location{}/upstream{} or haproxy frontend/backend/listen/
// global/defaults sections it contains — for the per-file "blocks" view.
func (m *ConfigManager) ListBlocks(path string) ([]parse.Block, error) {
	service, err := m.checkPath(path)
	if err != nil {
		return nil, err
	}
	return parse.Blocks(m.c, path, service)
}

// BlockWriteRequest describes one create/update/delete of a single block
// within a config file, applied as a line-range splice against the file's
// current raw text rather than a reparse-and-rebuild of the whole file.
type BlockWriteRequest struct {
	Op    string          `json:"op"` // "create" | "update" | "delete"
	Kind  parse.BlockKind `json:"kind"`
	Start int             `json:"start_line"` // required for update/delete
	End   int             `json:"end_line"`   // required for update/delete
	// ParentEnd is the parent server{}'s EndLine, required only when creating
	// a nginx location{} — every other create appends at the end of the file.
	ParentEnd int    `json:"parent_end_line,omitempty"`
	Content   string `json:"content"` // ignored for delete
	Note      string `json:"note"`
	Apply     bool   `json:"apply"`
	// Expected is the file's expected_sha256, the same optimistic-lock
	// contract Write already uses — a stale block edit (line numbers computed
	// against content someone else has since changed) is refused rather than
	// spliced into the wrong lines.
	Expected string `json:"expected_sha256"`
}

// singletonHAProxySections cannot be created or deleted through block CRUD:
// haproxy expects exactly one global and one defaults section, and removing
// either breaks the config. Viewing and updating their raw text is still a
// plain block update.
var singletonHAProxySections = map[parse.BlockKind]bool{
	parse.BlockGlobal:   true,
	parse.BlockDefaults: true,
}

// WriteBlock applies one block-level edit and delegates the actual disk
// write to Write unchanged — validation by the real nginx/haproxy binary,
// automatic rollback on failure, versioning and audit logging all come from
// there, so none of that safety logic is duplicated here.
func (m *ConfigManager) WriteBlock(ctx context.Context, lang msgs.Lang, user, path string, req BlockWriteRequest) (WriteResult, error) {
	if (req.Op == "create" || req.Op == "delete") && singletonHAProxySections[req.Kind] {
		return WriteResult{}, fmt.Errorf("%s: создание и удаление недоступны для этого раздела, доступна только правка", req.Kind)
	}
	if (req.Op == "create" || req.Op == "update") && strings.TrimSpace(req.Content) == "" {
		return WriteResult{}, fmt.Errorf("текст блока не может быть пустым")
	}

	current, err := m.Read(path)
	if err != nil {
		return WriteResult{}, err
	}
	if req.Expected != "" && req.Expected != current.SHA256 {
		return WriteResult{}, ErrStaleContent
	}

	var newText string
	switch req.Op {
	case "create":
		newText, err = parse.InsertBlockAtEnd(current.Content, req.Kind, req.Content, req.ParentEnd)
	case "update":
		newText, err = parse.SpliceBlock(current.Content, req.Start, req.End, req.Content)
	case "delete":
		newText, err = parse.SpliceBlock(current.Content, req.Start, req.End, "")
	default:
		return WriteResult{}, fmt.Errorf("неизвестная операция %q", req.Op)
	}
	if err != nil {
		return WriteResult{}, err
	}

	note := req.Note
	if note == "" {
		note = fmt.Sprintf("блок %s: %s", req.Kind, req.Op)
	}
	return m.Write(ctx, lang, user, path, newText, note, req.Apply)
}

// Write replaces a config file. The previous content is captured first, the new
// content is validated by the owning service, and a failed validation restores
// the file before returning — the host is never left with a config it rejects.
func (m *ConfigManager) Write(ctx context.Context, lang msgs.Lang, user, path, content, note string, apply bool) (WriteResult, error) {
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
			if hadPrevious {
				// Put the file back exactly as it was before returning the error.
				if rbErr := m.c.WriteFile(path, previous, 0o644); rbErr != nil {
					return res, fmt.Errorf("конфиг не прошёл проверку, и откат не удался: %v (проверка: %s)",
						rbErr, strings.TrimSpace(validation.Output()))
				}
				res.RolledBack = true
			} else {
				// There is no previous version to restore — the file did not
				// exist before this call, so "roll back" means remove it,
				// not leave a broken config the host never had on disk.
				if rbErr := m.c.DeleteFile(path); rbErr != nil {
					return res, fmt.Errorf("конфиг не прошёл проверку, и удалить новый файл не удалось: %v (проверка: %s)",
						rbErr, strings.TrimSpace(validation.Output()))
				}
				res.RolledBack = true
			}
			res.Message = msgs.T(lang, "configs.validationFailed")
			return res, fmt.Errorf("%s отклонил конфигурацию: %s",
				service, strings.TrimSpace(validation.Output()))
		}
	}

	versionID, err := m.recordVersion(ctx, path, service, user, store.ActionEdit, note, newBytes)
	if err != nil {
		return res, err
	}
	res.VersionID = versionID
	res.Message = msgs.T(lang, "configs.fileSaved")

	if apply {
		var out collect.CommandResult
		var applyErr error
		switch service {
		case model.ServiceDocker:
			// nginx/haproxy reread their config in place; docker has no such
			// thing — "apply" means recreating whatever container the new
			// compose definition actually changed. `up -d` is idempotent: it
			// only touches services whose definition differs, so it is safe
			// to run after any edit, not just the one service that changed.
			out, applyErr = m.svc.ApplyCompose(ctx, user, path)
		case model.ServiceLibvirt:
			// Same idea as compose's `up -d`: writing the XML file alone
			// does not register it with libvirtd, `virsh define` does —
			// this is what turns a saved skeleton into an actual domain
			// (or updates an already-defined one), covering both create
			// and edit through the one write path.
			out, applyErr = m.svc.DefineLibvirtDomain(ctx, user, path)
		default:
			out, applyErr = m.svc.Action(ctx, user, service, "reload")
		}
		if applyErr == nil {
			res.Applied = out.OK()
			res.Apply = &out
			if out.OK() {
				res.Message = msgs.T(lang, "configs.fileSavedAndReloaded")
			}
		} else {
			// The write itself succeeded — only the follow-up apply step
			// failed. That must stay visible instead of silently vanishing.
			// applyErr.Error() itself isn't localized (Phase 1/2 scope, see
			// internal/msgs) — it travels as a raw arg, same as everywhere
			// else a deep error gets embedded in an otherwise-translated
			// sentence.
			res.Message += msgs.T(lang, "configs.applyFailed", applyErr.Error())
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
func (m *ConfigManager) Rollback(ctx context.Context, lang msgs.Lang, user string, id int64, apply bool) (WriteResult, error) {
	v, content, err := m.VersionContent(ctx, id)
	if err != nil {
		return WriteResult{}, err
	}
	note := fmt.Sprintf("откат к версии #%d от %s", v.ID, v.TS)
	res, err := m.Write(ctx, lang, user, v.Path, content, note, apply)
	if err != nil {
		return res, err
	}
	// Mark the freshly written version as a rollback rather than a plain edit.
	if res.VersionID > 0 {
		_, _ = m.db.ExecContext(ctx, `UPDATE config_versions SET action = ? WHERE id = ?`,
			store.ActionRollback, res.VersionID)
	}
	res.Message = msgs.T(lang, "configs.versionRestored", v.ID)
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
