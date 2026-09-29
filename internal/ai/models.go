package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Список моделей провайдера — для выбора в настройках вместо ручного
// ввода имени. Запрос бесплатный (не тратит токены) и суточный лимит не
// расходует.

// ModelInfo — одна модель провайдера.
type ModelInfo struct {
	ID string `json:"id"`
	// Name — отображаемое имя (Anthropic), иначе пусто.
	Name string `json:"name,omitempty"`
	// Created — когда выпущена или загружена (RFC 3339), если известно.
	Created string `json:"created,omitempty"`
	// Size — размер файла модели в байтах (Ollama), если известно.
	Size int64 `json:"size,omitempty"`
	// Chat — годится для разбора (текстовый чат). У официального OpenAI
	// в списке есть эмбеддинги, распознавание речи, картинки — их
	// интерфейс по умолчанию прячет.
	Chat bool `json:"chat"`
}

// nonChatRe — модели OpenAI, которые не ведут текстовый диалог.
var nonChatRe = regexp.MustCompile(`(?i)(embed|rerank|whisper|tts|dall-e|moderation|transcribe|realtime|audio|image|babbage|davinci-00|search|similarity|computer-use|sora)`)

// IsChatModel — годится ли модель для разбора (по имени).
func IsChatModel(id string) bool { return !nonChatRe.MatchString(id) }

// ListModels — модели провайдера с теми настройками, что в c.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var (
		list []ModelInfo
		err  error
	)
	switch c.set.Provider {
	case ProviderOpenAI:
		list, err = c.listOpenAI(ctx)
	default:
		list, err = c.listAnthropic(ctx)
	}
	if err != nil {
		return nil, err
	}
	// Новые сверху; без даты — по имени, после датированных.
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Created != list[j].Created {
			return list[i].Created > list[j].Created
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

func (c *Client) base() string { return NormalizeBaseURL(c.set.BaseURL) }

// NormalizeBaseURL — адрес провайдера без хвостового «/v1»: пути хаб
// дописывает сам, а провайдеры в документации часто дают адрес уже с ним
// (http://127.0.0.1:8080/v1, https://openrouter.ai/api/v1) — иначе
// получалось …/v1/v1/… и 404.
func NormalizeBaseURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	return strings.TrimRight(strings.TrimSuffix(u, "/v1"), "/")
}

// listAnthropic — GET /v1/models постранично (after_id).
func (c *Client) listAnthropic(ctx context.Context) ([]ModelInfo, error) {
	var out []ModelInfo
	after := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"limit": {"1000"}}
		if after != "" {
			q.Set("after_id", after)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/v1/models?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("anthropic-version", "2023-06-01")
		if c.set.APIKey != "" {
			req.Header.Set("x-api-key", c.set.APIKey)
		}
		raw, err := c.do(req)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
				CreatedAt   string `json:"created_at"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, msgs.Errorf("ai.badResponse", err)
		}
		for _, m := range resp.Data {
			out = append(out, ModelInfo{ID: m.ID, Name: m.DisplayName, Created: m.CreatedAt, Chat: true})
		}
		if !resp.HasMore || resp.LastID == "" {
			break
		}
		after = resp.LastID
	}
	return out, nil
}

// listOpenAI — GET /v1/models; сервер, который его не знает (старый
// Ollama), — GET /api/tags.
func (c *Client) listOpenAI(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if c.set.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.set.APIKey)
	}
	raw, err := c.do(req)
	if err == nil {
		var resp struct {
			Data []struct {
				ID      string `json:"id"`
				Created int64  `json:"created"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, msgs.Errorf("ai.badResponse", err)
		}
		out := make([]ModelInfo, 0, len(resp.Data))
		for _, m := range resp.Data {
			mi := ModelInfo{ID: m.ID, Chat: IsChatModel(m.ID)}
			if m.Created > 0 {
				mi.Created = time.Unix(m.Created, 0).UTC().Format(time.RFC3339)
			}
			out = append(out, mi)
		}
		// Ollama отдаёт в /v1/models и размер знает только /api/tags —
		// дополняем, если он есть.
		if sizes := c.ollamaTags(ctx); len(sizes) > 0 {
			for i := range out {
				if t, ok := sizes[out[i].ID]; ok {
					out[i].Size = t.Size
					if out[i].Created == "" {
						out[i].Created = t.Created
					}
				}
			}
		}
		return out, nil
	}
	if tags := c.ollamaTags(ctx); len(tags) > 0 {
		out := make([]ModelInfo, 0, len(tags))
		for id, t := range tags {
			out = append(out, ModelInfo{ID: id, Size: t.Size, Created: t.Created, Chat: IsChatModel(id)})
		}
		return out, nil
	}
	return nil, err
}

type ollamaTag struct {
	Size    int64
	Created string
}

// ollamaTags — модели Ollama по /api/tags (nil — это не Ollama).
func (c *Client) ollamaTags(ctx context.Context) map[string]ollamaTag {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/api/tags", nil)
	if err != nil {
		return nil
	}
	raw, err := c.do(req)
	if err != nil {
		return nil
	}
	var resp struct {
		Models []struct {
			Name       string `json:"name"`
			Model      string `json:"model"`
			Size       int64  `json:"size"`
			ModifiedAt string `json:"modified_at"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return nil
	}
	out := map[string]ollamaTag{}
	for _, m := range resp.Models {
		id := m.Model
		if id == "" {
			id = m.Name
		}
		created := ""
		if t, err := time.Parse(time.RFC3339Nano, m.ModifiedAt); err == nil {
			created = t.UTC().Format(time.RFC3339)
		}
		out[id] = ollamaTag{Size: m.Size, Created: created}
	}
	return out
}
