// Package deploy — выкладки хаба: конвейер (описание в YAML) берёт
// репозиторий Git на нужной ветке, теге или коммите и выкладывает его
// сценарием хаба, манифестом Kubernetes или Helm-релизом в кластеры.
//
// Сборка образов — дело внешнего CI (GitHub Actions, GitLab CI, локальная
// сборка): nkt начинает с «образ готов» или «в репозитории новый коммит».
package deploy

import (
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Действия конвейера.
const (
	ActionManifest = "manifest"
	ActionHelm     = "helm"
	ActionScript   = "script"
	// ActionCompose — compose-стек из репозитория на хосты хаба.
	ActionCompose = "compose"
)

// Spec — описание конвейера.
type Spec struct {
	// Repo — репозиторий: https://… или git@host:path / ssh://….
	Repo string `yaml:"repo"`
	// Ref — ветка, из которой выкладывать (push в неё — выкладка).
	Ref string `yaml:"ref"`
	// Tags — шаблон тегов репозитория (glob, «v*»): тег по нему — выкладка
	// этого тега.
	Tags string `yaml:"tags,omitempty"`

	Action string `yaml:"action"`
	// Manifests — файлы манифестов в репозитории (manifest).
	Manifests []string `yaml:"manifests,omitempty"`
	// Clusters / Group — куда (manifest, helm): имена кластеров хаба или
	// группа хостов, на которых стоят кластеры.
	Clusters []string `yaml:"clusters,omitempty"`
	Group    string   `yaml:"group,omitempty"`
	// Helm — релиз (helm).
	Helm *HelmSpec `yaml:"helm,omitempty"`
	// Script — сценарий хаба в репозитории (script).
	Script string `yaml:"script,omitempty"`
	// Compose — стек (compose).
	Compose *ComposeSpec `yaml:"compose,omitempty"`

	// Poll — опрашивать репозиторий с этим интервалом (5m); пусто — нет.
	Poll string `yaml:"poll,omitempty"`
	// Registry — следить за тегами образа (ghcr.io/org/app) и выкладывать
	// новый; RegistryTags — регулярное выражение подходящих тегов.
	Registry     string `yaml:"registry,omitempty"`
	RegistryTags string `yaml:"registry_tags,omitempty"`
	RegistryPoll string `yaml:"registry_poll,omitempty"`
}

// ComposeSpec — compose-стек конвейера: файл из репозитория (и то, на
// что он ссылается) — в /srv/compose/<project> на каждом хосте по
// очереди, docker compose pull и up --wait; следующий хост — только когда
// стек на предыдущем поднялся.
type ComposeSpec struct {
	// File — compose-файл в репозитории.
	File string `yaml:"file"`
	// Project — имя стека на хосте.
	Project string `yaml:"project"`
	// Hosts / Group — куда: имена хостов хаба или группа.
	Hosts []string `yaml:"hosts,omitempty"`
	Group string   `yaml:"group,omitempty"`
	// Files — файлы и каталоги рядом (конфиги, на которые ссылается
	// compose); пути в репозитории, внутри каталога compose-файла.
	Files []string `yaml:"files,omitempty"`
	// Pull — скачивать образы перед подъёмом (по умолчанию да).
	Pull *bool `yaml:"pull,omitempty"`
	// WaitTimeout — сколько ждать подъёма и healthcheck на хосте (5m).
	WaitTimeout string `yaml:"wait_timeout,omitempty"`
	// Images — готовые образы сервисов вместо сборки: сервис → образ
	// (build в compose-файле убирается). Для чужих compose-файлов с build:.
	Images map[string]string `yaml:"images,omitempty"`
	// Ports — публикации портов сервисов вместо тех, что в compose-файле:
	// сервис → список ("127.0.0.1:8080:80"); пустой список — не
	// публиковать (порт 80 хоста нужен прокси сайта).
	Ports map[string][]string `yaml:"ports,omitempty"`
	// EnvKeys — переменные окружения сервисов, значения которых берутся из
	// .env конвейера (секреты), а не из compose-файла: сервис → имена.
	EnvKeys map[string][]string `yaml:"env_keys,omitempty"`
	// Bind — адрес публикаций портов без адреса (по умолчанию 127.0.0.1:
	// docker публикует в обход файрвола хоста); 0.0.0.0 — на всех.
	Bind string `yaml:"bind,omitempty"`
	// BindForce — адрес Bind и у публикаций, где адрес уже указан.
	BindForce bool `yaml:"bind_force,omitempty"`
	// Site — сайт стека: строкой — проверить по HTTPS после выкладки,
	// блоком — настроить (прокси, сертификат); см. SiteSpec.
	Site *SiteSpec `yaml:"site,omitempty"`
}

// PullImages — pull перед up (по умолчанию да).
func (c ComposeSpec) PullImages() bool { return c.Pull == nil || *c.Pull }

// Wait — время ожидания подъёма стека.
func (c ComposeSpec) Wait() time.Duration {
	if d, err := time.ParseDuration(c.WaitTimeout); err == nil && d > 0 {
		return d
	}
	return 5 * time.Minute
}

var (
	composeProjectRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	composeDomainRe  = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// HelmSpec — Helm-релиз конвейера.
type HelmSpec struct {
	RepoName  string `yaml:"repo_name"`
	RepoURL   string `yaml:"repo_url"`
	Chart     string `yaml:"chart"`
	Version   string `yaml:"version,omitempty"`
	Release   string `yaml:"release"`
	Namespace string `yaml:"namespace"`
	// Values — файл values в репозитории.
	Values string `yaml:"values,omitempty"`
	// TagKey — ключ values, куда подставить тег образа (image.tag).
	TagKey string `yaml:"tag_key,omitempty"`
}

var (
	refRe      = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	pathRe     = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,300}$`)
	tagGlobRe  = regexp.MustCompile(`^[A-Za-z0-9._*?/-]{1,100}$`)
	scpRepoRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._/~-]+$`)
	tagKeyRe   = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)
	registryRe = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)+$|^[a-z0-9._-]+(/[a-z0-9._-]+)?$`)
)

// ParseSpec разбирает и проверяет описание.
func ParseSpec(content string) (Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(strings.NewReader(content))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Spec{}, explainYAML(content, err)
	}
	return s, s.Validate()
}

// Validate — поля по отдельности: ни одно значение не станет частью
// команды без проверки (git вызывается с «--», пути — внутри checkout).
func (s Spec) Validate() error {
	if err := ValidRepo(s.Repo); err != nil {
		return err
	}
	if s.Ref == "" && s.Tags == "" {
		return msgs.Errorf("deploy.specNoRef")
	}
	if s.Ref != "" && !ValidRef(s.Ref) {
		return msgs.Errorf("deploy.specBad", "ref", s.Ref)
	}
	if s.Tags != "" && !tagGlobRe.MatchString(s.Tags) {
		return msgs.Errorf("deploy.specBad", "tags", s.Tags)
	}
	switch s.Action {
	case ActionManifest:
		if len(s.Manifests) == 0 {
			return msgs.Errorf("deploy.specNeeds", "manifests")
		}
		for _, p := range s.Manifests {
			if !ValidPath(p) {
				return msgs.Errorf("deploy.specBad", "manifests", p)
			}
		}
		if len(s.Clusters) == 0 && s.Group == "" {
			return msgs.Errorf("deploy.specNeeds", "clusters / group")
		}
	case ActionHelm:
		if s.Helm == nil {
			return msgs.Errorf("deploy.specNeeds", "helm")
		}
		if s.Helm.Values != "" && !ValidPath(s.Helm.Values) {
			return msgs.Errorf("deploy.specBad", "helm.values", s.Helm.Values)
		}
		if s.Helm.TagKey != "" && !tagKeyRe.MatchString(s.Helm.TagKey) {
			return msgs.Errorf("deploy.specBad", "helm.tag_key", s.Helm.TagKey)
		}
		if len(s.Clusters) == 0 && s.Group == "" {
			return msgs.Errorf("deploy.specNeeds", "clusters / group")
		}
	case ActionScript:
		if !ValidPath(s.Script) {
			return msgs.Errorf("deploy.specBad", "script", s.Script)
		}
	case ActionCompose:
		c := s.Compose
		if c == nil {
			return msgs.Errorf("deploy.specNeeds", "compose")
		}
		if !ValidPath(c.File) {
			return msgs.Errorf("deploy.specBad", "compose.file", c.File)
		}
		if !composeProjectRe.MatchString(c.Project) {
			return msgs.Errorf("deploy.specBad", "compose.project", c.Project)
		}
		if len(c.Hosts) == 0 && c.Group == "" {
			return msgs.Errorf("deploy.specNeeds", "compose.hosts / compose.group")
		}
		for _, f := range c.Files {
			if !ValidPath(strings.TrimSuffix(f, "/")) {
				return msgs.Errorf("deploy.specBad", "compose.files", f)
			}
		}
		if c.WaitTimeout != "" {
			d, err := time.ParseDuration(c.WaitTimeout)
			if err != nil || d < 10*time.Second || d > time.Hour {
				return msgs.Errorf("deploy.specBad", "compose.wait_timeout", c.WaitTimeout)
			}
		}
		if err := validateImages(c.Images); err != nil {
			return err
		}
		if err := validatePorts(c.Ports); err != nil {
			return err
		}
		if err := validateEnvKeys(c.EnvKeys); err != nil {
			return err
		}
		if err := validateBind(c.Bind); err != nil {
			return err
		}
		if err := c.Site.validate(c); err != nil {
			return err
		}
	default:
		return msgs.Errorf("deploy.specBad", "action", s.Action)
	}
	for _, iv := range []struct{ name, v string }{{"poll", s.Poll}, {"registry_poll", s.RegistryPoll}} {
		if iv.v == "" {
			continue
		}
		d, err := time.ParseDuration(iv.v)
		if err != nil || d < time.Minute {
			return msgs.Errorf("deploy.specInterval", iv.name, iv.v)
		}
	}
	if s.Registry != "" && !registryRe.MatchString(s.Registry) {
		return msgs.Errorf("deploy.specBad", "registry", s.Registry)
	}
	if s.RegistryTags != "" {
		if _, err := regexp.Compile(s.RegistryTags); err != nil {
			return msgs.Errorf("deploy.specBad", "registry_tags", s.RegistryTags)
		}
	}
	return nil
}

// ValidRepo — адрес репозитория: https/http/ssh или scp-вид git@host:path.
func ValidRepo(repo string) error {
	if allowLocalRepos && strings.HasPrefix(repo, "/") {
		return nil
	}
	if repo == "" || strings.HasPrefix(repo, "-") || strings.ContainsAny(repo, " \t\n'\"`$\\;&|<>") {
		return msgs.Errorf("deploy.specBad", "repo", repo)
	}
	if scpRepoRe.MatchString(repo) {
		return nil
	}
	u, err := url.Parse(repo)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh") {
		return msgs.Errorf("deploy.specBad", "repo", repo)
	}
	if u.User != nil && u.Scheme != "ssh" {
		// Логин и пароль в адресе — мимо зашифрованного хранилища.
		return msgs.Errorf("deploy.specRepoCreds")
	}
	return nil
}

