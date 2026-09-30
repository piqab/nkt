package deploy

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/site"
)

// BuildOnlyServices — сервисы compose-файла, которые собираются из
// исходников (build: без image:). На хосте их собрать не из чего: стек
// едет туда без контекста сборки, а выкладка берёт только готовые образы.
// Нечитаемый YAML — не наша забота: его отвергнет «compose config».
func BuildOnlyServices(text string) []string {
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(text), &doc) != nil {
		return nil
	}
	var out []string
	for name, svc := range doc.Services {
		_, build := svc["build"]
		image, _ := svc["image"].(string)
		if build && image == "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// SiteSpec — сайт стека (compose.site). Строкой — только проверка HTTPS
// после выкладки; блоком — сайт, который хаб настраивает сам (прокси,
// сертификат, публикация сервиса) при выкладке.
type SiteSpec struct {
	// Check — имя для проверки HTTPS (строковая форма).
	Check    string   `yaml:"-"`
	Domains  []string `yaml:"domains"`
	Service  string   `yaml:"service"`
	Port     int      `yaml:"port"`
	Proxy    string   `yaml:"proxy"`
	Firewall *bool    `yaml:"firewall"`
}

// UnmarshalYAML — строка или блок; в блоке — только известные ключи.
func (s *SiteSpec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&s.Check)
	}
	if n.Kind != yaml.MappingNode {
		return msgs.Errorf("deploy.specBad", "compose.site", n.Value)
	}
	known := map[string]bool{"domains": true, "service": true, "port": true, "proxy": true, "firewall": true}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; !known[k] {
			return msgs.Errorf("deploy.specBad", "compose.site."+k, n.Content[i+1].Value)
		}
	}
	type plain SiteSpec
	return n.Decode((*plain)(s))
}

// Managed — сайт настраивает хаб (блочная форма).
func (s *SiteSpec) Managed() bool { return s != nil && len(s.Domains) > 0 }

// CheckDomain — имя для проверки HTTPS после выкладки (пусто — нет).
func (s *SiteSpec) CheckDomain() string {
	switch {
	case s == nil:
		return ""
	case s.Managed():
		return strings.ToLower(s.Domains[0])
	}
	return strings.ToLower(s.Check)
}

// OpenFirewall — открыть 80/443 в файрволе хоста (по умолчанию да).
func (s *SiteSpec) OpenFirewall() bool { return s.Firewall == nil || *s.Firewall }

// validate — проверки блока; hosts — сколько хостов у стека (сайт — на
// одном: имя обычно указывает на один адрес).
func (s *SiteSpec) validate(c *ComposeSpec) error {
	if s == nil {
		return nil
	}
	if !s.Managed() {
		if s.Check == "" && (s.Service != "" || s.Port != 0 || s.Proxy != "") {
			return msgs.Errorf("deploy.specNeeds", "compose.site.domains")
		}
		if s.Check != "" && !composeDomainRe.MatchString(strings.ToLower(s.Check)) {
			return msgs.Errorf("deploy.specBad", "compose.site", s.Check)
		}
		return nil
	}
	if len(c.Hosts) != 1 || c.Group != "" {
		return msgs.Errorf("deploy.siteOneHost")
	}
	domains, err := site.NormalizeDomains(s.Domains)
	if err != nil {
		return err
	}
	s.Domains = domains
	if !site.ValidName(s.Service) {
		return msgs.Errorf("deploy.specBad", "compose.site.service", s.Service)
	}
	if s.Port < 1 || s.Port > 65535 {
		return msgs.Errorf("deploy.specBad", "compose.site.port", strconv.Itoa(s.Port))
	}
	if s.Proxy != "" && !slices.Contains(site.Proxies, s.Proxy) {
		return msgs.Errorf("deploy.specBad", "compose.site.proxy", s.Proxy)
	}
	return nil
}

// imageRe — ссылка на образ: [registry[:порт]/]путь[:тег][@sha256:…];
// {{nkt.tag}} подставляется до проверки на хосте, поэтому допустим.
var imageRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._/:-]|\{\{nkt\.(tag|commit|ref)\}\}){0,254}(@sha256:[0-9a-f]{64})?$`)

// composeServiceRe — имя сервиса compose.
var composeServiceRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// validateImages — compose.images: сервис → готовый образ.
func validateImages(images map[string]string) error {
	for svc, img := range images {
		if !composeServiceRe.MatchString(svc) {
			return msgs.Errorf("deploy.specBad", "compose.images", svc)
		}
		if !imageRe.MatchString(img) {
			return msgs.Errorf("deploy.specBad", "compose.images."+svc, img)
		}
	}
	return nil
}

// OverrideImages ставит сервисам готовые образы (compose.images): image —
// из описания конвейера, build убирается. Так выкладывается чужой
// compose-файл, который собирает образ из исходников, без форка. Сервиса
// нет в файле — ошибка (опечатка не должна пройти молча).
func OverrideImages(text string, images map[string]string) (string, error) {
	if len(images) == 0 {
		return text, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return "", msgs.Errorf("deploy.composeYAML", err.Error())
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", msgs.Errorf("deploy.composeYAML", "not a mapping")
	}
	services := mapValue(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return "", msgs.Errorf("deploy.imageServiceMissing", strings.Join(sortedKeys(images), ", "))
	}
	for _, name := range sortedKeys(images) {
		svc := mapValue(services, name)
		if svc == nil || svc.Kind != yaml.MappingNode {
			return "", msgs.Errorf("deploy.imageServiceMissing", name)
		}
		var kept []*yaml.Node
		for i := 0; i+1 < len(svc.Content); i += 2 {
			if k := svc.Content[i].Value; k == "build" || k == "image" {
				continue
			}
			kept = append(kept, svc.Content[i], svc.Content[i+1])
		}
		img := []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "image"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: images[name]},
		}
		svc.Content = append(img, kept...)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
