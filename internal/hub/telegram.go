package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Бот Telegram: оповещения с кнопками и команды из чата. Хаб сам ходит к
// Telegram — шлёт сообщения и забирает команды и нажатия кнопок долгим
// опросом (getUpdates), — поэтому вход в хаб не нужен ни для чего: ни
// edge, ни открытого порта. Отвечает только чатам из списка; действия
// (выкладка, сухой прогон, бан) — в чатах с ролью «администратор», от
// разрешённых людей и с подтверждением кнопкой.

const (
	tgSettingsKey = "hub.telegram"
	tgOffsetKey   = "hub.telegram.offset"
)

// tgAPIBase — адрес Bot API (тесты подставляют свой).
var tgAPIBase = "https://api.telegram.org"

// TelegramChat — чат, которому бот отвечает.
type TelegramChat struct {
	ID    int64  `json:"id"`
	Title string `json:"title,omitempty"`
	// Role — read: состояние и кнопки просмотра; admin — ещё и действия.
	Role string `json:"role"`
	// Notify — слать в чат оповещения.
	Notify bool `json:"notify"`
}

// TelegramSettings — настройки бота.
type TelegramSettings struct {
	Enabled  bool           `json:"enabled"`
	TokenEnc []byte         `json:"token_enc,omitempty"`
	BotName  string         `json:"bot_name,omitempty"`
	Chats    []TelegramChat `json:"chats"`
	// Users — Telegram user id, кому разрешены действия (пусто — любому
	// участнику чата с ролью admin).
	Users []int64 `json:"users"`
	// Kinds — события для оповещений (пусто — все).
	Kinds []string `json:"kinds"`
	Lang  string   `json:"lang"`
}

func (st TelegramSettings) chat(id int64) (TelegramChat, bool) {
	for _, c := range st.Chats {
		if c.ID == id {
			return c, true
		}
	}
	return TelegramChat{}, false
}

func (st TelegramSettings) lang() msgs.Lang {
	if st.Lang == "en" {
		return msgs.EN
	}
	return msgs.RU
}

