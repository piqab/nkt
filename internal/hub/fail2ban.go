package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// fail2ban на уровне хаба: сколько забанено на каждом хосте (из опроса
// /overview), оповещения о новых банах, сводка «какие адреса забанены и
// где», бан и разбан на всех хостах заданием, передача хостам внешнего
// адреса хаба (защита от самоблокировки) и факты об адресе для разбора
// моделью.

// f2bBan — забаненный адрес в сводке хоста.
type f2bBan struct {
	IP   string `json:"ip"`
	Jail string `json:"jail"`
}

// f2bSummary — поле fail2ban ответа /api/overview хоста.
type f2bSummary struct {
	Installed bool     `json:"installed"`
	Running   bool     `json:"running"`
	Banned    int      `json:"banned"`
	Bans      []f2bBan `json:"bans"`
	// HubProtected — адрес хаба в ignoreip хоста; nil — старый nkt.
	HubProtected *bool `json:"hub_protected,omitempty"`
}

// f2bPushInterval — как часто хаб заново сообщает хосту свой адрес: он
// меняется редко, но хост мог быть переустановлен или сменить сеть.
const f2bPushInterval = 6 * time.Hour

// f2bPushRetry — как скоро повторить, если хост говорит, что защиты хаба
// у него нет (fail2ban поставили только что, файл защиты убрали).
const f2bPushRetry = 10 * time.Minute

// f2bPushState — когда и какой адрес хаб последний раз передал хосту.
type f2bPushState struct {
	at   time.Time
	addr string
}

// noteBans записывает оповещение о новых банах: адреса, которых не было
// в прошлом опросе. Первый опрос после запуска хаба только запоминает.
func (m *Manager) noteBans(ctx context.Context, hostID int64, now *f2bSummary) {
	if now == nil {
		return
	}
	m.overviewMu.Lock()
	prev, seen := m.overview[hostID]
	m.overviewMu.Unlock()
	if !seen || prev.f2b == nil {
		return
	}
	old := map[string]bool{}
	for _, b := range prev.f2b.Bans {
		old[b.Jail+"|"+b.IP] = true
	}
	var fresh []string
	for _, b := range now.Bans {
		if !old[b.Jail+"|"+b.IP] {
			fresh = append(fresh, b.IP+" ("+b.Jail+")")
		}
	}
	if len(fresh) == 0 {
		return
	}
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return
	}
	sort.Strings(fresh)
	count := len(fresh)
	if len(fresh) > 20 {
		fresh = append(fresh[:20], "…")
	}
	m.recordEventMsg(ctx, host, store.EventBans, "", "hub.newBans", count, strings.Join(fresh, ", "))
}

// maybePushHubAddr — раз в f2bPushInterval (и сразу после запуска хаба)
// узнать свой внешний адрес так, как его видит хост (SSH_CONNECTION
// сессии), и передать хосту: он добавит его в ignoreip fail2ban. Только
// по SSH: через обратный туннель адрес соединения — не тот, с которого
// хаб ходит по SSH.
func (m *Manager) maybePushHubAddr(hostID int64, channel string, sum *f2bSummary) {
	if channel != channelSSH {
		return
	}
	interval := f2bPushInterval
	if sum != nil && sum.Installed && sum.HubProtected != nil && !*sum.HubProtected {
		interval = f2bPushRetry
	}
	m.f2bMu.Lock()
	if m.f2bPushed == nil {
		m.f2bPushed = map[int64]f2bPushState{}
	}
	st := m.f2bPushed[hostID]
	if time.Since(st.at) < interval {
		m.f2bMu.Unlock()
		return
	}
	m.f2bPushed[hostID] = f2bPushState{at: time.Now(), addr: st.addr}
	m.f2bMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		addr, err := m.hubAddrSeenBy(ctx, hostID)
		if err != nil {
			m.log.Debug("hub address for fail2ban not determined", "host", hostID, "err", err)
			return
		}
		if _, err := m.HostAPI(ctx, hostID, http.MethodPut, "/api/fail2ban/hub-addr", map[string]string{"addr": addr.String()}, nil); err != nil {
			// Старая версия nkt на хосте (404) — не ошибка; повтор через
			// f2bPushInterval.
			m.log.Debug("hub address not passed to host", "host", hostID, "err", err)
			return
		}
		m.f2bMu.Lock()
		m.f2bPushed[hostID] = f2bPushState{at: time.Now(), addr: addr.String()}
		m.f2bMu.Unlock()
	}()
}

