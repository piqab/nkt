package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Раздел «fail2ban» хоста: джейлы, забаненные адреса, бан и разбан,
// журнал за неделю, файлы джейлов через общий путь записи конфигураций
// (проверка fail2ban-client -t, история, откат), шаблоны.
//
// Защита от самоблокировки: хаб сообщает хосту свой внешний адрес — как
// его видит этот хост (PUT /fail2ban/hub-addr) — и он попадает в
// [DEFAULT] ignoreip отдельным файлом. Забанить адрес хаба или адрес, с
// которого пришёл сам запрос, нельзя.

const (
	f2bHubAddrKey = "fail2ban.hub_addr"
	// F2BTemplatesKey — свои шаблоны в kv (на хабе — общие для хостов;
	// экспорт хаба переносит их с историей).
	F2BTemplatesKey = "fail2ban.templates"
	// F2BTemplatePrefix — путь версий шаблона в истории.
	F2BTemplatePrefix = "nkt-f2b-tpl:"
	// f2bBansTTL — сколько живёт облегчённое состояние для /overview:
	// хаб опрашивает раз в минуту, чаще спрашивать fail2ban незачем.
	f2bBansTTL = 50 * time.Second
)

// f2bCache — последнее облегчённое состояние (для сводки хабу).
type f2bCache struct {
	mu sync.Mutex
	at time.Time
	st *model.Fail2banState
}

func (s *Server) f2bRoot() string { return s.cfg.Fail2banRoot }

func (s *Server) f2bCollector() collect.Collector { return s.scanner.Collector() }

// f2bBans — облегчённое состояние из кэша (не старше TTL).
func (s *Server) f2bBans(ctx context.Context) *model.Fail2banState {
	s.f2b.mu.Lock()
	defer s.f2b.mu.Unlock()
	if s.f2b.st != nil && time.Since(s.f2b.at) < f2bBansTTL {
		return s.f2b.st
	}
	s.f2b.st = fail2ban.CollectBans(ctx, s.f2bCollector())
	s.f2b.at = time.Now()
	return s.f2b.st
}

// f2bInvalidate — после бана или правки: следующий опрос спросит заново.
func (s *Server) f2bInvalidate() {
	s.f2b.mu.Lock()
	s.f2b.st = nil
	s.f2b.mu.Unlock()
}

// f2bSummary — сводка для /overview: хаб по ней считает баны и замечает
// новые.
func (s *Server) f2bSummary(ctx context.Context) map[string]any {
	st := s.f2bBans(ctx)
	bans := []map[string]string{}
	for _, j := range st.Jails {
		for _, b := range j.Bans {
			if len(bans) >= 1000 {
				break
			}
			bans = append(bans, map[string]string{"ip": b.IP, "jail": b.Jail})
		}
	}
	out := map[string]any{"installed": st.Installed, "running": st.Running, "banned": st.BannedNow, "bans": bans}
	if st.Installed {
		// Хаб по этому флагу передаёт свой адрес сразу, а не через
		// несколько часов: fail2ban могли поставить только что.
		out["hub_protected"] = s.f2bHubProtected(ctx)
	}
	return out
}

// f2bHubProtected — адрес хаба известен и файл защиты актуален.
func (s *Server) f2bHubProtected(ctx context.Context) bool {
	hub := s.f2bHubAddr(ctx)
	if !hub.IsValid() {
		return false
	}
	c := s.f2bCollector()
	raw, err := c.ReadFile(path.Join(s.f2bRoot(), fail2ban.HubIgnoreFile))
	return err == nil && string(raw) == fail2ban.HubIgnoreContent(fail2ban.DefaultIgnoreIP(c, s.f2bRoot()), hub)
}

// f2bHubAddr — внешний адрес хаба, сообщённый хабом.
func (s *Server) f2bHubAddr(ctx context.Context) netip.Addr {
	raw, ok, err := s.db.KVGet(ctx, f2bHubAddrKey)
	if err != nil || !ok {
		return netip.Addr{}
	}
	a, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return netip.Addr{}
	}
	return a
}

// clientAddr — адрес, с которого пришёл запрос оператора. Через хаб —
// первый адрес X-Forwarded-For (его добавляет прокси хаба); напрямую —
// адрес соединения. Используется только для защиты от собственного бана.
func clientAddr(r *http.Request) netip.Addr {
	if viaHubTunnel(r) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if a, err := netip.ParseAddr(strings.TrimSpace(strings.Split(xff, ",")[0])); err == nil {
				return a.Unmap()
			}
		}
		return netip.Addr{}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

// f2bFindings — находки fail2ban по последнему скану.
func (s *Server) f2bFindings(ctx context.Context) []model.Finding {
	if s.scanner == nil {
		return nil
	}
	snap := s.scanner.Latest()
	if snap == nil {
		return nil
	}
	return fail2ban.Findings(snap.Fail2ban, snap.Listeners, s.f2bHubAddr(ctx))
}

