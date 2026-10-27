package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Дополнения кластера (Longhorn): подготовка каждого узла и чарт на первом
// control plane. Ставятся последним шагом создания кластера (поле addons
// описания) или кнопкой «Дополнения» у готового кластера; новые worker'ы
// кластера с дополнением готовятся при пополнении.

// validateAddons проверяет список и заполняет версию по умолчанию —
// в описании хранится та, что поставлена.
func validateAddons(list []ClusterAddon) error {
	seen := map[string]bool{}
	for i := range list {
		a := &list[i]
		a.Name = strings.TrimSpace(a.Name)
		a.Version = strings.TrimPrefix(strings.TrimSpace(a.Version), "v")
		if !k8s.ValidAddon(a.Name) {
			return msgs.Errorf("k8s.addonUnknown", a.Name)
		}
		if seen[a.Name] {
			return msgs.Errorf("hub.clusterAddonDup", a.Name)
		}
		seen[a.Name] = true
		if a.Version == "" {
			a.Version = k8s.LonghornDefaultVersion
		}
		if !k8s.ValidAddonVersion(a.Version) {
			return msgs.Errorf("k8s.addonBadVersion", a.Version)
		}
	}
	return nil
}

// addonReplicas — реплик тома: по одной на узел, не больше трёх.
func addonReplicas(nodes int) int {
	return max(1, min(3, nodes))
}

// AddonVersion — версия чарта и ограничение на версию Kubernetes.
type AddonVersion struct {
	Version     string `json:"version"`
	KubeVersion string `json:"kube_version,omitempty"`
}

// AddonVersions — ответ /hub/k8s-addon-versions.
type AddonVersions struct {
	Default  string         `json:"default"`
	Versions []AddonVersion `json:"versions"`
	Error    string         `json:"error,omitempty"`
}

type addonIndex struct {
	at  time.Time
	all []AddonVersion
	err string
}

// addonIndexAll — все стабильные версии чарта из index.yaml репозитория,
// новые первыми; кэш — на час (и на 5 минут — неудача).
func (m *Manager) addonIndexAll(ctx context.Context, name string) ([]AddonVersion, string) {
	m.addonIdxMu.Lock()
	defer m.addonIdxMu.Unlock()
	if c, ok := m.addonIdx[name]; ok {
		ttl := time.Hour
		if c.err != "" {
			ttl = 5 * time.Minute
		}
		if time.Since(c.at) < ttl {
			return c.all, c.err
		}
	}
	all, err := fetchAddonIndex(ctx, k8s.LonghornRepoURL+"/index.yaml", name)
	c := addonIndex{at: time.Now(), all: all}
	if err != nil {
		c.err = err.Error()
	}
	if m.addonIdx == nil {
		m.addonIdx = map[string]addonIndex{}
	}
	m.addonIdx[name] = c
	return c.all, c.err
}

// maxAddonIndex — index.yaml Longhorn сейчас ~65 КБ; с запасом.
const maxAddonIndex = 8 << 20

func fetchAddonIndex(ctx context.Context, url, chart string) ([]AddonVersion, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAddonIndex))
	if err != nil {
		return nil, err
	}
	return parseAddonIndex(raw, chart)
}

// parseAddonIndex — версии чарта из index.yaml: только вида 1.2.3 (без
// -rc и -dev), новые первыми.
func parseAddonIndex(raw []byte, chart string) ([]AddonVersion, error) {
	var idx struct {
		Entries map[string][]struct {
			Version     string `yaml:"version"`
			KubeVersion string `yaml:"kubeVersion"`
		} `yaml:"entries"`
	}
	if err := yaml.Unmarshal(raw, &idx); err != nil {
		return nil, err
	}
	var out []AddonVersion
	seen := map[string]bool{}
	for _, e := range idx.Entries[chart] {
		v := strings.TrimPrefix(e.Version, "v")
		if !k8s.ValidAddonVersion(v) || seen[v] {
			continue
		}
		seen[v] = true
		kv := strings.TrimSpace(e.KubeVersion)
		if len(kv) > 64 {
			kv = kv[:64]
		}
		out = append(out, AddonVersion{Version: v, KubeVersion: kv})
	}
	if len(out) == 0 {
		return nil, msgs.Errorf("hub.addonIndexEmpty", chart)
	}
	sort.Slice(out, func(i, j int) bool { return k8s.AddonVersionCmp(out[i].Version, out[j].Version) > 0 })
	return out, nil
}

