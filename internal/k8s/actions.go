package k8s

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/msgs"
)

// Действия с объектами кластера. Правило то же, что у листинга: в команду
// не идёт ни одной строки из запроса. Вид — из разрешённого списка (или
// ресурс CRD из списка CRD кластера), имя и namespace — из листинга
// kubectl get, запрос с ними только сравнивается (Find). Новое имя
// (namespace при создании) проверяется по правилам DNS-1123.

// Target — объект, найденный в листинге кластера.
type Target struct {
	Kind      string // ключ nkt: deployments, pods, cr:<plural.group>
	Resource  string // что передать kubectl
	Namespace string
	Name      string
	Item      map[string]any
}

// Ref — «ресурс/имя» для rollout и create --from.
func (t Target) Ref() string { return t.Resource + "/" + t.Name }

// nsArgs — -n <namespace> для объектов в namespace.
func (t Target) nsArgs() []string {
	if t.Namespace == "" {
		return nil
	}
	return []string{"-n", t.Namespace}
}

// Find ищет объект в листинге: строки Target — из вывода kubectl.
func (m *Manager) Find(ctx context.Context, kind, namespace, name string) (Target, error) {
	resource := ""
	if crd, ok := strings.CutPrefix(kind, "cr:"); ok {
		crds, err := m.CRDs(ctx)
		if err != nil {
			return Target{}, err
		}
		for _, c := range crds {
			if c.Name == crd {
				resource = c.Name
			}
		}
	} else if def, ok := Kinds[kind]; ok {
		resource = def.Resource
	}
	if resource == "" {
		return Target{}, msgs.Errorf("k8s.badKind", kind)
	}
	items, err := m.kubectlJSON(ctx, resource)
	if err != nil {
		return Target{}, err
	}
	for _, it := range items {
		ns, n := str(it, "metadata", "namespace"), str(it, "metadata", "name")
		if ns == namespace && n == name {
			return Target{Kind: kind, Resource: resource, Namespace: ns, Name: n, Item: it}, nil
		}
	}
	return Target{}, msgs.Errorf("k8s.notFound", strings.TrimPrefix(namespace+"/"+name, "/"))
}

