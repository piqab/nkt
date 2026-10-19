package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/parse"
)

// Перенос контейнера Docker на другой хост (хаб ведёт задание, хосты —
// шаги): план по docker inspect, проверка нового хоста, упаковка на
// старом (остановка, tar томов и каталогов, docker save образов),
// передача файлов через хаб потоком, распаковка и подъём на новом.
// Единица переноса — весь compose-проект; одиночный контейнер (docker run)
// становится стеком compose в /srv/compose/<имя>. Старый после переноса
// остаётся остановленным — для отката.

// MigrateContainer — контейнер плана.
type MigrateContainer struct {
	Name    string `json:"name"`
	Service string `json:"service,omitempty"`
	Image   string `json:"image"`
	ImageID string `json:"image_id"`
	State   string `json:"state"`
}

// MigrateVolume — именованный том.
type MigrateVolume struct {
	Name       string            `json:"name"`
	Mountpoint string            `json:"mountpoint"`
	Driver     string            `json:"driver,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Size       int64             `json:"size"`
	// Container / Dest — контейнер, в котором том смонтирован, и куда:
	// данные копируются через него (docker cp), а не с диска хоста —
	// так работает и с Docker Desktop, и с rootless, и без root у nkt.
	Container string `json:"container"`
	Dest      string `json:"dest"`
}

// MigratePath — каталог или файл, который переносится как есть (рабочий
// каталог compose-проекта, bind-тома вне его).
type MigratePath struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// MigratePlan — что переносится.
type MigratePlan struct {
	Kind        string             `json:"kind"` // compose | standalone
	Project     string             `json:"project"`
	WorkingDir  string             `json:"working_dir,omitempty"`
	ConfigFiles []string           `json:"config_files,omitempty"`
	Containers  []MigrateContainer `json:"containers"`
	Images      []string           `json:"images"`
	ImageIDs    []string           `json:"image_ids"`
	Volumes     []MigrateVolume    `json:"volumes"`
	Paths       []MigratePath      `json:"paths"`
	Ports       []string           `json:"ports"`
	Arch        string             `json:"arch,omitempty"`
	// Refuse — почему перенести нельзя (ключи каталога с аргументом);
	// Skipped — системные bind-тома, которые не копируются.
	Refuse  []string `json:"refuse,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	// Compose / Env — у одиночного контейнера: собранный compose-файл и
	// .env (секреты окружения — в .env с правами 0600).
	Compose string `json:"compose,omitempty"`
	Env     string `json:"env,omitempty"`
}

type migrateInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Image  string `json:"Image"`
	Config struct {
		Image      string            `json:"Image"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		User       string            `json:"User"`
		WorkingDir string            `json:"WorkingDir"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode    string `json:"NetworkMode"`
		Privileged     bool   `json:"Privileged"`
		Devices        []any  `json:"Devices"`
		DeviceRequests []any  `json:"DeviceRequests"`
		PortBindings   map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
		CapAdd     []string `json:"CapAdd"`
		ExtraHosts []string `json:"ExtraHosts"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
		Driver      string `json:"Driver"`
	} `json:"Mounts"`
	State struct {
		Status string `json:"Status"`
	} `json:"State"`
}

// migrateSystemPrefixes — пути хоста, которые не копируются и не
// восстанавливаются: системные каталоги, сокет docker, данные движка.
var migrateSystemPrefixes = []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/boot", "/proc", "/sys", "/dev", "/run", "/var/run", "/var/lib/docker", "/var/lib/containerd", "/root", "/tmp", "/var/tmp"}

func migrateSystemPath(p string) bool {
	if p == "/" {
		return true
	}
	for _, pre := range migrateSystemPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// migrateSafePath — путь можно переносить: абсолютный, чистый, не системный.
func migrateSafePath(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.Contains(p, "..") && !migrateSystemPath(p) && !strings.ContainsAny(p, "\n\r\x00")
}

var migrateNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func dockerInspectAll(ctx context.Context, names ...string) ([]migrateInspect, error) {
	res, err := RunTooling(ctx, append([]string{"docker", "inspect", "--type", "container"}, names...)...)
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, msgs.Errorf("migrate.inspectFailed", strings.TrimSpace(res.Output()))
	}
	var out []migrateInspect
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// migratePlan — план переноса контейнера name (и его compose-проекта).
func (s *Server) migratePlan(ctx context.Context, name string) (MigratePlan, error) {
	// Пустые списки — [], не null: интерфейс их перебирает.
	p := MigratePlan{Containers: []MigrateContainer{}, Images: []string{}, ImageIDs: []string{}, Volumes: []MigrateVolume{}, Paths: []MigratePath{}, Ports: []string{}}
	if !migrateNameRe.MatchString(name) {
		return p, msgs.Errorf("migrate.badName", name)
	}
	if s.cfg.IsFixtures() {
		return p, msgs.Errorf("migrate.fixtures")
	}
	first, err := dockerInspectAll(ctx, name)
	if err != nil || len(first) == 0 {
		if err == nil {
			err = msgs.Errorf("migrate.badName", name)
		}
		return p, err
	}
	all := first
	labels := first[0].Config.Labels
	if proj := labels["com.docker.compose.project"]; proj != "" {
		p.Kind, p.Project = "compose", proj
		p.WorkingDir = labels["com.docker.compose.project.working_dir"]
		for _, f := range strings.Split(labels["com.docker.compose.project.config_files"], ",") {
			if f = strings.TrimSpace(f); f != "" {
				p.ConfigFiles = append(p.ConfigFiles, f)
			}
		}
		res, err := RunTooling(ctx, "docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+proj, "--format", "{{.Names}}")
		if err == nil && res.OK() {
			var names []string
			for _, l := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
				if l = strings.TrimSpace(l); migrateNameRe.MatchString(l) {
					names = append(names, l)
				}
			}
			if len(names) > 0 {
				if more, err := dockerInspectAll(ctx, names...); err == nil {
					all = more
				}
			}
		}
	} else {
		p.Kind, p.Project = "standalone", strings.ToLower(strings.TrimPrefix(first[0].Name, "/"))
	}
	if p.WorkingDir != "" && !migrateSafePath(p.WorkingDir) {
		p.Refuse = append(p.Refuse, "migrate.refuseWorkdir:"+p.WorkingDir)
	}
	seenImg := map[string]bool{}
	seenVol := map[string]bool{}
	seenPath := map[string]bool{}
	addPath := func(src string) {
		if seenPath[src] {
			return
		}
		// Внутри рабочего каталога — уже перенесётся с ним.
		if p.WorkingDir != "" && (src == p.WorkingDir || strings.HasPrefix(src, p.WorkingDir+"/")) {
			return
		}
		seenPath[src] = true
		p.Paths = append(p.Paths, MigratePath{Path: src})
	}
	if p.WorkingDir != "" && migrateSafePath(p.WorkingDir) {
		seenPath[p.WorkingDir] = true
		p.Paths = append(p.Paths, MigratePath{Path: p.WorkingDir})
	}
	for _, f := range p.ConfigFiles {
		if migrateSafePath(f) {
			addPath(f)
		}
	}
	for _, c := range all {
		cn := strings.TrimPrefix(c.Name, "/")
		p.Containers = append(p.Containers, MigrateContainer{Name: cn, Service: c.Config.Labels["com.docker.compose.service"], Image: c.Config.Image, ImageID: c.Image, State: c.State.Status})
		if !seenImg[c.Image] {
			seenImg[c.Image] = true
			p.Images = append(p.Images, c.Config.Image)
			p.ImageIDs = append(p.ImageIDs, c.Image)
		}
		switch {
		case c.HostConfig.NetworkMode == "host":
			p.Refuse = append(p.Refuse, "migrate.refuseHostNet:"+cn)
		case c.HostConfig.Privileged:
			p.Refuse = append(p.Refuse, "migrate.refusePrivileged:"+cn)
		case len(c.HostConfig.Devices) > 0 || len(c.HostConfig.DeviceRequests) > 0:
			p.Refuse = append(p.Refuse, "migrate.refuseDevices:"+cn)
		}
		for k := range c.Config.Labels {
			if strings.HasPrefix(k, "com.docker.swarm.") || strings.HasPrefix(k, "io.kubernetes.") {
				p.Refuse = append(p.Refuse, "migrate.refuseOrchestrated:"+cn)
				break
			}
		}
		for port, binds := range c.HostConfig.PortBindings {
			for _, b := range binds {
				if b.HostPort != "" {
					p.Ports = append(p.Ports, fmt.Sprintf("%s:%s->%s", orStr(b.HostIP, "0.0.0.0"), b.HostPort, port))
				}
			}
		}
		for _, m := range c.Mounts {
			switch m.Type {
			case "volume":
				if m.Name != "" && !seenVol[m.Name] {
					seenVol[m.Name] = true
					p.Volumes = append(p.Volumes, MigrateVolume{Name: m.Name, Mountpoint: m.Source, Driver: m.Driver, Container: cn, Dest: m.Destination})
				}
				if m.Driver != "" && m.Driver != "local" {
					p.Refuse = append(p.Refuse, "migrate.refuseVolumeDriver:"+m.Name)
				}
			case "bind":
				if migrateSafePath(m.Source) {
					addPath(m.Source)
				} else {
					p.Skipped = append(p.Skipped, m.Source)
				}
			}
		}
	}
	sort.Strings(p.Ports)
	// Метки и драйвер томов — чтобы compose на новом хосте признал их своими.
	for i, v := range p.Volumes {
		if res, err := RunTooling(ctx, "docker", "volume", "inspect", v.Name); err == nil && res.OK() {
			var vi []struct {
				Driver     string            `json:"Driver"`
				Mountpoint string            `json:"Mountpoint"`
				Labels     map[string]string `json:"Labels"`
			}
			if json.Unmarshal([]byte(res.Stdout), &vi) == nil && len(vi) > 0 {
				p.Volumes[i].Labels = vi[0].Labels
				p.Volumes[i].Driver = vi[0].Driver
				if vi[0].Mountpoint != "" {
					p.Volumes[i].Mountpoint = vi[0].Mountpoint
				}
			}
		}
	}
	// Размеры — одним du.
	var du []string
	for _, v := range p.Volumes {
		du = append(du, v.Mountpoint)
	}
	for _, x := range p.Paths {
		du = append(du, x.Path)
	}
	if len(du) > 0 {
		if res, err := RunTooling(ctx, append([]string{"du", "-sb", "--"}, du...)...); err == nil {
			sizes := map[string]int64{}
			for _, l := range strings.Split(res.Stdout, "\n") {
				f := strings.SplitN(l, "\t", 2)
				if len(f) == 2 {
					n, _ := strconv.ParseInt(strings.TrimSpace(f[0]), 10, 64)
					sizes[strings.TrimSpace(f[1])] = n
				}
			}
			for i := range p.Volumes {
				p.Volumes[i].Size = sizes[p.Volumes[i].Mountpoint]
			}
			for i := range p.Paths {
				p.Paths[i].Size = sizes[p.Paths[i].Path]
			}
		}
	}
	p.Arch = hostArch(ctx, s.scanner.Collector())
	if p.Kind == "standalone" && len(p.Refuse) == 0 {
		p.Compose, p.Env, err = standaloneCompose(ctx, first[0])
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

func orStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// standaloneCompose — compose-файл и .env для одиночного контейнера:
// образ, команда, порты, тома, перезапуск, пользователь; окружение образа
// по умолчанию не повторяется.
func standaloneCompose(ctx context.Context, c migrateInspect) (string, string, error) {
	name := strings.TrimPrefix(c.Name, "/")
	var img struct {
		Config struct {
			Env        []string `json:"Env"`
			Cmd        []string `json:"Cmd"`
			Entrypoint []string `json:"Entrypoint"`
			WorkingDir string   `json:"WorkingDir"`
			User       string   `json:"User"`
		} `json:"Config"`
	}
	if res, err := RunTooling(ctx, "docker", "image", "inspect", c.Image); err == nil && res.OK() {
		var list []json.RawMessage
		if json.Unmarshal([]byte(res.Stdout), &list) == nil && len(list) > 0 {
			_ = json.Unmarshal(list[0], &img)
		}
	}
	imgEnv := map[string]bool{}
	for _, e := range img.Config.Env {
		imgEnv[e] = true
	}
	svc := map[string]any{"image": c.Config.Image, "container_name": name}
	restart := c.HostConfig.RestartPolicy.Name
	if restart == "" || restart == "no" {
		restart = "unless-stopped"
	}
	svc["restart"] = restart
	var env []string
	for _, e := range c.Config.Env {
		if !imgEnv[e] {
			env = append(env, e)
		}
	}
	if len(env) > 0 {
		svc["env_file"] = []string{".env"}
	}
	if !equalStrings(c.Config.Cmd, img.Config.Cmd) && len(c.Config.Cmd) > 0 {
		svc["command"] = c.Config.Cmd
	}
	if !equalStrings(c.Config.Entrypoint, img.Config.Entrypoint) && len(c.Config.Entrypoint) > 0 {
		svc["entrypoint"] = c.Config.Entrypoint
	}
	if c.Config.User != "" && c.Config.User != img.Config.User {
		svc["user"] = c.Config.User
	}
	if c.Config.WorkingDir != "" && c.Config.WorkingDir != img.Config.WorkingDir {
		svc["working_dir"] = c.Config.WorkingDir
	}
	var ports []string
	for port, binds := range c.HostConfig.PortBindings {
		for _, b := range binds {
			if b.HostPort == "" {
				continue
			}
			if b.HostIP != "" && b.HostIP != "0.0.0.0" {
				ports = append(ports, b.HostIP+":"+b.HostPort+":"+port)
			} else {
				ports = append(ports, b.HostPort+":"+port)
			}
		}
	}
	sort.Strings(ports)
	if len(ports) > 0 {
		svc["ports"] = ports
	}
	var vols []string
	external := map[string]any{}
	for _, m := range c.Mounts {
		ro := ""
		if !m.RW {
			ro = ":ro"
		}
		switch m.Type {
		case "volume":
			vols = append(vols, m.Name+":"+m.Destination+ro)
			external[m.Name] = map[string]any{"external": true, "name": m.Name}
		case "bind":
			vols = append(vols, m.Source+":"+m.Destination+ro)
		}
	}
	if len(vols) > 0 {
		svc["volumes"] = vols
	}
	if len(c.HostConfig.CapAdd) > 0 {
		svc["cap_add"] = c.HostConfig.CapAdd
	}
	if len(c.HostConfig.ExtraHosts) > 0 {
		svc["extra_hosts"] = c.HostConfig.ExtraHosts
	}
	lbl := map[string]string{}
	for k, v := range c.Config.Labels {
		if !strings.HasPrefix(k, "com.docker.") && !strings.HasPrefix(k, "org.opencontainers.") {
			lbl[k] = v
		}
	}
	if len(lbl) > 0 {
		svc["labels"] = lbl
	}
	doc := map[string]any{"services": map[string]any{name: svc}}
	if len(external) > 0 {
		doc["volumes"] = external
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", "", err
	}
	var envText strings.Builder
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		// В одинарных кавычках compose ничего не подставляет.
		if strings.ContainsAny(v, " #'\"$\\") && !strings.Contains(v, "'") {
			v = "'" + v + "'"
		}
		envText.WriteString(k + "=" + v + "\n")
	}
	return "# nkt: собран из docker inspect контейнера " + name + " при переносе\n" + string(raw), envText.String(), nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- обработчики ---

// handleMigratePlan — GET /migrate/plan?container=.
func (s *Server) handleMigratePlan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := longCall(w, r)
	defer cancel()
	p, err := s.migratePlan(ctx, r.URL.Query().Get("container"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	// Секреты окружения в ответ плана не идут: только имена.
	p.Env = envNamesOnly(p.Env)
	writeJSON(w, http.StatusOK, p)
}

func envNamesOnly(env string) string {
	var out []string
	for _, l := range strings.Split(env, "\n") {
		if k, _, ok := strings.Cut(l, "="); ok {
			out = append(out, k+"=…")
		}
	}
	return strings.Join(out, "\n")
}

// MigrateCheck — что мешает поднять стек на этом хосте.
type MigrateCheck struct {
	Docker        bool     `json:"docker"`
	Arch          string   `json:"arch,omitempty"`
	BusyPorts     []string `json:"busy_ports,omitempty"`
	ExistingPaths []string `json:"existing_paths,omitempty"`
	ExistingVols  []string `json:"existing_volumes,omitempty"`
	HaveImages    []string `json:"have_images,omitempty"`
	FreeBytes     int64    `json:"free_bytes"`
	ProjectExists bool     `json:"project_exists,omitempty"`
}

// handleMigrateCheck — POST /migrate/check {plan}: новый хост.
func (s *Server) handleMigrateCheck(w http.ResponseWriter, r *http.Request) {
	var p MigratePlan
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := longCall(w, r)
	defer cancel()
	c := s.scanner.Collector()
	out := MigrateCheck{Docker: collect.Which(ctx, c, "docker"), Arch: hostArch(ctx, c)}
	for _, x := range p.Paths {
		if !migrateSafePath(x.Path) {
			continue
		}
		if c.Exists(x.Path) {
			out.ExistingPaths = append(out.ExistingPaths, x.Path)
		}
	}
	if p.Kind == "standalone" && c.Exists(parse.ComposeStacksDir+"/"+p.Project) {
		out.ExistingPaths = append(out.ExistingPaths, parse.ComposeStacksDir+"/"+p.Project)
	}
	if out.Docker {
		for _, v := range p.Volumes {
			if res, err := RunTooling(ctx, "docker", "volume", "inspect", v.Name); err == nil && res.OK() {
				out.ExistingVols = append(out.ExistingVols, v.Name)
			}
		}
		for i, id := range p.ImageIDs {
			if res, err := RunTooling(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", id); err == nil && res.OK() {
				out.HaveImages = append(out.HaveImages, p.Images[i])
			}
		}
		if p.Project != "" {
			if res, err := RunTooling(ctx, "docker", "ps", "-a", "-q", "--filter", "label=com.docker.compose.project="+p.Project); err == nil && strings.TrimSpace(res.Stdout) != "" {
				out.ProjectExists = true
			}
		}
	}
	listen := map[string]bool{}
	if res, err := c.RunTimeout(ctx, 10*time.Second, "ss", "-Hltnu"); err == nil {
		for _, l := range strings.Split(res.Stdout, "\n") {
			f := strings.Fields(l)
			if len(f) >= 5 {
				local := f[4]
				if i := strings.LastIndex(local, ":"); i >= 0 {
					listen[local[i+1:]] = true
				}
			}
		}
	}
	for _, pt := range p.Ports {
		hp := pt
		if i := strings.Index(hp, "->"); i >= 0 {
			hp = hp[:i]
		}
		if i := strings.LastIndex(hp, ":"); i >= 0 {
			hp = hp[i+1:]
		}
		if listen[hp] {
			out.BusyPorts = append(out.BusyPorts, pt)
		}
	}
	if res, err := c.RunTimeout(ctx, 10*time.Second, "df", "-B1", "--output=avail", "/var/lib/docker"); err == nil {
		lines := strings.Fields(res.Stdout)
		if len(lines) > 0 {
			out.FreeBytes, _ = strconv.ParseInt(lines[len(lines)-1], 10, 64)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