func (s *Server) tgSettings(ctx context.Context) TelegramSettings {
	st := TelegramSettings{Chats: []TelegramChat{}, Users: []int64{}, Kinds: []string{}}
	if raw, ok, err := s.db.KVGet(ctx, tgSettingsKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

// --- клиент Bot API ---------------------------------------------------------------

type tgBot struct {
	s      *Server
	client *http.Client

	mu      sync.Mutex
	running bool
	lastErr string
	lastAt  time.Time
	reload  chan struct{}
	// pending — подтверждения действий: ключ кнопки → что сделать.
	pending map[string]tgPending
}

type tgPending struct {
	action string
	arg    string
	user   int64
	at     time.Time
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
	Callback *struct {
		ID      string     `json:"id"`
		From    tgUser     `json:"from"`
		Message *tgMessage `json:"message"`
		Data    string     `json:"data"`
	} `json:"callback_query"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		Type  string `json:"type"`
	} `json:"chat"`
	Text string `json:"text"`
}

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	First    string `json:"first_name"`
}

func (u tgUser) name() string {
	if u.Username != "" {
		return u.Username
	}
	return strconv.FormatInt(u.ID, 10)
}

// tgButton — кнопка под сообщением.
type tgButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

func (b *tgBot) call(ctx context.Context, token, method string, in, out any) error {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tgAPIBase+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		// В ошибке net/http — URL с токеном: не показывать его.
		return fmt.Errorf("telegram %s: %s", method, strings.ReplaceAll(err.Error(), token, "<token>"))
	}
	defer resp.Body.Close()
	var res struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&res); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	if !res.OK {
		return fmt.Errorf("telegram %s: %s", method, res.Description)
	}
	if out != nil {
		return json.Unmarshal(res.Result, out)
	}
	return nil
}

func (b *tgBot) token(ctx context.Context, st TelegramSettings) string {
	if len(st.TokenEnc) == 0 {
		return ""
	}
	raw, err := secretbox.Decrypt(b.s.hub.key, st.TokenEnc)
	if err != nil {
		return ""
	}
	return string(raw)
}

// send — сообщение в чат (кнопки — рядами).
func (b *tgBot) send(ctx context.Context, token string, chat int64, text string, rows ...[]tgButton) error {
	in := map[string]any{"chat_id": chat, "text": trimRunes(text, 4000), "disable_web_page_preview": true}
	if len(rows) > 0 {
		in["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	return b.call(ctx, token, "sendMessage", in, nil)
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// --- запуск и опрос ---------------------------------------------------------------

// StartTelegram — опрос Telegram, пока бот включён; оповещения — тоже ему.
func (s *Server) StartTelegram(ctx context.Context) {
	b := &tgBot{s: s, client: &http.Client{Timeout: 70 * time.Second}, reload: make(chan struct{}, 1), pending: map[string]tgPending{}}
	s.tg = b
	s.hub.AddOutSink(func(ev OutEvent) { b.notify(context.Background(), ev) })
	go b.loop(ctx)
}

func (s *Server) kickTelegram() {
	if s.tg == nil {
		return
	}
	select {
	case s.tg.reload <- struct{}{}:
	default:
	}
}

func (b *tgBot) setState(running bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running = running
	b.lastAt = time.Now()
	if err != nil {
		b.lastErr = err.Error()
	} else if running {
		b.lastErr = ""
	}
}

func (b *tgBot) loop(ctx context.Context) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		st := b.s.tgSettings(ctx)
		token := b.token(ctx, st)
		if !st.Enabled || token == "" {
			b.setState(false, nil)
			select {
			case <-ctx.Done():
				return
			case <-b.reload:
			case <-time.After(time.Minute):
			}
			continue
		}
		// Смена настроек прерывает долгий опрос.
		pctx, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-b.reload:
				cancel()
			case <-pctx.Done():
			}
		}()
		err := b.poll(pctx, token)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil && pctx.Err() == nil {
			b.setState(false, err)
			b.s.log.Warn("бот Telegram: ошибка опроса", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 5*time.Minute)
			continue
		}
		backoff = 5 * time.Second
	}
}

// poll — getUpdates до ошибки или отмены.
func (b *tgBot) poll(ctx context.Context, token string) error {
	var offset int64
	if raw, ok, err := b.s.db.KVGet(ctx, tgOffsetKey); err == nil && ok {
		offset, _ = strconv.ParseInt(raw, 10, 64)
	}
	for ctx.Err() == nil {
		var updates []tgUpdate
		err := b.call(ctx, token, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		b.setState(true, nil)
		for _, u := range updates {
			b.handle(ctx, token, u)
			offset = max(offset, u.UpdateID+1)
		}
		if len(updates) > 0 {
			_ = b.s.db.KVSet(context.WithoutCancel(ctx), tgOffsetKey, strconv.FormatInt(offset, 10))
		}
	}
	return nil
}

// --- команды и кнопки ---------------------------------------------------------------

var tgIPRe = regexp.MustCompile(`^[0-9A-Fa-f:.]{2,45}(/\d{1,3})?$`)

func (b *tgBot) handle(ctx context.Context, token string, u tgUpdate) {
	st := b.s.tgSettings(ctx)
	lang := st.lang()
	if u.Callback != nil {
		cb := u.Callback
		_ = b.call(ctx, token, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID}, nil)
		if cb.Message == nil {
			return
		}
		chat, ok := st.chat(cb.Message.Chat.ID)
		if !ok {
			return
		}
		b.button(ctx, token, st, chat, cb.From, cb.Data, lang)
		return
	}
	m := u.Message
	if m == nil || !strings.HasPrefix(m.Text, "/") {
		return
	}
	fields := strings.Fields(m.Text)
	cmd := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	args := fields[1:]
	chat, ok := st.chat(m.Chat.ID)
	if !ok {
		// Чужому чату — только его номер (чтобы добавить его на хабе).
		if cmd == "/start" || cmd == "/id" {
			_ = b.send(ctx, token, m.Chat.ID, msgs.T(lang, "tg.unknownChat", m.Chat.ID))
		}
		return
	}
	var from tgUser
	if m.From != nil {
		from = *m.From
	}
	reply := func(text string, rows ...[]tgButton) { _ = b.send(ctx, token, chat.ID, text, rows...) }
	switch cmd {
	case "/start", "/help":
		key := "tg.helpRead"
		if chat.Role == store.TokenRoleAdmin {
			key = "tg.helpAdmin"
		}
		reply(msgs.T(lang, key))
	case "/id":
		reply(msgs.T(lang, "tg.chatID", chat.ID, from.ID))
	case "/status":
		reply(b.status(ctx, lang))
	case "/hosts":
		reply(b.hosts(ctx, lang))
	case "/alerts":
		reply(b.alerts(ctx, lang))
	case "/pipelines":
		reply(b.pipelines(ctx, lang))
	case "/deploy", "/dryrun":
		if !b.mayAct(st, chat, from) {
			reply(msgs.T(lang, "tg.denied"))
			return
		}
		pl, ok := b.findPipeline(ctx, strings.Join(args, " "))
		if !ok {
			reply(msgs.T(lang, "tg.noPipeline", strings.Join(args, " ")))
			return
		}
		if cmd == "/dryrun" {
			b.dryRun(ctx, token, chat.ID, from, pl, lang)
			return
		}
		key := b.ask("deploy", strconv.FormatInt(pl.ID, 10), from.ID)
		reply(msgs.T(lang, "tg.confirmDeploy", pl.Name), []tgButton{{msgs.T(lang, "tg.btnDeploy"), "ok:" + key}, {msgs.T(lang, "tg.btnCancel"), "no:" + key}})
	case "/ban", "/unban":
		if !b.mayAct(st, chat, from) {
			reply(msgs.T(lang, "tg.denied"))
			return
		}
		if len(args) != 1 || !tgIPRe.MatchString(args[0]) {
			reply(msgs.T(lang, "tg.banUsage"))
			return
		}
		ip, err := fail2ban.ParseIP(args[0])
		if err != nil {
			reply(msgs.Localize(lang, err))
			return
		}
		action, ask, btn := "ban", "tg.confirmBan", "tg.btnBan"
		if cmd == "/unban" {
			action, ask, btn = "unban", "tg.confirmUnban", "tg.btnUnban"
		}
		key := b.ask(action, ip.String(), from.ID)
		reply(msgs.T(lang, ask, ip.String()), []tgButton{{msgs.T(lang, btn), "ok:" + key}, {msgs.T(lang, "tg.btnCancel"), "no:" + key}})
	default:
		reply(msgs.T(lang, "tg.unknownCommand"))
	}
}

// mayAct — действие разрешено: чат с ролью admin и (если список задан)
// человек из списка.
func (b *tgBot) mayAct(st TelegramSettings, chat TelegramChat, from tgUser) bool {
	return chat.Role == store.TokenRoleAdmin && (len(st.Users) == 0 || slices.Contains(st.Users, from.ID))
}

// ask — подтверждение действия: ключ кнопки (живёт 10 минут, только для
// того же человека).
func (b *tgBot) ask(action, arg string, user int64) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, p := range b.pending {
		if time.Since(p.at) > 10*time.Minute {
			delete(b.pending, k)
		}
	}
	key := randomHex(6)
	b.pending[key] = tgPending{action: action, arg: arg, user: user, at: time.Now()}
	return key
}

func (b *tgBot) take(key string, user int64) (tgPending, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pending[key]
	if !ok || p.user != user || time.Since(p.at) > 10*time.Minute {
		return tgPending{}, false
	}
	delete(b.pending, key)
	return p, true
}

func (b *tgBot) button(ctx context.Context, token string, st TelegramSettings, chat TelegramChat, from tgUser, data string, lang msgs.Lang) {
	reply := func(text string, rows ...[]tgButton) { _ = b.send(ctx, token, chat.ID, text, rows...) }
	kind, arg, _ := strings.Cut(data, ":")
	switch kind {
	case "ov", "fd":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return
		}
		reply(b.hostDetail(ctx, id, kind == "fd", lang))
	case "jl":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return
		}
		reply(b.jobTail(ctx, id, 15, lang))
	case "rd":
		if !b.mayAct(st, chat, from) {
			reply(msgs.T(lang, "tg.denied"))
			return
		}
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return
		}
		pl, err := b.s.db.PipelineByID(ctx, id)
		if err != nil {
			return
		}
		key := b.ask("deploy", arg, from.ID)
		reply(msgs.T(lang, "tg.confirmDeploy", pl.Name), []tgButton{{msgs.T(lang, "tg.btnDeploy"), "ok:" + key}, {msgs.T(lang, "tg.btnCancel"), "no:" + key}})
	case "no":
		if _, ok := b.take(arg, from.ID); ok {
			reply(msgs.T(lang, "tg.cancelled"))
		}
	case "ok":
		p, ok := b.take(arg, from.ID)
		if !ok {
			reply(msgs.T(lang, "tg.expired"))
			return
		}
		if !b.mayAct(st, chat, from) {
			reply(msgs.T(lang, "tg.denied"))
			return
		}
		b.act(ctx, token, chat.ID, from, p, lang)
	}
}

// act — подтверждённое действие.
func (b *tgBot) act(ctx context.Context, token string, chat int64, from tgUser, p tgPending, lang msgs.Lang) {
	s := b.s
	author := "telegram:" + from.name()
	reply := func(text string, rows ...[]tgButton) { _ = b.send(ctx, token, chat, text, rows...) }
	switch p.action {
	case "deploy":
		id, _ := strconv.ParseInt(p.arg, 10, 64)
		pl, err := s.db.PipelineByID(ctx, id)
		if err != nil {
			reply(msgs.Localize(lang, err))
			return
		}
		d, err := s.startDeployment(ctx, pl, store.Deployment{Trigger: "telegram", Author: author}, true)
		s.db.Audit(ctx, author, "pipeline.deploy", pl.Name, auditOutcome(err), map[string]any{"via": "telegram"})
		if err != nil {
			reply(msgs.Localize(lang, err))
			return
		}
		reply(msgs.T(lang, "tg.deployStarted", pl.Name, d.JobID))
		go b.watchJob(context.WithoutCancel(ctx), token, chat, d.JobID, lang)
	case "ban", "unban":
		params := F2BFleetParams{Action: p.action, IPs: []string{p.arg}}
		targets, err := s.f2bTargets(ctx, nil)
		if err == nil && len(targets) == 0 {
			err = msgs.Errorf("hub.f2bNoHosts")
		}
		if err != nil {
			reply(msgs.Localize(lang, err))
			return
		}
		for _, t := range targets {
			params.HostIDs = append(params.HostIDs, t.ID)
		}
		titleKey := "hub.f2bJobBan"
		if p.action == "unban" {
			titleKey = "hub.f2bJobUnban"
		}
		id, err := s.jobs.Start(ctx, jobsSpecF2B(titleKey, author, params))
		s.db.Audit(ctx, author, "fail2ban.fleet", p.arg, auditOutcome(err), map[string]any{"action": p.action, "via": "telegram"})
		if err != nil {
			reply(msgs.Localize(lang, err))
			return
		}
		reply(msgs.T(lang, "tg.jobStarted", id))
		go b.watchJob(context.WithoutCancel(ctx), token, chat, id, lang)
	}
}

func (b *tgBot) dryRun(ctx context.Context, token string, chat int64, from tgUser, pl store.Pipeline, lang msgs.Lang) {
	author := "telegram:" + from.name()
	id, err := b.s.jobs.Start(ctx, jobsSpecDry(pl, author))
	b.s.db.Audit(ctx, author, "pipeline.dryrun", pl.Name, auditOutcome(err), map[string]any{"via": "telegram"})
	if err != nil {
		_ = b.send(ctx, token, chat, msgs.Localize(lang, err))
		return
	}
	_ = b.send(ctx, token, chat, msgs.T(lang, "tg.dryStarted", pl.Name, id))
	go b.watchJob(context.WithoutCancel(ctx), token, chat, id, lang)
}

// watchJob — итог задания в чат (до получаса).
func (b *tgBot) watchJob(ctx context.Context, token string, chat, id int64, lang msgs.Lang) {
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		j, err := b.s.db.JobByID(ctx, id)
		if err != nil {
			return
		}
		if j.Status != store.JobQueued && j.Status != store.JobRunning {
			_ = b.send(ctx, token, chat, b.jobTail(ctx, id, 12, lang))
			return
		}
		time.Sleep(3 * time.Second)
	}
}

// --- тексты ---------------------------------------------------------------------

func (b *tgBot) status(ctx context.Context, lang msgs.Lang) string {
	rows, err := b.s.hostRows(ctx)
	if err != nil {
		return msgs.Localize(lang, err)
	}
	up, down, findings := 0, 0, map[string]int{}
	for _, h := range rows {
		if h.Reachable != nil && !*h.Reachable {
			down++
		} else {
			up++
		}
		for k, v := range h.Findings {
			findings[k] += v
		}
	}
	return msgs.T(lang, "tg.status", len(rows), up, down, findings["critical"], findings["high"], findings["medium"])
}

func (b *tgBot) hosts(ctx context.Context, lang msgs.Lang) string {
	rows, err := b.s.hostRows(ctx)
	if err != nil {
		return msgs.Localize(lang, err)
	}
	var lines []string
	for i, h := range rows {
		if i >= 40 {
			lines = append(lines, "…")
			break
		}
		mark := "🟢"
		if h.Reachable != nil && !*h.Reachable {
			mark = "🔴"
		}
		lines = append(lines, fmt.Sprintf("%s %s (%d) — %s", mark, h.Name, h.ID, findingsText(lang, h.Findings)))
	}
	return strings.Join(lines, "\n")
}

func findingsText(lang msgs.Lang, f map[string]int) string {
	if f["critical"]+f["high"]+f["medium"] == 0 {
		return msgs.T(lang, "tg.noFindings")
	}
	return msgs.T(lang, "tg.findings", f["critical"], f["high"], f["medium"])
}

func (b *tgBot) hostDetail(ctx context.Context, id int64, withFindings bool, lang msgs.Lang) string {
	rows, err := b.s.hostRows(ctx)
	if err != nil {
		return msgs.Localize(lang, err)
	}
	i := slices.IndexFunc(rows, func(h hostWithOverview) bool { return h.ID == id })
	if i < 0 {
		return msgs.T(lang, "tg.noHost", id)
	}
	h := rows[i]
	state := msgs.T(lang, "tg.hostUp")
	if h.Reachable != nil && !*h.Reachable {
		state = msgs.T(lang, "tg.hostDown")
	}
	text := msgs.T(lang, "tg.hostDetail", h.Name, h.Addr, state, h.RunningVersion, findingsText(lang, h.Findings))
	if !withFindings {
		return text
	}
	var list []struct {
		Severity string `json:"severity"`
		Title    string `json:"title"`
	}
	path := "/api/findings"
	var err2 error
	if id == localHostID {
		_, err2 = b.s.localAPI(ctx, "", http.MethodGet, path, nil, &list)
	} else {
		_, err2 = b.s.hub.HostAPI(ctx, id, http.MethodGet, path, nil, &list)
	}
	if err2 != nil {
		return text + "\n" + msgs.Localize(lang, err2)
	}
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(list, func(i, j int) bool { return rank[list[i].Severity] < rank[list[j].Severity] })
	for i, f := range list {
		if i >= 10 {
			text += "\n…"
			break
		}
		text += fmt.Sprintf("\n• [%s] %s", f.Severity, f.Title)
	}
	return text
}

func (b *tgBot) alerts(ctx context.Context, lang msgs.Lang) string {
	res, err := b.s.hub.QueryEvents(msgs.WithLang(ctx, lang), EventQuery{Limit: 5})
	if err != nil {
		return msgs.Localize(lang, err)
	}
	if len(res.Events) == 0 {
		return msgs.T(lang, "tg.noAlerts")
	}
	var lines []string
	for _, e := range res.Events {
		lines = append(lines, fmt.Sprintf("#%d %s %s — %s: %s", e.ID, e.TS, e.HostName, msgs.T(lang, "tg.kind."+e.Kind), e.Detail))
	}
	return strings.Join(lines, "\n")
}

func (b *tgBot) pipelines(ctx context.Context, lang msgs.Lang) string {
	list, err := b.s.db.ListPipelines(ctx)
	if err != nil {
		return msgs.Localize(lang, err)
	}
	if len(list) == 0 {
		return msgs.T(lang, "tg.noPipelines")
	}
	var lines []string
	for _, p := range list {
		last := "—"
		if ds, err := b.s.db.Deployments(ctx, p.ID, 1); err == nil && len(ds) > 0 {
			last = ds[0].Status
		}
		lines = append(lines, fmt.Sprintf("%d · %s — %s", p.ID, p.Name, last))
	}
	return strings.Join(lines, "\n")
}

func (b *tgBot) findPipeline(ctx context.Context, arg string) (store.Pipeline, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return store.Pipeline{}, false
	}
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		p, err := b.s.db.PipelineByID(ctx, id)
		return p, err == nil
	}
	list, _ := b.s.db.ListPipelines(ctx)
	for _, p := range list {
		if strings.EqualFold(p.Name, arg) {
			return p, true
		}
	}
	return store.Pipeline{}, false
}

func (b *tgBot) jobTail(ctx context.Context, id int64, n int, lang msgs.Lang) string {
	j, err := b.s.db.JobByID(ctx, id)
	if err != nil {
		return msgs.T(lang, "tg.noJob", id)
	}
	lines, _ := b.s.db.JobLog(ctx, id, 0, 2000)
	var tail []string
	for _, l := range lines {
		tail = append(tail, msgs.Render(lang, l.Key, l.Args, l.Text))
	}
	if len(tail) > n {
		tail = tail[len(tail)-n:]
	}
	title := msgs.Render(lang, j.TitleKey, j.TitleArgs, j.Title)
	head := msgs.T(lang, "tg.job", id, title, msgs.T(lang, "tg.jobStatus."+j.Status))
	if j.Status == store.JobFailed {
		head += "\n" + msgs.Render(lang, j.ErrorKey, j.ErrorArgs, j.Error)
	}
	return head + "\n\n" + strings.Join(tail, "\n")
}

// notify — оповещение в чаты с Notify (события — из настроек бота).
func (b *tgBot) notify(ctx context.Context, ev OutEvent) {
	if ev.Kind == OutTest {
		return
	}
	st := b.s.tgSettings(ctx)
	token := b.token(ctx, st)
	if !st.Enabled || token == "" || (len(st.Kinds) > 0 && !slices.Contains(st.Kinds, ev.Kind)) {
		return
	}
	lang := st.lang()
	icon := map[string]string{store.EventUnreachable: "🔴", store.EventRecovered: "🟢", store.EventProblems: "⚠️", store.EventResolved: "✅",
		store.EventJobFailed: "❌", store.EventRebooted: "🔄", store.EventBans: "🚫", OutDeploySucceeded: "🚀", OutDeployFailed: "💥"}[ev.Kind]
	text := strings.TrimSpace(icon + " " + msgs.T(lang, "tg.kind."+ev.Kind))
	if ev.HostName != "" {
		text += " · " + ev.HostName
	}
	if body := msgs.Render(lang, ev.Key, ev.Args, ev.Text); body != "" {
		text += "\n" + body
	}
	var buttons []tgButton
	switch {
	case ev.HostName != "":
		buttons = append(buttons, tgButton{msgs.T(lang, "tg.btnOverview"), "ov:" + strconv.FormatInt(ev.HostID, 10)})
		if ev.Kind == store.EventProblems {
			buttons = append(buttons, tgButton{msgs.T(lang, "tg.btnFindings"), "fd:" + strconv.FormatInt(ev.HostID, 10)})
		}
	case ev.PipelineID != 0:
		buttons = append(buttons, tgButton{msgs.T(lang, "tg.btnLog"), "jl:" + strconv.FormatInt(ev.JobID, 10)})
		if ev.Kind == OutDeployFailed {
			buttons = append(buttons, tgButton{msgs.T(lang, "tg.btnRetry"), "rd:" + strconv.FormatInt(ev.PipelineID, 10)})
		}
	}
	for _, c := range st.Chats {
		if !c.Notify {
			continue
		}
		var err error
		if len(buttons) > 0 {
			err = b.send(ctx, token, c.ID, text, buttons)
		} else {
			err = b.send(ctx, token, c.ID, text)
		}
		if err != nil {
			b.s.log.Warn("бот Telegram: оповещение не отправлено", "chat", c.ID, "err", err)
		}
	}
}

// --- настройки (API) ---------------------------------------------------------------

type tgStatusJSON struct {
	TelegramSettings
	HasToken bool   `json:"has_token"`
	Running  bool   `json:"running"`
	LastErr  string `json:"last_error,omitempty"`
	LastAt   string `json:"last_at,omitempty"`
}

// handleTelegram — GET /hub/telegram.
func (s *Server) handleTelegram(w http.ResponseWriter, r *http.Request) {
	st := s.tgSettings(r.Context())
	out := tgStatusJSON{TelegramSettings: st, HasToken: len(st.TokenEnc) > 0}
	out.TokenEnc = nil
	if s.tg != nil {
		s.tg.mu.Lock()
		out.Running, out.LastErr = s.tg.running, s.tg.lastErr
		if !s.tg.lastAt.IsZero() {
			out.LastAt = store.FormatTime(s.tg.lastAt)
		}
		s.tg.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, out)
}

var tgTokenRe = regexp.MustCompile(`^\d{5,20}:[A-Za-z0-9_-]{30,100}$`)

// handleTelegramSave — PUT /hub/telegram {enabled, token, chats, users,
// kinds, lang}: пустой token — прежний. Новый токен проверяется getMe.
func (s *Server) handleTelegramSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool           `json:"enabled"`
		Token   string         `json:"token"`
		Chats   []TelegramChat `json:"chats"`
		Users   []int64        `json:"users"`
		Kinds   []string       `json:"kinds"`
		Lang    string         `json:"lang"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	st := s.tgSettings(ctx)
	if t := strings.TrimSpace(req.Token); t != "" {
		if !tgTokenRe.MatchString(t) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.badToken"))
			return
		}
		var me tgUser
		bot := &tgBot{s: s, client: &http.Client{Timeout: 15 * time.Second}}
		if err := bot.call(ctx, t, "getMe", map[string]any{}, &me); err != nil {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.tokenRejected", err.Error()))
			return
		}
		enc, err := secretbox.Encrypt(s.hub.key, []byte(t))
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		st.TokenEnc, st.BotName = enc, me.Username
		_ = s.db.KVSet(ctx, tgOffsetKey, "0")
	}
	if len(req.Chats) > 50 || len(req.Users) > 200 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.tooMany"))
		return
	}
	chats := []TelegramChat{}
	for _, c := range req.Chats {
		if c.ID == 0 || slices.ContainsFunc(chats, func(x TelegramChat) bool { return x.ID == c.ID }) {
			continue
		}
		if c.Role != store.TokenRoleAdmin {
			c.Role = store.TokenRoleRead
		}
		c.Title = trimRunes(strings.TrimSpace(c.Title), 64)
		chats = append(chats, c)
	}
	kinds := []string{}
	for _, k := range req.Kinds {
		if !slices.Contains(OutKinds, k) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.outBadKind", k))
			return
		}
		kinds = append(kinds, k)
	}
	st.Enabled, st.Chats, st.Users, st.Kinds = req.Enabled, chats, slices.Compact(slices.Sorted(slices.Values(req.Users))), kinds
	if st.Users == nil {
		st.Users = []int64{}
	}
	st.Lang = "ru"
	if req.Lang == "en" {
		st.Lang = "en"
	}
	if st.Enabled && len(st.TokenEnc) == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.noToken"))
		return
	}
	raw, _ := json.Marshal(st)
	err := s.db.KVSet(ctx, tgSettingsKey, string(raw))
	s.db.Audit(ctx, auth.Username(ctx), "telegram.save", st.BotName, auditOutcome(err), map[string]any{"enabled": st.Enabled, "chats": st.Chats, "users": st.Users, "kinds": st.Kinds})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.kickTelegram()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "bot_name": st.BotName})
}