// handleF2BStatus — GET /fail2ban: полное состояние (свежее).
func (s *Server) handleF2BStatus(w http.ResponseWriter, r *http.Request) {
	c := s.f2bCollector()
	st := fail2ban.Collect(r.Context(), c)
	hub := s.f2bHubAddr(r.Context())
	ignoreList, ignoreFile, ignoreDefined := fail2ban.DefaultIgnoreSource(c, s.f2bRoot())
	out := map[string]any{
		"state":          st,
		"root":           s.f2bRoot(),
		"manual_jail":    fail2ban.ManualJail,
		"manual_ready":   fail2ban.HasJail(st, fail2ban.ManualJail),
		"hub_file":       path.Join(s.f2bRoot(), fail2ban.HubIgnoreFile),
		"default_ignore": ignoreList,
		"ignore_source":  ignoreFile,
		"ignore_defined": ignoreDefined,
		"log_path":       fail2ban.LogPath,
		"actions":        fail2ban.Actions,
		"simulated":      s.cfg.IsFixtures(),
	}
	out["nkt_jails"] = s.f2bNktJails(st)
	if hub.IsValid() {
		out["hub_addr"] = hub.String()
	}
	if a := clientAddr(r); a.IsValid() {
		out["client_ip"] = a.String()
	}
	writeJSON(w, http.StatusOK, out)
}

// f2bNktJail — джейл из файла nkt (jail.d/nkt-*.local): по нему видно и
// выключенные джейлы, которых нет среди запущенных.
type f2bNktJail struct {
	Jail    string `json:"jail"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	// Failed — включён, сервер работает, а джейла среди запущенных нет:
	// fail2ban его не поднял. Reason — строки журнала fail2ban о нём.
	Failed bool   `json:"failed,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (s *Server) f2bNktJails(st *model.Fail2banState) []f2bNktJail {
	c := s.f2bCollector()
	files, _ := c.Glob(path.Join(s.f2bRoot(), "jail.d", "nkt-*.local"))
	sort.Strings(files)
	out := []f2bNktJail{}
	for _, f := range files {
		raw, err := c.ReadFile(f)
		if err != nil {
			continue
		}
		for name, keys := range fail2ban.ParseINI(string(raw)) {
			if name == "DEFAULT" || !fail2ban.ValidJailName(name) {
				continue
			}
			enabled := strings.EqualFold(strings.TrimSpace(keys["enabled"]), "true")
			j := f2bNktJail{Jail: name, Path: f, Enabled: enabled}
			if enabled && st != nil && st.Running && !fail2ban.HasJail(st, name) {
				j.Failed = true
				j.Reason = jailStartError(c, name)
			}
			out = append(out, j)
		}
	}
	return out
}

// jailStartError — последние строки журнала fail2ban об ошибках джейла
// (почему он не поднялся: нет журнала, нет фильтра, не тот backend).
func jailStartError(c collect.Collector, jail string) string {
	raw, err := c.ReadFile(fail2ban.LogPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > 2000 {
		lines = lines[len(lines)-2000:]
	}
	marks := []string{"'" + jail + "'", "[" + jail + "]", " " + jail + " jail", "jail " + jail}
	var hit []string
	for _, l := range lines {
		if !strings.Contains(l, "ERROR") && !strings.Contains(l, "WARNING") {
			continue
		}
		for _, m := range marks {
			if strings.Contains(l, m) {
				// Команды reload fail2ban пишет целиком — десятки
				// килобайт; причина в конце строки («Received …»).
				l = strings.TrimSpace(l)
				if len(l) > 400 {
					if i := strings.LastIndex(l, "Received "); i > 0 {
						l = l[:120] + " … " + l[i:]
					}
					if len(l) > 600 {
						l = l[:600] + " …"
					}
				}
				hit = append(hit, l)
				break
			}
		}
	}
	if len(hit) > 5 {
		hit = hit[len(hit)-5:]
	}
	return strings.Join(hit, "\n")
}

// handleF2BLog — GET /fail2ban/log?days=7&q=&jail=&action=&limit=.
func (s *Server) handleF2BLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	res := fail2ban.ReadLog(r.Context(), s.f2bCollector(), fail2ban.LogQuery{
		Days: days, Text: q.Get("q"), Jail: q.Get("jail"), Action: q.Get("action"), Limit: limit,
	})
	writeJSON(w, http.StatusOK, res)
}

type f2bBanRequest struct {
	// Jail — джейл; пусто — ручной джейл nkt.
	Jail string   `json:"jail"`
	IPs  []string `json:"ips"`
	// BanTime — срок ручного бана, секунды (0 — неделя).
	BanTime int64 `json:"ban_time"`
	All     bool  `json:"all"`
}

