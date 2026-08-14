package parse

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// ProcessDetail is what a PID turned out to actually be.
type ProcessDetail struct {
	Command     string
	User        string
	UptimeS     int
	Unit        string
	ContainerID string
	Origin      string
}

// Container runtimes each name their cgroup differently, and all of them
// have to be recognised before the plain ".service" case below: a Docker
// container's cgroup is commonly "/system.slice/docker-<id>.scope", which
// would otherwise be misread as belonging to some service. The daemon's
// own "docker.service" is not at risk of matching these — ".service" is
// not hex.
var (
	reDockerCgroup  = regexp.MustCompile(`docker[-/]([0-9a-f]{12,64})`)
	rePodmanCgroup  = regexp.MustCompile(`libpod[-/]([0-9a-f]{12,64})`)
	reLXCCgroup     = regexp.MustCompile(`lxc\.payload\.([^/\s]+)`)
	reServiceCgroup = regexp.MustCompile(`/([A-Za-z0-9@:_.\\-]+\.service)`)
	reSessionCgroup = regexp.MustCompile(`session-\d+\.scope|/user\.slice/`)
)

// ProcessDetails resolves what the given PIDs are: the full command line
// and owner from `ps`, and — more usefully for spotting a listener nobody
// configured — where the process sits in the cgroup tree, which is what
// separates "a systemd unit" from "someone's SSH session" from "a
// container".
//
// The `ps` lookup is one batched call for every PID at once rather than
// one call per PID: a host can easily have dozens of listeners, and the
// per-PID form would turn a single scan into dozens of subprocesses for
// no benefit. The cgroup lookup needs no subprocess at all — it is an
// ordinary file read per PID.
//
// Everything here is best-effort. A missing `ps`, a PID that exited
// between reading the socket table and this call, a snapshot with no
// /proc — all yield an absent entry rather than an error, because none of
// them mean the listener itself is any less real.
func ProcessDetails(ctx context.Context, c collect.Collector, pids []int) map[int]ProcessDetail {
	unique := make([]int, 0, len(pids))
	seen := make(map[int]struct{}, len(pids))
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if _, dup := seen[pid]; dup {
			continue
		}
		seen[pid] = struct{}{}
		unique = append(unique, pid)
	}
	if len(unique) == 0 {
		return nil
	}
	sort.Ints(unique)

	details := make(map[int]ProcessDetail, len(unique))

	// -ww matters: without it ps truncates the command line to the
	// terminal width, which throws away precisely the part that
	// identifies an unknown service. etimes (elapsed seconds) rather than
	// lstart because it is a single whitespace-free token — an absolute
	// date would make the columns ambiguous to split — and because "up 40
	// seconds" is a stronger signal of an ad-hoc process than a timestamp.
	list := make([]string, len(unique))
	for i, pid := range unique {
		list[i] = strconv.Itoa(pid)
	}
	if out, err := c.Run(ctx, "ps", "-ww", "-o", "pid=,user=,etimes=,args=", "-p", strings.Join(list, ",")); err == nil && out.OK() {
		for _, line := range strings.Split(strings.ReplaceAll(out.Stdout, "\r\n", "\n"), "\n") {
			pid, detail, ok := parsePSLine(line)
			if !ok {
				continue
			}
			details[pid] = detail
		}
	}

	for _, pid := range unique {
		raw, err := c.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
		if err != nil {
			continue
		}
		unit, containerID, origin := classifyCgroup(string(raw))
		if unit == "" && containerID == "" && origin == "" {
			continue
		}
		d := details[pid]
		d.Unit, d.ContainerID, d.Origin = unit, containerID, origin
		details[pid] = d
	}

	return details
}

// parsePSLine splits one `ps -o pid=,user=,etimes=,args=` row. The first
// three columns are single tokens; everything after them is the command
// line and is taken verbatim, spacing included, rather than re-joined
// from split fields.
func parsePSLine(line string) (int, ProcessDetail, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return 0, ProcessDetail{}, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, ProcessDetail{}, false
	}
	uptime, _ := strconv.Atoi(fields[2])

	rest := line
	for i := 0; i < 3; i++ {
		rest = strings.TrimLeft(rest, " \t")
		j := strings.IndexAny(rest, " \t")
		if j < 0 {
			rest = ""
			break
		}
		rest = rest[j:]
	}

	return pid, ProcessDetail{
		Command: strings.TrimSpace(rest),
		User:    fields[1],
		UptimeS: uptime,
	}, true
}

// classifyCgroup reads the cgroup path a process belongs to and works out
// what kind of thing is running. Handles both cgroup v2 ("0::/path") and
// v1 (several "N:controller:/path" lines) by simply scanning the whole
// file — the interesting markers are unambiguous wherever they appear.
func classifyCgroup(content string) (unit, containerID, origin string) {
	if m := reDockerCgroup.FindStringSubmatch(content); m != nil {
		return "", m[1], model.OriginContainer
	}
	if m := rePodmanCgroup.FindStringSubmatch(content); m != nil {
		return "", m[1], model.OriginContainer
	}
	if m := reLXCCgroup.FindStringSubmatch(content); m != nil {
		return "", m[1], model.OriginContainer
	}
	// A session scope check has to come before the .service one: an
	// interactive session lives at /user.slice/user-1000.slice/session-3.scope,
	// which contains no ".service" at all, but a process started from
	// inside one via `systemd-run --user` could show both — and the fact
	// that a human started it is the more useful of the two answers.
	if reSessionCgroup.MatchString(content) {
		return "", "", model.OriginManual
	}
	if m := reServiceCgroup.FindStringSubmatch(content); m != nil {
		return m[1], "", model.OriginService
	}
	return "", "", ""
}

// EnrichListeners fills in what each listening process actually is, in
// place. Listeners sharing a PID (nginx's master bound to both 80 and
// 443, say) all get the same details from a single lookup.
func EnrichListeners(ctx context.Context, c collect.Collector, listeners []model.Listener) {
	pids := make([]int, 0, len(listeners))
	for _, l := range listeners {
		pids = append(pids, l.PID)
	}
	details := ProcessDetails(ctx, c, pids)
	if len(details) == 0 {
		return
	}
	for i := range listeners {
		d, ok := details[listeners[i].PID]
		if !ok {
			continue
		}
		listeners[i].Command = d.Command
		listeners[i].User = d.User
		listeners[i].UptimeS = d.UptimeS
		listeners[i].Unit = d.Unit
		listeners[i].ContainerID = d.ContainerID
		listeners[i].Origin = d.Origin
	}
}
