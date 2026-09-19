package hub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Свои образы для кластеров: оператор загружает qcow2/img/raw на хаб один
// раз, а хаб при создании кластера сам заливает его на каждый хост
// размещения, где образа ещё нет (в каталог дисков libvirt, как загрузку
// «своего образа» на самом хосте). В размещении такой образ зовётся
// hub:<файл>; на хосте машина создаётся из host:<файл>.

const (
	HubImagePrefix   = "hub:"
	clusterImagesMax = 10 << 30
)

var clusterImageNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.(qcow2|img|raw)$`)

// ClusterImage — образ в библиотеке хаба.
type ClusterImage struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

func (m *Manager) clusterImagesDir() string { return filepath.Join(m.cfg.DataDir, "cluster-images") }

// ClusterImages — список библиотеки.
func (m *Manager) ClusterImages() []ClusterImage {
	out := []ClusterImage{}
	entries, err := os.ReadDir(m.clusterImagesDir())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !clusterImageNameRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, ClusterImage{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime().UTC().Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ClusterImage — образ по имени или ошибка.
func (m *Manager) ClusterImage(name string) (ClusterImage, error) {
	if !clusterImageNameRe.MatchString(name) {
		return ClusterImage{}, msgs.Errorf("vmcreate.invalidFileName", name)
	}
	info, err := os.Stat(filepath.Join(m.clusterImagesDir(), name))
	if err != nil {
		return ClusterImage{}, msgs.Errorf("hub.clusterImageMissing", name)
	}
	return ClusterImage{Name: name, Size: info.Size(), ModTime: info.ModTime().UTC().Format(time.RFC3339)}, nil
}

// SaveClusterImage кладёт загруженный файл в библиотеку (через .part).
func (m *Manager) SaveClusterImage(name string, src io.Reader) (ClusterImage, error) {
	if !clusterImageNameRe.MatchString(name) {
		return ClusterImage{}, msgs.Errorf("vmimage.imageMustQcow2ImgRaw")
	}
	dir := m.clusterImagesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ClusterImage{}, err
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); err == nil {
		return ClusterImage{}, msgs.Errorf("vmcreate.fileAlreadyExistsRemoveOld", name)
	}
	tmp := target + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return ClusterImage{}, err
	}
	n, err := io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return ClusterImage{}, err
	}
	if n == 0 {
		os.Remove(tmp)
		return ClusterImage{}, msgs.Errorf("vmimage.emptyFile")
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return ClusterImage{}, err
	}
	return m.ClusterImage(name)
}

// DeleteClusterImage убирает образ из библиотеки (на хостах копии
// остаются — это их диски).
func (m *Manager) DeleteClusterImage(name string) error {
	if !clusterImageNameRe.MatchString(name) {
		return msgs.Errorf("vmcreate.invalidFileName", name)
	}
	if err := os.Remove(filepath.Join(m.clusterImagesDir(), name)); err != nil {
		return msgs.Errorf("hub.clusterImageMissing", name)
	}
	return nil
}

// hostHasImage — есть ли на хосте файл того же размера в каталоге дисков.
func (m *Manager) hostHasImage(ctx context.Context, hostID int64, img ClusterImage) (bool, error) {
	var res struct {
		HostImages []vmcreate.HostImage `json:"host_images"`
	}
	if _, err := m.HostAPI(ctx, hostID, "GET", "/api/vm/images", nil, &res); err != nil {
		return false, err
	}
	for _, h := range res.HostImages {
		if h.Name == img.Name {
			return h.Size == img.Size, nil
		}
	}
	return false, nil
}

// ensureHostImage заливает образ на хост, если там нет такого же
// (файл с тем же именем, но другого размера — заменяется).
func (m *Manager) ensureHostImage(ctx context.Context, jc *jobs.Context, hostID int64, hostName string, img ClusterImage) error {
	has, err := m.hostHasImage(ctx, hostID, img)
	if err != nil {
		return err
	}
	if has {
		jc.Log("hub.clusterImageOnHost", img.Name, hostName)
		return nil
	}
	// Старый файл с тем же именем — убрать, иначе хост откажется.
	_, _ = m.HostAPI(ctx, hostID, "POST", "/api/vm/images/host-delete", map[string]string{"name": img.Name}, nil)
	f, err := os.Open(filepath.Join(m.clusterImagesDir(), img.Name))
	if err != nil {
		return msgs.Errorf("hub.clusterImageMissing", img.Name)
	}
	defer f.Close()
	jc.Log("hub.clusterImageUploading", img.Name, hostName, float64(img.Size)/(1<<30))
	last := time.Now()
	pr := &progressReader{r: f, total: img.Size, key: "hub.clusterImageProgress", report: func(key string, args ...any) {
		// В журнал задания — не чаще раза в 5 секунд: строки там не
		// заменяются.
		if time.Since(last) >= 5*time.Second || args[0].(int) == 100 {
			last = time.Now()
			jc.Log(key, args...)
		}
	}}
	code, raw, err := m.HostAPIStream(ctx, hostID, http.MethodPost, "/api/vm/images/upload?name="+url.QueryEscape(img.Name), pr, img.Size)
	if err != nil {
		return msgs.Errorf("hub.clusterImageUploadFailed", img.Name, hostName, err)
	}
	if code != http.StatusOK {
		return msgs.Errorf("hub.clusterImageUploadFailed", img.Name, hostName, fmt.Errorf("%d: %s", code, hostAPIError(raw)))
	}
	jc.Log("hub.clusterImageUploaded", img.Name, hostName)
	return nil
}

// HostAPIStream — как HostAPI, но с потоковым телом и без таймаута
// (образ на гигабайты); тело ответа возвращается как есть.
func (m *Manager) HostAPIStream(ctx context.Context, hostID int64, method, path string, body io.Reader, contentLength int64) (int, []byte, error) {
	dial, channel, onFail, err := m.dialerFor(ctx, hostID)
	if err != nil {
		return 0, nil, err
	}
	m.recordChannel(hostID, channel)
	cookie, err := m.cookieFor(ctx, hostID, dial)
	if err != nil {
		onFail()
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+remoteAPIAddr+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.ContentLength = contentLength
	req.Header.Set("Content-Type", "application/octet-stream")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	resp, err := tunnelHTTPClientNoTimeout(dial).Do(req)
	if err != nil {
		onFail()
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, hostAPIMaxBody))
	return resp.StatusCode, raw, nil
}

// hubImageName — имя файла из hub:<файл>; пусто, если это не образ хаба.
func hubImageName(imageID string) string {
	name, ok := strings.CutPrefix(imageID, HubImagePrefix)
	if !ok {
		return ""
	}
	return name
}

// DropClusterGroups убирает группы хостов с именами кластеров — раньше
// хаб заводил их под узлы, теперь узлы видны у своего хоста и в разделе
// «Кластеры», а группа только дублировала. Вызывается при старте.
func (m *Manager) DropClusterGroups(ctx context.Context) {
	clusters, err := m.db.ListClusters(ctx)
	if err != nil {
		return
	}
	groups, err := m.db.ListHostGroups(ctx)
	if err != nil {
		return
	}
	have := map[string]bool{}
	for _, g := range groups {
		have[g] = true
	}
	for _, cl := range clusters {
		if have[cl.Name] {
			_ = m.db.DeleteHostGroup(ctx, cl.Name)
		}
	}
}
