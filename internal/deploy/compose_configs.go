package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Конфиги сервисов из описания (compose.configs):
//
//	configs:
//	  app:                               # сервис compose
//	    - target: /etc/app/config.toml   # путь в контейнере
//	      content: |                     # текст в описании…
//	        secret = "${JWT_SECRET}"
//	    - target: /etc/app/extra.yaml
//	      file: deploy/extra.yaml        # …или файл из репозитория
//	      mode: "0440"                   # по умолчанию 0444
//	      uid: 1000                      # владелец (по умолчанию root)
//	      gid: 1000
//
// ${ИМЯ} подставляет хост из .env стека при выкладке — готовый файл с
// секретами не проходит через историю конфигов и журналы. Остальные «$»
// не трогаются ($host у nginx, $1 в скриптах); «$${» — буквальное «${».
// Файл ложится в .nkt-configs/ стека и монтируется сервису только для
// чтения; метка с хешем шаблона пересоздаёт сервис, когда конфиг сменился.

// ConfigSpec — один конфиг сервиса.
type ConfigSpec struct {
	Target  string `yaml:"target"`
	Content string `yaml:"content,omitempty"`
	File    string `yaml:"file,omitempty"`
	Mode    string `yaml:"mode,omitempty"`
	UID     *int   `yaml:"uid,omitempty"`
	GID     *int   `yaml:"gid,omitempty"`
}

// ConfigsDir — каталог готовых конфигов внутри стека.
const ConfigsDir = ".nkt-configs"

// ConfigMaxBytes — предел одного конфига (это текст настроек).
const ConfigMaxBytes = 256 << 10

// configTargetRe — путь в контейнере: абсолютный, без пробелов.
var configTargetRe = regexp.MustCompile(`^/[A-Za-z0-9._@+/-]{1,255}$`)

// configModeRe — права восьмерично: 0444, 440.
var configModeRe = regexp.MustCompile(`^0?[0-7]{3}$`)

// ModeBits — права конфига (по умолчанию 0444).
func (c ConfigSpec) ModeBits() uint32 {
	if c.Mode == "" {
		return 0o444
	}
	n, _ := strconv.ParseUint(c.Mode, 8, 32)
	return uint32(n)
}

func validateConfigs(cfgs map[string][]ConfigSpec) error {
	n := 0
	for svc, list := range cfgs {
		if !composeServiceRe.MatchString(svc) {
			return msgs.Errorf("deploy.specBad", "compose.configs", svc)
		}
		targets := map[string]bool{}
		for _, c := range list {
			n++
			where := "compose.configs." + svc
			if !configTargetRe.MatchString(c.Target) || strings.Contains(c.Target, "..") || strings.HasSuffix(c.Target, "/") {
				return msgs.Errorf("deploy.specBad", where+".target", c.Target)
			}
			if targets[c.Target] {
				return msgs.Errorf("deploy.specBad", where+".target", c.Target)
			}
			targets[c.Target] = true
			if (c.Content == "") == (c.File == "") {
				return msgs.Errorf("deploy.configSource", svc, c.Target)
			}
			if c.File != "" && !ValidPath(c.File) {
				return msgs.Errorf("deploy.specBad", where+".file", c.File)
			}
			if len(c.Content) > ConfigMaxBytes {
				return msgs.Errorf("deploy.configTooLarge", c.Target, ConfigMaxBytes>>10)
			}
			if c.Mode != "" && !configModeRe.MatchString(c.Mode) {
				return msgs.Errorf("deploy.specBad", where+".mode", c.Mode)
			}
			for _, id := range []*int{c.UID, c.GID} {
				if id != nil && (*id < 0 || *id > 65535) {
					return msgs.Errorf("deploy.specBad", where+".uid/gid", strconv.Itoa(*id))
				}
			}
		}
	}
	if n > 50 {
		return msgs.Errorf("deploy.specBad", "compose.configs", strconv.Itoa(n))
	}
	return nil
}

func validateEnvMap(env map[string]string) error {
	for k, v := range env {
		if !envKeyRe.MatchString(k) {
			return msgs.Errorf("deploy.specBad", "compose.env", k)
		}
		if g, ok := strings.CutPrefix(v, envGenPrefix); ok {
			if _, known := EnvGenKinds[g]; !known {
				return msgs.Errorf("deploy.envGenKind", k, g)
			}
		}
	}
	return nil
}

// ConfigFile — готовый к отправке на хост конфиг: путь в стеке, шаблон
// (с ${ИМЯ}), права и владелец.
type ConfigFile struct {
	Service string `json:"-"`
	Target  string `json:"-"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode"`
	UID     *int   `json:"uid,omitempty"`
	GID     *int   `json:"gid,omitempty"`
}

