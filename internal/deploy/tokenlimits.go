package deploy

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Потолки токена «по правам» для compose-стека: что сервис просит
// (deploy.resources.limits, cpus/mem_limit), потолок на сервис — тем,
// кто своих limits не задал, и запрет того, что выходит за стек:
// privileged, сеть и pid хоста, добавленные capability, устройства,
// монтирование путей хоста (кроме ./ внутри каталога стека) и сокета
// docker. Файл в репозитории не меняется — меняется копия, что едет на
// хост.

// ServiceCaps — потолок одного сервиса (0 — без потолка).
type ServiceCaps struct {
	CPU   float64
	MemMB int64
}

// StackNeed — сколько стек займёт: сумма по сервисам с учётом replicas.
type StackNeed struct {
	CPU   float64
	MemMB int64
	// Unlimited — сервисы без limits при отсутствии потолка (их не
	// посчитать).
	Unlimited []string
}

var memRe = regexp.MustCompile(`^(?i)\s*([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?)i?b?\s*$`)

// parseMemMB — «512M», «1g», «1073741824» → МБ.
func parseMemMB(s string) (int64, bool) {
	m := memRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(m[2]) {
	case "":
		v /= 1 << 20
	case "k":
		v /= 1 << 10
	case "g":
		v *= 1 << 10
	case "t":
		v *= 1 << 20
	}
	return int64(math.Ceil(v)), true
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// ApplyTokenLimits проверяет и дополняет compose-файл под потолки токена.
// project — имя стека (./… внутри его каталога разрешены).
func ApplyTokenLimits(text string, caps ServiceCaps) (string, StackNeed, error) {
	var need StackNeed
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return "", need, msgs.Errorf("deploy.composeYAML", err.Error())
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", need, msgs.Errorf("deploy.composeYAML", "not a mapping")
	}
	services := mapValue(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return text, need, nil
	}
	for i := 0; i+1 < len(services.Content); i += 2 {
		name, svc := services.Content[i].Value, services.Content[i+1]
		if svc.Kind != yaml.MappingNode {
			continue
		}
		if err := checkServiceSafety(name, svc); err != nil {
			return "", need, err
		}
		cpu, mem := serviceLimits(svc)
		replicas := 1
		if dep := mapValue(svc, "deploy"); dep != nil {
			if r, err := strconv.Atoi(scalar(mapValue(dep, "replicas"))); err == nil && r > 0 {
				replicas = r
			}
		}
		if caps.CPU > 0 && cpu > caps.CPU+1e-9 {
			return "", need, msgs.Errorf("deploy.tokenServiceCPU", name, cpu, caps.CPU)
		}
		if caps.MemMB > 0 && mem > caps.MemMB {
			return "", need, msgs.Errorf("deploy.tokenServiceMem", name, mem, caps.MemMB)
		}
		// Своих limits нет — потолок токена; без потолка сервис не
		// посчитать.
		if cpu == 0 && caps.CPU > 0 {
			cpu = caps.CPU
			setLimit(svc, "cpus", strconv.FormatFloat(caps.CPU, 'f', -1, 64))
		}
		if mem == 0 && caps.MemMB > 0 {
			mem = caps.MemMB
			setLimit(svc, "memory", fmt.Sprintf("%dM", caps.MemMB))
		}
		if cpu == 0 || mem == 0 {
			need.Unlimited = append(need.Unlimited, name)
		}
		need.CPU += cpu * float64(replicas)
		need.MemMB += mem * int64(replicas)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", need, err
	}
	return string(out), need, nil
}

// serviceLimits — limits сервиса: deploy.resources.limits, иначе cpus и
// mem_limit верхнего уровня.
func serviceLimits(svc *yaml.Node) (float64, int64) {
	var cpu float64
	var mem int64
	if lim := mapValue(mapValue(mapValue(svc, "deploy"), "resources"), "limits"); lim != nil {
		cpu, _ = strconv.ParseFloat(strings.TrimSpace(scalar(mapValue(lim, "cpus"))), 64)
		mem, _ = parseMemMB(scalar(mapValue(lim, "memory")))
	}
	if cpu == 0 {
		cpu, _ = strconv.ParseFloat(strings.TrimSpace(scalar(mapValue(svc, "cpus"))), 64)
	}
	if mem == 0 {
		mem, _ = parseMemMB(scalar(mapValue(svc, "mem_limit")))
	}
	return cpu, mem
}

// setLimit — deploy.resources.limits.<key> = value (узлы создаются).
func setLimit(svc *yaml.Node, key, value string) {
	child := func(parent *yaml.Node, k string) *yaml.Node {
		if v := mapValue(parent, k); v != nil && v.Kind == yaml.MappingNode {
			return v
		}
		v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, v)
		return v
	}
	lim := child(child(child(svc, "deploy"), "resources"), "limits")
	lim.Content = append(lim.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle})
}

// checkServiceSafety — то, что выходит за стек, токену с потолками нельзя.
func checkServiceSafety(name string, svc *yaml.Node) error {
	bad := func(what string) error { return msgs.Errorf("deploy.tokenServiceUnsafe", name, what) }
	if v := strings.ToLower(scalar(mapValue(svc, "privileged"))); v == "true" || v == "yes" {
		return bad("privileged")
	}
	for _, k := range []string{"network_mode", "pid", "ipc", "userns_mode", "uts"} {
		if v := scalar(mapValue(svc, k)); v == "host" || strings.HasPrefix(v, "container:") {
			return bad(k + ": " + v)
		}
	}
	if n := mapValue(svc, "cap_add"); n != nil && len(n.Content) > 0 {
		return bad("cap_add")
	}
	if n := mapValue(svc, "devices"); n != nil && len(n.Content) > 0 {
		return bad("devices")
	}
	if n := mapValue(svc, "security_opt"); n != nil {
		for _, o := range n.Content {
			if strings.Contains(scalar(o), "unconfined") {
				return bad("security_opt: " + scalar(o))
			}
		}
	}
	if n := mapValue(svc, "volumes"); n != nil {
		for _, v := range n.Content {
			src := ""
			switch v.Kind {
			case yaml.ScalarNode:
				if parts := strings.SplitN(v.Value, ":", 2); len(parts) == 2 {
					src = parts[0]
				}
			case yaml.MappingNode:
				if scalar(mapValue(v, "type")) == "bind" {
					src = scalar(mapValue(v, "source"))
				}
			}
			if hostPathOutside(src) {
				return bad("volume " + src)
			}
		}
	}
	return nil
}

// hostPathOutside — путь хоста вне каталога стека (имя тома — нет).
func hostPathOutside(src string) bool {
	if src == "" {
		return false
	}
	if strings.Contains(src, "docker.sock") {
		return true
	}
	if strings.HasPrefix(src, "/") || strings.HasPrefix(src, "~") || strings.HasPrefix(src, "$") {
		return true
	}
	if strings.HasPrefix(src, ".") {
		// ./data — внутри стека; ../ — выше него.
		clean := strings.TrimPrefix(src, "./")
		return clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../")
	}
	return false
}
