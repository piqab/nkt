package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/inventory"
)

// DockerImage is one image on the host.
type DockerImage struct {
	ID string `json:"id"`
	// Tags as "repo:tag". Empty for a dangling image — one whose tag has
	// moved to a newer build, which is exactly what accumulates and is worth
	// cleaning up.
	Tags    []string `json:"tags"`
	Size    int64    `json:"size"`
	Created string   `json:"created"`
	// InUse is set when a container on this host is running from the image.
	// Removing one of those fails, and knowing beforehand is better than
	// finding out from Docker's error.
	InUse bool `json:"in_use"`
	// UsedBy names the containers holding it, so "in use" is actionable.
	UsedBy   []string `json:"used_by,omitempty"`
	Dangling bool     `json:"dangling"`
}

// ImageManager lists and operates on Docker images.
type ImageManager struct {
	c        collect.Collector
	scanner  *inventory.Scanner
	backupTo string
}

// NewImageManager builds the image manager. backupDir is where `docker save`
// archives are written.
func NewImageManager(c collect.Collector, scanner *inventory.Scanner, backupDir string) *ImageManager {
	return &ImageManager{c: c, scanner: scanner, backupTo: backupDir}
}

// BackupDir is where saved images land, reported to the UI so the operator
// knows where to fetch them from.
func (m *ImageManager) BackupDir() string { return m.backupTo }

// dockerImageJSON is the subset of Docker's /images/json entry this needs.
type dockerImageJSON struct {
	ID       string   `json:"Id"`
	RepoTags []string `json:"RepoTags"`
	Size     int64    `json:"Size"`
	Created  int64    `json:"Created"`
}

// List returns the images Docker knows about, marked with whether a running
// container is using each.
func (m *ImageManager) List(ctx context.Context) ([]DockerImage, error) {
	raw, status, err := m.c.DockerAPI(ctx, "GET", "/images/json", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Docker ответил %d: %s", status, strings.TrimSpace(string(raw)))
	}
	var entries []dockerImageJSON
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("не удалось разобрать ответ Docker: %w", err)
	}

	// Which images containers are actually running from. Taken from the last
	// scan rather than a second Docker call: it is already there, and being
	// a moment stale only affects a warning label.
	usedBy := map[string][]string{}
	if snap := m.scanner.Latest(); snap != nil {
		for _, c := range snap.Container {
			if c.Image != "" {
				usedBy[c.Image] = append(usedBy[c.Image], c.Name)
			}
		}
	}

	out := make([]DockerImage, 0, len(entries))
	for _, e := range entries {
		img := DockerImage{
			ID:   e.ID,
			Size: e.Size,
			// Пустой срез, а не nil: nil уезжает в JSON как null, и
			// «tags.length» в браузере роняет отрисовку всей страницы.
			// У образа без тега (dangling) ровно этот случай и есть —
			// а такие образы копятся на любом хосте, где что-то
			// пересобирали.
			Tags:    []string{},
			Created: time.Unix(e.Created, 0).UTC().Format(time.RFC3339),
		}
		for _, tag := range e.RepoTags {
			// Docker reports an untagged image as "<none>:<none>".
			if tag == "" || strings.HasPrefix(tag, "<none>") {
				continue
			}
			img.Tags = append(img.Tags, tag)
			if names := usedBy[tag]; len(names) > 0 {
				img.InUse = true
				img.UsedBy = append(img.UsedBy, names...)
			}
		}
		img.Dangling = len(img.Tags) == 0
		out = append(out, img)
	}
	return out, nil
}

// imageRefRe accepts an image id ("sha256:…" or a hex prefix) or a
// repo:tag reference. Whatever is passed goes into a URL path, and a
// reference with a slash or a query character in it would address something
// other than the image meant.
var imageRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]*$`)

func checkImageRef(ref string) error {
	if ref == "" || !imageRefRe.MatchString(ref) || strings.Contains(ref, "..") {
		return fmt.Errorf("некорректная ссылка на образ: %q", ref)
	}
	return nil
}

// Remove deletes one image. force also removes it when several tags point at
// it; it never removes an image a container is using — Docker refuses that
// itself, and the error says which container holds it.
func (m *ImageManager) Remove(ctx context.Context, ref string, force bool) error {
	if err := checkImageRef(ref); err != nil {
		return err
	}
	path := "/images/" + url.PathEscape(ref)
	if force {
		path += "?force=1"
	}
	raw, status, err := m.c.DockerAPI(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("%s", dockerMessage(raw, status))
	}
	return nil
}

// Prune removes every dangling image in one call — the accumulated layers of
// previous builds, which is what actually fills a disk.
func (m *ImageManager) Prune(ctx context.Context) (reclaimed int64, err error) {
	raw, status, err := m.c.DockerAPI(ctx, "POST",
		`/images/prune?filters={"dangling":["true"]}`, nil)
	if err != nil {
		return 0, err
	}
	if status != 200 {
		return 0, fmt.Errorf("%s", dockerMessage(raw, status))
	}
	var out struct {
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.SpaceReclaimed, nil
}

// Save writes an image to a tar archive under the backup directory and
// returns its path.
//
// Uses the docker CLI rather than the Engine API deliberately: the API
// returns the archive as one response body, and an image is routinely
// gigabytes — `docker save -o` streams it straight to disk instead of
// through this process's memory.
func (m *ImageManager) Save(ctx context.Context, ref string) (string, error) {
	if err := checkImageRef(ref); err != nil {
		return "", err
	}
	if !collect.Which(ctx, m.c, "docker") {
		return "", fmt.Errorf("для сохранения образа нужен docker в PATH — через Docker API это выгрузило бы гигабайты в память")
	}
	if err := m.c.Mkdir(m.backupTo); err != nil {
		return "", fmt.Errorf("создать %s: %w", m.backupTo, err)
	}

	name := strings.NewReplacer("/", "_", ":", "_").Replace(ref)
	path := filepath.Join(m.backupTo, fmt.Sprintf("%s_%s.tar", name, time.Now().UTC().Format("20060102-150405")))

	res, err := m.c.Run(ctx, "docker", "save", "-o", path, ref)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("docker save: %s", strings.TrimSpace(res.Stderr))
	}
	return path, nil
}

// dockerMessage pulls the human-readable part out of a Docker error body.
func dockerMessage(raw []byte, status int) string {
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Message != "" {
		return body.Message
	}
	return fmt.Sprintf("Docker ответил %d: %s", status, strings.TrimSpace(string(raw)))
}
