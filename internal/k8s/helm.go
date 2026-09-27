package k8s

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/msgs"
)

// Helm на control plane: релизы, репозитории, установка, обновление,
// откат и удаление — фоновыми заданиями (см. API). Настройки Helm
// (репозитории, кэш, вход в реестры) — в каталоге данных nkt, а не в
// домашнем каталоге root: так их видят и чтение из песочницы службы, и
// задания, запущенные вне её. Глобальные флаги идут после подкоманды —
// helm принимает их где угодно.
//
// Релиз и namespace для действий над существующим релизом берутся из
// helm list (Find*), новые имена (релиз, репозиторий, чарт, версия)
// проверяются регулярными выражениями, адрес репозитория — разбором URL.

// HelmVersion — версия Helm, которую ставит «Установить Helm».
const HelmVersion = "v3.16.3"

// HelmRelease — строка helm list -o json.
type HelmRelease struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   string `json:"revision"`
	Updated    string `json:"updated"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

// HelmRevision — строка helm history -o json.
type HelmRevision struct {
	Revision    int    `json:"revision"`
	Updated     string `json:"updated"`
	Status      string `json:"status"`
	Chart       string `json:"chart"`
	AppVersion  string `json:"app_version"`
	Description string `json:"description"`
}

// HelmRepo — строка helm repo list -o json.
type HelmRepo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// HelmStatus — ответ /k8s/helm.
type HelmStatus struct {
	Installed bool          `json:"installed"`
	Version   string        `json:"version,omitempty"`
	Releases  []HelmRelease `json:"releases"`
	Repos     []HelmRepo    `json:"repos"`
	Error     string        `json:"error,omitempty"`
	// SetupVersion — какую версию поставит «Установить Helm».
	SetupVersion string `json:"setup_version"`
}

// helmDir — настройки Helm в каталоге данных nkt.
func (m *Manager) helmDir() string {
	if m.runDir == "" {
		return filepath.Join(os.TempDir(), "nkt-helm")
	}
	return filepath.Join(filepath.Dir(m.runDir), "helm")
}

// kubeconfig — файл kubeconfig control plane для helm.
func (m *Manager) kubeconfig(ctx context.Context) (string, error) {
	switch m.Status(ctx).Flavor {
	case FlavorK3s:
		return "/etc/rancher/k3s/k3s.yaml", nil
	case FlavorKubeadm:
		return "/etc/kubernetes/admin.conf", nil
	}
	return "", msgs.Errorf("k8s.notInstalled")
}

// HelmArgv — команда helm с kubeconfig и настройками nkt.
func (m *Manager) HelmArgv(ctx context.Context, args ...string) ([]string, error) {
	kc, err := m.kubeconfig(ctx)
	if err != nil {
		return nil, err
	}
	dir := m.helmDir()
	argv := append([]string{"helm"}, args...)
	return append(argv,
		"--kubeconfig", kc,
		"--repository-config", filepath.Join(dir, "repositories.yaml"),
		"--repository-cache", filepath.Join(dir, "cache"),
		"--registry-config", filepath.Join(dir, "registry.json")), nil
}

func (m *Manager) helmRead(ctx context.Context, args ...string) (string, error) {
	argv, err := m.HelmArgv(ctx, args...)
	if err != nil {
		return "", err
	}
	out, err := m.c.Run(ctx, argv[0], argv[1:]...)
	if err != nil {
		return "", err
	}
	if !out.OK() {
		return "", msgs.Errorf("k8s.helm", strings.TrimSpace(out.Stderr+"\n"+out.Stdout))
	}
	return out.Stdout, nil
}

// Helm — установлен ли helm, релизы и репозитории.
func (m *Manager) Helm(ctx context.Context) HelmStatus {
	st := HelmStatus{Releases: []HelmRelease{}, Repos: []HelmRepo{}, SetupVersion: HelmVersion}
	if !collect.Which(ctx, m.c, "helm") {
		return st
	}
	st.Installed = true
	if out, err := m.c.Run(ctx, "helm", "version", "--short"); err == nil && out.OK() {
		st.Version = strings.TrimSpace(out.Stdout)
	}
	raw, err := m.helmRead(ctx, "list", "-A", "-o", "json")
	if err != nil {
		st.Error = err.Error()
		return st
	}
	if err := json.Unmarshal([]byte(raw), &st.Releases); err != nil {
		st.Error = err.Error()
	}
	// Репозиториев может не быть вовсе — тогда helm отвечает ошибкой.
	if raw, err := m.helmRead(ctx, "repo", "list", "-o", "json"); err == nil {
		_ = json.Unmarshal([]byte(raw), &st.Repos)
	}
	if st.Releases == nil {
		st.Releases = []HelmRelease{}
	}
	if st.Repos == nil {
		st.Repos = []HelmRepo{}
	}
	return st
}

// FindRelease — релиз из helm list: имя и namespace — из вывода helm.
func (m *Manager) FindRelease(ctx context.Context, namespace, name string) (HelmRelease, error) {
	st := m.Helm(ctx)
	if !st.Installed {
		return HelmRelease{}, msgs.Errorf("k8s.helmMissing")
	}
	for _, r := range st.Releases {
		if r.Namespace == namespace && r.Name == name {
			return r, nil
		}
	}
	return HelmRelease{}, msgs.Errorf("k8s.helmNoRelease", strings.TrimPrefix(namespace+"/"+name, "/"))
}

// HelmHistory — ревизии релиза.
func (m *Manager) HelmHistory(ctx context.Context, namespace, name string) ([]HelmRevision, error) {
	r, err := m.FindRelease(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	raw, err := m.helmRead(ctx, "history", r.Name, "-n", r.Namespace, "-o", "json")
	if err != nil {
		return nil, err
	}
	var out []HelmRevision
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, msgs.Errorf("k8s.helm", err.Error())
	}
	return out, nil
}

// HelmValues — пользовательские значения релиза (helm get values).
func (m *Manager) HelmValues(ctx context.Context, namespace, name string) (string, error) {
	r, err := m.FindRelease(ctx, namespace, name)
	if err != nil {
		return "", err
	}
	out, err := m.helmRead(ctx, "get", "values", r.Name, "-n", r.Namespace, "-o", "yaml")
	if strings.TrimSpace(out) == "null" {
		out = ""
	}
	return out, err
}

var (
	helmRepoNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	helmChartRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	helmVersionRe  = regexp.MustCompile(`^v?[0-9A-Za-z.+_-]{1,64}$`)
)

// HelmInstallRequest — установка или обновление релиза.
type HelmInstallRequest struct {
	RepoName  string `json:"repo_name"`
	RepoURL   string `json:"repo_url"`
	Chart     string `json:"chart"`
	Version   string `json:"version"`
	Release   string `json:"release"`
	Namespace string `json:"namespace"`
	Values    string `json:"values"`
}

// Validate проверяет новые имена и адрес репозитория.
func (r HelmInstallRequest) Validate() error {
	if !dnsLabelRe.MatchString(r.Release) {
		return msgs.Errorf("k8s.badName", r.Release)
	}
	if !dnsLabelRe.MatchString(r.Namespace) {
		return msgs.Errorf("k8s.badName", r.Namespace)
	}
	if !helmChartRe.MatchString(r.Chart) {
		return msgs.Errorf("k8s.helmBadChart", r.Chart)
	}
	if r.Version != "" && !helmVersionRe.MatchString(r.Version) {
		return msgs.Errorf("k8s.badValue", r.Version)
	}
	if strings.HasPrefix(r.RepoURL, "oci://") {
		return validRepoURL(r.RepoURL)
	}
	if !helmRepoNameRe.MatchString(r.RepoName) {
		return msgs.Errorf("k8s.helmBadRepo", r.RepoName)
	}
	return validRepoURL(r.RepoURL)
}

func validRepoURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "oci") ||
		u.User != nil || strings.ContainsAny(raw, " \t\n'\"`$\\{}") {
		return msgs.Errorf("k8s.helmBadURL", raw)
	}
	return nil
}

