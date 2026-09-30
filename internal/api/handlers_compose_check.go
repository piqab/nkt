package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/parse"
	"github.com/piqab/nkt/internal/site"
)

// Движок compose на хосте и сухой прогон выкладки стека: всё, что сделала
// бы выкладка, кроме изменений — стек в /srv/compose и контейнеры не
// трогаются.

// ComposeEngineInfo — чем хост поднимает стеки.
type ComposeEngineInfo struct {
	// Engine — docker, podman или пусто (нет ни того, ни другого).
	Engine  string `json:"engine"`
	Version string `json:"version,omitempty"`
	// Compose — «<engine> compose» работает.
	Compose        bool   `json:"compose"`
	ComposeVersion string `json:"compose_version,omitempty"`
	ComposeError   string `json:"compose_error,omitempty"`
	// Installable — Docker можно поставить отсюда (apt-get, не фикстуры).
	Installable bool `json:"installable"`
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func (s *Server) composeEngineInfo(ctx context.Context) ComposeEngineInfo {
	c := s.scanner.Collector()
	info := ComposeEngineInfo{Engine: composeEngine(ctx, c)}
	info.Installable = s.cfg.Mode != config.ModeFixtures && collect.Which(ctx, c, "apt-get")
	if info.Engine == "" {
		return info
	}
	if s.cfg.Mode == config.ModeFixtures {
		// Версии в заготовках нет: вывод команд там — чужие образцы.
		info.Compose = true
		return info
	}
	if res, err := c.RunTimeout(ctx, 20*time.Second, info.Engine, "--version"); err == nil && res.OK() {
		info.Version = firstLine(res.Stdout)
	}
	res, err := c.RunTimeout(ctx, 30*time.Second, info.Engine, "compose", "version")
	switch {
	case err != nil:
		info.ComposeError = err.Error()
	case !res.OK():
		info.ComposeError = firstLine(res.Output())
	default:
		info.Compose = true
		info.ComposeVersion = firstLine(res.Stdout)
	}
	return info
}

// handleComposeEngine — GET /compose/engine.
func (s *Server) handleComposeEngine(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.composeEngineInfo(r.Context()))
}

// ComposeFileChange — что выкладка сделала бы с файлом стека.
type ComposeFileChange struct {
	Path string `json:"path"`
	// State — new, changed или same.
	State string `json:"state"`
}

// ComposeImageCheck — есть ли образ.
type ComposeImageCheck struct {
	Image string `json:"image"`
	// State — registry (есть в registry), local (скачан на хост), missing
	// (registry ответил «нет такого») или unknown (проверить не удалось).
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// Arches — архитектуры образа (из манифеста или скачанного образа);
	// ArchMismatch — среди них нет архитектуры хоста.
	Arches       []string `json:"arches,omitempty"`
	ArchMismatch bool     `json:"arch_mismatch,omitempty"`
}

// ComposeCheckResult — итог сухого прогона на хосте.
type ComposeCheckResult struct {
	ComposeEngineInfo
	StackExists bool                `json:"stack_exists"`
	Files       []ComposeFileChange `json:"files"`
	// Env — new, changed, same, keep (останется прежний) или none.
	Env string `json:"env"`
	// EnvEdited — .env на хосте правили вручную (выкладка перезапишет).
	EnvEdited   bool                `json:"env_edited,omitempty"`
	ConfigOK    bool                `json:"config_ok"`
	ConfigError string              `json:"config_error,omitempty"`
	Services    []string            `json:"services,omitempty"`
	Images      []ComposeImageCheck `json:"images,omitempty"`
	// PortsBusy — порты хоста из публикаций, занятые не этим стеком.
	PortsBusy []ComposePortBusy `json:"ports_busy,omitempty"`
	// HostArch — архитектура хоста (для сверки с образами).
	HostArch string `json:"host_arch,omitempty"`
	// Simulated — фикстуры: команды не выполнялись по-настоящему.
	Simulated bool `json:"simulated,omitempty"`
	// Сайт конвейера: проверен ли порт (SiteChecked), есть ли сервис
	// (SiteFound), какие порты объявляют сервис и образ.
	SiteChecked bool   `json:"site_checked,omitempty"`
	SiteFound   bool   `json:"site_found,omitempty"`
	SitePorts   []int  `json:"site_ports,omitempty"`
	SiteImage   string `json:"site_image,omitempty"`
}

