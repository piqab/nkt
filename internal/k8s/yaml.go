package k8s

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/msgs"
)

// YAML объектов: просмотр, дифф с кластером (kubectl diff — сервер
// применяет изменения «насухо» и сравнивает), запись kubectl apply.
// Текст для правки строится из того же листинга, что и таблица (Find):
// служебные поля (managedFields, resourceVersion, uid, status…) убраны,
// ключи по алфавиту — как у kubectl get -o yaml. Секреты здесь не
// показываются: их значения — только через «показать» с аудитом, а
// история версий nkt хранила бы их открытым текстом.
//
// Файл для kubectl — во временном каталоге данных nkt (RunDir), не в
// /tmp: под PrivateTmp у службы свой /tmp, и запущенный вне песочницы
// kubectl его бы не увидел.

// apiKinds — apiVersion и Kind встроенных видов nkt.
var apiKinds = map[string][2]string{
	"deployments":         {"apps/v1", "Deployment"},
	"statefulsets":        {"apps/v1", "StatefulSet"},
	"daemonsets":          {"apps/v1", "DaemonSet"},
	"jobs":                {"batch/v1", "Job"},
	"cronjobs":            {"batch/v1", "CronJob"},
	"pods":                {"v1", "Pod"},
	"services":            {"v1", "Service"},
	"ingresses":           {"networking.k8s.io/v1", "Ingress"},
	"configmaps":          {"v1", "ConfigMap"},
	"secrets":             {"v1", "Secret"},
	"pvc":                 {"v1", "PersistentVolumeClaim"},
	"pv":                  {"v1", "PersistentVolume"},
	"storageclasses":      {"storage.k8s.io/v1", "StorageClass"},
	"namespaces":          {"v1", "Namespace"},
	"nodes":               {"v1", "Node"},
	"events":              {"v1", "Event"},
	"serviceaccounts":     {"v1", "ServiceAccount"},
	"roles":               {"rbac.authorization.k8s.io/v1", "Role"},
	"rolebindings":        {"rbac.authorization.k8s.io/v1", "RoleBinding"},
	"clusterroles":        {"rbac.authorization.k8s.io/v1", "ClusterRole"},
	"clusterrolebindings": {"rbac.authorization.k8s.io/v1", "ClusterRoleBinding"},
	"networkpolicies":     {"networking.k8s.io/v1", "NetworkPolicy"},
	"hpa":                 {"autoscaling/v2", "HorizontalPodAutoscaler"},
}

// KindKey — ключ nkt по Kind из манифеста (Deployment → deployments).
func KindKey(apiKind string) string {
	for k, v := range apiKinds {
		if v[1] == apiKind {
			return k
		}
	}
	return ""
}

// DocPath — путь объекта в истории версий: k8s://ns/kind/name для
// объектов в namespace, k8s://kind/name — для кластерных.
func DocPath(kind, namespace, name string) string {
	if namespace == "" {
		return "k8s://" + kind + "/" + name
	}
	return "k8s://" + namespace + "/" + kind + "/" + name
}

// ParseDocPath — обратное DocPath.
func ParseDocPath(path string) (kind, namespace, name string, ok bool) {
	rest, ok := strings.CutPrefix(path, "k8s://")
	if !ok {
		return "", "", "", false
	}
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 2:
		kind, name = parts[0], parts[1]
	case 3:
		namespace, kind, name = parts[0], parts[1], parts[2]
	default:
		return "", "", "", false
	}
	if kind == "" || name == "" || kind == "secrets" {
		return "", "", "", false
	}
	return kind, namespace, name, true
}

