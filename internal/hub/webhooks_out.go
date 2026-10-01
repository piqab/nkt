package hub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Исходящие вебхуки: хаб сам шлёт оповещения наружу — в n8n, чат-бот,
// свою систему. Адресат — URL, секрет подписи, какие события, пределы по
// хостам и группам, язык текста. Тело — JSON; подпись — как у входящих
// вебхуков nkt: X-NKT-Timestamp и X-NKT-Signature = hex HMAC-SHA256
// секретом от «время.тело». Доставка — в фоне, с повторами; хаб наружу
// ходит сам, открывать его не нужно.

const outHooksKey = "hub.webhooks.out"

// Виды событий исходящих вебхуков сверх оповещений хостов.
const (
	OutDeploySucceeded = "deploy-succeeded"
	OutDeployFailed    = "deploy-failed"
	OutTest            = "test"
)

// OutKinds — все виды, которые можно выбрать у адресата.
var OutKinds = append(slices.Clone(EventKinds), OutDeploySucceeded, OutDeployFailed)

// outRetries — паузы перед повторами неудачной доставки.
var outRetries = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute}

// OutHook — адресат исходящих вебхуков.
type OutHook struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Kinds   []string `json:"kinds"`
	Hosts   []int64  `json:"hosts"`
	Groups  []string `json:"groups"`
	Lang    string   `json:"lang"`
	Enabled bool     `json:"enabled"`
	Author  string   `json:"author,omitempty"`
	Created string   `json:"created_at,omitempty"`

	SecretEnc []byte `json:"secret_enc,omitempty"`
}

func (h OutHook) wants(kind string) bool {
	return kind == OutTest || len(h.Kinds) == 0 || slices.Contains(h.Kinds, kind)
}

// OutEvent — событие для отправки.
type OutEvent struct {
	Kind string
	TS   time.Time
	// Хост события (нет — событие хаба, например выкладка).
	HostID   int64
	HostName string
	HostAddr string
	// HostIDs — все хосты события (выкладка): для пределов адресата.
	HostIDs  []int64
	Severity string
	EventID  int64
	// Текст: ключ каталога и аргументы (на языке адресата) или готовый.
	Key, Args, Text string

	PipelineID   int64
	PipelineName string
	JobID        int64
	Commit, Tag  string
	Trigger      string
	Error        string
}

// --- хранение ------------------------------------------------------------------

var outHooksMu sync.Mutex

func (m *Manager) outHooks(ctx context.Context) []OutHook {
	var list []OutHook
	if raw, ok, err := m.db.KVGet(ctx, outHooksKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	return list
}

func (m *Manager) saveOutHooks(ctx context.Context, list []OutHook) error {
	if list == nil {
		list = []OutHook{}
	}
	b, _ := json.Marshal(list)
	return m.db.KVSet(ctx, outHooksKey, string(b))
}

// --- доставка -------------------------------------------------------------------

type outStatus struct {
	At       time.Time
	Kind     string
	Code     int
	Err      string
	Attempts int
}

type outDispatcher struct {
	queue  chan OutEvent
	sem    chan struct{}
	client *http.Client

	mu     sync.Mutex
	status map[int64]outStatus
}

func (m *Manager) outD() *outDispatcher {
	m.outOnce.Do(func() {
		m.out = &outDispatcher{
			queue: make(chan OutEvent, 512), sem: make(chan struct{}, 4), status: map[int64]outStatus{},
			client: &http.Client{
				Timeout: 15 * time.Second,
				// Переадресацию не повторяем: подпись — для этого адреса.
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			},
		}
		go m.outLoop()
	})
	return m.out
}

// emitOut — событие исходящим вебхукам (не ждёт доставки; очередь полна
// — событие теряется с записью в журнал службы).
func (m *Manager) emitOut(ev OutEvent) {
	if ev.TS.IsZero() {
		ev.TS = time.Now()
	}
	d := m.outD()
	select {
	case d.queue <- ev:
	default:
		m.log.Warn("очередь исходящих вебхуков полна, событие пропущено", "kind", ev.Kind)
	}
}

func (m *Manager) outLoop() {
	d := m.out
	for ev := range d.queue {
		ctx := context.Background()
		hooks := m.outHooks(ctx)
		var groups map[int64]string
		for _, h := range hooks {
			if !h.Enabled || !h.wants(ev.Kind) {
				continue
			}
			if len(h.Hosts) > 0 || len(h.Groups) > 0 {
				if groups == nil {
					groups = m.hostGroups(ctx)
				}
				if !outInScope(h, ev, groups) {
					continue
				}
			}
			go m.deliverOut(ctx, h, ev, true)
		}
	}
}

// outInScope — событие в пределах адресата: хост события или все хосты
// выкладки.
func outInScope(h OutHook, ev OutEvent, groups map[int64]string) bool {
	tok := store.APIToken{Hosts: h.Hosts, Groups: h.Groups}
	ids := ev.HostIDs
	if len(ids) == 0 && ev.HostName != "" {
		ids = []int64{ev.HostID}
	}
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !tok.AllowsHost(id, groups[id]) {
			return false
		}
	}
	return true
}

