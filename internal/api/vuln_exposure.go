package api

import (
	"context"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// Доступность уязвимого из сети: уязвимость в пакете, чья служба слушает
// не только loopback, или в образе контейнера с опубликованным наружу
// портом, опаснее той же уязвимости в библиотеке, до которой снаружи не
// дотянуться. Это первый шаг приоритета «по реальной опасности»: связь
// пакета с портом — через владельца бинарника слушающего процесса
// (dpkg -S / rpm -qf), образа с портом — через публикацию контейнера.
// Библиотеки (libssl в nginx) пока не связываются.

// VulnExposure — порты, по которым до пакета или образа можно достучаться
// из сети. Ключи — имена пакетов ОС и ссылки на образы, как в находках.
type VulnExposure struct {
	Packages map[string][]int `json:"packages"`
	Images   map[string][]int `json:"images"`
}

// Сколько держится посчитанное: страница опрашивает раз в несколько
// секунд, а dpkg -S на каждый опрос незачем.
const vulnExposureTTL = 2 * time.Minute

type vulnExposureCache struct {
	mu     sync.Mutex
	digest string
	at     time.Time
	value  VulnExposure
}

// Процессы, за которыми стоят контейнеры: их порты считаются по
// публикации контейнера, а не по пакету docker.
var containerProxies = map[string]bool{"docker-proxy": true, "rootlessport": true, "conmon": true, "slirp4netns": true, "pasta": true}

// Если владельца бинарника узнать не вышло (нет dpkg/rpm, процесс уже
// завершился, снимок fixtures) — пакеты по имени процесса.
var processPackages = map[string][]string{
	"sshd":     {"openssh-server", "openssh"},
	"dhclient": {"isc-dhcp-client", "dhcp-client"},
	"nginx":    {"nginx", "nginx-core", "nginx-full", "nginx-light", "nginx-extras"},
	"apache2":  {"apache2", "apache2-bin"},
	"httpd":    {"httpd", "apache2-bin"},
	"mysqld":   {"mysql-server", "mysql-server-core", "mariadb-server", "mariadb-server-core"},
	"mariadbd": {"mariadb-server", "mariadb-server-core"},
	"postgres": {"postgresql"},
	"named":    {"bind9", "bind"},
	"master":   {"postfix"},
	"dovecot":  {"dovecot-core", "dovecot"},
	"smbd":     {"samba"},
	"exim4":    {"exim4-daemon-light", "exim4-daemon-heavy"},
}

// netReachable — сокет слушает не только loopback.
func netReachable(addr string) bool {
	a := strings.Trim(addr, "[]")
	if i := strings.IndexByte(a, '%'); i >= 0 {
		a = a[:i]
	}
	switch a {
	case "", "*", "0.0.0.0", "::":
		return true
	case "localhost":
		return false
	}
	ip, err := netip.ParseAddr(a)
	if err != nil {
		return true
	}
	return !ip.Unmap().IsLoopback()
}

func addPort(m map[string][]int, key string, port int) {
	if key == "" || port <= 0 || slices.Contains(m[key], port) {
		return
	}
	m[key] = append(m[key], port)
	slices.Sort(m[key])
}

// vulnExposure — доступность по последнему снимку (с кэшем).
func (s *Server) vulnExposure(ctx context.Context) VulnExposure {
	empty := VulnExposure{Packages: map[string][]int{}, Images: map[string][]int{}}
	if s.scanner == nil {
		return empty
	}
	snap := s.scanner.Latest()
	if snap == nil {
		return empty
	}
	c := &s.vulnExposureCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.digest == snap.Digest && time.Since(c.at) < vulnExposureTTL {
		return c.value
	}
	c.value = computeVulnExposure(ctx, s.scanner.Collector(), snap)
	c.digest, c.at = snap.Digest, time.Now()
	return c.value
}

func computeVulnExposure(ctx context.Context, col collect.Collector, snap *model.Snapshot) VulnExposure {
	out := VulnExposure{Packages: map[string][]int{}, Images: map[string][]int{}}

	// Образы: контейнер публикует порт не только на loopback.
	for _, ct := range snap.Container {
		if ct.State == "exited" || ct.State == "declared" {
			continue
		}
		for _, p := range ct.Ports {
			if p.Published() && netReachable(p.HostIP) {
				addPort(out.Images, ct.Image, p.HostPort)
			}
		}
	}
	for _, ct := range snap.Podman {
		if ct.State == "exited" {
			continue
		}
		for _, p := range ct.Ports {
			if p.Published() && netReachable(p.HostIP) {
				addPort(out.Images, ct.Image, p.HostPort)
			}
		}
	}

	// Пакеты ОС: слушающие в сети процессы хоста → их бинарники → пакеты.
	type listener struct {
		process string
		port    int
		exe     string
	}
	var ls []listener
	exes := map[int]string{}
	for _, l := range snap.Listeners {
		if !netReachable(l.Address) || l.Origin == model.OriginContainer || l.ContainerID != "" || containerProxies[l.Process] {
			continue
		}
		exe, ok := exes[l.PID]
		if !ok && l.PID > 0 && col != nil {
			if res, err := col.Run(ctx, "readlink", "/proc/"+strconv.Itoa(l.PID)+"/exe"); err == nil && res.ExitCode == 0 {
				exe = strings.TrimSuffix(strings.TrimSpace(res.Stdout), " (deleted)")
				if !strings.HasPrefix(exe, "/") {
					exe = ""
				}
			}
			exes[l.PID] = exe
		}
		ls = append(ls, listener{process: l.Process, port: l.Port, exe: exe})
	}
	var paths []string
	for _, l := range ls {
		if l.exe != "" && !slices.Contains(paths, l.exe) {
			paths = append(paths, l.exe)
		}
	}
	owners := packageOwners(ctx, col, paths)
	for _, l := range ls {
		if pkgs := owners[l.exe]; len(pkgs) > 0 {
			for _, p := range pkgs {
				addPort(out.Packages, p, l.port)
			}
			continue
		}
		names := processPackages[l.process]
		if names == nil && l.process != "" {
			names = []string{l.process, l.process + "-server"}
		}
		for _, p := range names {
			addPort(out.Packages, p, l.port)
		}
	}
	return out
}

// packageOwners — пакеты, которым принадлежат файлы (dpkg -S, иначе rpm -qf).
func packageOwners(ctx context.Context, col collect.Collector, paths []string) map[string][]string {
	owners := map[string][]string{}
	if len(paths) == 0 || col == nil {
		return owners
	}
	if collect.Which(ctx, col, "dpkg") {
		res, err := col.Run(ctx, "dpkg", append([]string{"-S"}, paths...)...)
		if err == nil {
			parseDpkgOwners(res.Stdout, owners)
		}
		return owners
	}
	if collect.Which(ctx, col, "rpm") {
		for _, p := range paths {
			res, err := col.Run(ctx, "rpm", "-qf", "--qf", "%{NAME}\n", p)
			if err != nil || res.ExitCode != 0 {
				continue
			}
			if name := strings.TrimSpace(res.Stdout); name != "" && !strings.Contains(name, " ") {
				owners[p] = []string{name}
			}
		}
	}
	return owners
}

// parseDpkgOwners — строки «пакет[, пакет]: путь»; архитектура
// («openssl:amd64») отрезается — в находках имена без неё.
func parseDpkgOwners(out string, owners map[string][]string) {
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, ": /")
		if i <= 0 {
			continue
		}
		path := strings.TrimSpace(line[i+2:])
		for _, p := range strings.Split(line[:i], ",") {
			p = strings.TrimSpace(p)
			if j := strings.IndexByte(p, ':'); j > 0 {
				p = p[:j]
			}
			if p != "" && !strings.HasPrefix(p, "diversion by") && !slices.Contains(owners[path], p) {
				owners[path] = append(owners[path], p)
			}
		}
	}
}