// addonLatestPatches — последний патч каждой из minors последних минорных
// веток (для выбора в форме: обновляться можно только на ветку вперёд).
func addonLatestPatches(all []AddonVersion, minors int) []AddonVersion {
	var out []AddonVersion
	seen := map[string]bool{}
	for _, v := range all {
		p := strings.SplitN(v.Version, ".", 3)
		key := p[0] + "." + p[1]
		if seen[key] {
			continue
		}
		if len(seen) == minors {
			break
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

// AddonVersions — версии дополнения для формы.
func (m *Manager) AddonVersions(ctx context.Context, name string) AddonVersions {
	all, errText := m.addonIndexAll(ctx, name)
	out := AddonVersions{Default: k8s.LonghornDefaultVersion, Versions: addonLatestPatches(all, 4), Error: errText}
	if out.Versions == nil {
		out.Versions = []AddonVersion{}
	}
	return out
}

// addonKubeConstraint — ограничение kubeVersion версии чарта; known —
// есть ли такая версия в index.yaml; reachable — удалось ли его прочитать.
func (m *Manager) addonKubeConstraint(ctx context.Context, name, version string) (constraint string, known, reachable bool) {
	all, errText := m.addonIndexAll(ctx, name)
	if errText != "" {
		return "", false, false
	}
	for _, v := range all {
		if v.Version == version {
			return v.KubeVersion, true, true
		}
	}
	return "", false, true
}

// handleAddonVersions — GET /hub/k8s-addon-versions?addon=longhorn.
func (s *Server) handleAddonVersions(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("addon")
	if !k8s.ValidAddon(name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("k8s.addonUnknown", name))
		return
	}
	writeJSON(w, http.StatusOK, s.hub.AddonVersions(r.Context(), name))
}

// addonPrepare — подготовить узел заданием на нём и дождаться.
func (m *Manager) addonPrepare(ctx context.Context, jc *jobs.Context, h store.Host, name string, skipLocalPath bool) error {
	jc.Log("hub.addonPreparing", name, h.Name)
	return m.addonHostJob(ctx, jc, h, k8s.AddonRequest{Action: k8s.AddonPrepare, Name: name, SkipLocalPath: skipLocalPath})
}

func (m *Manager) addonHostJob(ctx context.Context, jc *jobs.Context, h store.Host, req k8s.AddonRequest) error {
	var started struct {
		JobID int64 `json:"job_id"`
	}
	code, err := m.HostAPI(ctx, h.ID, "POST", "/api/k8s/addon", req, &started)
	if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
		return msgs.Errorf("hub.addonOldNode", h.Name)
	}
	if err != nil {
		return msgs.Errorf("hub.addonStart", h.Name, err)
	}
	g := &GroupApplyRunner{m: m}
	return g.waitHostJob(ctx, jc, h, started.JobID)
}

// addonInstalled — установленная версия дополнения на кластере (пусто —
// не стоит): релиз helm на control plane.
func (m *Manager) addonInstalled(ctx context.Context, cp store.Host, name string) (string, error) {
	var st k8s.HelmStatus
	if _, err := m.HostAPI(ctx, cp.ID, "GET", "/api/k8s/helm", nil, &st); err != nil {
		return "", err
	}
	for _, rel := range st.Releases {
		if rel.Name == name && rel.Namespace == k8s.LonghornNamespace {
			return strings.TrimPrefix(rel.Chart, name+"-"), nil
		}
	}
	return "", nil
}

// applyAddons — шаг создания кластера: подготовить узлы prepare и, если
// install, поставить дополнения на cp1. Пройденное — в done.
func (m *Manager) applyAddons(ctx context.Context, jc *jobs.Context, spec ClusterSpec, cp1 store.Host, prepare []store.Host, nodes int, install bool, done *clusterResume) error {
	if done.Addons == nil {
		done.Addons = map[string]bool{}
	}
	for _, a := range spec.Addons {
		for _, h := range prepare {
			key := a.Name + ":prepare:" + h.Name
			if done.Addons[key] {
				continue
			}
			if err := m.addonPrepare(ctx, jc, h, a.Name, spec.Flavor == k8s.FlavorK3s); err != nil {
				return err
			}
			done.Addons[key] = true
			jc.SaveResume(*done)
		}
		key := a.Name + ":install"
		if !install || done.Addons[key] {
			continue
		}
		jc.Log("hub.addonInstalling", a.Name, a.Version, cp1.Name)
		req := k8s.AddonRequest{Action: k8s.AddonInstall, Name: a.Name, Version: a.Version, Replicas: addonReplicas(nodes), Default: true}
		if err := m.addonHostJob(ctx, jc, cp1, req); err != nil {
			return err
		}
		done.Addons[key] = true
		jc.SaveResume(*done)
		jc.Log("hub.addonInstalled", a.Name, a.Version)
	}
	return nil
}

// addonPreflight — пункты сухого прогона: версия есть в репозитории и
// подходит к Kubernetes кластера, диск узлов не меньше разумного.
func (r *ClusterRunner) addonPreflight(ctx context.Context, spec ClusterSpec, add func(ok, warn bool, key string, args ...any)) {
	k8sVer := spec.K8sVersion
	if k8sVer == "" {
		k8sVer = r.m.k8sStableMinor(ctx)
	}
	for _, a := range spec.Addons {
		constraint, known, reachable := r.m.addonKubeConstraint(ctx, a.Name, a.Version)
		switch {
		case !reachable:
			add(true, true, "hub.preflightAddonIndexDown", a.Name, k8s.LonghornRepoURL)
		case !known:
			add(false, false, "hub.preflightAddonNoVersion", a.Name, a.Version)
		case !k8s.KubeVersionOK(constraint, k8sVer):
			add(false, false, "hub.preflightAddonKube", a.Name, a.Version, constraint, k8sVer)
		default:
			add(true, false, "hub.preflightAddonOK", a.Name, a.Version, k8sVer)
		}
		if disk := spec.addonMinDisk(); disk > 0 && disk < k8s.LonghornMinDiskGB {
			add(true, true, "hub.preflightAddonDisk", a.Name, disk, k8s.LonghornMinDiskGB)
		}
	}
}

// addonMinDisk — наименьший диск машины-узла, ГБ (0 — узлов-машин нет).
func (s ClusterSpec) addonMinDisk() int {
	disk := 0
	take := func(gb int) {
		if gb > 0 && (disk == 0 || gb < disk) {
			disk = gb
		}
	}
	if len(s.Placements) > 0 {
		for _, p := range s.Placements {
			if p.Kind == KindVM {
				take(p.DiskGB)
			}
		}
		return disk
	}
	take(s.CPDiskGB)
	if s.Workers > 0 {
		take(s.WDiskGB)
	}
	return disk
}

// KindClusterAddon — задание «дополнение на готовый кластер».
const KindClusterAddon = "k8s.cluster.addon"

// ClusterAddonParams — вход задания.
type ClusterAddonParams struct {
	ClusterID int64  `json:"cluster_id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
}

// ClusterAddonRunner ставит или обновляет дополнение на готовом кластере.
type ClusterAddonRunner struct{ m *Manager }

// NewClusterAddonRunner строит исполнителя.
func NewClusterAddonRunner(m *Manager) *ClusterAddonRunner { return &ClusterAddonRunner{m: m} }

// Resumable — да: подготовленные узлы сохранены.
func (r *ClusterAddonRunner) Resumable() bool { return true }

// Run — подготовка всех узлов, затем установка (или обновление) на
// control plane, затем версия — в описание кластера.
func (r *ClusterAddonRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ClusterAddonParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	var done clusterResume
	if err := jc.LoadResume(&done); err != nil {
		return msgs.Errorf("hub.parsingResumeState", err)
	}
	cl, err := r.m.db.ClusterByID(ctx, p.ClusterID)
	if err != nil {
		return err
	}
	var spec ClusterSpec
	if err := json.Unmarshal([]byte(cl.SpecJSON), &spec); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	hosts, err := r.m.db.ClusterHosts(ctx, cl.ID)
	if err != nil || len(hosts) == 0 {
		return msgs.Errorf("hub.clusterNoNodes")
	}
	cp := hosts[0]
	cur, err := r.m.addonInstalled(ctx, cp, p.Name)
	if err != nil {
		return err
	}
	if err := k8s.CheckLonghornUpgrade(cur, p.Version); err != nil {
		return err
	}
	if cur != "" {
		jc.Log("hub.addonUpgrading", p.Name, cur, p.Version)
	}
	total := len(hosts) + 1
	if done.Addons == nil {
		done.Addons = map[string]bool{}
	}
	for i, h := range hosts {
		jc.StepKey(i+1, total, "hub.addonStepPrepare", h.Name)
		key := p.Name + ":prepare:" + h.Name
		if done.Addons[key] {
			continue
		}
		if err := r.m.addonPrepare(ctx, jc, h, p.Name, cur == "" && spec.Flavor == k8s.FlavorK3s); err != nil {
			return err
		}
		done.Addons[key] = true
		jc.SaveResume(done)
	}
	jc.StepKey(total, total, "hub.addonStepInstall", p.Name, p.Version)
	req := k8s.AddonRequest{Action: k8s.AddonInstall, Name: p.Name, Version: p.Version, Replicas: addonReplicas(len(hosts)), Default: cur == "", Upgrade: cur != ""}
	jc.Log("hub.addonInstalling", p.Name, p.Version, cp.Name)
	if err := r.m.addonHostJob(ctx, jc, cp, req); err != nil {
		return err
	}
	// Версия — в описание: пополнение кластера подготовит новые узлы.
	if cl, err = r.m.db.ClusterByID(ctx, p.ClusterID); err == nil && json.Unmarshal([]byte(cl.SpecJSON), &spec) == nil {
		found := false
		for i := range spec.Addons {
			if spec.Addons[i].Name == p.Name {
				spec.Addons[i].Version, found = p.Version, true
			}
		}
		if !found {
			spec.Addons = append(spec.Addons, ClusterAddon{Name: p.Name, Version: p.Version})
		}
		if raw, err := json.Marshal(spec); err == nil {
			_ = r.m.db.SetClusterSpec(ctx, cl.ID, string(raw))
		}
	}
	jc.Log("hub.addonInstalled", p.Name, p.Version)
	return nil
}

// ClusterAddonState — строка окна «Дополнения».
type ClusterAddonState struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Spec      string `json:"spec,omitempty"`
	Error     string `json:"error,omitempty"`
}

// handleClusterAddons — GET /hub/clusters/{id}/addons: что стоит (по
// helm на control plane) и что записано в описании; POST {name, version}
// — поставить или обновить заданием.
func (s *Server) handleClusterAddons(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cl, err := s.db.ClusterByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	var spec ClusterSpec
	_ = json.Unmarshal([]byte(cl.SpecJSON), &spec)
	hosts, err := s.db.ClusterHosts(r.Context(), id)
	if err != nil || len(hosts) == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.clusterNoNodes"))
		return
	}
	if r.Method == http.MethodGet {
		out := []ClusterAddonState{}
		for _, name := range k8s.Addons {
			st := ClusterAddonState{Name: name}
			for _, a := range spec.Addons {
				if a.Name == name {
					st.Spec = a.Version
				}
			}
			if st.Installed, err = s.hub.addonInstalled(r.Context(), hosts[0], name); err != nil {
				st.Error = msgs.Localize(msgs.LangFromRequest(r), err)
			}
			out = append(out, st)
		}
		writeJSON(w, http.StatusOK, map[string]any{"addons": out, "nodes": len(hosts)})
		return
	}
	if cl.Status != store.ClusterReady {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.addonClusterNotReady", cl.Name))
		return
	}
	var req struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	list := []ClusterAddon{{Name: req.Name, Version: req.Version}}
	if err := validateAddons(list); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	a := list[0]
	// Правило обновления — сразу, понятным отказом, а не упавшим заданием.
	cur, err := s.hub.addonInstalled(r.Context(), hosts[0], a.Name)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if err := k8s.CheckLonghornUpgrade(cur, a.Version); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	jobID, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindClusterAddon, TitleKey: "hub.addonJobTitle", TitleArgs: []any{a.Name, a.Version, cl.Name},
		Queue: fmt.Sprintf("cluster:%d", cl.ID), Author: user, Steps: len(hosts) + 1,
		Params: ClusterAddonParams{ClusterID: cl.ID, Name: a.Name, Version: a.Version},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "hub.cluster.addon", cl.Name+" "+a.Name+" "+cur+"→"+a.Version, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}
