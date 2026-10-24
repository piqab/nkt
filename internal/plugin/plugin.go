// Package plugin — протокол внешних плагинов хаба nkt (версия 1).
//
// Плагин — отдельная программа (обычно контейнер, выложенный конвейером),
// которая слушает HTTP на 127.0.0.1 своего хоста. Хаб ходит к ней через
// SSH-соединение с хостом (на машине хаба — напрямую) и подписывает
// каждый запрос общим секретом. Плагин описывает себя манифестом
// (GET /nkt/manifest): операции, разделы интерфейса, команды сценариев,
// что он умеет для хаба (provides) и какие права просит у хаба
// (hub_actions). Все вызовы — POST /nkt/call {op, args}.
//
// Тексты (названия, подписи полей, ошибки) плагин отдаёт уже на языке
// запроса — заголовок X-NKT-Lang (ru, en).
package plugin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Protocol — версия протокола, которую понимает хаб.
const Protocol = 1

// Пути протокола.
const (
	PathManifest = "/nkt/manifest"
	PathCall     = "/nkt/call"
)

// Заголовки подписи и языка.
const (
	HeaderTimestamp = "X-NKT-Timestamp"
	HeaderSignature = "X-NKT-Signature"
	HeaderLang      = "X-NKT-Lang"
)

// MaxSkew — насколько отметка времени запроса может отличаться от часов
// плагина.
const MaxSkew = 5 * time.Minute

// Служебные операции: их нет в манифесте, хаб вызывает их сам.
const (
	// OpConnect — плагин подключён (или переподключён): {name, hub_url,
	// token} — имя в хабе и токен хаба, если плагин просил hub_actions.
	OpConnect = "_connect"
	// OpCommand — команда сценария: {argv, dry_run}; argv — слова после
	// команды. Ответ text — что сделано (или будет сделано).
	OpCommand = "_command"
	// OpCheck — «Проверить» сценарий: {argv} — разобрать без действия.
	OpCheck = "_check"
	// OpDNSSync — provides dns: записи владельца {owner, records,
	// previous, dry_run} — привести зону к records, убрав previous,
	// которых в records нет.
	OpDNSSync = "_dns_sync"
	// OpDNSZones — provides dns: зоны плагина {zones: [имя…]}.
	OpDNSZones = "_dns_zones"
)

// Provides — что плагин умеет для самого хаба.
const ProvidesDNS = "dns"

// Manifest — описание плагина.
type Manifest struct {
	Protocol    int    `json:"protocol"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	// Ops — операции: их вызывают разделы интерфейса, инструменты MCP
	// (mcp: true) и сам хаб.
	Ops []Op `json:"ops"`
	// Pages — разделы интерфейса: каждый — операция, которая возвращает
	// вид (View).
	Pages []Page `json:"pages,omitempty"`
	// Commands — команды сценариев: слово и справка; выполняет их
	// операция _command.
	Commands []Command `json:"commands,omitempty"`
	// Provides — что плагин делает для хаба: «dns» — записи для маршрутов
	// Traffic Fly, сайтов и имён хостов (_dns_sync, _dns_zones).
	Provides []string `json:"provides,omitempty"`
	// HubActions — права, которые плагин просит у хаба: действия токена
	// «по правам» (pipeline.deploy, fly.manage…). Хаб выпускает плагину
	// такой токен при подключении, если администратор согласился.
	HubActions []string `json:"hub_actions,omitempty"`
}

// Op — операция плагина.
type Op struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Input — JSON Schema аргументов (объект).
	Input map[string]any `json:"input,omitempty"`
	// ReadOnly — ничего не меняет: доступна всем, кто видит хаб, и
	// токенам чтения. Иначе — администратору или токену с действием
	// plugin.<имя>.
	ReadOnly    bool `json:"read_only,omitempty"`
	Destructive bool `json:"destructive,omitempty"`
	// MCP — показать агентам как инструмент «<имя плагина>_<операция>».
	MCP bool `json:"mcp,omitempty"`
}

// Page — раздел интерфейса.
type Page struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Op — операция (только чтение), которая возвращает вид раздела.
	Op string `json:"op"`
	// Hidden — не кнопкой вверху: раздел открывается только из другого
	// (Action.Open с аргументами — «записи зоны»).
	Hidden bool `json:"hidden,omitempty"`
}

// Command — команда сценария.
type Command struct {
	Word    string `json:"word"`
	Usage   string `json:"usage"`
	Summary string `json:"summary,omitempty"`
	Example string `json:"example,omitempty"`
}

// Request — тело /nkt/call.
type Request struct {
	Op     string         `json:"op"`
	Args   map[string]any `json:"args,omitempty"`
	User   string         `json:"user,omitempty"`
	DryRun bool           `json:"dry_run,omitempty"`
}

// Response — ответ /nkt/call.
type Response struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Text   string          `json:"text,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	View   *View           `json:"view,omitempty"`
}

// View — что показать в разделе: таблица, текст или несколько видов
// подряд.
type View struct {
	Type    string   `json:"type"` // table | text | sections
	Title   string   `json:"title,omitempty"`
	Note    string   `json:"note,omitempty"`
	Text    string   `json:"text,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	Rows    []Row    `json:"rows,omitempty"`
	Actions []Action `json:"actions,omitempty"`
	Items   []View   `json:"items,omitempty"`
}

// Column — столбец таблицы.
type Column struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// Row — строка таблицы: значения по ключам столбцов и действия строки.
type Row struct {
	Cells   map[string]any `json:"cells"`
	Actions []Action       `json:"actions,omitempty"`
}

// Action — кнопка: вызвать операцию (с формой и подтверждением), открыть
// раздел с аргументами или править документ (текст с диффом и историей).
type Action struct {
	Title   string         `json:"title"`
	Op      string         `json:"op,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	Fields  []Field        `json:"fields,omitempty"`
	Confirm string         `json:"confirm,omitempty"`
	Danger  bool           `json:"danger,omitempty"`
	// Open — раздел (Page.ID) с Args: «открыть зону».
	Open string `json:"open,omitempty"`
	// Edit — документ: Get возвращает result.text, Put принимает
	// args.text; Doc — его имя в истории версий хаба.
	Edit *Edit `json:"edit,omitempty"`
}

