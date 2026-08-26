package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// DockerResult is everything the docker parser produces.
type DockerResult struct {
	Status     model.SourceStatus
	Containers []model.Container
	Networks   []model.DockerNetwork
	Endpoints  []model.Endpoint
	Files      []model.ManagedFile
}

// Docker combines what the engine reports with what compose files declare, so
// the dashboard can show both "running but undeclared" and "declared but down".
func Docker(ctx context.Context, c collect.Collector, composePaths []string) DockerResult {
	started := time.Now()
	res := DockerResult{Status: model.SourceStatus{Name: model.ServiceDocker}}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()
	// None of these fields have `omitempty` — a host with no containers/
	// networks/compose files at all would otherwise leave them nil, which
	// encoding/json marshals as `null` and crashes any frontend .map/.filter
	// expecting an array.
	res.Containers = []model.Container{}
	res.Networks = []model.DockerNetwork{}
	res.Endpoints = []model.Endpoint{}
	res.Files = []model.ManagedFile{}

	// --- what the engine is actually running -------------------------------------
	running, version, err := dockerFromEngine(ctx, c)
	if err != nil {
		res.Status.Warnings = append(res.Status.Warnings, err.Error())
	} else {
		res.Status.Available = true
		res.Status.Version = version
	}
	if nets, err := dockerNetworks(ctx, c); err != nil {
		res.Status.Warnings = append(res.Status.Warnings, err.Error())
	} else {
		res.Networks = nets
	}

	// --- what compose files declare ----------------------------------------------
	declared := map[string]*model.Container{}
	order := []string{}
	for _, path := range composePaths {
		if !c.Exists(path) {
			continue
		}
		res.Status.Available = true
		res.Files = append(res.Files, describeFile(c, path, model.ServiceDocker, true))
		items, warns := parseCompose(c, path)
		res.Status.Warnings = append(res.Status.Warnings, warns...)
		for _, item := range items {
			key := item.Name
			if _, ok := declared[key]; !ok {
				order = append(order, key)
			}
			cp := item
			declared[key] = &cp
		}
	}
	res.Status.Files = fileNames(res.Files)

	// --- merge --------------------------------------------------------------------
	byName := map[string]*model.Container{}
	for _, name := range order {
		byName[name] = declared[name]
	}
	for i := range running {
		r := running[i]
		target := matchDeclared(byName, r)
		if target == nil {
			r.Declared = false
			r.Running = true
			cp := r
			byName[r.Name] = &cp
			order = append(order, r.Name)
			continue
		}
		mergeContainer(target, r)
	}

	for _, name := range order {
		ct := byName[name]
		if ct == nil {
			continue
		}
		if !ct.Running && ct.State == "" {
			ct.State = "declared"
			ct.Status = "описан в compose, но не запущен"
		}
		res.Containers = append(res.Containers, *ct)
	}
	sort.Slice(res.Containers, func(i, j int) bool { return res.Containers[i].Name < res.Containers[j].Name })

	// --- endpoints from published ports -------------------------------------------
	for _, ct := range res.Containers {
		for _, p := range ct.Ports {
			if !p.Published() {
				continue
			}
			addr := p.HostIP
			if addr == "" {
				addr = "0.0.0.0"
			}
			target := fmt.Sprintf("%s:%d", ct.Name, p.ContainerPort)
			if ip := primaryIP(ct); ip != "" {
				target = fmt.Sprintf("%s:%d", ip, p.ContainerPort)
			}
			ep := model.Endpoint{
				ID:       fmt.Sprintf("docker:%s:%s:%d", ct.Name, addr, p.HostPort),
				Service:  model.ServiceDocker,
				Kind:     "published-port",
				Address:  addr,
				Port:     p.HostPort,
				Protocol: p.Protocol,
				Mode:     "tcp",
				Names:    []string{ct.Name},
				Label:    ct.Name,
				File:     ct.ComposeFile,
				Routes: []model.Route{{
					Match: "", Target: target, TargetKind: "address",
				}},
				Extra: map[string]string{
					"container":      ct.Name,
					"container_port": strconv.Itoa(p.ContainerPort),
					"image":          ct.Image,
					"state":          ct.State,
				},
			}
			res.Endpoints = append(res.Endpoints, ep)
		}
	}
	sort.Slice(res.Endpoints, func(i, j int) bool { return res.Endpoints[i].Port < res.Endpoints[j].Port })

	if !res.Status.Available && res.Status.Error == "" {
		res.Status.Error = "docker недоступен: нет ни сокета движка, ни compose-файлов"
		res.Status.ErrorKey = "parse.dockerUnavailable"
	}
	return res
}