// outPayload — тело запроса.
func outPayload(h OutHook, ev OutEvent, delivery, hub string) []byte {
	lang := msgs.Lang(h.Lang)
	if lang != msgs.EN {
		lang = msgs.RU
	}
	text := msgs.Render(lang, ev.Key, ev.Args, ev.Text)
	body := map[string]any{
		"id": delivery, "kind": ev.Kind, "ts": ev.TS.UTC().Format(time.RFC3339), "hub": hub, "text": text,
	}
	if ev.Severity != "" {
		body["severity"] = ev.Severity
	}
	if ev.EventID != 0 {
		body["event_id"] = ev.EventID
	}
	if ev.HostName != "" {
		body["host"] = map[string]any{"id": ev.HostID, "name": ev.HostName, "addr": ev.HostAddr}
	}
	if ev.PipelineID != 0 {
		body["pipeline"] = map[string]any{"id": ev.PipelineID, "name": ev.PipelineName}
		body["job_id"] = ev.JobID
		body["commit"], body["tag"], body["trigger"] = ev.Commit, ev.Tag, ev.Trigger
		if ev.Error != "" {
			body["error"] = ev.Error
		}
	}
	// Без экранирования <, >, & (\u003c…): получатель на JS
	// (JSON.stringify разобранного тела) получит те же байты для подписи.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// OutSignature — подпись тела (её проверяет получатель).
func OutSignature(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// deliverOut — одна доставка (retry — с повторами); итог — в состоянии
// адресата.
func (m *Manager) deliverOut(ctx context.Context, h OutHook, ev OutEvent, retry bool) outStatus {
	d := m.outD()
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	delivery := hex.EncodeToString(b)
	secret, err := secretbox.Decrypt(m.key, h.SecretEnc)
	st := outStatus{Kind: ev.Kind}
	if err != nil {
		st.At, st.Err = time.Now(), err.Error()
	} else {
		body := outPayload(h, ev, delivery, "nkt "+m.version)
		for attempt := 0; ; attempt++ {
			// Слот — только на сам запрос: паузы между повторами не
			// занимают его, и мёртвый адрес не держит остальных.
			d.sem <- struct{}{}
			st = m.postOut(ctx, d.client, h, ev.Kind, delivery, string(secret), body)
			<-d.sem
			st.Attempts = attempt + 1
			if st.Err == "" || !retry || attempt >= len(outRetries) {
				break
			}
			select {
			case <-ctx.Done():
				return st
			case <-time.After(outRetries[attempt]):
			}
		}
	}
	d.mu.Lock()
	d.status[h.ID] = st
	d.mu.Unlock()
	if st.Err != "" {
		m.log.Warn("исходящий вебхук не доставлен", "hook", h.Name, "kind", ev.Kind, "err", st.Err)
	}
	return st
}

func (m *Manager) postOut(ctx context.Context, c *http.Client, h OutHook, kind, delivery, secret string, body []byte) outStatus {
	st := outStatus{At: time.Now(), Kind: kind}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		st.Err = err.Error()
		return st
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "nkt-hub/"+m.version)
	req.Header.Set("X-NKT-Event", kind)
	req.Header.Set("X-NKT-Delivery", delivery)
	req.Header.Set("X-NKT-Timestamp", ts)
	req.Header.Set("X-NKT-Signature", OutSignature(secret, ts, body))
	resp, err := c.Do(req)
	if err != nil {
		st.Err = err.Error()
		return st
	}
	defer resp.Body.Close()
	st.Code = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		st.Err = strings.TrimSpace(fmt.Sprintf("HTTP %d %s", resp.StatusCode, snippet))
	}
	return st
}

