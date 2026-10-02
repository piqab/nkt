package hub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Бот Slack: оповещения с кнопками (chat.postMessage) и слэш-команда
// /nkt, нажатия кнопок. В отличие от Telegram, команды и кнопки Slack сам
// присылает запросами на адрес приложения — значит, нужен вход: хаб,
// доступный из интернета, или nkt-edge с ролью «колбэки». Каждый запрос
// хаб проверяет подписью Slack (X-Slack-Signature, v0, секрет подписи
// приложения) и временем (не старше 5 минут).

const slackSettingsKey = "hub.slack"

// slackAPIBase — адрес Web API (тесты подставляют свой).
var slackAPIBase = "https://slack.com/api"

// SlackChannel — канал, которому бот отвечает.
type SlackChannel struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Role   string `json:"role"`
	Notify bool   `json:"notify"`
}

// SlackSettings — настройки приложения Slack.
type SlackSettings struct {
	Enabled    bool           `json:"enabled"`
	TokenEnc   []byte         `json:"token_enc,omitempty"`
	SigningEnc []byte         `json:"signing_enc,omitempty"`
	Team       string         `json:"team,omitempty"`
	BotUser    string         `json:"bot_user,omitempty"`
	Channels   []SlackChannel `json:"channels"`
	// Users — Slack user id, кому разрешены действия (пусто — любому в
	// канале с ролью admin).
	Users []string `json:"users"`
	Kinds []string `json:"kinds"`
	Lang  string   `json:"lang"`
	// Timezone — часовой пояс времени в сообщениях (IANA); пусто — как у
	// хаба.
	Timezone string `json:"timezone,omitempty"`
}

func (st SlackSettings) channel(id string) (SlackChannel, bool) {
	for _, c := range st.Channels {
		if c.ID == id {
			return c, true
		}
	}
	return SlackChannel{}, false
}

func (st SlackSettings) lang() msgs.Lang {
	if st.Lang == "en" {
		return msgs.EN
	}
	return msgs.RU
}