// allowLocalRepos — только для тестов: локальный путь вместо адреса.
var allowLocalRepos bool

// AllowLocalReposForTest разрешает локальный путь как репозиторий —
// только тестам других пакетов (хаба), в работе не вызывается.
func AllowLocalReposForTest(v bool) { allowLocalRepos = v }

// ValidRef — имя ветки или тега (без «..» и ведущего «-»).
func ValidRef(ref string) bool {
	return refRe.MatchString(ref) && !strings.HasPrefix(ref, "-") && !strings.Contains(ref, "..")
}

// ValidPath — путь внутри репозитория: относительный, без «..».
func ValidPath(p string) bool {
	if !pathRe.MatchString(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// MatchTag — подходит ли тег под шаблон (glob: * и ?).
func MatchTag(glob, tag string) bool {
	if glob == "" {
		return false
	}
	re := "^" + strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(glob)) + "$"
	ok, _ := regexp.MatchString(re, tag)
	return ok
}

// TemplateFor — описание нового конвейера на языке читающего.
func TemplateFor(lang string) string {
	if lang == "en" {
		return templateEN
	}
	return templateRU
}

const templateRU = `# Репозиторий и ветка: push в неё — выкладка.
repo: https://github.com/org/app.git
ref: main
# tags: "v*"            # или выкладывать теги по шаблону

# Что делать: manifest | helm | script | compose
action: manifest
manifests:
  - deploy/k8s.yaml     # {{nkt.tag}}, {{nkt.commit}}, {{nkt.ref}} подставляются
clusters: [prod]        # кластеры хаба
# group: prod           # или кластеры группы хостов

# helm:
#   repo_name: bitnami
#   repo_url: https://charts.bitnami.com/bitnami
#   chart: nginx
#   release: web
#   namespace: web
#   values: deploy/values.yaml
#   tag_key: image.tag

# script: deploy/deploy.nkt   # сценарий хаба; параметры TAG, COMMIT, REF

# compose:                     # action: compose — стек на хосты по очереди
#   file: deploy/docker-compose.yml
#   project: app               # /srv/compose/app на хосте
#   hosts: [web1, web2]        # или group: prod
#   files: [deploy/nginx.conf] # что ещё нужно стеку (внутри каталога compose-файла)
#   images:                    # готовые образы вместо build: в чужом compose-файле
#     web: ghcr.io/org/app:{{nkt.tag}}
#   ports:                     # публикации портов вместо тех, что в compose-файле
#     web: []                  # [] — не публиковать (80/443 нужны прокси сайта)
#   bind: 127.0.0.1            # адрес публикаций без адреса (по умолчанию; 0.0.0.0 — наружу)
#   bind_force: false          # true — и у публикаций с указанным адресом
#   wait_timeout: 5m           # ждать подъёма и healthcheck
#   site: app.example.com      # после выкладки проверить сайт по HTTPS, или блоком —
#   # сайт настроит хаб (прокси, сертификат; стек — на одном хосте):
#   # site:
#   #   domains: [app.example.com]
#   #   service: web             # сервис стека
#   #   port: 80                 # порт контейнера
#   #   proxy: nginx             # необязательно: иначе какой есть, нет ни одного — nginx
#   #   cert: manual             # готовый сертификат (по умолчанию); auto — выпуск certbot; /путь — свой файл
#   # .env стека — в «Секреты» конвейера, хранится зашифрованным

# Когда ещё выкладывать, кроме вебхука и кнопки:
# poll: 5m                     # новый коммит в ветке
# registry: ghcr.io/org/app    # новый тег образа
# registry_tags: '^v?\d+\.\d+\.\d+$'
# registry_poll: 5m
`

const templateEN = `# Repository and branch: a push to it is a deployment.
repo: https://github.com/org/app.git
ref: main
# tags: "v*"            # or deploy tags matching a pattern

# What to do: manifest | helm | script | compose
action: manifest
manifests:
  - deploy/k8s.yaml     # {{nkt.tag}}, {{nkt.commit}}, {{nkt.ref}} are substituted
clusters: [prod]        # hub clusters
# group: prod           # or the clusters of a host group

# helm:
#   repo_name: bitnami
#   repo_url: https://charts.bitnami.com/bitnami
#   chart: nginx
#   release: web
#   namespace: web
#   values: deploy/values.yaml
#   tag_key: image.tag

# script: deploy/deploy.nkt   # a hub script; parameters TAG, COMMIT, REF

# compose:                     # action: compose — a stack to hosts one by one
#   file: deploy/docker-compose.yml
#   project: app               # /srv/compose/app on the host
#   hosts: [web1, web2]        # or group: prod
#   files: [deploy/nginx.conf] # what else the stack needs (inside the compose file's directory)
#   images:                    # ready images instead of build: in someone else's compose file
#     web: ghcr.io/org/app:{{nkt.tag}}
#   ports:                     # port publications instead of those in the compose file
#     web: []                  # [] — publish nothing (the site proxy needs 80/443)
#   bind: 127.0.0.1            # address for publications without one (default; 0.0.0.0 — public)
#   bind_force: false          # true — also for publications that set an address
#   wait_timeout: 5m           # wait for startup and healthchecks
#   site: app.example.com      # check the site over HTTPS after the deployment, or as a
#   # block the hub sets the site up (proxy, certificate; the stack on one host):
#   # site:
#   #   domains: [app.example.com]
#   #   service: web             # the stack service
#   #   port: 80                 # the container port
#   #   proxy: nginx             # optional: otherwise whichever is there, with none — nginx
#   #   cert: manual             # a ready certificate (default); auto — certbot issues it; /path — your own file
#   # the stack's .env goes into the pipeline's "Secrets", stored encrypted

# When else to deploy, besides the webhook and the button:
# poll: 5m                     # a new commit in the branch
# registry: ghcr.io/org/app    # a new image tag
# registry_tags: '^v?\d+\.\d+\.\d+$'
# registry_poll: 5m
`