// handleTelegramTest — POST /hub/telegram/test {chat_id}: пробное сообщение.
func (s *Server) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64 `json:"chat_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	st := s.tgSettings(ctx)
	if _, ok := st.chat(req.ChatID); !ok {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.noChat", req.ChatID))
		return
	}
	bot := s.tg
	if bot == nil {
		bot = &tgBot{s: s, client: &http.Client{Timeout: 15 * time.Second}}
	}
	token := bot.token(ctx, st)
	if token == "" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.noToken"))
		return
	}
	err := bot.send(ctx, token, req.ChatID, msgs.T(st.lang(), "tg.test", auth.Username(ctx)))
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// jobsSpecF2B — задание бана на хостах (как из «fail2ban» хаба).
func jobsSpecF2B(titleKey, author string, p F2BFleetParams) jobs.Spec {
	return jobs.Spec{Kind: KindF2BFleet, TitleKey: titleKey, TitleArgs: []any{strings.Join(p.IPs, ", "), len(p.HostIDs)},
		Author: author, Steps: len(p.HostIDs), Params: p}
}

// jobsSpecDry — сухой прогон сохранённого конвейера с галочками,
// выбранными администраторами.
func jobsSpecDry(pl store.Pipeline, author string) jobs.Spec {
	var skip []string
	_ = json.Unmarshal([]byte(pl.DrySkip), &skip)
	return jobs.Spec{Kind: KindDeploy, TitleKey: "deploy.dryTitle", TitleArgs: []any{pl.Name},
		Queue: fmt.Sprintf("deploy:%d", pl.ID), Author: author, Steps: 3,
		Params: DeployParams{DryRun: true, PipelineID: pl.ID, Content: pl.Content, Skip: skip}}
}
