package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Обзор объектов кластера: kubectl get <вид> -A -o json, разбор в строки
// с колонками по виду. Фильтр по namespace применяется здесь, после
// листинга: в команду не попадает ни одной строки из запроса — вид
// берётся из разрешённого списка, ресурс CRD — из списка CRD кластера.

// KindDef — разрешённый вид объектов.
type KindDef struct {
	Resource   string // что передать kubectl get
	Namespaced bool
	Columns    []string
}

// Kinds — встроенные виды, ключ — имя в API nkt.
var Kinds = map[string]KindDef{
	"deployments":    {"deployments.apps", true, []string{"ready", "up_to_date", "available", "images"}},
	"statefulsets":   {"statefulsets.apps", true, []string{"ready", "images"}},
	"daemonsets":     {"daemonsets.apps", true, []string{"desired", "current", "ready", "images"}},
	"jobs":           {"jobs.batch", true, []string{"completions", "duration", "state"}},
	"cronjobs":       {"cronjobs.batch", true, []string{"schedule", "suspend", "active", "last_schedule"}},
	"pods":           {"pods", true, []string{"phase", "ready", "restarts", "node", "ip"}},
	"services":       {"services", true, []string{"type", "cluster_ip", "external", "ports"}},
	"ingresses":      {"ingresses.networking.k8s.io", true, []string{"class", "hosts", "address", "tls"}},
	"configmaps":     {"configmaps", true, []string{"keys"}},
	"secrets":        {"secrets", true, []string{"type", "keys"}},
	"pvc":            {"persistentvolumeclaims", true, []string{"phase", "volume", "capacity", "access", "storage_class"}},
	"pv":             {"persistentvolumes", false, []string{"capacity", "access", "reclaim", "phase", "claim", "storage_class"}},
	"storageclasses": {"storageclasses.storage.k8s.io", false, []string{"provisioner", "reclaim", "binding", "default"}},
	"namespaces":     {"namespaces", false, []string{"phase"}},
	"nodes":          {"nodes", false, []string{"ready", "roles", "version", "ip"}},
	"events":         {"events", true, []string{"type", "reason", "object", "message", "count", "last_seen"}},
	// Доступ (RBAC): у ServiceAccount — привязанные роли («кто что может»).
	"serviceaccounts":     {"serviceaccounts", true, []string{"bound_roles"}},
	"roles":               {"roles.rbac.authorization.k8s.io", true, []string{"rules"}},
	"rolebindings":        {"rolebindings.rbac.authorization.k8s.io", true, []string{"role", "subjects"}},
	"clusterroles":        {"clusterroles.rbac.authorization.k8s.io", false, []string{"rules"}},
	"clusterrolebindings": {"clusterrolebindings.rbac.authorization.k8s.io", false, []string{"role", "subjects"}},
	"networkpolicies":     {"networkpolicies.networking.k8s.io", true, []string{"pod_selector", "policy_types", "ingress_rules", "egress_rules"}},
	"hpa":                 {"horizontalpodautoscalers.autoscaling", true, []string{"target", "min", "max", "current", "metrics"}},
}

// Column — колонка таблицы: ключ перевода или готовая подпись (у CRD).
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
}

// Row — объект в таблице.
type Row struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Created   string            `json:"created,omitempty"`
	Status    string            `json:"status,omitempty"` // ok | warn | error | ""
	Cols      map[string]string `json:"cols"`
}

// ResourceList — ответ /k8s/resources.
type ResourceList struct {
	Kind       string   `json:"kind"`
	Namespaced bool     `json:"namespaced"`
	Columns    []Column `json:"columns"`
	Rows       []Row    `json:"rows"`
}

// CRD — пользовательский вид из списка CRD кластера.
type CRD struct {
	Name       string   `json:"name"` // plural.group — что передать kubectl
	Group      string   `json:"group"`
	Kind       string   `json:"kind"`
	Plural     string   `json:"plural"`
	Namespaced bool     `json:"namespaced"`
	Version    string   `json:"version"`
	Columns    []Column `json:"columns"`
	paths      []string
}