func parseIPs(list []string) ([]netip.Addr, error) {
	if len(list) == 0 {
		return nil, msgs.Errorf("f2b.noIPs")
	}
	if len(list) > 256 {
		return nil, msgs.Errorf("f2b.tooManyIPs", len(list))
	}
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, raw := range list {
		a, err := fail2ban.ParseIP(raw)
		if err != nil {
			return nil, err
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out, nil
}

// handleF2BBan — POST /fail2ban/ban {jail, ips, ban_time}.
func (s *Server) handleF2BBan(w http.ResponseWriter, r *http.Request) {
	var req f2bBanRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ips, err := parseIPs(req.IPs)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	hub, self := s.f2bHubAddr(ctx), clientAddr(r)
	for _, a := range ips {
		if a.IsLoopback() || a.IsUnspecified() {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.banLoopback", a.String()))
			return
		}
		if hub.IsValid() && a == hub {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.banHub", a.String()))
			return
		}
		if self.IsValid() && a == self {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.banSelf", a.String()))
			return
		}
	}
	user := auth.Username(ctx)
	c := s.f2bCollector()
	if !fail2ban.Installed(ctx, c) {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("f2b.notInstalled"))
		return
	}
	jail, banTime := req.Jail, int64(0)
	if jail == "" || jail == fail2ban.ManualJail {
		jail, banTime = fail2ban.ManualJail, req.BanTime
		if banTime <= 0 {
			banTime = fail2ban.DefaultManualBanTime
		}
		if banTime > 10*365*24*3600 {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.badBanTime"))
			return
		}
		if err := s.f2bEnsureManual(ctx, user); err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	} else if !fail2ban.HasJail(fail2ban.CollectBans(ctx, c), jail) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.badJail", jail))
		return
	}
	res, err := fail2ban.Ban(ctx, c, jail, ips, banTime)
	s.db.Audit(ctx, user, "fail2ban.ban", jail+": "+joinAddrs(ips), auditResult(err), errText(err))
	s.f2bInvalidate()
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jail": jail, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated})
}

func joinAddrs(ips []netip.Addr) string {
	parts := make([]string, len(ips))
	for i, a := range ips {
		parts[i] = a.String()
	}
	return strings.Join(parts, " ")
}

// handleF2BUnban — POST /fail2ban/unban {jail, ips} или {all: true}.
func (s *Server) handleF2BUnban(w http.ResponseWriter, r *http.Request) {
	var req f2bBanRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	c := s.f2bCollector()
	var (
		res    collect.CommandResult
		err    error
		target string
	)
	if req.All {
		target = "*"
		res, err = fail2ban.UnbanAll(ctx, c)
	} else {
		ips, perr := parseIPs(req.IPs)
		if perr != nil {
			writeErr(w, r, http.StatusBadRequest, perr)
			return
		}
		if req.Jail != "" && !fail2ban.ValidJailName(req.Jail) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.badJail", req.Jail))
			return
		}
		target = strings.TrimPrefix(req.Jail+": ", ": ") + joinAddrs(ips)
		res, err = fail2ban.Unban(ctx, c, req.Jail, ips)
	}
	s.db.Audit(ctx, user, "fail2ban.unban", target, auditResult(err), errText(err))
	s.f2bInvalidate()
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated})
}

