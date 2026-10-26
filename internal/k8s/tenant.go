package k8s

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Пространство имён для чужого клиента (1.17.8): хаб заводит namespace с
// квотой, сетевой политикой и учётной записью (ServiceAccount) и отдаёт
// клиенту kubeconfig этой записи — права только в его namespace. Здесь —
// то, что для этого нужно на control plane.

var podNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

// NamespaceExists — есть ли namespace.
func (m *Manager) NamespaceExists(ctx context.Context, ns string) (bool, error) {
	if !dnsLabelRe.MatchString(ns) {
		return false, msgs.Errorf("k8s.badName", ns)
	}
	out, err := m.kubectl(ctx, "get", "namespace", ns, "-o", "name")
	if err != nil {
		return false, err
	}
	if out.OK() {
		return true, nil
	}
	if strings.Contains(strings.ToLower(out.Output()), "not found") {
		return false, nil
	}
	return false, msgs.Errorf("k8s.kubectl", strings.TrimSpace(out.Stderr))
}

// ServiceAccountKubeconfig — kubeconfig учётной записи namespace: адрес API
// и CA — из kubeconfig control plane (server — адрес снаружи, если задан),
// токен — из её секрета (kubernetes.io/service-account-token).
func (m *Manager) ServiceAccountKubeconfig(ctx context.Context, ns, secret, server string) (string, error) {
	if !dnsLabelRe.MatchString(ns) || !dnsLabelRe.MatchString(secret) {
		return "", msgs.Errorf("k8s.badName", ns+"/"+secret)
	}
	admin, err := m.Kubeconfig(ctx, server)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
				CA     string `yaml:"certificate-authority-data"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
	}
	if err := yaml.Unmarshal([]byte(admin), &cfg); err != nil || len(cfg.Clusters) == 0 || cfg.Clusters[0].Cluster.Server == "" {
		return "", msgs.Errorf("k8s.kubeconfigRead", fmt.Errorf("no cluster"))
	}
	out, err := m.kubectl(ctx, "get", "secret", secret, "-n", ns, "-o", "jsonpath={.data.token}")
	if err != nil {
		return "", err
	}
	if !out.OK() || strings.TrimSpace(out.Stdout) == "" {
		return "", msgs.Errorf("k8s.saTokenMissing", ns, secret)
	}
	tok, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.Stdout))
	if err != nil {
		return "", msgs.Errorf("k8s.saTokenMissing", ns, secret)
	}
	c := cfg.Clusters[0].Cluster
	doc := map[string]any{
		"apiVersion": "v1", "kind": "Config", "current-context": ns,
		"clusters": []any{map[string]any{"name": ns, "cluster": map[string]any{"server": c.Server, "certificate-authority-data": c.CA}}},
		"users":    []any{map[string]any{"name": ns, "user": map[string]any{"token": string(tok)}}},
		"contexts": []any{map[string]any{"name": ns, "context": map[string]any{"cluster": ns, "user": ns, "namespace": ns}}},
	}
	raw, err := yaml.Marshal(doc)
	return string(raw), err
}

// PodLogs — последние строки журнала пода (контейнер — пусто: первый).
func (m *Manager) PodLogs(ctx context.Context, ns, pod, container string, tail int) (string, error) {
	if !dnsLabelRe.MatchString(ns) || !podNameRe.MatchString(pod) || container != "" && !dnsLabelRe.MatchString(container) {
		return "", msgs.Errorf("k8s.badName", ns+"/"+pod)
	}
	if tail <= 0 || tail > 5000 {
		tail = 200
	}
	args := []string{"logs", pod, "-n", ns, "--tail", strconv.Itoa(tail)}
	if container != "" {
		args = append(args, "-c", container)
	}
	out, err := m.kubectl(ctx, args...)
	if err != nil {
		return "", err
	}
	if !out.OK() {
		return "", msgs.Errorf("k8s.kubectl", strings.TrimSpace(out.Stderr))
	}
	return out.Stdout, nil
}

// HelmTemplateArgv — helm template чарта без установки и без репозиториев
// хоста (--repo): манифесты отдаются хабу, он проверяет их правилами
// токена и применяет сам.
func (m *Manager) HelmTemplateArgv(ctx context.Context, r HelmInstallRequest, valuesFile string) ([]string, error) {
	if r.RepoName == "" {
		r.RepoName = "repo"
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	args := []string{"template", r.Release, "-n", r.Namespace}
	if strings.HasPrefix(r.RepoURL, "oci://") {
		args = append(args, r.ChartRef())
	} else {
		args = append(args, r.Chart, "--repo", r.RepoURL)
	}
	if r.Version != "" {
		args = append(args, "--version", r.Version)
	}
	if valuesFile != "" {
		args = append(args, "-f", valuesFile)
	}
	return m.HelmArgv(ctx, args...)
}