// --- события: откуда берутся ----------------------------------------------------

// outFromHostEvent — оповещение хоста для вебхуков.
func outFromHostEvent(host store.Host, addr, kind, severity, detail, key, args string, eventID int64) OutEvent {
	return OutEvent{Kind: kind, HostID: host.ID, HostName: host.Name, HostAddr: addr, Severity: severity,
		EventID: eventID, Key: key, Args: args, Text: detail}
}

// emitDeploy — выкладка закончилась (не сухой прогон).
func (s *Server) emitDeploy(ctx context.Context, pl store.Pipeline, d store.Deployment, jobID int64, commit string, failure error) {
	if s.hub == nil {
		return
	}
	ev := OutEvent{Kind: OutDeploySucceeded, PipelineID: pl.ID, PipelineName: pl.Name, JobID: jobID,
		Commit: commit, Tag: d.Tag, Trigger: d.Trigger, HostIDs: s.pipelineHostIDs(ctx, pl)}
	what := d.Tag
	if what == "" && commit != "" {
		what = commit[:min(len(commit), 12)]
	}
	ev.Key, ev.Args = "hub.outDeployOK", msgs.EncodeArgs([]any{pl.Name, what})
	if failure != nil {
		ev.Kind, ev.Severity = OutDeployFailed, "error"
		ev.Error = msgs.Localize(msgs.EN, failure)
		ev.Key, ev.Args = "hub.outDeployFailed", msgs.EncodeArgs([]any{pl.Name, what, ev.Error})
	}
	s.hub.emitOut(ev)
}

// pipelineHostIDs — хосты compose-конвейера (прочие виды — без хостов).
func (s *Server) pipelineHostIDs(ctx context.Context, pl store.Pipeline) []int64 {
	targets, ok := s.pipelineTargets(ctx, pl)
	if !ok {
		return nil
	}
	ids := make([]int64, len(targets))
	for i, t := range targets {
		ids[i] = t.ID
	}
	return ids
}

// --- API -----------------------------------------------------------------------