// configNameRe — что остаётся от имени файла в пути внутри стека.
var configNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ConfigFiles — конфиги описания по порядку (сервисы по имени); read —
// чтение file: из репозитория (с подстановкой {{nkt.…}}).
func (c ComposeSpec) ConfigFiles(read func(repoPath string) (string, error)) ([]ConfigFile, error) {
	svcs := make([]string, 0, len(c.Configs))
	for s := range c.Configs {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	var out []ConfigFile
	for _, svc := range svcs {
		for i, cs := range c.Configs[svc] {
			text := cs.Content
			if cs.File != "" {
				t, err := read(cs.File)
				if err != nil {
					return nil, err
				}
				if len(t) > ConfigMaxBytes {
					return nil, msgs.Errorf("deploy.configTooLarge", cs.Target, ConfigMaxBytes>>10)
				}
				text = t
			}
			name := configNameRe.ReplaceAllString(path.Base(cs.Target), "_")
			out = append(out, ConfigFile{
				Service: svc, Target: cs.Target,
				Path:    fmt.Sprintf("%s/%s-%d-%s", ConfigsDir, svc, i+1, name),
				Content: text, Mode: cs.ModeBits(), UID: cs.UID, GID: cs.GID,
			})
		}
	}
	return out, nil
}

// configRefRe — ${ИМЯ} в шаблоне конфига (не «$${»).
var configRefRe = regexp.MustCompile(`(^|[^$])\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ConfigVars — имена ${ИМЯ}, на которые ссылается шаблон, без повторов.
func ConfigVars(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range configRefRe.FindAllStringSubmatch(text, -1) {
		if !seen[m[2]] {
			seen[m[2]] = true
			out = append(out, m[2])
		}
	}
	sort.Strings(out)
	return out
}

// RenderConfig — шаблон со значениями vals: ${ИМЯ} из vals заменяется,
// неизвестное остаётся как есть, «$${» становится «${».
func RenderConfig(text string, vals map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "$${") {
			b.WriteString("${")
			i += 3
			continue
		}
		if strings.HasPrefix(text[i:], "${") {
			if j := strings.IndexByte(text[i+2:], '}'); j >= 0 {
				name := text[i+2 : i+2+j]
				if v, ok := vals[name]; ok && envKeyRe.MatchString(name) {
					b.WriteString(v)
					i += 3 + j
					continue
				}
			}
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

// configSecretRe — строка конфига, похожая на секрет, вписанный прямо
// в текст (а не ссылкой ${ИМЯ}).
var configSecretRe = regexp.MustCompile(`(?i)(pass(word|wd)?|secret|token|api[_-]?key|private[_-]?key)["']?\s*[:=]\s*["']?([^\s"'$#{][^\s"']{5,})`)

// ConfigSecretHints — цели конфигов, где в content похоже на вписанный
// секрет (предупреждение при сохранении, не запрет).
func (c ComposeSpec) ConfigSecretHints() []string {
	var out []string
	for svc, list := range c.Configs {
		for _, cs := range list {
			if cs.Content != "" && configSecretRe.MatchString(cs.Content) {
				out = append(out, svc+":"+cs.Target)
			}
		}
	}
	sort.Strings(out)
	return out
}

// MountConfigs — в копии compose-файла сервисам конфигов: том «только
// чтение» на готовый файл и метка с хешем шаблона (сменился — compose
// пересоздаст контейнер). Сервиса нет в файле — ошибка.
func MountConfigs(text string, cfgs []ConfigFile) (string, error) {
	if len(cfgs) == 0 {
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
	str := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	for _, cf := range cfgs {
		if services == nil || services.Kind != yaml.MappingNode {
			return "", msgs.Errorf("deploy.imageServiceMissing", cf.Service)
		}
		svc := mapValue(services, cf.Service)
		if svc == nil || svc.Kind != yaml.MappingNode {
			return "", msgs.Errorf("deploy.imageServiceMissing", cf.Service)
		}
		vols := mapValue(svc, "volumes")
		if vols == nil {
			vols = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			svc.Content = append(svc.Content, str("volumes"), vols)
		}
		if vols.Kind != yaml.SequenceNode {
			return "", msgs.Errorf("deploy.composeYAML", cf.Service+".volumes")
		}
		vols.Content = append(vols.Content, str("./"+cf.Path+":"+cf.Target+":ro"))
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%o\x00%v\x00%v", cf.Content, cf.Mode, idOf(cf.UID), idOf(cf.GID))))
		label := "nkt.config." + strings.TrimPrefix(cf.Path, ConfigsDir+"/")
		val := hex.EncodeToString(sum[:])[:16]
		labels := mapValue(svc, "labels")
		switch {
		case labels == nil:
			labels = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			svc.Content = append(svc.Content, str("labels"), labels)
			fallthrough
		case labels.Kind == yaml.MappingNode:
			labels.Content = append(labels.Content, str(label), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val, Style: yaml.DoubleQuotedStyle})
		case labels.Kind == yaml.SequenceNode:
			labels.Content = append(labels.Content, str(label+"="+val))
		default:
			return "", msgs.Errorf("deploy.composeYAML", cf.Service+".labels")
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func idOf(p *int) any {
	if p == nil {
		return "-"
	}
	return *p
}

// ParseEnv — переменные .env, как их видит compose: кавычки снимаются,
// в значениях без одинарных кавычек ${ИМЯ} раскрывается заданным выше.
func ParseEnv(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !envKeyRe.MatchString(k) {
			continue
		}
		v = strings.TrimSpace(v)
		switch {
		case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
			out[k] = v[1 : len(v)-1]
			continue
		case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
			v = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(v[1 : len(v)-1])
		default:
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		v = strings.ReplaceAll(RenderConfig(strings.ReplaceAll(v, "$$", "\x00"), out), "\x00", "$")
		out[k] = v
	}
	return out
}

// MaskSecrets — текст, где значения vals (от 4 знаков) заменены на «••••»:
// сухой прогон показывает разницу конфигов, не показывая секретов.
func MaskSecrets(text string, vals map[string]string) string {
	list := make([]string, 0, len(vals))
	for _, v := range vals {
		if len(v) >= 4 {
			list = append(list, v)
		}
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, v := range list {
		text = strings.ReplaceAll(text, v, "••••")
	}
	return text
}