func primaryIP(ct model.Container) string {
	for _, n := range ct.Networks {
		if n.IPAddress != "" {
			return n.IPAddress
		}
	}
	return ""
}

// matchDeclared finds the compose entry a running container belongs to, first by
// compose labels, then by container name.
func matchDeclared(byName map[string]*model.Container, r model.Container) *model.Container {
	if r.Project != "" && r.ServiceName != "" {
		for _, d := range byName {
			if d.Declared && d.Project == r.Project && d.ServiceName == r.ServiceName {
				return d
			}
		}
	}
	if d, ok := byName[r.Name]; ok && d.Declared {
		return d
	}
	return nil
}

func mergeContainer(dst *model.Container, src model.Container) {
	dst.Running = true
	dst.ID = src.ID
	dst.State = src.State
	dst.Status = src.Status
	dst.Created = src.Created
	if src.Image != "" {
		dst.Image = src.Image
	}
	if src.Project != "" {
		dst.Project = src.Project
	}
	if src.ServiceName != "" {
		dst.ServiceName = src.ServiceName
	}
	if len(src.Networks) > 0 {
		dst.Networks = src.Networks
	}
	// Union rather than overwrite. The engine's view wins where both agree —
	// it knows the real bind address — but a restarting container reports only
	// the mappings it managed to bind, and the declared ones still matter for
	// conflict analysis.
	merged := append([]model.PortMapping{}, src.Ports...)
	seen := map[string]bool{}
	for _, p := range merged {
		seen[portKey(p)] = true
	}
	for _, p := range dst.Ports {
		if !seen[portKey(p)] {
			merged = append(merged, p)
			seen[portKey(p)] = true
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].HostPort < merged[j].HostPort })
	dst.Ports = merged
}

// portKey identifies a mapping by what it forwards, not by how the bind address
// happens to be spelled ("" and 0.0.0.0 mean the same thing to docker).
func portKey(p model.PortMapping) string {
	return fmt.Sprintf("%d|%d|%s", p.HostPort, p.ContainerPort, p.Protocol)
}

// --------------------------------------------------------------- engine API