// Edit — правка документа.
type Edit struct {
	Get      string `json:"get"`
	Put      string `json:"put"`
	Doc      string `json:"doc"`
	Language string `json:"language,omitempty"`
}

// Field — поле формы.
type Field struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Type        string   `json:"type,omitempty"` // text | textarea | number | select | bool
	Options     []Option `json:"options,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Hint        string   `json:"hint,omitempty"`
}

// Option — вариант поля select.
type Option struct {
	Value string `json:"value"`
	Title string `json:"title,omitempty"`
}

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	opRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	wordRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
)

// ValidName — имя плагина (и его экземпляра в хабе).
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Validate — манифест можно подключить: версия протокола, имена,
// страницы и действия ссылаются на свои операции, без повторов.
func (m *Manifest) Validate() error {
	if m.Protocol != Protocol {
		return fmt.Errorf("protocol %d, the hub speaks %d", m.Protocol, Protocol)
	}
	if !ValidName(m.Name) {
		return fmt.Errorf("bad plugin name %q", m.Name)
	}
	if strings.TrimSpace(m.Title) == "" {
		m.Title = m.Name
	}
	ops := map[string]Op{}
	for _, o := range m.Ops {
		if !opRe.MatchString(o.Name) {
			return fmt.Errorf("bad op name %q", o.Name)
		}
		if _, dup := ops[o.Name]; dup {
			return fmt.Errorf("op %q twice", o.Name)
		}
		ops[o.Name] = o
	}
	pages := map[string]bool{}
	for _, p := range m.Pages {
		if !opRe.MatchString(p.ID) || pages[p.ID] {
			return fmt.Errorf("bad or repeated page %q", p.ID)
		}
		pages[p.ID] = true
		if o, ok := ops[p.Op]; !ok || !o.ReadOnly {
			return fmt.Errorf("page %q: op %q is missing or not read-only", p.ID, p.Op)
		}
	}
	words := map[string]bool{}
	for _, c := range m.Commands {
		if !wordRe.MatchString(c.Word) || words[c.Word] {
			return fmt.Errorf("bad or repeated command %q", c.Word)
		}
		words[c.Word] = true
	}
	for _, p := range m.Provides {
		if p != ProvidesDNS {
			return fmt.Errorf("unknown provides %q", p)
		}
	}
	return nil
}

// Op — операция по имени.
func (m *Manifest) Op(name string) (Op, bool) {
	i := slices.IndexFunc(m.Ops, func(o Op) bool { return o.Name == name })
	if i < 0 {
		return Op{}, false
	}
	return m.Ops[i], true
}

// Provides — плагин делает это для хаба.
func (m *Manifest) Has(what string) bool { return slices.Contains(m.Provides, what) }

// Sign — подпись запроса: HMAC-SHA256 строки «<отметка>.<путь>.<тело>».
func Sign(secret, ts, path string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + path + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify — проверка подписи на стороне плагина (для плагинов на Go и
// тестов): отметка не старше MaxSkew, подпись совпадает.
func Verify(secret string, r *http.Request, body []byte, now time.Time) bool {
	ts := r.Header.Get(HeaderTimestamp)
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(sec, 0)); d > MaxSkew || d < -MaxSkew {
		return false
	}
	want := Sign(secret, ts, r.URL.Path, body)
	return hmac.Equal([]byte(want), []byte(r.Header.Get(HeaderSignature)))
}

// Dial — соединение с плагином (через SSH-канал хоста или напрямую).
type Dial func(ctx context.Context) (net.Conn, error)

// Client — вызовы плагина.
type Client struct {
	Dial    Dial
	Secret  string
	Lang    string
	Timeout time.Duration
}

// maxBody — предел ответа плагина.
const maxBody = 8 << 20

func (c Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hc := &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return c.Dial(ctx) },
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequestWithContext(ctx, method, "http://plugin"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, Sign(c.Secret, ts, path, body))
	req.Header.Set(HeaderLang, c.Lang)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("plugin refused the signature (HTTP %d): wrong secret or clock skew", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 300)])))
		}
		return fmt.Errorf("plugin answer is not JSON: %w", err)
	}
	return nil
}

// Manifest — GET /nkt/manifest с проверкой.
func (c Client) Manifest(ctx context.Context) (Manifest, error) {
	var m Manifest
	if err := c.do(ctx, http.MethodGet, PathManifest, nil, &m); err != nil {
		return m, err
	}
	return m, m.Validate()
}

// Call — POST /nkt/call. Ответ с ok=false — ошибка с текстом плагина.
func (c Client) Call(ctx context.Context, req Request) (Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := c.do(ctx, http.MethodPost, PathCall, body, &resp); err != nil {
		return resp, err
	}
	if !resp.OK {
		msg := resp.Error
		if msg == "" {
			msg = "plugin returned ok=false"
		}
		return resp, &CallError{Msg: msg}
	}
	return resp, nil
}

// CallError — ошибка, которую вернул сам плагин (а не связь с ним).
type CallError struct{ Msg string }

func (e *CallError) Error() string { return e.Msg }
