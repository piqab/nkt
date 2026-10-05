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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
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
	// Timezone — часовой пояс времени в сообщениях (IANA); пусто — как у
	// хаба.
	Timezone string `json:"timezone,omitempty"`
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
	core    *botCore
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

// --- запуск и опрос ---------------------------------------------------------------

// StartTelegram — опрос Telegram, пока бот включён; оповещения — тоже ему.
func (s *Server) StartTelegram(ctx context.Context) {
	b := &tgBot{s: s, client: &http.Client{Timeout: 70 * time.Second}, reload: make(chan struct{}, 1), core: s.botCore()}
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
			b.s.log.Warn("Telegram bot: polling error", "err", err)
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

// turn — кто пишет в этом чате и что ему можно.
func (b *tgBot) turn(ctx context.Context, token string, st TelegramSettings, chat TelegramChat, from tgUser) botTurn {
	return botTurn{
		Platform: "telegram", User: strconv.FormatInt(from.ID, 10), UserName: from.name(), Lang: st.lang(), Loc: botLocation(st.Timezone),
		CanAct: chat.Role == store.TokenRoleAdmin && (len(st.Users) == 0 || slices.Contains(st.Users, from.ID)),
		Later:  func(r botReply) { _ = b.sendReply(context.WithoutCancel(ctx), token, chat.ID, r) },
	}
}

func (b *tgBot) sendReply(ctx context.Context, token string, chat int64, r botReply) error {
	var rows [][]tgButton
	if len(r.Buttons) > 0 {
		row := make([]tgButton, len(r.Buttons))
		for i, btn := range r.Buttons {
			row[i] = tgButton{Text: btn.Text, Data: btn.Data}
		}
		rows = append(rows, row)
	}
	in := map[string]any{"chat_id": chat, "text": r.telegramHTML(), "parse_mode": "HTML", "disable_web_page_preview": true}
	if len(rows) > 0 {
		in["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	return b.call(ctx, token, "sendMessage", in, nil)
}

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
		for _, r := range b.core.press(ctx, b.turn(ctx, token, st, chat, cb.From), cb.Data) {
			_ = b.sendReply(ctx, token, chat.ID, r)
		}
		return
	}
	m := u.Message
	if m == nil || !strings.HasPrefix(m.Text, "/") {
		return
	}
	fields := strings.Fields(m.Text)
	cmd := strings.TrimPrefix(strings.ToLower(strings.SplitN(fields[0], "@", 2)[0]), "/")
	chat, ok := st.chat(m.Chat.ID)
	if !ok {
		// Чужому чату — только его номер (чтобы добавить его на хабе).
		if cmd == "start" || cmd == "id" {
			_ = b.send(ctx, token, m.Chat.ID, msgs.T(lang, "tg.unknownChat", m.Chat.ID))
		}
		return
	}
	var from tgUser
	if m.From != nil {
		from = *m.From
	}
	if cmd == "id" {
		_ = b.send(ctx, token, chat.ID, msgs.T(lang, "tg.chatID", chat.ID, from.ID))
		return
	}
	for _, r := range b.core.command(ctx, b.turn(ctx, token, st, chat, from), cmd, fields[1:]) {
		_ = b.sendReply(ctx, token, chat.ID, r)
	}
}

// notify — оповещение в чаты с Notify (события — из настроек бота).
func (b *tgBot) notify(ctx context.Context, ev OutEvent) {
	st := b.s.tgSettings(ctx)
	token := b.token(ctx, st)
	if !st.Enabled || token == "" {
		return
	}
	r, ok := b.core.notifyReply(st.lang(), botLocation(st.Timezone), st.Kinds, ev)
	if !ok {
		return
	}
	for _, c := range st.Chats {
		if !c.Notify {
			continue
		}
		if err := b.sendReply(ctx, token, c.ID, r); err != nil {
			b.s.log.Warn("Telegram bot: alert not sent", "chat", c.ID, "err", err)
		}
	}
}

// --- настройки (API) ---------------------------------------------------------------

type tgStatusJSON struct {
	TelegramSettings
	HasToken bool   `json:"has_token"`
	HubZone  string `json:"hub_timezone"`
	Running  bool   `json:"running"`
	LastErr  string `json:"last_error,omitempty"`
	LastAt   string `json:"last_at,omitempty"`
}

// handleTelegram — GET /hub/telegram.
func (s *Server) handleTelegram(w http.ResponseWriter, r *http.Request) {
	st := s.tgSettings(r.Context())
	out := tgStatusJSON{TelegramSettings: st, HasToken: len(st.TokenEnc) > 0, HubZone: hubZone()}
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
		Enabled  bool           `json:"enabled"`
		Token    string         `json:"token"`
		Chats    []TelegramChat `json:"chats"`
		Users    []int64        `json:"users"`
		Kinds    []string       `json:"kinds"`
		Lang     string         `json:"lang"`
		Timezone string         `json:"timezone"`
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
	if !validZone(req.Timezone) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.badTimezone", req.Timezone))
		return
	}
	st.Timezone = req.Timezone
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