// composeCheckDir — каталог сухого прогона: рядом со стеками, чтобы
// команды видели его так же, как настоящий стек (вне песочницы у них
// свой /tmp).
const composeCheckDir = parse.ComposeStacksDir + "/.nkt-check"

// handleComposeCheck — POST /compose/stacks/check: сухой прогон выкладки.
func (s *Server) handleComposeCheck(w http.ResponseWriter, r *http.Request) {
	var req composeDeployRequest
	if err := decodeJSONLimit(r, &req, composeDeployMaxBytes+1<<20); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := req.validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	c := s.scanner.Collector()
	res := ComposeCheckResult{ComposeEngineInfo: s.composeEngineInfo(ctx), Env: "none"}
	res.Simulated = s.cfg.Mode == config.ModeFixtures

	// Что изменится: сравнение с тем, что лежит в стеке сейчас.
	dir := composeStackDir(req.Project)
	res.StackExists = c.Exists(dir + "/" + req.File)
	for rel, content := range req.Files {
		st := "new"
		if old, err := c.ReadFile(dir + "/" + rel); err == nil {
			st = "changed"
			if string(old) == content {
				st = "same"
			}
		}
		res.Files = append(res.Files, ComposeFileChange{Path: rel, State: st})
	}
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	oldEnv, envErr := c.ReadFile(dir + "/.env")
	switch {
	case req.Env != nil && envErr != nil:
		res.Env = "new"
	case req.Env != nil && string(oldEnv) == *req.Env:
		res.Env = "same"
	case req.Env != nil:
		res.Env = "changed"
	case envErr == nil:
		res.Env = "keep"
	}
	if req.Env != nil {
		res.EnvEdited = envEditedOnHost(c, dir, req.EnvSHA)
	}
	if res.Engine == "" || !res.Compose {
		writeJSON(w, http.StatusOK, res)
		return
	}

	// compose config — на копии стека во временном каталоге. На
	// фикстурах команды не настоящие, а писать в дерево фикстур незачем.
	work := dir
	if !res.Simulated {
		var rels []string
		var err error
		work, rels, err = s.composeCheckCopy(c, req, oldEnv, envErr == nil)
		if work != "" {
			defer composeCheckCleanup(c, work, rels)
		}
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
	}
	// Файл публикации сайта в копии — из настоящего стека (composeArgsIn
	// подхватит его, раз он лежит рядом).
	args := composeArgsIn(c, work, req.Project, req.File)
	out, err := c.RunTimeout(ctx, 2*time.Minute, res.Engine, append(args, "config", "-q")...)
	switch {
	case err != nil:
		res.ConfigError = err.Error()
	case !out.OK():
		res.ConfigError = strings.TrimSpace(out.Output())
	default:
		res.ConfigOK = true
	}
	if !res.ConfigOK || res.Simulated {
		// На фикстурах вывод команд — заготовка, а не список сервисов.
		writeJSON(w, http.StatusOK, res)
		return
	}
	if req.SiteService != "" {
		ports, image, found, err := serviceDeclaredPorts(ctx, c, res.Engine, work, req.Project, req.File, req.SiteService)
		res.SiteChecked, res.SiteFound, res.SitePorts, res.SiteImage = err == nil, found, ports, image
	}
	if out, err := c.RunTimeout(ctx, time.Minute, res.Engine, append(args, "config", "--services")...); err == nil && out.OK() {
		res.Services = splitLines(out.Stdout)
	}
	res.HostArch = hostArch(ctx, c)
	if out, err := c.RunTimeout(ctx, time.Minute, res.Engine, append(args, "config", "--images")...); err == nil && out.OK() {
		for _, img := range dedupe(splitLines(out.Stdout)) {
			res.Images = append(res.Images, s.checkImage(ctx, c, res.Engine, img, res.HostArch))
		}
	}
	res.PortsBusy = busyPorts(ctx, c, res.Engine, work, req.Project, req.File)
	writeJSON(w, http.StatusOK, res)
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// composeCheckCopy раскладывает стек во временный каталог: файлы из
// запроса, .env (новый или прежний) и файл публикации сайта из стека.
func (s *Server) composeCheckCopy(c collect.Collector, req composeDeployRequest, oldEnv []byte, hasOldEnv bool) (string, []string, error) {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	// Каталог создаёт WriteFile — и вне песочницы, если /srv в ней
	// закрыт: так же, как пишутся файлы настоящей выкладки.
	work := composeCheckDir + "/" + req.Project + "-" + hex.EncodeToString(b)
	var rels []string
	for rel, content := range req.Files {
		rels = append(rels, rel)
		if err := c.WriteFile(work+"/"+rel, []byte(content), 0o644); err != nil {
			return work, rels, err
		}
	}
	switch {
	case req.Env != nil:
		rels = append(rels, ".env")
		if err := c.WriteFile(work+"/.env", []byte(*req.Env), 0o600); err != nil {
			return work, rels, err
		}
	case hasOldEnv:
		rels = append(rels, ".env")
		if err := c.WriteFile(work+"/.env", oldEnv, 0o600); err != nil {
			return work, rels, err
		}
	}
	if ov, err := c.ReadFile(composeStackDir(req.Project) + "/" + site.OverrideFile); err == nil {
		rels = append(rels, site.OverrideFile)
		if err := c.WriteFile(work+"/"+site.OverrideFile, ov, 0o644); err != nil {
			return work, rels, err
		}
	}
	return work, rels, nil
}

// composeCheckCleanup удаляет временный каталог: ровно записанные в
// него файлы (не обход каталога — ссылки и скрытые файлы не в счёт), затем
// их каталоги от глубоких к верхним (удаляется только пустой каталог).
func composeCheckCleanup(c collect.Collector, work string, rels []string) {
	dirs := map[string]bool{}
	for _, rel := range rels {
		_ = c.DeleteFile(work + "/" + rel)
		for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
			dirs[work+"/"+d] = true
		}
	}
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return strings.Count(list[i], "/") > strings.Count(list[j], "/") })
	for _, d := range list {
		_ = c.DeleteFile(d)
	}
	_ = c.DeleteFile(work)
	_ = c.DeleteFile(composeCheckDir) // только если пуст
}