type outHookJSON struct {
	OutHook
	HasSecret bool   `json:"has_secret"`
	LastAt    string `json:"last_at,omitempty"`
	LastKind  string `json:"last_kind,omitempty"`
	LastCode  int    `json:"last_code,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// handleOutHooks — GET /hub/webhooks.
func (s *Server) handleOutHooks(w http.ResponseWriter, r *http.Request) {
	d := s.hub.outD()
	out := []outHookJSON{}
	for _, h := range s.hub.outHooks(r.Context()) {
		item := outHookJSON{OutHook: h, HasSecret: len(h.SecretEnc) > 0}
		item.SecretEnc = nil
		d.mu.Lock()
		if st, ok := d.status[h.ID]; ok {
			item.LastAt, item.LastKind, item.LastCode, item.LastError = store.FormatTime(st.At), st.Kind, st.Code, st.Err
		}
		d.mu.Unlock()
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"hooks": out, "kinds": OutKinds})
}

type outHookReq struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Kinds   []string `json:"kinds"`
	Hosts   []int64  `json:"hosts"`
	Groups  []string `json:"groups"`
	Lang    string   `json:"lang"`
	Enabled bool     `json:"enabled"`
}

func (s *Server) validateOutHook(r *http.Request, req *outHookReq) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 64 || strings.ContainsAny(req.Name, "\n\r\t") {
		return msgs.Errorf("hub.outBadName")
	}
	req.URL = strings.TrimSpace(req.URL)
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(req.URL) > 2048 || u.User != nil {
		return msgs.Errorf("hub.outBadURL", req.URL)
	}
	kinds := []string{}
	for _, k := range req.Kinds {
		if !slices.Contains(OutKinds, k) {
			return msgs.Errorf("hub.outBadKind", k)
		}
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	req.Kinds = kinds
	if req.Lang != "en" {
		req.Lang = "ru"
	}
	// Хосты и группы — те же проверки, что у токенов.
	tr := tokenReq{Name: "x", Role: store.TokenRoleRead, Hosts: req.Hosts, Groups: req.Groups}
	if err := s.validateTokenReq(r, &tr); err != nil {
		return err
	}
	req.Hosts, req.Groups = tr.Hosts, tr.Groups
	return nil
}

func outHookIDParam(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}

func newOutSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// handleOutHookSave — POST /hub/webhooks (новый; секрет — один раз в
// ответе) и PUT /hub/webhooks/{id} (правка; секрет прежний).
func (s *Server) handleOutHookSave(w http.ResponseWriter, r *http.Request) {
	var req outHookReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.validateOutHook(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	outHooksMu.Lock()
	defer outHooksMu.Unlock()
	list := s.hub.outHooks(ctx)
	var h OutHook
	secret := ""
	if r.Method == http.MethodPut {
		i := slices.IndexFunc(list, func(x OutHook) bool { return x.ID == outHookIDParam(r) })
		if i < 0 {
			writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.outMissing", outHookIDParam(r)))
			return
		}
		h = list[i]
	} else {
		if len(list) >= 32 {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.outTooMany"))
			return
		}
		var err error
		if secret, err = newOutSecret(); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		if h.SecretEnc, err = secretbox.Encrypt(s.hub.key, []byte(secret)); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		for _, x := range list {
			h.ID = max(h.ID, x.ID)
		}
		h.ID++
		h.Author, h.Created = user, store.FormatTime(time.Now())
	}
	h.Name, h.URL, h.Kinds, h.Hosts, h.Groups, h.Lang, h.Enabled = req.Name, req.URL, req.Kinds, req.Hosts, req.Groups, req.Lang, req.Enabled
	if r.Method == http.MethodPut {
		list[slices.IndexFunc(list, func(x OutHook) bool { return x.ID == h.ID })] = h
	} else {
		list = append(list, h)
	}
	err := s.hub.saveOutHooks(ctx, list)
	s.db.Audit(ctx, user, "webhook.save", h.Name, auditOutcome(err), map[string]any{"url": h.URL, "kinds": h.Kinds, "hosts": h.Hosts, "groups": h.Groups, "enabled": h.Enabled})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	out := map[string]any{"id": h.ID}
	if secret != "" {
		out["secret"] = secret
	}
	writeJSON(w, http.StatusOK, out)
}

// handleOutHookRotate — POST /hub/webhooks/{id}/rotate: новый секрет.
func (s *Server) handleOutHookRotate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	outHooksMu.Lock()
	defer outHooksMu.Unlock()
	list := s.hub.outHooks(ctx)
	i := slices.IndexFunc(list, func(x OutHook) bool { return x.ID == outHookIDParam(r) })
	if i < 0 {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.outMissing", outHookIDParam(r)))
		return
	}
	secret, err := newOutSecret()
	if err == nil {
		list[i].SecretEnc, err = secretbox.Encrypt(s.hub.key, []byte(secret))
	}
	if err == nil {
		err = s.hub.saveOutHooks(ctx, list)
	}
	s.db.Audit(ctx, auth.Username(ctx), "webhook.rotate", list[i].Name, auditOutcome(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": list[i].ID, "secret": secret})
}

// handleOutHookDelete — DELETE /hub/webhooks/{id}.
func (s *Server) handleOutHookDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	outHooksMu.Lock()
	defer outHooksMu.Unlock()
	list := s.hub.outHooks(ctx)
	i := slices.IndexFunc(list, func(x OutHook) bool { return x.ID == outHookIDParam(r) })
	if i < 0 {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.outMissing", outHookIDParam(r)))
		return
	}
	name := list[i].Name
	err := s.hub.saveOutHooks(ctx, slices.Delete(list, i, i+1))
	s.db.Audit(ctx, auth.Username(ctx), "webhook.delete", name, auditOutcome(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleOutHookTest — POST /hub/webhooks/{id}/test: пробное событие сразу,
// без повторов; ответ — итог доставки.
func (s *Server) handleOutHookTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list := s.hub.outHooks(ctx)
	i := slices.IndexFunc(list, func(x OutHook) bool { return x.ID == outHookIDParam(r) })
	if i < 0 {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.outMissing", outHookIDParam(r)))
		return
	}
	user := auth.Username(ctx)
	st := s.hub.deliverOut(ctx, list[i], OutEvent{Kind: OutTest, TS: time.Now(), Key: "hub.outTestText", Args: msgs.EncodeArgs([]any{user})}, false)
	var err error
	if st.Err != "" {
		err = errors.New(st.Err)
	}
	s.db.Audit(ctx, user, "webhook.test", list[i].Name, auditOutcome(err), map[string]any{"code": st.Code})
	writeJSON(w, http.StatusOK, map[string]any{"code": st.Code, "error": st.Err})
}
