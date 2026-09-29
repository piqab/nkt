// Package fail2ban — состояние и управление fail2ban на хосте: джейлы,
// забаненные адреса, бан и разбан, журнал событий, файлы джейлов nkt и
// защита от самоблокировки (внешний адрес хаба в ignoreip).
//
// Всё через fail2ban-client: его вывод — единственный интерфейс, который
// одинаков у пакетов разных дистрибутивов. Команды собираются только из
// проверенных значений: имя джейла сверяется с тем, что вернул сам
// fail2ban, адрес разбирается netip — значения из запроса как есть в argv
// не попадают.
package fail2ban

import (
	"context"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// Client — программа управления.
const Client = "fail2ban-client"

// ManualJail — джейл ручных банов nkt: без фильтра и журнала, баны в
// него ставятся только командой. Отдельно от автоматических, чтобы
// перезагрузка sshd-джейла их не снимала и было видно, что бан ручной.
const ManualJail = "nkt-manual"

// DefaultManualBanTime — срок ручного бана по умолчанию: неделя.
const DefaultManualBanTime = 7 * 24 * 3600

var jailNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,63}$`)

// ValidJailName — имя, которое можно передать fail2ban-client.
func ValidJailName(name string) bool { return jailNameRe.MatchString(name) }

// ParseIP — адрес для бана: только одиночный IPv4/IPv6 без зоны.
func ParseIP(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, msgs.Errorf("f2b.badIP", s)
	}
	return a.Unmap(), nil
}

// Installed — есть ли fail2ban-client.
func Installed(ctx context.Context, c collect.Collector) bool {
	return collect.Which(ctx, c, Client)
}

// Collect читает состояние: список джейлов и по каждому счётчики,
// забаненные адреса со сроками, настройки. Вызовы fail2ban-client идут
// параллельно (у каждого свой запуск python — последовательно это
// секунды на хосте с несколькими джейлами).
func Collect(ctx context.Context, c collect.Collector) *model.Fail2banState {
	return collectState(ctx, c, true)
}

// CollectBans — облегчённый вариант для частого опроса хабом: только
// счётчики и забаненные адреса (fail2ban-client status по джейлам), без
// настроек и сроков.
func CollectBans(ctx context.Context, c collect.Collector) *model.Fail2banState {
	return collectState(ctx, c, false)
}

func collectState(ctx context.Context, c collect.Collector, full bool) *model.Fail2banState {
	st := &model.Fail2banState{Jails: []model.Fail2banJail{}}
	if !Installed(ctx, c) {
		return st
	}
	st.Installed = true
	if full {
		if res, err := c.Run(ctx, Client, "version"); err == nil && res.OK() {
			st.Version = parseVersion(res.Stdout)
		}
	}
	res, err := c.Run(ctx, Client, "status")
	if err != nil || !res.OK() {
		st.Error = strings.TrimSpace(res.Output())
		if err != nil {
			st.Error = err.Error()
		}
		return st
	}
	st.Running = true
	names := parseJailList(res.Stdout)
	st.Jails = make([]model.Fail2banJail, len(names))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, name := range names {
		st.Jails[i] = model.Fail2banJail{Name: name, Bans: []model.Fail2banBan{}}
		if !ValidJailName(name) {
			continue
		}
		wg.Add(1)
		go func(j *model.Fail2banJail) {
			defer wg.Done()
			if full {
				collectJail(ctx, c, j, sem)
				return
			}
			sem <- struct{}{}
			res, err := c.Run(ctx, Client, "status", j.Name)
			<-sem
			if err == nil && res.OK() {
				applyJailStatus(j, res.Stdout)
			}
		}(&st.Jails[i])
	}
	wg.Wait()
	for _, j := range st.Jails {
		st.BannedNow += j.Banned
	}
	if c.Mode() == "fixtures" {
		rebaseBans(st)
	}
	return st
}

// rebaseBans — у снимка (режим fixtures) сроки банов записаны однажды:
// они сдвигаются так, чтобы самый свежий бан начался «только что».
func rebaseBans(st *model.Fail2banState) {
	var newest time.Time
	for _, j := range st.Jails {
		for _, b := range j.Bans {
			if t, err := time.Parse(time.RFC3339, b.Since); err == nil && t.After(newest) {
				newest = t
			}
		}
	}
	if newest.IsZero() {
		return
	}
	shift := time.Since(newest) - 4*time.Minute
	move := func(s string) string {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return s
		}
		return t.Add(shift).Format(time.RFC3339)
	}
	for i := range st.Jails {
		for k := range st.Jails[i].Bans {
			b := &st.Jails[i].Bans[k]
			b.Since, b.Until = move(b.Since), move(b.Until)
		}
	}
}

// collectJail — счётчики, баны и настройки одного джейла.
func collectJail(ctx context.Context, c collect.Collector, j *model.Fail2banJail, sem chan struct{}) {
	run := func(args ...string) (string, bool) {
		sem <- struct{}{}
		defer func() { <-sem }()
		res, err := c.Run(ctx, Client, args...)
		if err != nil || !res.OK() {
			return "", false
		}
		return res.Stdout, true
	}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	do := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	var status, banTimes string
	do(func() {
		out, _ := run("status", j.Name)
		mu.Lock()
		status = out
		mu.Unlock()
	})
	do(func() {
		out, _ := run("get", j.Name, "banip", "--with-time")
		mu.Lock()
		banTimes = out
		mu.Unlock()
	})
	num := func(key string, dst *int64) {
		do(func() {
			if out, ok := run("get", j.Name, key); ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
					mu.Lock()
					*dst = n
					mu.Unlock()
				}
			}
		})
	}
	var maxRetry int64
	num("maxretry", &maxRetry)
	num("findtime", &j.FindTime)
	num("bantime", &j.BanTime)
	do(func() {
		if out, ok := run("get", j.Name, "ignoreip"); ok {
			list := parseList(out)
			mu.Lock()
			j.IgnoreIP = list
			mu.Unlock()
		}
	})
	wg.Wait()
	j.MaxRetry = int(maxRetry)
	applyJailStatus(j, status)
	for _, p := range j.LogPaths {
		if p != "/dev/null" && !c.Exists(p) {
			j.MissingLogs = append(j.MissingLogs, p)
		}
	}
	// Сроки банов (0.11+) — поверх списка из status: в status только
	// адреса, в banip --with-time ещё и когда снимется.
	times := parseBanTimes(banTimes)
	for i := range j.Bans {
		if t, ok := times[j.Bans[i].IP]; ok {
			j.Bans[i].Since, j.Bans[i].Until = t[0], t[1]
		}
	}
}

// parseVersion — «0.11.2» из «fail2ban-client version» (старые пишут
// «Fail2Ban v0.10.2»).
func parseVersion(out string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	if i := strings.LastIndex(line, " "); i >= 0 {
		line = line[i+1:]
	}
	return strings.TrimPrefix(line, "v")
}

// statusLineRe — строка дерева status: «|- Currently failed:	1».
var statusLineRe = regexp.MustCompile("^[\\s|`-]*([A-Za-z][A-Za-z ]*?):\\s*(.*)$")