// ChartRef — что передать helm: repo/chart или oci://…/chart.
func (r HelmInstallRequest) ChartRef() string {
	if strings.HasPrefix(r.RepoURL, "oci://") {
		return strings.TrimRight(r.RepoURL, "/") + "/" + r.Chart
	}
	return r.RepoName + "/" + r.Chart
}

// ValuesPath — файл значений релиза в каталоге Helm nkt (остаётся для
// следующего обновления).
func (m *Manager) ValuesPath(namespace, release string) string {
	return filepath.Join(m.helmDir(), "values", namespace+"__"+release+".yaml")
}

// WriteValues кладёт значения релиза в файл 0600 (пусто — файл убирается).
func (m *Manager) WriteValues(namespace, release, values string) (string, error) {
	p := m.ValuesPath(namespace, release)
	if strings.TrimSpace(values) == "" {
		_ = os.Remove(p)
		return "", nil
	}
	if len(values) > MaxManifest {
		return "", msgs.Errorf("k8s.manifestTooBig", MaxManifest>>10)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(values), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// HelmInstallCommands — шаги задания установки/обновления: репозиторий
// (для обычного — add и update), затем upgrade --install. valuesFile —
// путь от WriteValues (пусто — без -f).
func (m *Manager) HelmInstallCommands(ctx context.Context, r HelmInstallRequest, valuesFile string) ([][]string, error) {
	var out [][]string
	if !strings.HasPrefix(r.RepoURL, "oci://") {
		add, err := m.HelmArgv(ctx, "repo", "add", r.RepoName, r.RepoURL, "--force-update")
		if err != nil {
			return nil, err
		}
		upd, _ := m.HelmArgv(ctx, "repo", "update", r.RepoName)
		out = append(out, add, upd)
	}
	args := []string{"upgrade", "--install", r.Release, r.ChartRef(), "-n", r.Namespace, "--create-namespace", "--wait", "--timeout", "10m"}
	if r.Version != "" {
		args = append(args, "--version", r.Version)
	}
	if valuesFile != "" {
		args = append(args, "-f", valuesFile)
	}
	inst, err := m.HelmArgv(ctx, args...)
	if err != nil {
		return nil, err
	}
	return append(out, inst), nil
}

// HelmRollbackArgs — откат релиза на ревизию (0 — на предыдущую).
func (m *Manager) HelmRollbackArgs(ctx context.Context, r HelmRelease, revision int) ([]string, error) {
	args := []string{"rollback", r.Name}
	if revision > 0 {
		args = append(args, strconv.Itoa(revision))
	}
	return m.HelmArgv(ctx, append(args, "-n", r.Namespace, "--wait", "--timeout", "10m")...)
}

// HelmUninstallArgs — удаление релиза.
func (m *Manager) HelmUninstallArgs(ctx context.Context, r HelmRelease) ([]string, error) {
	return m.HelmArgv(ctx, "uninstall", r.Name, "-n", r.Namespace, "--wait", "--timeout", "10m")
}

// HelmSetupScript — установка helm: официальный архив нужной архитектуры
// в /usr/local/bin. Сценарий — константа, без значений из запроса.
const HelmSetupScript = `set -eu
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  armv7l|armv7*) arch=arm ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/helm.tgz" "https://get.helm.sh/helm-` + HelmVersion + `-linux-$arch.tar.gz"
tar -xzf "$tmp/helm.tgz" -C "$tmp"
install -m 0755 "$tmp/linux-$arch/helm" /usr/local/bin/helm
/usr/local/bin/helm version --short`

// HelmSource — откуда релиз ставился из nkt: для формы обновления.
type HelmSource struct {
	RepoName string `json:"repo_name"`
	RepoURL  string `json:"repo_url"`
	Chart    string `json:"chart"`
	Version  string `json:"version,omitempty"`
}

func (m *Manager) sourcePath(namespace, release string) string {
	return filepath.Join(m.helmDir(), "releases", namespace+"__"+release+".json")
}

// SaveSource запоминает репозиторий и чарт релиза.
func (m *Manager) SaveSource(r HelmInstallRequest) {
	p := m.sourcePath(r.Namespace, r.Release)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	b, _ := json.Marshal(HelmSource{RepoName: r.RepoName, RepoURL: r.RepoURL, Chart: r.Chart, Version: r.Version})
	_ = os.WriteFile(p, b, 0o600)
}

// Source — сохранённый источник релиза; нет — чарт из helm list.
func (m *Manager) Source(r HelmRelease) HelmSource {
	var src HelmSource
	if raw, err := os.ReadFile(m.sourcePath(r.Namespace, r.Name)); err == nil && json.Unmarshal(raw, &src) == nil {
		return src
	}
	return HelmSource{Chart: ChartName(r.Chart)}
}

// ChartName — имя чарта из «имя-версия» helm list.
func ChartName(chart string) string {
	for i := len(chart) - 1; i > 0; i-- {
		rest := strings.TrimPrefix(chart[i+1:], "v")
		if chart[i] == '-' && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
			return chart[:i]
		}
	}
	return chart
}
