package deploy

import (
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Разбор чужого compose-файла для «Заполнить описание»: сервисы, их образы
// и порты, переменные ${…}, файлы рядом, — из этого окно собирает описание
// конвейера вместо заглушек.

// ScanService — сервис compose-файла.
type ScanService struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
	// BuildOnly — build: без image: (на хосте собрать не из чего).
	BuildOnly bool `json:"build_only,omitempty"`
	// Ports — порты внутри контейнера: из образа (ImagePorts) или из
	// compose (ports/expose).
	Ports      []int `json:"ports,omitempty"`
	ImagePorts bool  `json:"image_ports,omitempty"`
	// Published — опубликованные compose наружу «хост:контейнер».
	Published []string `json:"published,omitempty"`
	// DB — образ базы, кэша или очереди (сайтом не бывает).
	DB bool `json:"db,omitempty"`
	// Unpinned — образ без версии (latest или без тега).
	Unpinned bool `json:"unpinned,omitempty"`
}

// ScanVar — переменная ${NAME} из compose-файла.
type ScanVar struct {
	Name    string `json:"name"`
	Default string `json:"default,omitempty"`
	// HasDefault — ${NAME:-x} / ${NAME-x}: без .env подставится значение.
	HasDefault bool `json:"has_default,omitempty"`
	// Required — ${NAME:?…} / ${NAME?…}: без значения compose откажется.
	Required bool `json:"required,omitempty"`
	// Secret — по имени похоже на пароль, ключ или токен.
	Secret bool `json:"secret,omitempty"`
}

// ComposeScan — итог разбора.
type ComposeScan struct {
	Services []ScanService `json:"services"`
	Vars     []ScanVar     `json:"vars"`
	// Files — файлы рядом, смонтированные относительным путём (от корня
	// репозитория).
	Files []string `json:"files,omitempty"`
	// Web — веб-сервис, выбранный по портам («авто»), и его порт.
	Web     string `json:"web,omitempty"`
	WebPort int    `json:"web_port,omitempty"`
}

var (
	composeVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?+])([^}]*))?\}`)
	secretNameRe = regexp.MustCompile(`(?i)(PASS|SECRET|TOKEN|KEY|SALT|CREDENTIAL|PRIVATE)`)
	dbImageRe    = regexp.MustCompile(`(?i)(^|/)(postgres|postgis|mysql|mariadb|mongo|redis|valkey|memcached|rabbitmq|clickhouse|elasticsearch|opensearch|minio|kafka|zookeeper|etcd|cassandra|influxdb|timescale)[^/]*$`)
)

// webPortOrder — порты, по которым узнаётся веб (раньше — вероятнее).
var webPortOrder = []int{80, 8080, 3000, 8000, 5000, 8081, 8888, 9000, 4000, 5173, 8443, 443}

// ScanCompose разбирает текст compose-файла file (путь в репозитории).
// imagePorts — порты образа по ссылке (EXPOSE из registry); nil — не
// известны.
func ScanCompose(text, file string, imagePorts func(image string) []int) (ComposeScan, error) {
	var doc struct {
		Services map[string]struct {
			Image   string      `yaml:"image"`
			Build   any         `yaml:"build"`
			Ports   []yaml.Node `yaml:"ports"`
			Expose  []yaml.Node `yaml:"expose"`
			Volumes []yaml.Node `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return ComposeScan{}, err
	}
	out := ComposeScan{Services: []ScanService{}, Vars: []ScanVar{}}
	names := make([]string, 0, len(doc.Services))
	for n := range doc.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	dir := path.Dir(file)
	for _, n := range names {
		svc := doc.Services[n]
		s := ScanService{Name: n, Image: svc.Image, BuildOnly: svc.Build != nil && svc.Image == ""}
		if s.Image != "" {
			s.DB = dbImageRe.MatchString(imageName(s.Image))
			tag := imageTagOf(s.Image)
			s.Unpinned = tag == "" || tag == "latest"
			if imagePorts != nil && !strings.Contains(s.Image, "${") {
				if p := imagePorts(s.Image); len(p) > 0 {
					s.Ports, s.ImagePorts = p, true
				}
			}
		}
		var composePorts []int
		for _, p := range svc.Ports {
			target, published := portTarget(p)
			if target > 0 && !slices.Contains(composePorts, target) {
				composePorts = append(composePorts, target)
			}
			if published != "" {
				s.Published = append(s.Published, published)
			}
		}
		for _, e := range svc.Expose {
			if n, err := strconv.Atoi(strings.SplitN(e.Value, "/", 2)[0]); err == nil && !slices.Contains(composePorts, n) {
				composePorts = append(composePorts, n)
			}
		}
		if !s.ImagePorts {
			sort.Ints(composePorts)
			s.Ports = composePorts
		}
		for _, v := range svc.Volumes {
			src := ""
			if v.Kind == yaml.ScalarNode {
				src, _, _ = strings.Cut(v.Value, ":")
			} else if v.Kind == yaml.MappingNode {
				var m struct {
					Type   string `yaml:"type"`
					Source string `yaml:"source"`
				}
				if v.Decode(&m) == nil && (m.Type == "" || m.Type == "bind") {
					src = m.Source
				}
			}
			if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") {
				rel := path.Clean(path.Join(dir, src))
				if ValidPath(rel) && !slices.Contains(out.Files, rel) {
					out.Files = append(out.Files, rel)
				}
			}
		}
		out.Services = append(out.Services, s)
	}
	seen := map[string]int{}
	for _, m := range composeVarRe.FindAllStringSubmatch(text, -1) {
		name, op, val := m[1], m[2], m[3]
		v := ScanVar{Name: name, Secret: secretNameRe.MatchString(name)}
		switch strings.TrimPrefix(op, ":") {
		case "-":
			v.HasDefault, v.Default = true, val
		case "?":
			v.Required = true
		}
		if i, ok := seen[name]; ok {
			old := &out.Vars[i]
			old.Required = old.Required || v.Required
			if !old.HasDefault && v.HasDefault {
				old.HasDefault, old.Default = true, v.Default
			}
			continue
		}
		seen[name] = len(out.Vars)
		out.Vars = append(out.Vars, v)
	}
	out.Web, out.WebPort = pickWeb(out.Services)
	return out, nil
}