// hubAddrSeenBy — внешний адрес хаба глазами хоста: первое поле
// SSH_CONNECTION («клиент порт сервер порт»).
func (m *Manager) hubAddrSeenBy(ctx context.Context, hostID int64) (netip.Addr, error) {
	client, err := m.clientFor(ctx, hostID)
	if err != nil {
		return netip.Addr{}, err
	}
	out, err := runRemote(client, "echo $SSH_CONNECTION")
	if err != nil {
		return netip.Addr{}, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return netip.Addr{}, msgs.Errorf("hub.f2bNoSSHConnection")
	}
	return fail2ban.ParseIP(fields[0])
}

// HubAddrFor — последний переданный хосту адрес хаба (для интерфейса).
func (m *Manager) HubAddrFor(hostID int64) string {
	m.f2bMu.Lock()
	defer m.f2bMu.Unlock()
	return m.f2bPushed[hostID].addr
}

// F2BOf — сводка fail2ban хоста из последнего опроса.
func (m *Manager) F2BOf(hostID int64) (*f2bSummary, bool) {
	m.overviewMu.Lock()
	defer m.overviewMu.Unlock()
	ov, ok := m.overview[hostID]
	if !ok || ov.f2b == nil {
		return nil, false
	}
	return ov.f2b, true
}

// --- сводка по хостам -------------------------------------------------

// F2BHostRef — хост, на котором забанен адрес.
type F2BHostRef struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Jails []string `json:"jails"`
}

// F2BBannedIP — адрес и где он забанен.
type F2BBannedIP struct {
	IP    string       `json:"ip"`
	Hosts []F2BHostRef `json:"hosts"`
}