type engineContainer struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
	Create int64    `json:"Created"`
	Ports  []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
	Labels          map[string]string `json:"Labels"`
	NetworkSettings struct {
		Networks map[string]struct {
			NetworkID string `json:"NetworkID"`
			IPAddress string `json:"IPAddress"`
			Gateway   string `json:"Gateway"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// dockerFromEngine lists running containers. The Docker Engine API accepts
// unversioned paths and answers with its newest supported version, which keeps
// this client identical against a real socket and against a snapshot.
func dockerFromEngine(ctx context.Context, c collect.Collector) ([]model.Container, string, error) {
	version := ""
	if raw, code, err := c.DockerAPI(ctx, "GET", "/version", nil); err == nil && code == 200 {
		var v struct {
			Version    string `json:"Version"`
			APIVersion string `json:"ApiVersion"`
		}
		if json.Unmarshal(raw, &v) == nil {
			version = fmt.Sprintf("Docker %s (API %s)", v.Version, v.APIVersion)
		}
	}

	raw, code, err := c.DockerAPI(ctx, "GET", "/containers/json?all=1", nil)
	if err != nil {
		return nil, version, fmt.Errorf("docker: %w", err)
	}
	if code != 200 {
		return nil, version, fmt.Errorf("docker: список контейнеров вернул HTTP %d", code)
	}
	var list []engineContainer
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, version, fmt.Errorf("docker: разбор списка контейнеров: %w", err)
	}

	out := make([]model.Container, 0, len(list))
	for _, e := range list {
		ct := model.Container{
			ID:          e.ID,
			Name:        strings.TrimPrefix(firstOr(e.Names, ""), "/"),
			Image:       e.Image,
			State:       e.State,
			Status:      e.Status,
			Created:     e.Create,
			Project:     e.Labels["com.docker.compose.project"],
			ServiceName: e.Labels["com.docker.compose.service"],
			ComposeFile: primaryComposeFile(e.Labels["com.docker.compose.project.config_files"]),
			Running:     e.State == "running",
		}
		// A dual-stack publication is reported once per address family, but it is
		// one forwarding rule; keeping both would double every port of every
		// container and invent conflicts that do not exist.
		seenPorts := map[string]int{}
		for _, p := range e.Ports {
			proto := p.Type
			if proto == "" {
				proto = "tcp"
			}
			mapping := model.PortMapping{
				HostIP: p.IP, HostPort: p.PublicPort, ContainerPort: p.PrivatePort, Protocol: proto,
			}
			key := portKey(mapping)
			if idx, ok := seenPorts[key]; ok {
				// Prefer the IPv4 spelling: it is what operators recognise.
				if strings.Contains(ct.Ports[idx].HostIP, ":") && !strings.Contains(mapping.HostIP, ":") {
					ct.Ports[idx] = mapping
				}
				continue
			}
			seenPorts[key] = len(ct.Ports)
			ct.Ports = append(ct.Ports, mapping)
		}
		names := make([]string, 0, len(e.NetworkSettings.Networks))
		for name := range e.NetworkSettings.Networks {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			n := e.NetworkSettings.Networks[name]
			ct.Networks = append(ct.Networks, model.ContainerNetwork{
				Name: name, IPAddress: n.IPAddress, Gateway: n.Gateway,
			})
		}
		out = append(out, ct)
	}
	return out, version, nil
}

func dockerNetworks(ctx context.Context, c collect.Collector) ([]model.DockerNetwork, error) {
	raw, code, err := c.DockerAPI(ctx, "GET", "/networks", nil)
	if err != nil {
		return nil, fmt.Errorf("docker networks: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("docker networks: HTTP %d", code)
	}
	var list []struct {
		ID       string `json:"Id"`
		Name     string `json:"Name"`
		Driver   string `json:"Driver"`
		Scope    string `json:"Scope"`
		Internal bool   `json:"Internal"`
		IPAM     struct {
			Config []struct {
				Subnet  string `json:"Subnet"`
				Gateway string `json:"Gateway"`
			} `json:"Config"`
		} `json:"IPAM"`
		Options map[string]string `json:"Options"`
		Labels  map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("docker networks: разбор ответа: %w", err)
	}

	out := make([]model.DockerNetwork, 0, len(list))
	for _, n := range list {
		net := model.DockerNetwork{
			ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal,
			Bridge:  n.Options["com.docker.network.bridge.name"],
			Project: n.Labels["com.docker.compose.project"],
		}
		for _, cfg := range n.IPAM.Config {
			if cfg.Subnet != "" {
				net.Subnets = append(net.Subnets, cfg.Subnet)
			}
			if net.Gateway == "" {
				net.Gateway = cfg.Gateway
			}
		}
		out = append(out, net)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// --------------------------------------------------------------- compose files

type composeFile struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image         string       `yaml:"image"`
	ContainerName string       `yaml:"container_name"`
	Restart       string       `yaml:"restart"`
	Ports         composePorts `yaml:"ports"`
	Networks      flexList     `yaml:"networks"`
	DependsOn     flexList     `yaml:"depends_on"`
}

// flexList accepts both the list form ([a, b]) and the mapping form ({a: {...}})
// that compose allows for networks and depends_on.
type flexList []string

func (f *flexList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*f = flexList{node.Value}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind == yaml.ScalarNode {
				*f = append(*f, item.Value)
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			*f = append(*f, node.Content[i].Value)
		}
	}
	return nil
}

type composePorts []model.PortMapping

func (p *composePorts) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			if pm, ok := parseComposePort(item.Value); ok {
				*p = append(*p, pm)
			}
		case yaml.MappingNode:
			var long struct {
				Target    int    `yaml:"target"`
				Published string `yaml:"published"`
				HostIP    string `yaml:"host_ip"`
				Protocol  string `yaml:"protocol"`
			}
			if err := item.Decode(&long); err != nil {
				continue
			}
			proto := long.Protocol
			if proto == "" {
				proto = "tcp"
			}
			published, _ := strconv.Atoi(strings.SplitN(long.Published, "-", 2)[0])
			*p = append(*p, model.PortMapping{
				HostIP: long.HostIP, HostPort: published, ContainerPort: long.Target, Protocol: proto,
			})
		}
	}
	return nil
}

// parseComposePort understands "3000", "8080:80", "127.0.0.1:8080:80" and the
// "/udp" suffix. Port ranges collapse to their first port, which is enough to
// reason about exposure.
func parseComposePort(spec string) (model.PortMapping, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return model.PortMapping{}, false
	}
	proto := "tcp"
	if i := strings.LastIndex(spec, "/"); i >= 0 {
		proto = spec[i+1:]
		spec = spec[:i]
	}
	firstPort := func(s string) int {
		n, _ := strconv.Atoi(strings.SplitN(s, "-", 2)[0])
		return n
	}

	parts := strings.Split(spec, ":")
	switch len(parts) {
	case 1: // container port only: docker picks a random host port
		return model.PortMapping{ContainerPort: firstPort(parts[0]), Protocol: proto}, true
	case 2: // host:container
		return model.PortMapping{
			HostPort: firstPort(parts[0]), ContainerPort: firstPort(parts[1]), Protocol: proto,
		}, true
	case 3: // ip:host:container
		return model.PortMapping{
			HostIP: parts[0], HostPort: firstPort(parts[1]), ContainerPort: firstPort(parts[2]), Protocol: proto,
		}, true
	default:
		return model.PortMapping{}, false
	}
}

func parseCompose(c collect.Collector, path string) ([]model.Container, []string) {
	raw, err := c.ReadFile(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("compose %s: %v", path, err)}
	}
	var cf composeFile
	if err := yaml.Unmarshal(raw, &cf); err != nil {
		return nil, []string{fmt.Sprintf("compose %s: разбор YAML: %v", path, err)}
	}

	project := cf.Name
	if project == "" {
		project = defaultProjectName(path)
	}

	names := make([]string, 0, len(cf.Services))
	for name := range cf.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]model.Container, 0, len(names))
	for _, name := range names {
		svc := cf.Services[name]
		containerName := svc.ContainerName
		if containerName == "" {
			containerName = project + "-" + name
		}
		ct := model.Container{
			Name:        containerName,
			Image:       svc.Image,
			Project:     project,
			ServiceName: name,
			ComposeFile: path,
			Restart:     svc.Restart,
			Ports:       svc.Ports,
			DependsOn:   svc.DependsOn,
			Declared:    true,
		}
		for _, n := range svc.Networks {
			ct.Networks = append(ct.Networks, model.ContainerNetwork{Name: project + "_" + n})
		}
		out = append(out, ct)
	}
	return out, nil
}

// defaultProjectName mirrors compose's rule of naming the project after the
// directory holding the file.
func defaultProjectName(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return "default"
	}
	return parts[len(parts)-2]
}

// primaryComposeFile returns the first path out of the
// com.docker.compose.project.config_files label — a single path when the
// stack was brought up with one -f, but a comma-joined list of ABSOLUTE
// paths when it was brought up with several (-f a.yml -f b.yml). Storing
// the raw label verbatim made "редактировать конфиг" try to open the whole
// joined string as one filename, which is never a real path — os.Stat on
// it always fails with "файл не найден" the moment a stack uses more than
// one compose file. The base/first file is the one that actually declares
// the service (later -f files only override fields in it), so it is the
// one worth editing here.
func primaryComposeFile(label string) string {
	first, _, _ := strings.Cut(label, ",")
	return first
}

func firstOr(list []string, def string) string {
	if len(list) == 0 {
		return def
	}
	return list[0]
}