// parseJailList — имена джейлов из «fail2ban-client status».
func parseJailList(out string) []string {
	for _, line := range strings.Split(out, "\n") {
		m := statusLineRe.FindStringSubmatch(line)
		if m == nil || m[1] != "Jail list" {
			continue
		}
		var names []string
		for _, n := range strings.Split(m[2], ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		return names
	}
	return nil
}

// applyJailStatus разбирает «fail2ban-client status <jail>».
func applyJailStatus(j *model.Fail2banJail, out string) {
	for _, line := range strings.Split(out, "\n") {
		m := statusLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		val := strings.TrimSpace(m[2])
		n, _ := strconv.Atoi(val)
		switch m[1] {
		case "Currently failed":
			j.Failed = n
		case "Total failed":
			j.TotalFailed = n
		case "Currently banned":
			j.Banned = n
		case "Total banned":
			j.TotalBanned = n
		case "File list":
			j.LogPaths = strings.Fields(val)
		case "Journal matches":
			j.Journal = val
		case "Banned IP list":
			for _, ip := range strings.Fields(val) {
				j.Bans = append(j.Bans, model.Fail2banBan{IP: ip, Jail: j.Name})
			}
		}
	}
}

// banTimeRe — строка «get <jail> banip --with-time»:
// «1.2.3.4 	2026-09-29 10:00:00 + 600 = 2026-09-29 10:10:00».
var banTimeRe = regexp.MustCompile(`^(\S+)\s+(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\s*\+\s*(-?\d+)\s*=\s*(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)

// parseBanTimes — адрес → [начало, конец] в RFC 3339. Срок -1 —
// бессрочный бан: конца нет.
func parseBanTimes(out string) map[string][2]string {
	res := map[string][2]string{}
	for _, line := range strings.Split(out, "\n") {
		m := banTimeRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		since := localTime(m[2])
		until := ""
		if m[3] != "-1" {
			until = localTime(m[4])
		}
		res[m[1]] = [2]string{since, until}
	}
	return res
}

func localTime(s string) string {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	if err != nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// parseList — перечень из «get <jail> ignoreip/logpath»: строки дерева
// после заголовка; «No … is …» — пусто.
func parseList(out string) []string {
	var list []string
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "|-") && !strings.HasPrefix(t, "`-") {
			continue
		}
		if v := strings.TrimSpace(t[2:]); v != "" {
			list = append(list, v)
		}
	}
	return list
}

