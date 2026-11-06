package api

import (
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/k8s"
)

// Шаги задания собираются отдельной функцией именно затем, чтобы их
// можно было проверить без сервера и без ssh: без кэша хаба путь обязан
// остаться прежним (два шага, ничего про кэш), с кэхом — чарт приезжает
// первым и опциональным шагом, а установка helm по-прежнему говорит с
// API кластера напрямую.
func TestAddonInstallCommandsCache(t *testing.T) {
	const cache = "http://127.0.0.1:3142"
	helm := []string{"helm", "--kubeconfig", "/etc/rancher/k3s/k3s.yaml"}
	req := k8s.AddonRequest{Action: k8s.AddonInstall, Name: k8s.AddonLonghorn, Version: "1.12.1", Replicas: 2, Default: true}

	direct := addonInstallCommands("", true, k8s.FlavorK3s, helm, req)
	if len(direct) != 2 {
		t.Fatalf("без кэха шагов %d: %+v", len(direct), direct)
	}
	if direct[0].StepKey != "k8s.helmStep.setup" || direct[1].StepKey != "k8s.addonInstallStep" {
		t.Errorf("порядок шагов: %+v", direct)
	}
	for _, c := range direct {
		if c.Argv != nil {
			t.Errorf("шаг %s обязан быть сценарием, а не argv'ом", c.StepKey)
		}
		if strings.Contains(c.Script, "HTTPS_PROXY") || strings.Contains(c.Script, "NKT_CACHE") {
			t.Errorf("без кэха в шаге %s кэш появился ниоткуда:\n%s", c.StepKey, c.Script)
		}
	}

	cached := addonInstallCommands(cache, false, k8s.FlavorK3s, helm, req)
	if len(cached) != 2 {
		t.Fatalf("с кэхом шагов %d: %+v", len(cached), cached)
	}
	if cached[0].StepKey != "k8s.addonChartStep" || !cached[0].Optional {
		t.Errorf("шаг чарта: %+v", cached[0])
	}
	if !strings.Contains(cached[0].Script, "NKT_CACHE="+cache) {
		t.Errorf("шаг чарта не качает через кэш:\n%s", cached[0].Script)
	}
	if cached[1].StepKey != "k8s.addonInstallStep" || !strings.Contains(cached[1].Script, "$vflags") {
		t.Errorf("шаг установки: %+v", cached[1])
	}
	if a := cached[0].StepArgs; len(a) != 2 || a[0] != "longhorn" || a[1] != "1.12.1" {
		t.Errorf("подпись шага чарта: %v", a)
	}
}

// Под прокси только команды, которые качают индекс репозитория: шаг
// установки остаётся argv'ом без окружения, иначе go-клиент helm увёл бы
// запрос к API кластера через хаб.
func TestHelmJobCommandsProxyRepo(t *testing.T) {
	const cache = "http://127.0.0.1:3142"
	argvs := [][]string{
		{"helm", "repo", "add", "bitnami", "https://charts.bitnami.com/bitnami"},
		{"helm", "repo", "update", "bitnami"},
		{"helm", "upgrade", "--install", "cache", "bitnami/redis", "-n", "shop"},
	}
	c := helmJobCommands(cache, argvs, "cache", "bitnami")
	if len(c) != 3 {
		t.Fatalf("шагов %d: %+v", len(c), c)
	}
	if c[0].Argv != nil || !strings.Contains(c[0].Script, "export HTTPS_PROXY='"+cache+"'") ||
		!strings.Contains(c[0].Script, "exec 'helm' 'repo' 'add' 'bitnami'") {
		t.Errorf("repo add: %+v\n%s", c[0], c[0].Script)
	}
	if c[1].Argv != nil || !strings.Contains(c[1].Script, "exec 'helm' 'repo' 'update' 'bitnami'") {
		t.Errorf("repo update: %+v\n%s", c[1], c[1].Script)
	}
	if c[0].StepKey != "k8s.helmStep.repoAdd" || c[1].StepKey != "k8s.helmStep.repoUpdate" {
		t.Errorf("названия шагов репозитория: %+v", c[:2])
	}
	if c[2].Script != "" || strings.Join(c[2].Argv, " ") != "helm upgrade --install cache bitnami/redis -n shop" {
		t.Errorf("установка обязана остаться argv'ом без окружения: %+v %s", c[2], c[2].Script)
	}
	if c[2].StepKey != "k8s.helmStep.install" {
		t.Errorf("шаг установки: %+v", c[2])
	}
	for i, x := range helmJobCommands("", argvs, "cache", "bitnami") {
		if x.Script != "" || len(x.Argv) == 0 {
			t.Errorf("без кэха шаг %d идёт как раньше, argv'ом: %+v", i, x)
		}
	}
}