// checkImage — есть ли образ: сначала на хосте, потом в registry (без
// скачивания: только манифест).
func (s *Server) checkImage(ctx context.Context, c collect.Collector, engine, img, hostArch string) (out ComposeImageCheck) {
	out = ComposeImageCheck{Image: img}
	defer func() {
		if hostArch != "" && len(out.Arches) > 0 && !slices.Contains(out.Arches, hostArch) {
			out.ArchMismatch = true
		}
	}()
	if res, err := c.RunTimeout(ctx, 20*time.Second, engine, "image", "inspect", "--format", "{{.Architecture}}", img); err == nil && res.OK() {
		out.State = "local"
		if a := strings.TrimSpace(res.Stdout); a != "" {
			out.Arches = []string{a}
		}
	}
	var res collect.CommandResult
	var err error
	if engine == "docker" {
		res, err = c.RunTimeout(ctx, 45*time.Second, "docker", "manifest", "inspect", img)
	} else if collect.Which(ctx, c, "skopeo") {
		res, err = c.RunTimeout(ctx, 45*time.Second, "skopeo", "inspect", "--raw", "docker://"+img)
	} else {
		if out.State == "" {
			out.State = "unknown"
			out.Detail = msgs.Tc(ctx, "compose.checkNoSkopeo")
		}
		return out
	}
	switch {
	case err == nil && res.OK():
		out.State = "registry"
		out.Detail = ""
		if arches := manifestArches(res.Stdout); len(arches) > 0 {
			out.Arches = arches
		}
	case out.State == "local":
		// Скачан, а в registry не виден (закрытый, нет входа) — поднимется
		// из скачанного, но pull упадёт: сказать об этом.
		out.Detail = firstLine(res.Output())
	default:
		text := firstLine(res.Output())
		if err != nil {
			text = err.Error()
		}
		low := strings.ToLower(text)
		out.State = "unknown"
		if strings.Contains(low, "no such manifest") || strings.Contains(low, "manifest unknown") || strings.Contains(low, "not found") {
			out.State = "missing"
		}
		out.Detail = text
	}
	return out
}