type object = map[string]any

// get — значение по пути a.b.c (индексы массивов — числами).
func get(o any, path ...string) any {
	cur := o
	for _, p := range path {
		switch v := cur.(type) {
		case map[string]any:
			cur = v[p]
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			cur = v[i]
		default:
			return nil
		}
	}
	return cur
}

func str(o any, path ...string) string {
	switch v := get(o, path...).(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func num(o any, path ...string) int {
	if f, ok := get(o, path...).(float64); ok {
		return int(f)
	}
	return 0
}

func list(o any, path ...string) []any {
	l, _ := get(o, path...).([]any)
	return l
}

func keysOf(o any, path ...string) []string {
	m, _ := get(o, path...).(map[string]any)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// kubectlJSON — kubectl get <resource> -A -o json, список items.
func (m *Manager) kubectlJSON(ctx context.Context, resource string) ([]object, error) {
	out, err := m.kubectl(ctx, "get", resource, "-o", "json", "-A")
	if err != nil {
		return nil, err
	}
	if !out.OK() {
		return nil, msgs.Errorf("k8s.kubectl", strings.TrimSpace(out.Stderr))
	}
	var l struct {
		Items []object `json:"items"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &l); err != nil {
		return nil, msgs.Errorf("k8s.kubectl", err.Error())
	}
	return l.Items, nil
}

// Resources — строки вида kind, отфильтрованные по namespace (пусто — все).
func (m *Manager) Resources(ctx context.Context, kind, namespace string) (ResourceList, error) {
	if name, ok := strings.CutPrefix(kind, "cr:"); ok {
		return m.customResources(ctx, name, namespace)
	}
	def, ok := Kinds[kind]
	if !ok {
		return ResourceList{}, msgs.Errorf("k8s.badKind", kind)
	}
	items, err := m.kubectlJSON(ctx, def.Resource)
	if err != nil {
		return ResourceList{}, err
	}
	res := ResourceList{Kind: kind, Namespaced: def.Namespaced, Rows: []Row{}}
	for _, c := range def.Columns {
		res.Columns = append(res.Columns, Column{Key: c})
	}
	for _, it := range items {
		ns := str(it, "metadata", "namespace")
		if def.Namespaced && namespace != "" && ns != namespace {
			continue
		}
		row := Row{Name: str(it, "metadata", "name"), Namespace: ns, Created: str(it, "metadata", "creationTimestamp"), Cols: map[string]string{}}
		fillRow(kind, it, &row)
		res.Rows = append(res.Rows, row)
	}
	sort.SliceStable(res.Rows, func(i, j int) bool {
		if res.Rows[i].Namespace != res.Rows[j].Namespace {
			return res.Rows[i].Namespace < res.Rows[j].Namespace
		}
		return res.Rows[i].Name < res.Rows[j].Name
	})
	if kind == "pods" || kind == "nodes" {
		m.addTop(ctx, kind, &res)
	}
	if kind == "serviceaccounts" {
		m.addBoundRoles(ctx, &res)
	}
	if kind == "events" {
		// События — свежие сверху.
		sort.SliceStable(res.Rows, func(i, j int) bool { return res.Rows[i].Cols["last_seen"] > res.Rows[j].Cols["last_seen"] })
	}
	return res, nil
}

func images(it object, path ...string) string {
	var out []string
	for _, c := range list(it, path...) {
		out = append(out, str(c, "image"))
	}
	return strings.Join(out, ", ")
}

func ratio(ready, want int) (string, string) {
	st := "ok"
	if ready < want {
		st = "warn"
		if ready == 0 && want > 0 {
			st = "error"
		}
	}
	return fmt.Sprintf("%d/%d", ready, want), st
}

// fillRow — колонки и состояние строки по виду.
func fillRow(kind string, it object, row *Row) {
	c := row.Cols
	switch kind {
	case "deployments":
		want := num(it, "spec", "replicas")
		c["ready"], row.Status = ratio(num(it, "status", "readyReplicas"), want)
		c["up_to_date"] = strconv.Itoa(num(it, "status", "updatedReplicas"))
		c["available"] = strconv.Itoa(num(it, "status", "availableReplicas"))
		c["images"] = images(it, "spec", "template", "spec", "containers")
	case "statefulsets":
		c["ready"], row.Status = ratio(num(it, "status", "readyReplicas"), num(it, "spec", "replicas"))
		c["images"] = images(it, "spec", "template", "spec", "containers")
	case "daemonsets":
		desired := num(it, "status", "desiredNumberScheduled")
		c["desired"] = strconv.Itoa(desired)
		c["current"] = strconv.Itoa(num(it, "status", "currentNumberScheduled"))
		c["ready"], row.Status = ratio(num(it, "status", "numberReady"), desired)
		c["images"] = images(it, "spec", "template", "spec", "containers")
	case "jobs":
		want := num(it, "spec", "completions")
		if want == 0 {
			want = 1
		}
		c["completions"] = fmt.Sprintf("%d/%d", num(it, "status", "succeeded"), want)
		if st, fin := str(it, "status", "startTime"), str(it, "status", "completionTime"); st != "" && fin != "" {
			if a, err1 := time.Parse(time.RFC3339, st); err1 == nil {
				if b, err2 := time.Parse(time.RFC3339, fin); err2 == nil {
					c["duration"] = b.Sub(a).String()
				}
			}
		}
		switch {
		case num(it, "status", "failed") > 0:
			c["state"], row.Status = "Failed", "error"
		case num(it, "status", "succeeded") >= want:
			c["state"], row.Status = "Complete", "ok"
		default:
			c["state"], row.Status = "Running", "warn"
		}
	case "cronjobs":
		c["schedule"] = str(it, "spec", "schedule")
		c["suspend"] = str(it, "spec", "suspend")
		if c["suspend"] == "" {
			c["suspend"] = "false"
		}
		c["active"] = strconv.Itoa(len(list(it, "status", "active")))
		c["last_schedule"] = str(it, "status", "lastScheduleTime")
		row.Status = "ok"
		if c["suspend"] == "true" {
			row.Status = "warn"
		}
	case "pods":
		cs := list(it, "status", "containerStatuses")
		ready, restarts := 0, 0
		for _, s := range cs {
			if b, _ := get(s, "ready").(bool); b {
				ready++
			}
			restarts += num(s, "restartCount")
			if r := str(s, "state", "waiting", "reason"); r != "" {
				c["reason"] = r
			}
		}
		c["phase"] = str(it, "status", "phase")
		if c["reason"] != "" {
			c["phase"] += " (" + c["reason"] + ")"
		}
		delete(c, "reason")
		c["ready"] = fmt.Sprintf("%d/%d", ready, len(cs))
		c["restarts"] = strconv.Itoa(restarts)
		c["node"] = str(it, "spec", "nodeName")
		c["ip"] = str(it, "status", "podIP")
		switch phase := str(it, "status", "phase"); {
		case phase == "Succeeded" || (phase == "Running" && ready == len(cs)):
			row.Status = "ok"
		case phase == "Failed" || strings.Contains(c["phase"], "BackOff") || strings.Contains(c["phase"], "Err"):
			row.Status = "error"
		default:
			row.Status = "warn"
		}
	case "services":
		c["type"] = str(it, "spec", "type")
		c["cluster_ip"] = str(it, "spec", "clusterIP")
		var ext []string
		for _, i := range list(it, "status", "loadBalancer", "ingress") {
			ext = append(ext, str(i, "ip")+str(i, "hostname"))
		}
		for _, ip := range list(it, "spec", "externalIPs") {
			if s, ok := ip.(string); ok {
				ext = append(ext, s)
			}
		}
		c["external"] = strings.Join(ext, ", ")
		var ports []string
		for _, p := range list(it, "spec", "ports") {
			s := fmt.Sprintf("%d/%s", num(p, "port"), str(p, "protocol"))
			if np := num(p, "nodePort"); np > 0 {
				s = fmt.Sprintf("%d:%d/%s", num(p, "port"), np, str(p, "protocol"))
			}
			ports = append(ports, s)
		}
		c["ports"] = strings.Join(ports, ", ")
	case "ingresses":
		c["class"] = str(it, "spec", "ingressClassName")
		var hosts []string
		for _, r := range list(it, "spec", "rules") {
			if h := str(r, "host"); h != "" {
				hosts = append(hosts, h)
			}
		}
		c["hosts"] = strings.Join(hosts, ", ")
		var addr []string
		for _, i := range list(it, "status", "loadBalancer", "ingress") {
			addr = append(addr, str(i, "ip")+str(i, "hostname"))
		}
		c["address"] = strings.Join(addr, ", ")
		c["tls"] = strconv.FormatBool(len(list(it, "spec", "tls")) > 0)
	case "configmaps":
		c["keys"] = strings.Join(keysOf(it, "data"), ", ")
	case "secrets":
		// Только имена ключей: значения — отдельным запросом с аудитом.
		c["type"] = str(it, "type")
		c["keys"] = strings.Join(keysOf(it, "data"), ", ")
	case "pvc":
		c["phase"] = str(it, "status", "phase")
		c["volume"] = str(it, "spec", "volumeName")
		c["capacity"] = str(it, "status", "capacity", "storage")
		c["access"] = strings.Trim(str(it, "spec", "accessModes"), `[]"`)
		c["storage_class"] = str(it, "spec", "storageClassName")
		row.Status = map[string]string{"Bound": "ok", "Pending": "warn", "Lost": "error"}[c["phase"]]
	case "pv":
		c["capacity"] = str(it, "spec", "capacity", "storage")
		c["access"] = strings.Trim(str(it, "spec", "accessModes"), `[]"`)
		c["reclaim"] = str(it, "spec", "persistentVolumeReclaimPolicy")
		c["phase"] = str(it, "status", "phase")
		if ns, n := str(it, "spec", "claimRef", "namespace"), str(it, "spec", "claimRef", "name"); n != "" {
			c["claim"] = ns + "/" + n
		}
		c["storage_class"] = str(it, "spec", "storageClassName")
		row.Status = map[string]string{"Bound": "ok", "Available": "ok", "Released": "warn", "Failed": "error"}[c["phase"]]
	case "storageclasses":
		c["provisioner"] = str(it, "provisioner")
		c["reclaim"] = str(it, "reclaimPolicy")
		c["binding"] = str(it, "volumeBindingMode")
		c["default"] = strconv.FormatBool(str(it, "metadata", "annotations", "storageclass.kubernetes.io/is-default-class") == "true")
	case "namespaces":
		c["phase"] = str(it, "status", "phase")
		row.Status = map[string]string{"Active": "ok", "Terminating": "warn"}[c["phase"]]
	case "nodes":
		ready := false
		for _, cond := range list(it, "status", "conditions") {
			if str(cond, "type") == "Ready" {
				ready = str(cond, "status") == "True"
			}
		}
		c["ready"] = strconv.FormatBool(ready)
		row.Status = map[bool]string{true: "ok", false: "error"}[ready]
		var roles []string
		for _, l := range keysOf(it, "metadata", "labels") {
			if r, ok := strings.CutPrefix(l, "node-role.kubernetes.io/"); ok {
				roles = append(roles, r)
			}
		}
		c["roles"] = strings.Join(roles, ",")
		c["version"] = str(it, "status", "nodeInfo", "kubeletVersion")
		for _, a := range list(it, "status", "addresses") {
			if str(a, "type") == "InternalIP" {
				c["ip"] = str(a, "address")
			}
		}
	case "events":
		c["type"] = str(it, "type")
		c["reason"] = str(it, "reason")
		c["object"] = str(it, "involvedObject", "kind") + "/" + str(it, "involvedObject", "name")
		c["message"] = str(it, "message")
		c["count"] = strconv.Itoa(max(num(it, "count"), 1))
		c["last_seen"] = str(it, "lastTimestamp")
		if c["last_seen"] == "" {
			c["last_seen"] = str(it, "eventTime")
		}
		row.Status = map[string]string{"Normal": "ok", "Warning": "warn"}[c["type"]]
	case "roles", "clusterroles":
		c["rules"] = rulesSummary(it)
		if strings.Contains(c["rules"], "*: *") {
			row.Status = "warn"
		}
	case "rolebindings", "clusterrolebindings":
		c["role"] = str(it, "roleRef", "kind") + "/" + str(it, "roleRef", "name")
		c["subjects"] = strings.Join(subjects(it), ", ")
		if str(it, "roleRef", "name") == "cluster-admin" {
			row.Status = "warn"
		}
	case "networkpolicies":
		c["pod_selector"] = labelsText(get(it, "spec", "podSelector", "matchLabels"))
		var types []string
		for _, t := range list(it, "spec", "policyTypes") {
			if s, ok := t.(string); ok {
				types = append(types, s)
			}
		}
		c["policy_types"] = strings.Join(types, ", ")
		c["ingress_rules"] = strconv.Itoa(len(list(it, "spec", "ingress")))
		c["egress_rules"] = strconv.Itoa(len(list(it, "spec", "egress")))
	case "hpa":
		c["target"] = str(it, "spec", "scaleTargetRef", "kind") + "/" + str(it, "spec", "scaleTargetRef", "name")
		c["min"] = strconv.Itoa(max(num(it, "spec", "minReplicas"), 1))
		c["max"] = strconv.Itoa(num(it, "spec", "maxReplicas"))
		c["current"] = fmt.Sprintf("%d → %d", num(it, "status", "currentReplicas"), num(it, "status", "desiredReplicas"))
		var ms []string
		cur := list(it, "status", "currentMetrics")
		for i, m := range list(it, "spec", "metrics") {
			name := str(m, "resource", "name")
			target := str(m, "resource", "target", "averageUtilization")
			now := ""
			if i < len(cur) {
				now = str(cur[i], "resource", "current", "averageUtilization")
			}
			if name != "" {
				ms = append(ms, fmt.Sprintf("%s %s%%/%s%%", name, orDash(now), target))
			}
		}
		c["metrics"] = strings.Join(ms, "; ")
		if num(it, "status", "currentReplicas") >= num(it, "spec", "maxReplicas") && num(it, "spec", "maxReplicas") > 0 {
			row.Status = "warn"
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// rulesSummary — правила роли кратко: «ресурсы: глаголы; …».
func rulesSummary(it object) string {
	var parts []string
	for _, r := range list(it, "rules") {
		var res, verbs []string
		for _, x := range list(r, "resources") {
			if s, ok := x.(string); ok {
				res = append(res, s)
			}
		}
		for _, x := range list(r, "nonResourceURLs") {
			if s, ok := x.(string); ok {
				res = append(res, s)
			}
		}
		for _, x := range list(r, "verbs") {
			if s, ok := x.(string); ok {
				verbs = append(verbs, s)
			}
		}
		parts = append(parts, strings.Join(res, ",")+": "+strings.Join(verbs, ","))
	}
	return strings.Join(parts, "; ")
}

// subjects — «вид:ns/имя» субъектов привязки.
func subjects(it object) []string {
	var out []string
	for _, sub := range list(it, "subjects") {
		out = append(out, str(sub, "kind")+":"+strings.TrimPrefix(str(sub, "namespace")+"/"+str(sub, "name"), "/"))
	}
	return out
}

func labelsText(v any) string {
	m, _ := v.(map[string]any)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ",")
}

// ---------------------------------------------------------- Custom Resources

// jsonPathRe — простые пути printer columns: .spec.x, .status.y[0].z.
var jsonPathRe = regexp.MustCompile(`^\.[A-Za-z0-9_.\[\]-]+$`)

// CRDs — пользовательские виды кластера с их колонками для таблицы.
func (m *Manager) CRDs(ctx context.Context) ([]CRD, error) {
	items, err := m.kubectlJSON(ctx, "customresourcedefinitions.apiextensions.k8s.io")
	if err != nil {
		return nil, err
	}
	out := make([]CRD, 0, len(items))
	for _, it := range items {
		crd := CRD{
			Name: str(it, "metadata", "name"), Group: str(it, "spec", "group"),
			Kind: str(it, "spec", "names", "kind"), Plural: str(it, "spec", "names", "plural"),
			Namespaced: str(it, "spec", "scope") == "Namespaced",
		}
		// Колонки — из версии, которая хранится (storage), иначе первой.
		versions := list(it, "spec", "versions")
		var ver any
		for _, v := range versions {
			if b, _ := get(v, "storage").(bool); b {
				ver = v
			}
		}
		if ver == nil && len(versions) > 0 {
			ver = versions[0]
		}
		crd.Version = str(ver, "name")
		for _, pc := range list(ver, "additionalPrinterColumns") {
			path := str(pc, "jsonPath")
			name := str(pc, "name")
			if name == "" || name == "Age" || !jsonPathRe.MatchString(path) {
				continue
			}
			crd.Columns = append(crd.Columns, Column{Key: "pc" + strconv.Itoa(len(crd.Columns)), Label: name})
			crd.paths = append(crd.paths, path)
		}
		out = append(out, crd)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

// pathValue — значение простого jsonPath «.a.b[0].c».
func pathValue(it object, path string) string {
	var parts []string
	for _, seg := range strings.Split(strings.TrimPrefix(path, "."), ".") {
		name, idx, hasIdx := strings.Cut(seg, "[")
		if name != "" {
			parts = append(parts, name)
		}
		if hasIdx {
			parts = append(parts, strings.TrimSuffix(idx, "]"))
		}
	}
	return str(it, parts...)
}

func (m *Manager) customResources(ctx context.Context, name, namespace string) (ResourceList, error) {
	crds, err := m.CRDs(ctx)
	if err != nil {
		return ResourceList{}, err
	}
	var crd *CRD
	for i := range crds {
		if crds[i].Name == name {
			crd = &crds[i]
		}
	}
	if crd == nil {
		return ResourceList{}, msgs.Errorf("k8s.badKind", name)
	}
	// В команду идёт имя из списка CRD кластера, а не строка запроса.
	items, err := m.kubectlJSON(ctx, crd.Name)
	if err != nil {
		return ResourceList{}, err
	}
	res := ResourceList{Kind: "cr:" + crd.Name, Namespaced: crd.Namespaced, Columns: crd.Columns, Rows: []Row{}}
	for _, it := range items {
		ns := str(it, "metadata", "namespace")
		if crd.Namespaced && namespace != "" && ns != namespace {
			continue
		}
		row := Row{Name: str(it, "metadata", "name"), Namespace: ns, Created: str(it, "metadata", "creationTimestamp"), Cols: map[string]string{}}
		for i, p := range crd.paths {
			row.Cols[crd.Columns[i].Key] = pathValue(it, p)
		}
		// Состояние — по условию Ready, если оно есть.
		for _, cond := range list(it, "status", "conditions") {
			if str(cond, "type") == "Ready" {
				row.Status = map[string]string{"True": "ok", "False": "error", "Unknown": "warn"}[str(cond, "status")]
			}
		}
		res.Rows = append(res.Rows, row)
	}
	sort.SliceStable(res.Rows, func(i, j int) bool {
		if res.Rows[i].Namespace != res.Rows[j].Namespace {
			return res.Rows[i].Namespace < res.Rows[j].Namespace
		}
		return res.Rows[i].Name < res.Rows[j].Name
	})
	return res, nil
}

// ------------------------------------------------------- данные ConfigMap/Secret

// Data — ключи и значения ConfigMap или (декодированные) Secret. Объект
// ищется в листинге: namespace и имя из запроса только сравниваются.
func (m *Manager) Data(ctx context.Context, kind, namespace, name string) (map[string]string, error) {
	var resource string
	switch kind {
	case "configmaps":
		resource = "configmaps"
	case "secrets":
		resource = "secrets"
	default:
		return nil, msgs.Errorf("k8s.badKind", kind)
	}
	items, err := m.kubectlJSON(ctx, resource)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if str(it, "metadata", "namespace") != namespace || str(it, "metadata", "name") != name {
			continue
		}
		out := map[string]string{}
		data, _ := get(it, "data").(map[string]any)
		for k, v := range data {
			s, _ := v.(string)
			if kind == "secrets" {
				if b, err := base64.StdEncoding.DecodeString(s); err == nil {
					s = string(b)
				}
			}
			out[k] = s
		}
		if bin, ok := get(it, "binaryData").(map[string]any); ok {
			for k := range bin {
				out[k] = "(binary)"
			}
		}
		return out, nil
	}
	return nil, msgs.Errorf("k8s.notFound", namespace+"/"+name)
}

// addTop — колонки CPU и памяти из kubectl top (есть metrics-server —
// есть колонки; нет — таблица без них, без ошибки).
func (m *Manager) addTop(ctx context.Context, kind string, res *ResourceList) {
	args := []string{"top", "pods", "-A", "--no-headers"}
	if kind == "nodes" {
		args = []string{"top", "nodes", "--no-headers"}
	}
	out, err := m.kubectl(ctx, args...)
	if err != nil || !out.OK() {
		return
	}
	usage := ParseTop(kind, out.Stdout)
	if len(usage) == 0 {
		return
	}
	res.Columns = append(res.Columns, Column{Key: "cpu"}, Column{Key: "memory"})
	for i := range res.Rows {
		r := &res.Rows[i]
		if u, ok := usage[r.Namespace+"/"+r.Name]; ok {
			r.Cols["cpu"], r.Cols["memory"] = u[0], u[1]
		}
	}
}

// ParseTop — kubectl top … --no-headers: «ns/имя» (у узлов — «/имя») →
// CPU и память; у узлов с долей от ёмкости в скобках.
func ParseTop(kind, text string) map[string][2]string {
	out := map[string][2]string{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		switch {
		case kind == "pods" && len(f) >= 4:
			out[f[0]+"/"+f[1]] = [2]string{f[2], f[3]}
		case kind == "nodes" && len(f) >= 5:
			out["/"+f[0]] = [2]string{f[1] + " (" + f[2] + ")", f[3] + " (" + f[4] + ")"}
		}
	}
	return out
}

// addBoundRoles — у ServiceAccount: роли из RoleBinding и
// ClusterRoleBinding, где он субъект («кто что может»).
func (m *Manager) addBoundRoles(ctx context.Context, res *ResourceList) {
	bound := map[string][]string{}
	for _, resource := range []string{"rolebindings.rbac.authorization.k8s.io", "clusterrolebindings.rbac.authorization.k8s.io"} {
		items, err := m.kubectlJSON(ctx, resource)
		if err != nil {
			continue
		}
		for _, it := range items {
			role := str(it, "roleRef", "name")
			where := str(it, "metadata", "namespace")
			if where == "" {
				role += " (cluster)"
			}
			for _, sub := range list(it, "subjects") {
				if str(sub, "kind") != "ServiceAccount" {
					continue
				}
				ns := str(sub, "namespace")
				if ns == "" {
					ns = where
				}
				key := ns + "/" + str(sub, "name")
				bound[key] = append(bound[key], role)
			}
		}
	}
	for i := range res.Rows {
		r := &res.Rows[i]
		roles := bound[r.Namespace+"/"+r.Name]
		sort.Strings(roles)
		r.Cols["bound_roles"] = strings.Join(roles, ", ")
		for _, role := range roles {
			if strings.HasPrefix(role, "cluster-admin") {
				r.Status = "warn"
			}
		}
	}
}
