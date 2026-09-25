package control

import (
	"context"
	"encoding/json"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// LXDImage — образ для «Новый инстанс LXD»: локальный (уже на хосте) или
// на удалённом сервере образов.
type LXDImage struct {
	// Ref — что передать в `lxc launch`: «images:debian/12», «ubuntu:24.04»
	// или алиас/отпечаток локального образа.
	Ref         string `json:"ref"`
	Alias       string `json:"alias"`
	Remote      string `json:"remote"`
	Description string `json:"description"`
	Type        string `json:"type"` // container | virtual-machine
	Arch        string `json:"arch"`
	Size        int64  `json:"size"`
	OS          string `json:"os"`
	Release     string `json:"release"`
}

// LXDImageRemotes — откуда брать образы: «local» — уже скачанные на хост,
// «images» — images.lxd.canonical.com (Debian, Alpine, Rocky, Fedora,
// Arch…), «ubuntu» — cloud-images.ubuntu.com (официальные Ubuntu).
var LXDImageRemotes = []string{"local", "images", "ubuntu"}

type lxdImageCache struct {
	at   time.Time
	list []LXDImage
}

var (
	lxdImagesMu    sync.Mutex
	lxdImagesCache = map[string]lxdImageCache{}
)

// lxdRemoteCacheTTL — удалённый список меняется раз в сутки, а
// скачивается секундами: кэшируется на хосте.
const lxdRemoteCacheTTL = 24 * time.Hour

// hostLXDArch — архитектура хоста в терминах LXD.
func hostLXDArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "arm":
		return "armv7l"
	default:
		return runtime.GOARCH
	}
}

// ListImages — образы источника remote под архитектуру хоста.
func (m *LXDManager) ListImages(ctx context.Context, remote string) ([]LXDImage, error) {
	known := false
	for _, r := range LXDImageRemotes {
		if r == remote {
			known = true
		}
	}
	if !known {
		return nil, msgs.Errorf("control.lxdUnknownRemote", remote)
	}
	if remote != "local" {
		lxdImagesMu.Lock()
		c, ok := lxdImagesCache[remote]
		lxdImagesMu.Unlock()
		if ok && time.Since(c.at) < lxdRemoteCacheTTL {
			return c.list, nil
		}
	}
	args := []string{"image", "list", "--format", "json"}
	if remote != "local" {
		args = []string{"image", "list", remote + ":", "--format", "json"}
	}
	res, err := m.c.RunTimeout(ctx, 90*time.Second, "lxc", args...)
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, msgs.Errorf("control.lxdImageListFailed", remote, strings.TrimSpace(res.Stderr+res.Stdout))
	}
	list, err := parseLXDImages(res.Stdout, remote, hostLXDArch())
	if err != nil {
		return nil, err
	}
	if remote != "local" {
		lxdImagesMu.Lock()
		lxdImagesCache[remote] = lxdImageCache{at: time.Now(), list: list}
		lxdImagesMu.Unlock()
	}
	return list, nil
}

// parseLXDImages разбирает `lxc image list --format json`: по одному
// образу на алиас (у удалённых их по нескольку — «debian/12»,
// «debian/bookworm»; берётся первый), только архитектура хоста.
func parseLXDImages(out, remote, arch string) ([]LXDImage, error) {
	var raw []struct {
		Aliases []struct {
			Name string `json:"name"`
		} `json:"aliases"`
		Architecture string            `json:"architecture"`
		Type         string            `json:"type"`
		Size         int64             `json:"size"`
		Fingerprint  string            `json:"fingerprint"`
		Properties   map[string]string `json:"properties"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, msgs.Errorf("control.lxdImageListParse", err)
	}
	seen := map[string]bool{}
	var list []LXDImage
	for _, im := range raw {
		if arch != "" && im.Architecture != "" && im.Architecture != arch {
			continue
		}
		alias := ""
		if len(im.Aliases) > 0 {
			alias = im.Aliases[0].Name
		}
		if alias == "" && remote != "local" {
			continue
		}
		ref := alias
		if remote != "local" {
			ref = remote + ":" + alias
		} else if ref == "" {
			ref = im.Fingerprint
			if len(ref) > 12 {
				alias = ref[:12]
			}
		}
		key := ref + "|" + im.Type
		if seen[key] {
			continue
		}
		seen[key] = true
		t := im.Type
		if t == "" {
			t = "container"
		}
		list = append(list, LXDImage{
			Ref: ref, Alias: alias, Remote: remote, Description: im.Properties["description"],
			Type: t, Arch: im.Architecture, Size: im.Size, OS: im.Properties["os"], Release: im.Properties["release"],
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].OS != list[j].OS {
			return list[i].OS < list[j].OS
		}
		return list[i].Alias < list[j].Alias
	})
	return list, nil
}