// Containers — контейнеры пода (сначала init), имена из спецификации.
func (t Target) Containers() []string {
	var out []string
	for _, path := range [][]string{{"spec", "initContainers"}, {"spec", "containers"}} {
		for _, c := range list(t.Item, path...) {
			if n := str(c, "name"); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// Container — контейнер пода из спецификации, равный запрошенному (пусто —
// первый обычный контейнер).
func (t Target) Container(want string) (string, bool) {
	all := t.Containers()
	if want == "" {
		main := list(t.Item, "spec", "containers")
		if len(main) > 0 {
			return str(main[0], "name"), true
		}
		return "", false
	}
	for _, c := range all {
		if c == want {
			return c, true
		}
	}
	return "", false
}

// KubectlArgv — полная команда kubectl с kubeconfig control plane: для
// заданий и PTY-сессий, которые запускает API.
func (m *Manager) KubectlArgv(ctx context.Context, args ...string) ([]string, error) {
	switch m.Status(ctx).Flavor {
	case FlavorK3s:
		return append([]string{"k3s", "kubectl"}, args...), nil
	case FlavorKubeadm:
		return append([]string{"kubectl", "--kubeconfig", "/etc/kubernetes/admin.conf"}, args...), nil
	}
	return nil, msgs.Errorf("k8s.notInstalled")
}

// mutate — изменяющая команда kubectl: вне песочницы (как удаление роли),
// в fixtures — через коллектор, где она имитируется.
func (m *Manager) mutate(ctx context.Context, args ...string) (string, error) {
	argv, err := m.KubectlArgv(ctx, args...)
	if err != nil {
		return "", err
	}
	var out collect.CommandResult
	if m.run != nil {
		out, err = m.run(ctx, argv...)
	} else {
		out, err = m.c.Run(ctx, argv[0], argv[1:]...)
	}
	if err != nil {
		return "", err
	}
	if !out.OK() {
		return "", msgs.Errorf("k8s.kubectl", strings.TrimSpace(out.Stderr+"\n"+out.Stdout))
	}
	return strings.TrimSpace(out.Stdout), nil
}

// read — читающая команда kubectl, вывод как текст.
func (m *Manager) read(ctx context.Context, args ...string) (string, error) {
	out, err := m.kubectl(ctx, args...)
	if err != nil {
		return "", err
	}
	if !out.OK() {
		return "", msgs.Errorf("k8s.kubectl", strings.TrimSpace(out.Stderr))
	}
	return out.Stdout, nil
}

// Actions — какие действия доступны виду (в интерфейсе — те же).
var Actions = map[string][]string{
	"deployments":         {"scale", "restart", "undo", "delete"},
	"statefulsets":        {"scale", "restart", "undo", "delete"},
	"daemonsets":          {"restart", "undo", "delete"},
	"jobs":                {"delete"},
	"cronjobs":            {"trigger", "suspend", "resume", "delete"},
	"pods":                {"delete"},
	"services":            {"delete"},
	"ingresses":           {"delete"},
	"configmaps":          {"delete"},
	"secrets":             {"delete"},
	"pvc":                 {"delete"},
	"nodes":               {"cordon", "uncordon"},
	"namespaces":          {"delete"},
	"serviceaccounts":     {"delete"},
	"roles":               {"delete"},
	"rolebindings":        {"delete"},
	"clusterroles":        {"delete"},
	"clusterrolebindings": {"delete"},
	"networkpolicies":     {"delete"},
	"hpa":                 {"bounds", "delete"},
}

// ActionRequest — POST /k8s/objects/action.
type ActionRequest struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	Replicas  int    `json:"replicas"`
	Revision  int    `json:"revision"`
	// Min, Max — границы HPA.
	Min int `json:"min"`
	Max int `json:"max"`
}

// MaxReplicas — предел масштабирования из интерфейса.
const MaxReplicas = 1000

// Act выполняет действие и возвращает вывод kubectl.
func (m *Manager) Act(ctx context.Context, req ActionRequest) (string, error) {
	allowed := Actions[req.Kind]
	if strings.HasPrefix(req.Kind, "cr:") {
		allowed = []string{"delete"}
	}
	if !slices.Contains(allowed, req.Action) {
		return "", msgs.Errorf("k8s.badAction", req.Action, req.Kind)
	}
	t, err := m.Find(ctx, req.Kind, req.Namespace, req.Name)
	if err != nil {
		return "", err
	}
	ns := t.nsArgs()
	switch req.Action {
	case "delete":
		return m.mutate(ctx, append([]string{"delete", t.Resource, t.Name, "--wait=false"}, ns...)...)
	case "scale":
		if req.Replicas < 0 || req.Replicas > MaxReplicas {
			return "", msgs.Errorf("k8s.badReplicas", MaxReplicas)
		}
		return m.mutate(ctx, append([]string{"scale", t.Ref(), "--replicas=" + strconv.Itoa(req.Replicas)}, ns...)...)
	case "restart":
		return m.mutate(ctx, append([]string{"rollout", "restart", t.Ref()}, ns...)...)
	case "undo":
		args := []string{"rollout", "undo", t.Ref()}
		if req.Revision > 0 {
			args = append(args, "--to-revision="+strconv.Itoa(req.Revision))
		}
		return m.mutate(ctx, append(args, ns...)...)
	case "cordon", "uncordon":
		return m.mutate(ctx, req.Action, t.Name)
	case "trigger":
		return m.mutate(ctx, append([]string{"create", "job", ManualJobName(t.Name, time.Now()), "--from=cronjob/" + t.Name}, ns...)...)
	case "bounds":
		if req.Min < 1 || req.Max < req.Min || req.Max > MaxReplicas {
			return "", msgs.Errorf("k8s.badBounds", MaxReplicas)
		}
		patch := fmt.Sprintf(`{"spec":{"minReplicas":%d,"maxReplicas":%d}}`, req.Min, req.Max)
		return m.mutate(ctx, append([]string{"patch", t.Resource, t.Name, "--type=merge", "-p", patch}, ns...)...)
	case "suspend", "resume":
		patch := fmt.Sprintf(`{"spec":{"suspend":%t}}`, req.Action == "suspend")
		return m.mutate(ctx, append([]string{"patch", t.Resource, t.Name, "--type=merge", "-p", patch}, ns...)...)
	}
	return "", msgs.Errorf("k8s.badAction", req.Action, req.Kind)
}

// ManualJobName — имя задания «запустить сейчас» из CronJob: как у
// kubectl create job --from, с отметкой времени; не длиннее 63 символов.
func ManualJobName(cron string, now time.Time) string {
	suffix := "-manual-" + strconv.FormatInt(now.Unix()%1000000, 10)
	if len(cron)+len(suffix) > 63 {
		cron = strings.TrimRight(cron[:63-len(suffix)], "-.")
	}
	return cron + suffix
}

var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// CreateNamespace — kubectl create namespace NAME (имя по DNS-1123).
func (m *Manager) CreateNamespace(ctx context.Context, name string) (string, error) {
	if !dnsLabelRe.MatchString(name) {
		return "", msgs.Errorf("k8s.badName", name)
	}
	return m.mutate(ctx, "create", "namespace", name)
}

// Describe — kubectl describe объекта (с событиями в конце).
func (m *Manager) Describe(ctx context.Context, kind, namespace, name string) (string, error) {
	t, err := m.Find(ctx, kind, namespace, name)
	if err != nil {
		return "", err
	}
	return m.read(ctx, append([]string{"describe", t.Resource, t.Name}, t.nsArgs()...)...)
}

// RolloutHistory — kubectl rollout history (ревизии для отката).
func (m *Manager) RolloutHistory(ctx context.Context, kind, namespace, name string) (string, error) {
	if !slices.Contains(Actions[kind], "undo") {
		return "", msgs.Errorf("k8s.badAction", "history", kind)
	}
	t, err := m.Find(ctx, kind, namespace, name)
	if err != nil {
		return "", err
	}
	return m.read(ctx, append([]string{"rollout", "history", t.Ref()}, t.nsArgs()...)...)
}

// DrainArgs — аргументы kubectl drain для задания (узел — из листинга).
func DrainArgs(node string) []string {
	return []string{"drain", node, "--ignore-daemonsets", "--delete-emptydir-data", "--timeout=300s"}
}

// LogsArgs — kubectl logs пода: хвост, слежение, предыдущий запуск.
func LogsArgs(t Target, container string, tail int, follow, previous bool) []string {
	args := []string{"logs", t.Name, "-n", t.Namespace, "-c", container, "--tail=" + strconv.Itoa(tail), "--timestamps"}
	if follow {
		args = append(args, "-f")
	}
	if previous {
		args = append(args, "--previous")
	}
	return args
}

// ExecArgs — kubectl exec в контейнер пода: bash, если есть, иначе sh.
func ExecArgs(t Target, container string) []string {
	return []string{"exec", "-it", t.Name, "-n", t.Namespace, "-c", container, "--",
		"sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}
}