// F2BHostState — fail2ban одного хоста для сводки.
type F2BHostState struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Known     bool   `json:"known"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Banned    int    `json:"banned"`
}

// localF2B — fail2ban машины самого хаба: из её последнего скана.
func (s *Server) localF2B() *f2bSummary {
	if s.localScanner == nil {
		return nil
	}
	snap := s.localScanner.Latest()
	if snap == nil || snap.Fail2ban == nil {
		return nil
	}
	st := snap.Fail2ban
	out := &f2bSummary{Installed: st.Installed, Running: st.Running, Banned: st.BannedNow}
	for _, j := range st.Jails {
		for _, b := range j.Bans {
			out.Bans = append(out.Bans, f2bBan{IP: b.IP, Jail: b.Jail})
		}
	}
	return out
}

// f2bHosts — сводки всех хостов (включая машину хаба).
func (s *Server) f2bHosts(ctx context.Context) ([]F2BHostState, map[int64]*f2bSummary, error) {
	hosts, err := s.db.ListHosts(ctx)
	if err != nil {
		return nil, nil, err
	}
	var states []F2BHostState
	sums := map[int64]*f2bSummary{}
	allow := s.scopeFilter(ctx)
	if s.local != nil && (allow == nil || allow(localHostID)) {
		st := F2BHostState{ID: localHostID, Name: "localhost"}
		if sum := s.localF2B(); sum != nil {
			sums[localHostID] = sum
			st.Known, st.Installed, st.Running, st.Banned = true, sum.Installed, sum.Running, sum.Banned
		}
		states = append(states, st)
	}
	for _, h := range hosts {
		if h.Status != store.HostStatusOnline || (allow != nil && !allow(h.ID)) {
			continue
		}
		st := F2BHostState{ID: h.ID, Name: h.Name}
		if sum, ok := s.hub.F2BOf(h.ID); ok {
			sums[h.ID] = sum
			st.Known, st.Installed, st.Running, st.Banned = true, sum.Installed, sum.Running, sum.Banned
		}
		states = append(states, st)
	}
	return states, sums, nil
}

// handleF2BBanned — GET /hub/fail2ban/banned: адреса, забаненные сейчас,
// и на каких хостах; сверху — те, что забанены на большем числе хостов.
func (s *Server) handleF2BBanned(w http.ResponseWriter, r *http.Request) {
	states, sums, err := s.f2bHosts(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	names := map[int64]string{}
	for _, st := range states {
		names[st.ID] = st.Name
	}
	byIP := map[string]map[int64][]string{}
	for id, sum := range sums {
		for _, b := range sum.Bans {
			if byIP[b.IP] == nil {
				byIP[b.IP] = map[int64][]string{}
			}
			byIP[b.IP][id] = append(byIP[b.IP][id], b.Jail)
		}
	}
	list := make([]F2BBannedIP, 0, len(byIP))
	for ip, hosts := range byIP {
		item := F2BBannedIP{IP: ip}
		for id, jails := range hosts {
			sort.Strings(jails)
			item.Hosts = append(item.Hosts, F2BHostRef{ID: id, Name: names[id], Jails: jails})
		}
		sort.Slice(item.Hosts, func(i, j int) bool { return item.Hosts[i].Name < item.Hosts[j].Name })
		list = append(list, item)
	}
	sort.Slice(list, func(i, j int) bool {
		if len(list[i].Hosts) != len(list[j].Hosts) {
			return len(list[i].Hosts) > len(list[j].Hosts)
		}
		return list[i].IP < list[j].IP
	})
	writeJSON(w, http.StatusOK, map[string]any{"ips": list, "hosts": states})
}

// --- бан и разбан на всех хостах -----------------------------------------

// KindF2BFleet — задание хаба: бан или разбан адресов на хостах.
const KindF2BFleet = "fail2ban.fleet"

// F2BFleetParams — вход задания.
type F2BFleetParams struct {
	// Action — ban | unban.
	Action string   `json:"action"`
	IPs    []string `json:"ips"`
	// BanTime — срок бана, секунды (0 — неделя).
	BanTime int64 `json:"ban_time,omitempty"`
	// HostIDs — на каких хостах; пусто — на всех, где есть fail2ban.
	// localHostID — машина самого хаба.
	HostIDs []int64 `json:"host_ids,omitempty"`
}

// handleF2BFleet — POST /hub/fail2ban/fleet {action, ips, ban_time,
// host_ids}: задание хаба, по шагу на хост.
func (s *Server) handleF2BFleet(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var p F2BFleetParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if p.Action != "ban" && p.Action != "unban" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.f2bBadAction", p.Action))
		return
	}
	if len(p.IPs) == 0 || len(p.IPs) > 256 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.noIPs"))
		return
	}
	for i, raw := range p.IPs {
		a, err := fail2ban.ParseIP(raw)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		p.IPs[i] = a.String()
	}
	if allow := s.scopeFilter(r.Context()); allow != nil {
		for _, id := range p.HostIDs {
			if !allow(id) {
				writeErr(w, r, http.StatusForbidden, msgs.Errorf("auth.tokenHostDenied"))
				return
			}
		}
	}
	targets, err := s.f2bTargets(r.Context(), p.HostIDs)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(targets) == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.f2bNoHosts"))
		return
	}
	p.HostIDs = make([]int64, len(targets))
	for i, t := range targets {
		p.HostIDs[i] = t.ID
	}
	titleKey := "hub.f2bJobBan"
	if p.Action == "unban" {
		titleKey = "hub.f2bJobUnban"
	}
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindF2BFleet, Queue: F2BQueue, TitleKey: titleKey, TitleArgs: []any{strings.Join(p.IPs, ", "), len(targets)},
		Author: auth.Username(r.Context()), Steps: len(targets), Params: p,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// f2bTargets — хосты задания: выбранные или все онлайн-хосты, у которых
// fail2ban установлен (или ещё не известно — старый опрос).
func (s *Server) f2bTargets(ctx context.Context, ids []int64) ([]F2BHostState, error) {
	states, _, err := s.f2bHosts(ctx)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []F2BHostState
	for _, st := range states {
		if len(ids) > 0 && !want[st.ID] {
			continue
		}
		if len(ids) == 0 && st.Known && !st.Installed {
			continue
		}
		out = append(out, st)
	}
	return out, nil
}

// F2BFleetRunner выполняет задание.
type F2BFleetRunner struct{ s *Server }

// NewF2BFleetRunner — исполнитель бана на хостах.
func NewF2BFleetRunner(s *Server) *F2BFleetRunner { return &F2BFleetRunner{s: s} }

// Run — по шагу на хост; неудача на одном хосте не останавливает
// остальные.
func (r *F2BFleetRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p F2BFleetParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	path := "/api/fail2ban/ban"
	body := map[string]any{"ips": p.IPs, "ban_time": p.BanTime}
	if p.Action == "unban" {
		path = "/api/fail2ban/unban"
		body = map[string]any{"ips": p.IPs}
	}
	failed := 0
	var touched []int64
	for i, id := range p.HostIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := "localhost"
		if id != localHostID {
			h, err := r.s.db.HostByID(ctx, id)
			if err != nil {
				failed++
				jc.Log("hub.f2bHostGone", id)
				continue
			}
			name = h.Name
		}
		jc.StepKey(i+1, len(p.HostIDs), "hub.f2bStep", name)
		var code int
		var err error
		if id == localHostID {
			code, err = r.s.localAPI(ctx, jc.Job.Author, http.MethodPost, path, body, nil)
		} else {
			code, err = r.s.hub.HostAPI(ctx, id, http.MethodPost, path, body, nil)
		}
		switch {
		case err == nil:
			touched = append(touched, id)
			jc.Log("hub.f2bHostDone", name)
		case code == http.StatusNotFound || code == http.StatusMethodNotAllowed:
			jc.Log("hub.f2bHostOld", name)
		case code == http.StatusConflict:
			jc.Log("hub.f2bHostNoFail2ban", name)
		default:
			failed++
			jc.Log("hub.f2bHostFailed", name, msgs.Localize(jc.Lang(), err))
		}
	}
	s := r.s
	// Список забаненных на хабе берётся из опроса хостов по таймеру —
	// без этого новый бан появлялся в нём только через интервал опроса.
	s.refreshF2B(ctx, touched)
	s.db.Audit(ctx, jc.Job.Author, "fail2ban.fleet_"+p.Action, strings.Join(p.IPs, " "), auditOK(failed == 0), nil)
	if failed > 0 {
		return msgs.Errorf("hub.f2bFailedCount", failed, len(p.HostIDs))
	}
	return nil
}

// refreshF2B — сводки fail2ban хостов сейчас, а не к следующему опросу:
// хостов — переопросом, машины хаба — сканом.
func (s *Server) refreshF2B(ctx context.Context, ids []int64) {
	sem := make(chan struct{}, pollHostConcurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		if id == localHostID {
			if s.localScanner != nil {
				_, _ = s.localScanner.Scan(ctx)
			}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			s.hub.pollHost(ctx, id)
		}(id)
	}
	wg.Wait()
}

func auditOK(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

// localAPI — запрос к встроенному API машины хаба от имени пользователя
// (задание идёт без браузера: на время запроса заводится короткая
// сессия этого пользователя и сразу закрывается).
func (s *Server) localAPI(ctx context.Context, username, method, path string, in, out any) (int, error) {
	if s.local == nil {
		return 0, msgs.Errorf("hub.f2bNoLocal")
	}
	// Задание, запущенное API-токеном или ботом, — от имени действующего
	// администратора хаба: права уже проверены на входе (пределы токена,
	// чат бота).
	user, err := s.db.UserByName(ctx, s.actingUser(ctx, username, s.firstAdmin(ctx)))
	if err != nil {
		return 0, err
	}
	token, err := auth.NewToken()
	if err != nil {
		return 0, err
	}
	if err := s.db.CreateSession(ctx, token, user.ID, time.Now().Add(5*time.Minute), "nkt-hub-job"); err != nil {
		return 0, err
	}
	defer func() { _ = s.db.DeleteSession(context.Background(), token) }()
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	lctx := context.WithValue(ctx, chi.RouteCtxKey, (*chi.Context)(nil))
	req := httptest.NewRequestWithContext(lctx, method, path, body)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:0"
	rec := httptest.NewRecorder()
	s.local.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, msgs.Errorf("control.code", method, path, rec.Code, hostAPIError(rec.Body.Bytes()))
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			return rec.Code, err
		}
	}
	return rec.Code, nil
}

// --- факты об адресе для разбора моделью ---------------------------------

// f2bIPFacts — что хаб знает об адресе: тип, обратный DNS, на каких
// хостах забанен сейчас, события журналов fail2ban за неделю (по хостам,
// где он есть), упоминания в оповещениях хаба.
func (s *Server) f2bIPFacts(ctx context.Context, username string, ip netip.Addr) []string {
	var facts []string
	add := func(key string, args ...any) { facts = append(facts, msgs.Tc(ctx, key, args...)) }

	switch {
	case ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast():
		add("hub.aiIPPrivate")
	default:
		add("hub.aiIPPublic")
	}
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if names, err := net.DefaultResolver.LookupAddr(rctx, ip.String()); err == nil && len(names) > 0 {
		add("hub.aiIPReverse", strings.Join(names, ", "))
	} else {
		add("hub.aiIPNoReverse")
	}
	cancel()

	states, sums, err := s.f2bHosts(ctx)
	if err != nil {
		return facts
	}
	names := map[int64]string{}
	for _, st := range states {
		names[st.ID] = st.Name
	}
	var bannedOn []string
	for id, sum := range sums {
		for _, b := range sum.Bans {
			if b.IP == ip.String() {
				bannedOn = append(bannedOn, names[id]+" ("+b.Jail+")")
			}
		}
	}
	sort.Strings(bannedOn)
	installed := 0
	for _, st := range states {
		if st.Installed {
			installed++
		}
	}
	if len(bannedOn) > 0 {
		add("hub.aiIPBannedOn", len(bannedOn), installed, strings.Join(bannedOn, ", "))
	} else {
		add("hub.aiIPNotBanned", installed)
	}

	// Журналы fail2ban хостов: до 8 хостов параллельно, по 5 с на хост.
	type hostLog struct {
		name  string
		lines []string
	}
	var (
		mu   sync.Mutex
		logs []hostLog
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 4)
	)
	count := 0
	for _, st := range states {
		if !st.Installed || count >= 8 {
			continue
		}
		count++
		wg.Add(1)
		go func(st F2BHostState) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var res fail2ban.LogResult
			path := "/api/fail2ban/log?days=7&limit=200&q=" + ip.String()
			var err error
			if st.ID == localHostID {
				_, err = s.localAPI(hctx, username, http.MethodGet, path, nil, &res)
			} else {
				_, err = s.hub.HostAPI(hctx, st.ID, http.MethodGet, path, nil, &res)
			}
			if err != nil || res.Total == 0 {
				return
			}
			byAction := map[string]int{}
			for _, e := range res.Events {
				if e.IP == ip.String() {
					byAction[e.Action]++
				}
			}
			var parts []string
			for _, a := range fail2ban.Actions {
				if n := byAction[a]; n > 0 {
					parts = append(parts, fmt.Sprintf("%s×%d", a, n))
				}
			}
			lines := []string{msgs.Tc(ctx, "hub.aiIPLog", st.Name, res.Total, strings.Join(parts, ", "))}
			for i, e := range res.Events {
				if i >= 3 {
					break
				}
				lines = append(lines, "  "+e.Line)
			}
			mu.Lock()
			logs = append(logs, hostLog{name: st.Name, lines: lines})
			mu.Unlock()
		}(st)
	}
	wg.Wait()
	sort.Slice(logs, func(i, j int) bool { return logs[i].name < logs[j].name })
	for _, l := range logs {
		facts = append(facts, l.lines...)
	}
	if len(logs) == 0 {
		add("hub.aiIPNoLog")
	}

	// Упоминания в оповещениях хаба.
	if events, _, err := s.hub.Events(ctx, 2000); err == nil {
		n, last := 0, ""
		for _, e := range events {
			if strings.Contains(e.Detail, ip.String()) {
				n++
				if last == "" {
					last = e.TS
				}
			}
		}
		if n > 0 {
			add("hub.aiIPEvents", n, last)
		}
	}
	return facts
}

// f2bLocalState — fail2ban машины хаба для строки localhost.
func f2bLocalState(snap *model.Snapshot) *f2bSummary {
	if snap == nil || snap.Fail2ban == nil {
		return nil
	}
	return &f2bSummary{Installed: snap.Fail2ban.Installed, Running: snap.Fail2ban.Running, Banned: snap.Fail2ban.BannedNow}
}