// pickWeb — веб-сервис: не база, порт из известных веб-портов (по
// порядку вероятности); иначе — первый не-база с любым портом.
func pickWeb(list []ScanService) (string, int) {
	for _, wp := range webPortOrder {
		for _, s := range list {
			if !s.DB && slices.Contains(s.Ports, wp) {
				return s.Name, wp
			}
		}
	}
	for _, s := range list {
		if !s.DB && len(s.Ports) > 0 {
			return s.Name, s.Ports[0]
		}
	}
	return "", 0
}

// WebPortOf — порт сервиса name для сайта: веб-порт из известных, иначе
// первый.
func (c ComposeScan) WebPortOf(name string) int {
	for _, s := range c.Services {
		if s.Name != name {
			continue
		}
		for _, wp := range webPortOrder {
			if slices.Contains(s.Ports, wp) {
				return wp
			}
		}
		if len(s.Ports) > 0 {
			return s.Ports[0]
		}
	}
	return 0
}

// portTarget — порт контейнера и «хост:контейнер», если опубликован.
func portTarget(n yaml.Node) (int, string) {
	if n.Kind == yaml.MappingNode {
		var m struct {
			Target    int    `yaml:"target"`
			Published any    `yaml:"published"`
			HostIP    string `yaml:"host_ip"`
		}
		if n.Decode(&m) != nil {
			return 0, ""
		}
		pub := ""
		if m.Published != nil {
			pub = strings.TrimPrefix(m.HostIP+":", ":") + toString(m.Published) + ":" + strconv.Itoa(m.Target)
		}
		return m.Target, pub
	}
	spec := strings.SplitN(n.Value, "/", 2)[0]
	parts := strings.Split(spec, ":")
	last := parts[len(parts)-1]
	if i := strings.Index(last, "-"); i > 0 {
		last = last[:i]
	}
	target, err := strconv.Atoi(last)
	if err != nil {
		return 0, ""
	}
	if len(parts) == 1 {
		return target, ""
	}
	return target, spec
}

func toString(v any) string {
	switch x := v.(type) {
	case int:
		return strconv.Itoa(x)
	case string:
		return x
	}
	return ""
}

// imageName — ссылка без тега и дайджеста.
func imageName(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}

// imageTagOf — тег образа (пусто — не указан; дайджест — «@»).
func imageTagOf(ref string) string {
	if strings.Contains(ref, "@") {
		return "@"
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[i+1:]
	}
	return ""
}