// Covers — входит ли адрес в список ignoreip (адреса и сети CIDR).
func Covers(list []string, addr netip.Addr) bool {
	for _, item := range list {
		if p, err := netip.ParsePrefix(item); err == nil && p.Contains(addr) {
			return true
		}
		if a, err := netip.ParseAddr(item); err == nil && a.Unmap() == addr {
			return true
		}
	}
	return false
}

// run — одна команда fail2ban-client; ненулевой код — ошибка с выводом.
func run(ctx context.Context, c collect.Collector, args ...string) (collect.CommandResult, error) {
	res, err := c.Run(ctx, Client, args...)
	if err != nil {
		return res, err
	}
	if !res.OK() {
		return res, msgs.Errorf("f2b.commandFailed", strings.Join(args, " "), res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return res, nil
}

func addrStrings(ips []netip.Addr) []string {
	out := make([]string, len(ips))
	for i, a := range ips {
		out[i] = a.String()
	}
	return out
}

// Ban — забанить адреса в джейле. banTime > 0 — сначала задать срок
// джейлу (для ручного джейла: срок из окна бана).
func Ban(ctx context.Context, c collect.Collector, jail string, ips []netip.Addr, banTime int64) (collect.CommandResult, error) {
	if !ValidJailName(jail) {
		return collect.CommandResult{}, msgs.Errorf("f2b.badJail", jail)
	}
	if banTime > 0 {
		if res, err := run(ctx, c, "set", jail, "bantime", strconv.FormatInt(banTime, 10)); err != nil {
			return res, err
		}
	}
	return run(ctx, c, append([]string{"set", jail, "banip"}, addrStrings(ips)...)...)
}

// Unban — снять бан. jail пусто — со всех джейлов.
func Unban(ctx context.Context, c collect.Collector, jail string, ips []netip.Addr) (collect.CommandResult, error) {
	if jail == "" {
		return run(ctx, c, append([]string{"unban"}, addrStrings(ips)...)...)
	}
	if !ValidJailName(jail) {
		return collect.CommandResult{}, msgs.Errorf("f2b.badJail", jail)
	}
	return run(ctx, c, append([]string{"set", jail, "unbanip"}, addrStrings(ips)...)...)
}

// UnbanAll — снять все баны.
func UnbanAll(ctx context.Context, c collect.Collector) (collect.CommandResult, error) {
	return run(ctx, c, "unban", "--all")
}

// Reload — перечитать конфигурацию: всю или одного джейла.
func Reload(ctx context.Context, c collect.Collector, jail string) (collect.CommandResult, error) {
	if jail == "" {
		return run(ctx, c, "reload")
	}
	if !ValidJailName(jail) {
		return collect.CommandResult{}, msgs.Errorf("f2b.badJail", jail)
	}
	return run(ctx, c, "reload", jail)
}

// HasJail — есть ли такой запущенный джейл.
func HasJail(st *model.Fail2banState, name string) bool {
	if st == nil {
		return false
	}
	for _, j := range st.Jails {
		if j.Name == name {
			return true
		}
	}
	return false
}