func (s *Server) slackSettings(ctx context.Context) SlackSettings {
	st := SlackSettings{Channels: []SlackChannel{}, Users: []string{}, Kinds: []string{}}
	if raw, ok, err := s.db.KVGet(ctx, slackSettingsKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

func (s *Server) slackSecret(enc []byte) string {
	if len(enc) == 0 {
		return ""
	}
	raw, err := secretbox.Decrypt(s.hub.key, enc)
	if err != nil {
		return ""
	}
	return string(raw)
}

var slackHTTP = &http.Client{Timeout: 15 * time.Second}

// StartSlack — оповещения хаба — и в каналы Slack.
func (s *Server) StartSlack() {
	s.hub.AddOutSink(func(ev OutEvent) { s.slackNotify(context.Background(), ev) })
}

// slackCall — метод Web API.
func slackCall(ctx context.Context, token, method string, in, out any) error {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackAPIBase+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := slackHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("slack %s: HTTP %d", method, resp.StatusCode)
	}
	if !res.OK {
		return fmt.Errorf("slack %s: %s", method, res.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// slackEscape — текст для mrkdwn: &, <, > — сущностями.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// slackBlocks — сообщение с кнопками (Block Kit).
func slackBlocks(r botReply) []map[string]any {
	blocks := []map[string]any{{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": r.slackMrkdwn()}}}
	if len(r.Buttons) > 0 {
		var els []map[string]any
		for i, b := range r.Buttons {
			els = append(els, map[string]any{"type": "button", "action_id": "nkt_" + strconv.Itoa(i),
				"text": map[string]any{"type": "plain_text", "text": trimRunes(b.Text, 70)}, "value": b.Data})
		}
		blocks = append(blocks, map[string]any{"type": "actions", "elements": els})
	}
	return blocks
}

func slackPost(ctx context.Context, token, channel string, r botReply) error {
	return slackCall(ctx, token, "chat.postMessage", map[string]any{"channel": channel, "text": trimRunes(r.plain(), 3000), "blocks": slackBlocks(r)}, nil)
}

// slackVerify — подпись запроса Slack: v0=hex HMAC-SHA256 секретом от
// «v0:<время>:<тело>», время — не старше 5 минут.
func slackVerify(secret, ts, sig string, body []byte, now time.Time) bool {
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || secret == "" || !strings.HasPrefix(sig, "v0=") {
		return false
	}
	if d := now.Sub(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	return hmac.Equal([]byte(sig[3:]), []byte(hex.EncodeToString(mac.Sum(nil))))
}

func (s *Server) slackTurn(ctx context.Context, st SlackSettings, ch SlackChannel, userID, userName, token string) botTurn {
	if userName == "" {
		userName = userID
	}
	return botTurn{
		Platform: "slack", User: userID, UserName: userName, Lang: st.lang(), Loc: botLocation(st.Timezone),
		CanAct: ch.Role == store.TokenRoleAdmin && (len(st.Users) == 0 || slices.Contains(st.Users, userID)),
		Later:  func(r botReply) { _ = slackPost(context.WithoutCancel(ctx), token, ch.ID, r) },
	}
}

// handleSlackCallback — POST /api/hub/callbacks/slack/{kind} (commands —
// слэш-команда, interactive — кнопки). Без сессии: доступ — подписью.
func (s *Server) handleSlackCallback(w http.ResponseWriter, r *http.Request) {
	s.serveSlack(w, r, chi.URLParam(r, "kind"))
}

func (s *Server) serveSlack(w http.ResponseWriter, r *http.Request, kind string) {
	ctx := r.Context()
	st := s.slackSettings(ctx)
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || !st.Enabled {
		http.Error(w, "slack is off", http.StatusNotFound)
		return
	}
	if !slackVerify(s.slackSecret(st.SigningEnc), r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body, time.Now()) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	token := s.slackSecret(st.TokenEnc)
	core := s.botCore()
	lang := st.lang()
	switch kind {
	case "commands":
		ch, ok := st.channel(form.Get("channel_id"))
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"response_type": "ephemeral", "text": msgs.T(lang, "slack.unknownChannel", form.Get("channel_id"))})
			return
		}
		fields := strings.Fields(form.Get("text"))
		cmd := ""
		if len(fields) > 0 {
			cmd = strings.ToLower(fields[0])
			fields = fields[1:]
		}
		if cmd == "id" {
			writeJSON(w, http.StatusOK, map[string]any{"response_type": "ephemeral", "text": msgs.T(lang, "slack.ids", ch.ID, form.Get("user_id"))})
			return
		}
		replies := core.command(ctx, s.slackTurn(ctx, st, ch, form.Get("user_id"), form.Get("user_name"), token), cmd, fields)
		if len(replies) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"response_type": "in_channel", "text": trimRunes(replies[0].plain(), 3000), "blocks": slackBlocks(replies[0])})
		for _, rep := range replies[1:] {
			_ = slackPost(ctx, token, ch.ID, rep)
		}
	case "interactive":
		var p struct {
			Type string `json:"type"`
			User struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"user"`
			Channel struct {
				ID string `json:"id"`
			} `json:"channel"`
			Actions []struct {
				Value string `json:"value"`
			} `json:"actions"`
		}
		if json.Unmarshal([]byte(form.Get("payload")), &p) != nil || p.Type != "block_actions" || len(p.Actions) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}
		ch, ok := st.channel(p.Channel.ID)
		if !ok {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		for _, rep := range core.press(ctx, s.slackTurn(ctx, st, ch, p.User.ID, p.User.Username, token), p.Actions[0].Value) {
			_ = slackPost(context.WithoutCancel(ctx), token, ch.ID, rep)
		}
	default:
		http.NotFound(w, r)
	}
}

// slackNotify — оповещение в каналы с Notify.
func (s *Server) slackNotify(ctx context.Context, ev OutEvent) {
	st := s.slackSettings(ctx)
	token := s.slackSecret(st.TokenEnc)
	if !st.Enabled || token == "" {
		return
	}
	r, ok := s.botCore().notifyReply(st.lang(), botLocation(st.Timezone), st.Kinds, ev)
	if !ok {
		return
	}
	for _, c := range st.Channels {
		if c.Notify {
			if err := slackPost(ctx, token, c.ID, r); err != nil {
				s.log.Warn("бот Slack: оповещение не отправлено", "channel", c.ID, "err", err)
			}
		}
	}
}

// --- настройки (API) ---------------------------------------------------------------

var (
	slackTokenRe   = regexp.MustCompile(`^xoxb-[A-Za-z0-9-]{20,250}$`)
	slackSigningRe = regexp.MustCompile(`^[a-f0-9]{32}$`)
	slackChannelRe = regexp.MustCompile(`^[CGD][A-Z0-9]{6,20}$`)
	slackUserRe    = regexp.MustCompile(`^[UW][A-Z0-9]{6,20}$`)
)

type slackStatusJSON struct {
	SlackSettings
	HasToken   bool   `json:"has_token"`
	HasSigning bool   `json:"has_signing"`
	HubZone    string `json:"hub_timezone"`
}

// handleSlack — GET /hub/slack.
func (s *Server) handleSlack(w http.ResponseWriter, r *http.Request) {
	st := s.slackSettings(r.Context())
	out := slackStatusJSON{SlackSettings: st, HasToken: len(st.TokenEnc) > 0, HasSigning: len(st.SigningEnc) > 0, HubZone: hubZone()}
	out.TokenEnc, out.SigningEnc = nil, nil
	writeJSON(w, http.StatusOK, out)
}

// handleSlackSave — PUT /hub/slack {enabled, token, signing_secret,
// channels, users, kinds, lang}: пустые секреты — прежние; новый токен
// проверяется auth.test.
func (s *Server) handleSlackSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled  bool           `json:"enabled"`
		Token    string         `json:"token"`
		Signing  string         `json:"signing_secret"`
		Channels []SlackChannel `json:"channels"`
		Users    []string       `json:"users"`
		Kinds    []string       `json:"kinds"`
		Lang     string         `json:"lang"`
		Timezone string         `json:"timezone"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	st := s.slackSettings(ctx)
	if t := strings.TrimSpace(req.Token); t != "" {
		if !slackTokenRe.MatchString(t) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("slack.badToken"))
			return
		}
		var me struct {
			Team string `json:"team"`
			User string `json:"user"`
		}
		if err := slackCall(ctx, t, "auth.test", map[string]any{}, &me); err != nil {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("slack.tokenRejected", err.Error()))
			return
		}
		enc, err := secretbox.Encrypt(s.hub.key, []byte(t))
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		st.TokenEnc, st.Team, st.BotUser = enc, me.Team, me.User
	}
	if sg := strings.TrimSpace(req.Signing); sg != "" {
		if !slackSigningRe.MatchString(sg) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("slack.badSigning"))
			return
		}
		enc, err := secretbox.Encrypt(s.hub.key, []byte(sg))
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		st.SigningEnc = enc
	}
	if len(req.Channels) > 50 || len(req.Users) > 200 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.tooMany"))
		return
	}
	channels := []SlackChannel{}
	for _, c := range req.Channels {
		c.ID = strings.TrimSpace(c.ID)
		if c.ID == "" {
			continue
		}
		if !slackChannelRe.MatchString(c.ID) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("slack.badChannel", c.ID))
			return
		}
		if slices.ContainsFunc(channels, func(x SlackChannel) bool { return x.ID == c.ID }) {
			continue
		}
		if c.Role != store.TokenRoleAdmin {
			c.Role = store.TokenRoleRead
		}
		c.Name = trimRunes(strings.TrimSpace(c.Name), 64)
		channels = append(channels, c)
	}
	users := []string{}
	for _, u := range req.Users {
		if u = strings.TrimSpace(u); u == "" {
			continue
		}
		if !slackUserRe.MatchString(u) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("slack.badUser", u))
			return
		}
		if !slices.Contains(users, u) {
			users = append(users, u)
		}
	}
	kinds := []string{}
	for _, k := range req.Kinds {
		if !slices.Contains(OutKinds, k) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.outBadKind", k))
			return
		}
		kinds = append(kinds, k)
	}
	st.Enabled, st.Channels, st.Users, st.Kinds = req.Enabled, channels, users, kinds
	st.Lang = "ru"
	if req.Lang == "en" {
		st.Lang = "en"
	}
	if !validZone(req.Timezone) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("tg.badTimezone", req.Timezone))
		return
	}
	st.Timezone = req.Timezone
	if st.Enabled && (len(st.TokenEnc) == 0 || len(st.SigningEnc) == 0) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("slack.incomplete"))
		return
	}
	raw, _ := json.Marshal(st)
	err := s.db.KVSet(ctx, slackSettingsKey, string(raw))
	s.db.Audit(ctx, auth.Username(ctx), "slack.save", st.Team, auditOutcome(err), map[string]any{"enabled": st.Enabled, "channels": st.Channels, "users": st.Users, "kinds": st.Kinds})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "team": st.Team})
}

// handleSlackTest — POST /hub/slack/test {channel}: пробное сообщение.
func (s *Server) handleSlackTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	st := s.slackSettings(ctx)
	if _, ok := st.channel(req.Channel); !ok {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("slack.noChannel", req.Channel))
		return
	}
	token := s.slackSecret(st.TokenEnc)
	if token == "" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("slack.incomplete"))
		return
	}
	if err := slackPost(ctx, token, req.Channel, botReply{Text: msgs.T(st.lang(), "tg.test", auth.Username(ctx))}); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
