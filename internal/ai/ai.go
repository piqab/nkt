// Package ai — разбор находок и архитектуры языковой моделью.
//
// Модель здесь только объясняет: что значит находка, чем она грозит
// именно на этом хосте и как её чинить. Ничего не выполняет и ничего не
// меняет — команды из ответа показываются оператору для копирования, а
// применяются обычными кнопками nkt. Решение остаётся за человеком, и
// это не осторожность ради осторожности: модель ошибается, а на той
// стороне боевой сервер.
//
// Настраивается один раз на хабе и действует для всех хостов: ключ и
// адрес живут в базе хаба, запросы уходят с него. Хостам наружу ничего
// не нужно — у них может не быть интернета вовсе.
//
// Провайдеры: Anthropic и всё, что говорит на языке OpenAI Chat
// Completions — включая локальные (Ollama, vLLM, LM Studio). Локальная
// модель здесь не экзотика: она единственный вариант, когда наружу
// отдавать нельзя ничего.
package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Provider — с кем разговаривать.
const (
	// ProviderAnthropic — https://api.anthropic.com/v1/messages.
	ProviderAnthropic = "anthropic"
	// ProviderOpenAI — любой /v1/chat/completions: OpenAI, Ollama
	// (http://127.0.0.1:11434/v1), vLLM, LM Studio, шлюз компании.
	ProviderOpenAI = "openai"
)

// Settings — то, что оператор задаёт в «О системе». Ключ здесь уже
// расшифрован: в базе он лежит зашифрованным (secretbox), как ключи SSH.
type Settings struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	// APIKey — пустой для локальной модели, которой ключ не нужен.
	APIKey string `json:"-"`
	// HasKey — есть ли сохранённый ключ (сам ключ наружу не отдаётся).
	HasKey bool `json:"has_key"`
	// Anonymize — заменять имена хостов, адреса и пути псевдонимами
	// перед отправкой. По умолчанию включено: запрос уходит к чужому
	// сервису, и структура проблемы там нужна, а адреса — нет.
	Anonymize bool `json:"anonymize"`
	// DailyLimit — сколько запросов в сутки разрешено (0 — без лимита).
	// Считается на хабе, общий для всех хостов и пользователей.
	DailyLimit int `json:"daily_limit"`
	// TimeoutS — предел одного запроса в секундах; локальная модель на
	// слабой машине думает дольше облачной, и 90 секунд ей мало.
	TimeoutS int `json:"timeout_s"`
	// Timeout — то же для кода; выставляется из TimeoutS в New.
	Timeout time.Duration `json:"-"`
}

// DefaultTimeoutS — время ожидания по умолчанию: облачной модели хватает
// с запасом, локальной обычно нужно больше — это настраивается.
const DefaultTimeoutS = 90

// DefaultSettings — с чего начинается выключенный ИИ.
func DefaultSettings() Settings {
	return Settings{
		Provider:   ProviderAnthropic,
		BaseURL:    "https://api.anthropic.com",
		Model:      "claude-sonnet-5",
		Anonymize:  true,
		DailyLimit: 200,
		TimeoutS:   DefaultTimeoutS,
	}
}

// Client — один провайдер с его настройками.
type Client struct {
	set  Settings
	http *http.Client
}

// New строит клиента. Вызывающий сам решает, включён ли ИИ (Settings.Enabled).
func New(set Settings) *Client {
	if set.TimeoutS > 0 {
		set.Timeout = time.Duration(set.TimeoutS) * time.Second
	}
	if set.Timeout <= 0 {
		set.Timeout = DefaultTimeoutS * time.Second
	}
	return &Client{set: set, http: &http.Client{Timeout: set.Timeout}}
}

// Settings — текущие настройки клиента.
func (c *Client) Settings() Settings { return c.set }

// Ask отправляет системную инструкцию и запрос, возвращает текст ответа.
//
// Формат ответа не навязывается протоколом: локальные модели обычно не
// умеют structured output, а разбирать их «почти JSON» — источник
// постоянных сюрпризов. Вместо этого просится размеченный текст, и
// разбор (parseSections) прощает отклонения.
func (c *Client) Ask(ctx context.Context, system, user string) (string, error) {
	switch c.set.Provider {
	case ProviderOpenAI:
		return c.askOpenAI(ctx, system, user)
	default:
		return c.askAnthropic(ctx, system, user)
	}
}

func (c *Client) askAnthropic(ctx context.Context, system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      c.set.Model,
		"max_tokens": 1500,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.set.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if c.set.APIKey != "" {
		req.Header.Set("x-api-key", c.set.APIKey)
	}
	raw, err := c.do(req)
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", msgs.Errorf("ai.badResponse", err)
	}
	var b strings.Builder
	for _, part := range out.Content {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	if b.Len() == 0 {
		return "", msgs.Errorf("ai.emptyResponse")
	}
	return b.String(), nil
}

func (c *Client) askOpenAI(ctx context.Context, system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": c.set.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.set.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.set.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.set.APIKey)
	}
	raw, err := c.do(req)
	if err != nil {
		return "", err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", msgs.Errorf("ai.badResponse", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", msgs.Errorf("ai.emptyResponse")
	}
	return out.Choices[0].Message.Content, nil
}

// maxResponseBytes — потолок тела ответа: модель может заговориться, а
// хаб не должен из-за этого съесть память.
const maxResponseBytes = 4 << 20

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		// Таймаут — самая частая причина у локальных моделей, и «context
		// deadline exceeded» ничего не говорит о том, что делать.
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
			return nil, msgs.Errorf("ai.timeout", int(c.set.Timeout/time.Second))
		}
		return nil, msgs.Errorf("ai.requestFailed", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, msgs.Errorf("ai.requestFailed", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, msgs.Errorf("ai.providerCode", resp.StatusCode, firstLine(string(raw)))
	}
	return raw, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// CacheKey — ключ кэша ответа: одна и та же находка на десяти хостах
// стоит одного запроса. Модель и язык входят в ключ — ответ зависит и
// от них.
func CacheKey(kind, model, lang, payload string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + model + "\x00" + lang + "\x00" + payload))
	return hex.EncodeToString(sum[:])
}

// Section — раздел разобранного ответа (заголовок + текст).
type Section struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// ParseSections разбирает ответ на разделы по строкам вида «## Заголовок».
// Текст до первого заголовка становится безымянным разделом — модель
// иногда начинает с одного абзаца, и терять его незачем.
func ParseSections(text string) []Section {
	var out []Section
	cur := Section{}
	flush := func() {
		cur.Body = strings.TrimSpace(cur.Body)
		if cur.Title != "" || cur.Body != "" {
			out = append(out, cur)
		}
		cur = Section{}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			cur.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		cur.Body += line + "\n"
	}
	flush()
	return out
}

// Usage — расход за сутки для лимита.
type Usage struct {
	Day      string `json:"day"`
	Requests int    `json:"requests"`
}

// LimitReached — исчерпан ли суточный лимит.
func LimitReached(set Settings, u Usage) bool {
	return set.DailyLimit > 0 && u.Requests >= set.DailyLimit
}

// Describe — короткое описание провайдера для интерфейса и журнала.
func Describe(set Settings) string {
	return fmt.Sprintf("%s %s (%s)", set.Provider, set.Model, set.BaseURL)
}