// manifestArches — архитектуры из списка манифестов (multi-arch образ);
// одиночный манифест архитектуры не называет — пусто.
func manifestArches(raw string) []string {
	var doc struct {
		Manifests []struct {
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal([]byte(raw), &doc) != nil {
		return nil
	}
	var out []string
	for _, m := range doc.Manifests {
		a := m.Platform.Architecture
		if a == "" || a == "unknown" || (m.Platform.OS != "" && m.Platform.OS != "linux") || slices.Contains(out, a) {
			continue
		}
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// hostArch — архитектура хоста в терминах образов (amd64, arm64…).
func hostArch(ctx context.Context, c collect.Collector) string {
	res, err := c.RunTimeout(ctx, 10*time.Second, "uname", "-m")
	if err != nil || !res.OK() {
		return ""
	}
	switch m := strings.TrimSpace(res.Stdout); m {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l", "armv6l", "armhf":
		return "arm"
	case "i686", "i386":
		return "386"
	default:
		return m
	}
}

// ComposePortBusy — порт хоста из публикаций стека, который уже слушает
// кто-то другой.
type ComposePortBusy struct {
	Addr   string `json:"addr"`
	Holder string `json:"holder,omitempty"`
}

// busyPorts — публикации стека (копия в work) против слушающих сокетов
// хоста; порты, которые держит этот же стек (compose ps), — не заняты.
func busyPorts(ctx context.Context, c collect.Collector, engine, work, project, file string) []ComposePortBusy {
	res, err := c.RunTimeout(ctx, time.Minute, engine, append(composeArgsIn(c, work, project, file), "config", "--format", "json")...)
	if err != nil || !res.OK() {
		return nil
	}
	var doc struct {
		Services map[string]struct {
			Ports []struct {
				HostIP    string `json:"host_ip"`
				Published any    `json:"published"`
				Protocol  string `json:"protocol"`
			} `json:"ports"`
		} `json:"services"`
	}
	if json.Unmarshal([]byte(res.Stdout), &doc) != nil {
		return nil
	}
	type want struct {
		ip, proto string
		port      int
	}
	var wants []want
	for _, svc := range doc.Services {
		for _, p := range svc.Ports {
			port := 0
			switch v := p.Published.(type) {
			case float64:
				port = int(v)
			case string:
				port, _ = strconv.Atoi(v)
			}
			if port == 0 {
				continue // порт хоста — любой свободный
			}
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			wants = append(wants, want{p.HostIP, proto, port})
		}
	}
	if len(wants) == 0 {
		return nil
	}
	// Что уже держит сам стек (повторная выкладка — не «занято»).
	own := map[string]bool{}
	if dir := composeStackDir(project); composeFileIn(c, dir) != "" {
		if ps, err := c.RunTimeout(ctx, time.Minute, engine, append(composeArgs(c, project, composeFileIn(c, dir)), "ps", "--format", "json")...); err == nil && ps.OK() {
			for _, ct := range composePS(ps.Stdout) {
				for _, p := range ct.Publishers {
					if p.PublishedPort > 0 {
						own[p.Protocol+"/"+strconv.Itoa(p.PublishedPort)] = true
					}
				}
			}
		}
	}
	ss, err := c.RunTimeout(ctx, 20*time.Second, "ss", "-Hltunp")
	if err != nil || !ss.OK() {
		return nil
	}
	wild := func(ip string) bool { return ip == "" || ip == "0.0.0.0" || ip == "::" || ip == "*" || ip == "[::]" }
	var out []ComposePortBusy
	seen := map[string]bool{}
	for _, line := range splitLines(ss.Stdout) {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		proto := f[0]
		local := f[4]
		i := strings.LastIndexByte(local, ':')
		if i < 0 {
			continue
		}
		ip := strings.Trim(strings.SplitN(local[:i], "%", 2)[0], "[]")
		port, err := strconv.Atoi(local[i+1:])
		if err != nil {
			continue
		}
		holder := ""
		if len(f) >= 7 {
			if j := strings.Index(f[6], "((\""); j >= 0 {
				holder = strings.SplitN(f[6][j+3:], "\"", 2)[0]
			}
		}
		for _, w := range wants {
			if w.port != port || w.proto != proto || own[proto+"/"+strconv.Itoa(port)] {
				continue
			}
			if !wild(w.ip) && !wild(ip) && w.ip != ip {
				continue
			}
			addr := w.ip
			if addr == "" {
				addr = "0.0.0.0"
			}
			key := proto + " " + addr + ":" + strconv.Itoa(port)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ComposePortBusy{Addr: key, Holder: holder})
		}
	}
	return out
}

// composePSEntry — контейнер из «compose ps --format json».
type composePSEntry struct {
	Name       string `json:"Name"`
	Service    string `json:"Service"`
	State      string `json:"State"`
	Health     string `json:"Health"`
	ExitCode   int    `json:"ExitCode"`
	Publishers []struct {
		URL           string `json:"URL"`
		PublishedPort int    `json:"PublishedPort"`
		TargetPort    int    `json:"TargetPort"`
		Protocol      string `json:"Protocol"`
	} `json:"Publishers"`
}

// composePS — вывод «compose ps --format json»: массив (старые compose) или
// объект на строку (новые).
func composePS(out string) []composePSEntry {
	out = strings.TrimSpace(out)
	var list []composePSEntry
	if strings.HasPrefix(out, "[") {
		_ = json.Unmarshal([]byte(out), &list)
		return list
	}
	for _, line := range splitLines(out) {
		var e composePSEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			list = append(list, e)
		}
	}
	return list
}

// Установка Docker на хост: docker.io из репозитория дистрибутива и
// плагин compose (Ubuntu — docker-compose-v2, репозиторий Docker —
// docker-compose-plugin, Debian 13 — docker-compose). Уже стоящий docker
// (в том числе docker-ce) не трогается: доставляется только compose.
const dockerInstallScript = `set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update
if ! command -v docker >/dev/null 2>&1; then
  apt-get install -y docker.io
fi
if ! docker compose version >/dev/null 2>&1; then
  for p in docker-compose-v2 docker-compose-plugin docker-compose; do
    if apt-cache show "$p" >/dev/null 2>&1 && apt-get install -y "$p" && docker compose version >/dev/null 2>&1; then
      break
    fi
  done
fi
systemctl enable --now docker
docker --version
docker compose version
`

// handleDockerInstallWS — /system/docker-install/ws (?job=1 — заданием).
func (s *Server) handleDockerInstallWS(w http.ResponseWriter, r *http.Request) {
	lang := msgs.LangFromRequest(r)
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(lang, "pkgInstall.fixturesDisabled"))
		return
	}
	c := s.scanner.Collector()
	if !collect.Which(r.Context(), c, "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(lang, "pkgInstall.aptGetMissing"))
		return
	}
	if info := s.composeEngineInfo(r.Context()); info.Engine == "docker" && info.Compose {
		writeError(w, http.StatusConflict, msgs.T(lang, "compose.dockerInstalled", info.Version))
		return
	}
	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", dockerInstallScript)
	}
	s.runUpdateSession(w, r, "docker-install", buildCmd, "system.install_docker", "docker", s.cfg.TerminalIdleTimeout)
}

// handleDockerInstallStatus — итог установки для живого вывода.
func (s *Server) handleDockerInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("docker-install")
	writeSessionStatus(w, active, finished, exitCode)
}