// handleF2BReload — POST /fail2ban/reload {jail}.
func (s *Server) handleF2BReload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Jail string `json:"jail"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	res, err := fail2ban.Reload(ctx, s.f2bCollector(), req.Jail)
	target := req.Jail
	if target == "" {
		target = "fail2ban"
	}
	s.db.Audit(ctx, auth.Username(ctx), "fail2ban.reload", target, auditResult(err), errText(err))
	s.f2bInvalidate()
	s.rescanLater()
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated})
}

// f2bWrite — запись файла fail2ban общим путём конфигураций: проверка
// fail2ban-client -t с откатом, версия в истории, apply — перезагрузка.
// Возвращает, изменился ли файл.
func (s *Server) f2bWrite(ctx context.Context, user, rel, content, note string, apply bool) (bool, error) {
	if s.configs == nil {
		return false, msgs.Errorf("f2b.configsUnavailable")
	}
	full := path.Join(s.f2bRoot(), rel)
	if raw, err := s.f2bCollector().ReadFile(full); err == nil && string(raw) == content {
		return false, nil
	}
	_, err := s.configs.Write(ctx, msgs.FromContext(ctx), user, full, content, note, apply)
	return err == nil, err
}

// f2bEnsureManual — ручной джейл nkt: фильтр и джейл, если их ещё нет.
func (s *Server) f2bEnsureManual(ctx context.Context, user string) error {
	c := s.f2bCollector()
	if fail2ban.HasJail(fail2ban.CollectBans(ctx, c), fail2ban.ManualJail) {
		return nil
	}
	// Фильтр — наш и без настроек: переписывается, если отличается (в
	// 1.11.x он был без <HOST>, и fail2ban не принимал его при reload).
	if _, err := s.f2bWrite(ctx, user, fail2ban.ManualFilterFile, fail2ban.ManualFilterContent, msgs.Tc(ctx, "f2b.noteManual"), false); err != nil {
		return err
	}
	if _, err := s.f2bWrite(ctx, user, fail2ban.ManualJailFile, fail2ban.ManualJailContent(fail2ban.DefaultManualBanTime), msgs.Tc(ctx, "f2b.noteManual"), false); err != nil {
		return err
	}
	_, err := fail2ban.Reload(ctx, c, "")
	return err
}

// f2bEnsureHubIgnore — файл защиты хаба актуален (если адрес известен и
// fail2ban установлен). changed — файл переписан.
func (s *Server) f2bEnsureHubIgnore(ctx context.Context, user string) (bool, error) {
	hub := s.f2bHubAddr(ctx)
	c := s.f2bCollector()
	if !hub.IsValid() || !fail2ban.Installed(ctx, c) {
		return false, nil
	}
	content := fail2ban.HubIgnoreContent(fail2ban.DefaultIgnoreIP(c, s.f2bRoot()), hub)
	running := fail2ban.CollectBans(ctx, c).Running
	return s.f2bWrite(ctx, user, fail2ban.HubIgnoreFile, content, msgs.Tc(ctx, "f2b.noteHub", hub.String()), running)
}

// f2bAfterConfigChange — после записи или отката файла fail2ban в
// «Конфигурациях»: общий ignoreip мог измениться, файл защиты хаба
// собирается заново (он читается последним и иначе перекрыл бы правку).
func (s *Server) f2bAfterConfigChange(ctx context.Context, user, p string) {
	if s.cfg == nil || s.scanner == nil || s.db == nil || strings.Trim(s.f2bRoot(), "/") == "" {
		return
	}
	root := strings.TrimSuffix(s.f2bRoot(), "/") + "/"
	if !strings.HasPrefix(p, root) || p == path.Join(s.f2bRoot(), fail2ban.HubIgnoreFile) || !s.f2bHubAddr(ctx).IsValid() {
		return
	}
	if _, err := s.f2bEnsureHubIgnore(ctx, user); err != nil && s.log != nil {
		s.log.Warn("fail2ban hub ignore file not refreshed", "err", err)
	}
	s.f2bInvalidate()
}

// handleF2BIgnore — PUT /fail2ban/ignore {list, note, dry_run}: общий
// список ignoreip ([DEFAULT]). Пишется туда, где он задан (иначе в
// jail.local), затем пересобирается файл защиты хаба — адрес хаба в
// списке закреплён и в запросе не нужен. dry_run — дифф всех файлов.
func (s *Server) handleF2BIgnore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		List   []string `json:"list"`
		Note   string   `json:"note"`
		DryRun bool     `json:"dry_run"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	c := s.f2bCollector()
	hub := s.f2bHubAddr(ctx)
	var list []string
	seen := map[string]bool{}
	for _, raw := range req.List {
		for _, item := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
			if !fail2ban.ValidIgnoreEntry(item) {
				writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.badIgnoreEntry", item))
				return
			}
			if a, err := netip.ParseAddr(item); err == nil && hub.IsValid() && a.Unmap() == hub {
				continue // закреплён в файле защиты
			}
			if !seen[item] {
				seen[item] = true
				list = append(list, item)
			}
		}
	}
	if len(list) > 500 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.tooManyIgnore", len(list)))
		return
	}
	_, source, _ := fail2ban.DefaultIgnoreSource(c, s.f2bRoot())
	var changes []f2bFileChange
	ch := f2bFileChange{Path: source}
	if raw, err := c.ReadFile(source); err == nil {
		ch.Before, ch.Exists = string(raw), true
	}
	ch.After = fail2ban.SetINIKey(ch.Before, "DEFAULT", "ignoreip", strings.Join(list, " "))
	changes = append(changes, ch)
	if hub.IsValid() {
		effective := list
		if len(effective) == 0 {
			effective = []string{"127.0.0.1/8", "::1"}
		}
		hubPath := path.Join(s.f2bRoot(), fail2ban.HubIgnoreFile)
		hc := f2bFileChange{Path: hubPath, After: fail2ban.HubIgnoreContent(effective, hub)}
		if raw, err := c.ReadFile(hubPath); err == nil {
			hc.Before, hc.Exists = string(raw), true
		}
		changes = append(changes, hc)
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"files": changes})
		return
	}
	if !fail2ban.Installed(ctx, c) {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("f2b.notInstalled"))
		return
	}
	user := auth.Username(ctx)
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = msgs.Tc(ctx, "f2b.noteIgnore")
	}
	running := fail2ban.CollectBans(ctx, c).Running
	for i, ch := range changes {
		if ch.Before == ch.After {
			continue
		}
		last := i == len(changes)-1
		if _, err := s.configs.Write(ctx, msgs.FromContext(ctx), user, ch.Path, ch.After, note, last && running); err != nil {
			for _, prev := range changes[:i] {
				if prev.Before == prev.After {
					continue
				}
				if prev.Exists {
					_, _ = s.configs.Write(ctx, msgs.FromContext(ctx), user, prev.Path, prev.Before, note, false)
				} else {
					_ = c.DeleteFile(prev.Path)
				}
			}
			s.db.Audit(ctx, user, "fail2ban.ignore", strings.Join(list, " "), "error", err.Error())
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	// Без адреса хаба файла защиты нет — перечитать конфигурацию здесь.
	if !hub.IsValid() && running && changes[0].Before != changes[0].After {
		_, _ = fail2ban.Reload(ctx, c, "")
	}
	s.db.Audit(ctx, user, "fail2ban.ignore", strings.Join(list, " "), "ok", nil)
	s.f2bInvalidate()
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": changes})
}

// f2bSSHJournal — у sshd нет журнала в файле: только journald.
func (s *Server) f2bSSHJournal() bool {
	c := s.f2bCollector()
	return !c.Exists("/var/log/auth.log") && !c.Exists("/var/log/secure")
}

// handleF2BSetup — POST /fail2ban/setup: после установки и по кнопке у
// находки — защита хаба, ручной джейл, sshd через journald там, где
// журнала в файле нет. Затем перезапуск, если сервер не работал.
func (s *Server) handleF2BSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := auth.Username(ctx)
	c := s.f2bCollector()
	if !fail2ban.Installed(ctx, c) {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("f2b.notInstalled"))
		return
	}
	var done []string
	note := msgs.Tc(ctx, "f2b.noteSetup")
	if s.f2bSSHJournal() {
		rel := fail2ban.JailFile("sshd")
		if !c.Exists(path.Join(s.f2bRoot(), rel)) {
			if ok, err := s.f2bWrite(ctx, user, rel, "# Managed by nkt: sshd has no log file here, read journald.\n[sshd]\nbackend = systemd\n", note, false); err != nil {
				writeErr(w, r, http.StatusBadRequest, err)
				return
			} else if ok {
				done = append(done, rel)
			}
		}
	}
	if ok, err := s.f2bEnsureHubIgnore(ctx, user); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	} else if ok {
		done = append(done, fail2ban.HubIgnoreFile)
	}
	if ok, err := s.f2bWrite(ctx, user, fail2ban.ManualFilterFile, fail2ban.ManualFilterContent, note, false); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	} else if ok {
		done = append(done, fail2ban.ManualFilterFile)
	}
	if !c.Exists(path.Join(s.f2bRoot(), fail2ban.ManualJailFile)) {
		if _, err := s.f2bWrite(ctx, user, fail2ban.ManualJailFile, fail2ban.ManualJailContent(fail2ban.DefaultManualBanTime), note, false); err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		done = append(done, fail2ban.ManualJailFile)
	}
	// Всё записано — один перезапуск: работающему — reload, стоящему
	// (после установки на Debian 12 он часто и не стартовал) — restart.
	var applyErr error
	if fail2ban.CollectBans(ctx, c).Running {
		if len(done) > 0 {
			_, applyErr = fail2ban.Reload(ctx, c, "")
		}
	} else {
		_, applyErr = s.services.Action(ctx, user, model.ServiceFail2ban, "restart")
	}
	s.db.Audit(ctx, user, "fail2ban.setup", strings.Join(done, ", "), auditResult(applyErr), errText(applyErr))
	s.f2bInvalidate()
	s.rescanLater()
	if applyErr != nil {
		writeErr(w, r, http.StatusBadRequest, applyErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": done})
}

// handleF2BHubAddr — PUT /fail2ban/hub-addr {addr}: внешний адрес хаба,
// как его видит этот хост (хаб узнаёт его из SSH_CONNECTION). Файл защиты
// обновляется сразу, если fail2ban установлен.
func (s *Server) handleF2BHubAddr(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Addr string `json:"addr"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	a, err := fail2ban.ParseIP(req.Addr)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	prev := s.f2bHubAddr(ctx)
	if err := s.db.KVSet(ctx, f2bHubAddrKey, a.String()); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	user := auth.Username(ctx)
	if prev != a {
		s.db.Audit(ctx, user, "fail2ban.hub_addr", a.String(), "ok", nil)
	}
	changed, err := s.f2bEnsureHubIgnore(ctx, user)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "changed": changed})
}

// --- шаблоны ----------------------------------------------------------

func (s *Server) f2bCustomTemplates(ctx context.Context) []fail2ban.Template {
	var list []fail2ban.Template
	if raw, ok, err := s.db.KVGet(ctx, F2BTemplatesKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	for i := range list {
		list[i].Builtin, list[i].Available = false, true
	}
	return list
}

func (s *Server) f2bSaveTemplates(ctx context.Context, list []fail2ban.Template) error {
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.db.KVSet(ctx, F2BTemplatesKey, string(b))
}

// F2BTemplateDoc — шаблон одним текстом для истории версий (и для
// экспорта хаба).
func F2BTemplateDoc(t fail2ban.Template) string {
	return "## description: " + strings.TrimSpace(t.Description) + "\n## jail\n" + strings.TrimRight(t.Jail, "\n") + "\n## filter\n" + strings.TrimRight(t.Filter, "\n") + "\n"
}

// parseTemplateDoc — обратно из текста версии.
func parseTemplateDoc(name, doc string) fail2ban.Template {
	t := fail2ban.Template{Name: name}
	section := ""
	var jail, filter []string
	for _, line := range strings.Split(doc, "\n") {
		switch {
		case strings.HasPrefix(line, "## description:"):
			t.Description = strings.TrimSpace(strings.TrimPrefix(line, "## description:"))
			continue
		case line == "## jail":
			section = "jail"
			continue
		case line == "## filter":
			section = "filter"
			continue
		}
		switch section {
		case "jail":
			jail = append(jail, line)
		case "filter":
			filter = append(filter, line)
		}
	}
	t.Jail = strings.TrimSpace(strings.Join(jail, "\n")) + "\n"
	if f := strings.TrimSpace(strings.Join(filter, "\n")); f != "" {
		t.Filter = f + "\n"
	}
	return t
}

// f2bTemplateDocs — история шаблонов в общей истории версий.
type f2bTemplateDocs struct{ s *Server }

func (d *f2bTemplateDocs) ValidDocPath(p string) bool {
	return fail2ban.ValidTemplateName(strings.TrimPrefix(p, F2BTemplatePrefix))
}

func (d *f2bTemplateDocs) CurrentDoc(ctx context.Context, p string) (string, error) {
	name := strings.TrimPrefix(p, F2BTemplatePrefix)
	for _, t := range d.s.f2bCustomTemplates(ctx) {
		if t.Name == name {
			return F2BTemplateDoc(t), nil
		}
	}
	return "", nil
}

func (d *f2bTemplateDocs) RestoreDoc(ctx context.Context, user, p, content string) (string, error) {
	name := strings.TrimPrefix(p, F2BTemplatePrefix)
	t := parseTemplateDoc(name, content)
	if err := d.s.f2bPutTemplate(ctx, t); err != nil {
		return "", err
	}
	d.s.db.Audit(ctx, user, "fail2ban.template_rollback", name, "ok", nil)
	return F2BTemplateDoc(t), nil
}

func (s *Server) f2bPutTemplate(ctx context.Context, t fail2ban.Template) error {
	list := s.f2bCustomTemplates(ctx)
	replaced := false
	for i := range list {
		if list[i].Name == t.Name {
			list[i], replaced = t, true
		}
	}
	if !replaced {
		list = append(list, t)
	}
	return s.f2bSaveTemplates(ctx, list)
}

// handleF2BTemplates — GET /fail2ban/templates: встроенные (с признаком
// «программа есть на этом хосте») и свои.
func (s *Server) handleF2BTemplates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.f2bCollector()
	builtin := fail2ban.Builtin(s.f2bSSHJournal())
	for i := range builtin {
		builtin[i].Builtin = true
		bin := fail2ban.ServiceBinary[builtin[i].Service]
		builtin[i].Available = bin == "" || collect.Which(ctx, c, bin)
	}
	custom := s.f2bCustomTemplates(ctx)
	if custom == nil {
		custom = []fail2ban.Template{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"builtin": builtin, "custom": custom, "version_prefix": F2BTemplatePrefix})
}

type f2bTemplateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Jail        string `json:"jail"`
	Filter      string `json:"filter"`
	Note        string `json:"note"`
	DryRun      bool   `json:"dry_run"`
	// HubAddr — адрес хаба глазами этого хоста (хаб узнаёт его по SSH
	// перед применением): защита от самобана ставится до джейла.
	HubAddr string `json:"hub_addr,omitempty"`
}

func (req *f2bTemplateRequest) check() error {
	req.Name = strings.TrimSpace(req.Name)
	if !fail2ban.ValidTemplateName(req.Name) {
		return msgs.Errorf("f2b.badTemplateName", req.Name)
	}
	jail := fail2ban.TemplateJailName(req.Jail)
	if jail == "" || jail == "DEFAULT" || !fail2ban.ValidJailName(jail) {
		return msgs.Errorf("f2b.templateNoJail")
	}
	if len(req.Jail) > 64<<10 || len(req.Filter) > 64<<10 || len(req.Description) > 500 {
		return msgs.Errorf("f2b.templateTooLarge")
	}
	return nil
}

// handleF2BTemplateSave — PUT /fail2ban/templates: создать или изменить
// свой шаблон (версией в истории).
func (s *Server) handleF2BTemplateSave(w http.ResponseWriter, r *http.Request) {
	var req f2bTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := req.check(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	t := fail2ban.Template{Name: req.Name, Description: strings.TrimSpace(req.Description),
		Jail: strings.TrimRight(req.Jail, "\n") + "\n", Filter: strings.TrimSpace(req.Filter)}
	if t.Filter != "" {
		t.Filter += "\n"
	}
	var before []byte
	for _, old := range s.f2bCustomTemplates(ctx) {
		if old.Name == t.Name {
			before = []byte(F2BTemplateDoc(old))
		}
	}
	// Нового шаблона до правки не было — исходной версии не нужно.
	if err := s.f2bPutTemplate(ctx, t); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if s.configs != nil {
		_, _ = s.configs.RecordDoc(ctx, F2BTemplatePrefix+t.Name, model.ServiceFail2ban, user, store.ActionEdit,
			strings.TrimSpace(req.Note), before, []byte(F2BTemplateDoc(t)))
	}
	s.db.Audit(ctx, user, "fail2ban.template_save", t.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "template": t})
}

// handleF2BTemplateDelete — DELETE /fail2ban/templates/{name}.
func (s *Server) handleF2BTemplateDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !fail2ban.ValidTemplateName(name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.badTemplateName", name))
		return
	}
	ctx := r.Context()
	list := s.f2bCustomTemplates(ctx)
	out := list[:0]
	var removed *fail2ban.Template
	for i := range list {
		if list[i].Name == name {
			t := list[i]
			removed = &t
			continue
		}
		out = append(out, list[i])
	}
	if removed == nil {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("f2b.templateNotFound", name))
		return
	}
	if err := s.f2bSaveTemplates(ctx, out); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	user := auth.Username(ctx)
	if s.configs != nil {
		// Удаление — тоже версия (пустая): из истории шаблон можно вернуть.
		_, _ = s.configs.RecordDoc(ctx, F2BTemplatePrefix+name, model.ServiceFail2ban, user, store.ActionEdit,
			msgs.Tc(ctx, "f2b.templateDeletedNote"), []byte(F2BTemplateDoc(*removed)), []byte(F2BTemplateDoc(fail2ban.Template{Name: name})))
	}
	s.db.Audit(ctx, user, "fail2ban.template_delete", name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// f2bFileChange — файл, который запишет применение шаблона.
type f2bFileChange struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
	Exists bool   `json:"exists"`
}

// handleF2BTemplateApply — POST /fail2ban/templates/apply: записать джейл
// шаблона (и его фильтр) на этот хост. dry_run — только показать, что
// изменится. Текст приходит из окна: шаблон мог быть поправлен перед
// применением, а свои шаблоны живут на хабе, не на хосте.
func (s *Server) handleF2BTemplateApply(w http.ResponseWriter, r *http.Request) {
	var req f2bTemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := req.check(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	c := s.f2bCollector()
	root := s.f2bRoot()
	hub := s.f2bHubAddr(ctx)
	if req.HubAddr != "" {
		a, err := fail2ban.ParseIP(req.HubAddr)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		hub = a
	}
	jailName := fail2ban.TemplateJailName(req.Jail)
	jailText := strings.TrimRight(fail2ban.WithFilter(req.Name, req.Jail, req.Filter), "\n") + "\n"
	header := "# Managed by nkt: template " + req.Name + "\n"
	jailText = header + strings.TrimPrefix(jailText, header)
	var changes []f2bFileChange
	add := func(rel, after string) {
		full := path.Join(root, rel)
		ch := f2bFileChange{Path: full, After: after}
		if raw, err := c.ReadFile(full); err == nil {
			ch.Before, ch.Exists = string(raw), true
		}
		changes = append(changes, ch)
	}
	// Защита хаба — первой: джейл не должен начать работу раньше, чем
	// адрес хаба окажется в ignoreip.
	hubProtected := false
	if hub.IsValid() {
		add(fail2ban.HubIgnoreFile, fail2ban.HubIgnoreContent(fail2ban.DefaultIgnoreIP(c, root), hub))
		last := changes[len(changes)-1]
		hubProtected = last.Before == last.After
	}
	if f := strings.TrimSpace(req.Filter); f != "" {
		add(fail2ban.FilterFile(req.Name), f+"\n")
	}
	add(fail2ban.JailFile(jailName), jailText)

	// Проверка конфигурации с будущими файлами — и в пробном прогоне, и
	// перед записью.
	pending := map[string]string{}
	for _, ch := range changes {
		if ch.Before != ch.After {
			pending[strings.TrimPrefix(strings.TrimPrefix(ch.Path, root), "/")] = ch.After
		}
	}
	installed := fail2ban.Installed(ctx, c)
	var test fail2ban.ConfigTest
	if installed && len(pending) > 0 {
		test = fail2ban.TestConfig(ctx, c, root, pending)
	} else {
		test = fail2ban.ConfigTest{OK: true}
	}
	hubStr := ""
	if hub.IsValid() {
		hubStr = hub.String()
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"files": changes, "test": test, "hub_addr": hubStr, "hub_protected": hubProtected})
		return
	}
	if !installed {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("f2b.notInstalled"))
		return
	}
	if !test.OK {
		key := "f2b.configTestFailed"
		if test.Preexisting {
			key = "f2b.configBrokenBefore"
		}
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf(key, test.Output))
		return
	}
	user := auth.Username(ctx)
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = msgs.Tc(ctx, "f2b.noteTemplate", req.Name)
	}
	if req.HubAddr != "" && hub != s.f2bHubAddr(ctx) {
		_ = s.db.KVSet(ctx, f2bHubAddrKey, hub.String())
		s.db.Audit(ctx, user, "fail2ban.hub_addr", hub.String(), "ok", nil)
	}
	before := fail2ban.CollectBans(ctx, c)
	running := before.Running
	rollback := func(upto int, reload bool) {
		for k, prev := range changes[:upto] {
			if prev.Before == prev.After {
				continue
			}
			last := reload && k == upto-1
			if prev.Exists {
				_, _ = s.configs.Write(ctx, msgs.FromContext(ctx), user, prev.Path, prev.Before, note, last)
			} else {
				_ = c.DeleteFile(prev.Path)
			}
		}
		if reload {
			_, _ = fail2ban.Reload(ctx, c, "")
		}
	}
	lastChanged := -1
	for i, ch := range changes {
		if ch.Before != ch.After {
			lastChanged = i
		}
	}
	for i, ch := range changes {
		if ch.Before == ch.After {
			continue
		}
		if _, err := s.configs.Write(ctx, msgs.FromContext(ctx), user, ch.Path, ch.After, note, i == lastChanged && running); err != nil {
			// Не прошла проверка или перезагрузка — записанное до этого
			// возвращается как было.
			rollback(i, false)
			s.db.Audit(ctx, user, "fail2ban.template_apply", req.Name, "error", err.Error())
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	s.f2bInvalidate()
	var unbanned []string
	// В снимке (fixtures) команды поддельные: список джейлов после
	// перезагрузки не меняется, сверять нечего.
	if running && c.Mode() != "fixtures" {
		// Перезагрузка прошла, но джейл мог не подняться (нет журнала,
		// не тот backend): прежние джейлы и джейл шаблона должны работать.
		after := fail2ban.CollectBans(ctx, c)
		want := fail2ban.RunningJails(before)
		if jailEnabled(jailText, jailName) {
			want = append(want, jailName)
		}
		have := map[string]bool{}
		for _, n := range fail2ban.RunningJails(after) {
			have[n] = true
		}
		var lost []string
		for _, n := range want {
			if !have[n] && !slices.Contains(lost, n) {
				lost = append(lost, n)
			}
		}
		if len(lost) > 0 {
			rollback(len(changes), true)
			s.f2bInvalidate()
			msg := msgs.Errorf("f2b.jailsLost", strings.Join(lost, ", "), f2bLogTail(c))
			s.db.Audit(ctx, user, "fail2ban.template_apply", req.Name, "error", msgs.Localize(msgs.FromContext(ctx), msg))
			writeErr(w, r, http.StatusBadRequest, msg)
			return
		}
		// Хаб успел попасть в бан (старые записи журнала свежим джейлом) —
		// снять сразу.
		if hub.IsValid() {
			if unbanned = fail2ban.BannedIn(after, hub.String()); len(unbanned) > 0 {
				_ = fail2ban.UnbanIP(ctx, c, hub.String())
				s.db.Audit(ctx, user, "fail2ban.hub_unban", hub.String(), "ok", strings.Join(unbanned, ", "))
			}
		}
	}
	s.db.Audit(ctx, user, "fail2ban.template_apply", req.Name, "ok", nil)
	s.f2bInvalidate()
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": changes, "jail": jailName, "hub_unbanned": unbanned})
}

// jailEnabled — включён ли джейл name в тексте.
func jailEnabled(text, name string) bool {
	v := strings.ToLower(strings.TrimSpace(fail2ban.ParseINI(text)[name]["enabled"]))
	return v == "true" || v == "yes" || v == "1" || v == "on"
}

// f2bLogTail — последние строки журнала fail2ban (почему джейл не
// поднялся).
func f2bLogTail(c collect.Collector) string {
	raw, err := c.ReadFile("/var/log/fail2ban.log")
	if err != nil {
		if res, err := c.Run(context.Background(), "journalctl", "-u", "fail2ban", "-n", "15", "--no-pager"); err == nil {
			return strings.TrimSpace(res.Stdout)
		}
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	return strings.Join(lines, "\n")
}

var logPathRe = regexp.MustCompile(`^/var/log/[A-Za-z0-9._/-]+$`)

// handleF2BRegexTest — POST /fail2ban/regex-test {filter, log}: сколько
// строк журнала совпадает с фильтром (fail2ban-regex). log — файл под
// /var/log или «systemd-journal».
func (s *Server) handleF2BRegexTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filter string `json:"filter"`
		Log    string `json:"log"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	logArg := strings.TrimSpace(req.Log)
	if logArg != "systemd-journal" && (!logPathRe.MatchString(logArg) || strings.Contains(logArg, "..")) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.badLogPath", logArg))
		return
	}
	filter := strings.TrimSpace(req.Filter)
	if filter == "" || len(filter) > 64<<10 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.emptyFilter"))
		return
	}
	if !strings.Contains(filter, "[Definition]") {
		filter = "[Definition]\nfailregex = " + filter
	}
	tmp, err := os.CreateTemp("", "nkt-f2b-filter-*.conf")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.WriteString(filter + "\n")
	_ = tmp.Close()
	res, err := s.f2bCollector().RunTimeout(r.Context(), 2*time.Minute, "fail2ban-regex", logArg, tmp.Name())
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	out := res.Output()
	matched, total := -1, -1
	if m := regexpLines.FindStringSubmatch(out); m != nil {
		total, _ = strconv.Atoi(m[1])
		matched, _ = strconv.Atoi(m[2])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": res.OK(), "matched": matched, "lines": total, "output": tailLines(out, 60), "simulated": res.Simulated,
	})
}

var regexpLines = regexp.MustCompile(`Lines:\s*(\d+) lines?, \d+ ignored, (\d+) matched`)

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// handleF2BInstallWS — установка fail2ban (apt), как ufw: живой вывод или
// фоновым заданием (POST ?job=1). После неё интерфейс зовёт
// /fail2ban/setup.
func (s *Server) handleF2BInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	c := s.f2bCollector()
	if !collect.Which(r.Context(), c, "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	if fail2ban.Installed(r.Context(), c) {
		writeError(w, http.StatusConflict, msgs.Tc(r.Context(), "f2b.alreadyInstalled"))
		return
	}
	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get update && apt-get install -y fail2ban")
	}
	s.runUpdateSession(w, r, "fail2ban-install", buildCmd, "firewall.install_fail2ban", "fail2ban", s.cfg.TerminalIdleTimeout)
}

func (s *Server) handleF2BInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("fail2ban-install")
	writeSessionStatus(w, active, finished, exitCode)
}

// f2bTemplateDocs годится в историю версий конфигураций.
var _ control.K8sDocs = (*f2bTemplateDocs)(nil)