// Doc — YAML объекта для окна правки.
type Doc struct {
	Content     string `json:"content"`
	SHA256      string `json:"sha256"`
	HistoryPath string `json:"history_path"`
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// YAML — текст объекта для правки (без служебных полей).
func (m *Manager) YAML(ctx context.Context, kind, namespace, name string) (Doc, error) {
	if kind == "secrets" {
		return Doc{}, msgs.Errorf("k8s.secretYAML")
	}
	t, err := m.Find(ctx, kind, namespace, name)
	if err != nil {
		return Doc{}, err
	}
	if av, ok := apiKinds[kind]; ok {
		// В элементах списка kubectl их проставляет сам, но не всегда.
		if str(t.Item, "apiVersion") == "" {
			t.Item["apiVersion"] = av[0]
		}
		if str(t.Item, "kind") == "" {
			t.Item["kind"] = av[1]
		}
	} else if crd, ok := strings.CutPrefix(kind, "cr:"); ok && str(t.Item, "kind") == "" {
		crds, _ := m.CRDs(ctx)
		for _, c := range crds {
			if c.Name == crd {
				t.Item["apiVersion"] = c.Group + "/" + c.Version
				t.Item["kind"] = c.Kind
			}
		}
	}
	text, err := CleanYAML(t.Item)
	if err != nil {
		return Doc{}, err
	}
	return Doc{Content: text, SHA256: sha(text), HistoryPath: DocPath(t.Kind, t.Namespace, t.Name)}, nil
}

// CleanYAML — объект в YAML без полей, которые ставит сам кластер.
func CleanYAML(item map[string]any) (string, error) {
	obj := make(map[string]any, len(item))
	for k, v := range item {
		if k != "status" {
			obj[k] = v
		}
	}
	if md, ok := obj["metadata"].(map[string]any); ok {
		clean := map[string]any{}
		for k, v := range md {
			switch k {
			case "managedFields", "resourceVersion", "uid", "generation", "creationTimestamp", "selfLink":
				continue
			case "annotations":
				ann, _ := v.(map[string]any)
				rest := map[string]any{}
				for ak, av := range ann {
					if ak != "kubectl.kubernetes.io/last-applied-configuration" && ak != "deployment.kubernetes.io/revision" {
						rest[ak] = av
					}
				}
				if len(rest) > 0 {
					clean[k] = rest
				}
				continue
			}
			clean[k] = v
		}
		obj["metadata"] = clean
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(obj); err != nil {
		return "", err
	}
	_ = enc.Close()
	return buf.String(), nil
}

// DocID — объект манифеста: вид, namespace, имя.
type DocID struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

// MaxManifest — предел размера манифеста из окна.
const MaxManifest = 1 << 20

// ParseManifest — объекты многодокументного YAML; у каждого должны быть
// apiVersion, kind и metadata.name.
func ParseManifest(content string) ([]DocID, error) {
	if len(content) > MaxManifest {
		return nil, msgs.Errorf("k8s.manifestTooBig", MaxManifest>>10)
	}
	dec := yaml.NewDecoder(strings.NewReader(content))
	var out []DocID
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, msgs.Errorf("k8s.manifestYAML", err.Error())
		}
		if doc == nil {
			continue
		}
		id := DocID{APIVersion: str(doc, "apiVersion"), Kind: str(doc, "kind"), Namespace: str(doc, "metadata", "namespace"), Name: str(doc, "metadata", "name")}
		if id.APIVersion == "" || id.Kind == "" || id.Name == "" {
			return nil, msgs.Errorf("k8s.manifestIncomplete", len(out)+1)
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, msgs.Errorf("k8s.manifestEmpty")
	}
	return out, nil
}

// WithRunDir — каталог для временных файлов манифестов (в данных nkt).
func (m *Manager) WithRunDir(dir string) *Manager {
	m.runDir = dir
	return m
}

// withManifest пишет манифест во временный файл и вызывает fn с его путём.
func (m *Manager) withManifest(content string, fn func(path string) (string, error)) (string, error) {
	dir := m.runDir
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "k8s-manifest-*.yaml")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return fn(f.Name())
}

// Diff — kubectl diff манифеста с кластером (пусто — отличий нет).
// namespace — для правки существующего объекта (иначе из манифеста).
func (m *Manager) Diff(ctx context.Context, content, namespace string) (string, error) {
	if _, err := ParseManifest(content); err != nil {
		return "", err
	}
	return m.withManifest(content, func(path string) (string, error) {
		args := []string{"diff", "-f", path}
		if namespace != "" {
			args = append(args, "-n", namespace)
		}
		argv, err := m.KubectlArgv(ctx, args...)
		if err != nil {
			return "", err
		}
		// Сравнение — тот же запрос dry-run, что и у apply: вне песочницы,
		// с тем же kubeconfig.
		var out collect.CommandResult
		if m.run != nil {
			out, err = m.run(ctx, argv...)
		} else {
			out, err = m.c.Run(ctx, argv[0], argv[1:]...)
		}
		if err != nil {
			return "", err
		}
		// 0 — отличий нет, 1 — есть, больше — ошибка.
		if out.ExitCode > 1 {
			return "", msgs.Errorf("k8s.kubectl", strings.TrimSpace(out.Stderr+"\n"+out.Stdout))
		}
		return out.Stdout, nil
	})
}

// Apply — kubectl apply манифеста.
func (m *Manager) Apply(ctx context.Context, content, namespace string) (string, error) {
	if _, err := ParseManifest(content); err != nil {
		return "", err
	}
	return m.withManifest(content, func(path string) (string, error) {
		args := []string{"apply", "-f", path}
		if namespace != "" {
			args = append(args, "-n", namespace)
		}
		return m.mutate(ctx, args...)
	})
}

// CheckIdentity — манифест правки описывает ровно этот объект.
func (m *Manager) CheckIdentity(ctx context.Context, t Target, content string) error {
	docs, err := ParseManifest(content)
	if err != nil {
		return err
	}
	if len(docs) != 1 {
		return msgs.Errorf("k8s.manifestOneObject")
	}
	d := docs[0]
	wantKind := ""
	if av, ok := apiKinds[t.Kind]; ok {
		wantKind = av[1]
	} else if crd, ok := strings.CutPrefix(t.Kind, "cr:"); ok {
		crds, _ := m.CRDs(ctx)
		for _, c := range crds {
			if c.Name == crd {
				wantKind = c.Kind
			}
		}
	}
	ns := d.Namespace
	if ns == "" {
		ns = t.Namespace
	}
	if d.Kind != wantKind || d.Name != t.Name || ns != t.Namespace {
		return msgs.Errorf("k8s.manifestOtherObject", d.Kind+" "+strings.TrimPrefix(d.Namespace+"/"+d.Name, "/"))
	}
	return nil
}
