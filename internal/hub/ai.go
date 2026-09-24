package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/ai"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Разбор находок и архитектуры языковой моделью — на стороне хаба.
//
// Настройка одна на всю установку: адрес, модель и ключ живут в базе
// хаба, запросы уходят с него. Хосту наружу ничего не нужно — у него
// может не быть интернета вовсе, а ключ раздавать на каждый хост было
// бы и лишним, и опасным.
//
// Ключ хранится зашифрованным тем же мастер-ключом, что и секреты SSH
// (secretbox), и наружу не отдаётся никогда — API сообщает только,
// задан он или нет.

const (
	aiSettingsKVKey = "ai.settings"
	aiKeyKVKey      = "ai.api_key_enc"
)

// AISettings — настройки модели без ключа.
func (m *Manager) AISettings(ctx context.Context) ai.Settings {
	set := ai.DefaultSettings()
	if raw, ok, err := m.db.KVGet(ctx, aiSettingsKVKey); err == nil && ok {
		var stored struct {
			Enabled    bool   `json:"enabled"`
			Provider   string `json:"provider"`
			BaseURL    string `json:"base_url"`
			Model      string `json:"model"`
			Anonymize  bool   `json:"anonymize"`
			DailyLimit int    `json:"daily_limit"`
		}
		if json.Unmarshal([]byte(raw), &stored) == nil {
			set.Enabled = stored.Enabled
			set.Provider = stored.Provider
			set.BaseURL = stored.BaseURL
			set.Model = stored.Model
			set.Anonymize = stored.Anonymize
			set.DailyLimit = stored.DailyLimit
		}
	}
	if enc, ok, err := m.db.KVGet(ctx, aiKeyKVKey); err == nil && ok && enc != "" {
		set.HasKey = true
	}
	return set
}

// aiClient собирает клиента с расшифрованным ключом.
func (m *Manager) aiClient(ctx context.Context) (*ai.Client, ai.Settings, error) {
	set := m.AISettings(ctx)
	if !set.Enabled {
		return nil, set, msgs.Errorf("ai.disabled")
	}
	if enc, ok, err := m.db.KVGet(ctx, aiKeyKVKey); err == nil && ok && enc != "" {
		key, err := secretbox.Decrypt(m.key, []byte(enc))
		if err != nil {
			return nil, set, err
		}
		set.APIKey = string(key)
	}
	// Локальной модели ключ не нужен; облачной — нужен, и молчаливый
	// 401 объясняет меньше, чем понятный отказ здесь.
	if set.APIKey == "" && !isLocalURL(set.BaseURL) {
		return nil, set, msgs.Errorf("ai.noKey")
	}
	return ai.New(set), set, nil
}

// isLocalURL — модель на этой же машине или в локальной сети: Ollama,
// vLLM, LM Studio, шлюз внутри периметра.
func isLocalURL(u string) bool {
	low := strings.ToLower(u)
	for _, p := range []string{"http://127.0.0.1", "http://localhost", "http://[::1]", "http://10.", "http://192.168.", "http://172."} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// SetAISettings сохраняет настройки. apiKey: nil — не трогать, "" —
// стереть, иначе — заменить. Смена модели или провайдера чистит кэш:
// прежние ответы принадлежат другой модели.
func (m *Manager) SetAISettings(ctx context.Context, set ai.Settings, apiKey *string) error {
	switch set.Provider {
	case ai.ProviderAnthropic, ai.ProviderOpenAI:
	default:
		return msgs.Errorf("ai.badProvider", set.Provider)
	}
	if !strings.HasPrefix(set.BaseURL, "http://") && !strings.HasPrefix(set.BaseURL, "https://") {
		return msgs.Errorf("ai.badURL")
	}
	prev := m.AISettings(ctx)
	raw, err := json.Marshal(map[string]any{
		"enabled":     set.Enabled,
		"provider":    set.Provider,
		"base_url":    strings.TrimRight(set.BaseURL, "/"),
		"model":       strings.TrimSpace(set.Model),
		"anonymize":   set.Anonymize,
		"daily_limit": set.DailyLimit,
	})
	if err != nil {
		return err
	}
	if err := m.db.KVSet(ctx, aiSettingsKVKey, string(raw)); err != nil {
		return err
	}
	if apiKey != nil {
		if *apiKey == "" {
			if err := m.db.KVSet(ctx, aiKeyKVKey, ""); err != nil {
				return err
			}
		} else {
			enc, err := secretbox.Encrypt(m.key, []byte(*apiKey))
			if err != nil {
				return err
			}
			if err := m.db.KVSet(ctx, aiKeyKVKey, string(enc)); err != nil {
				return err
			}
		}
	}
	if prev.Model != set.Model || prev.Provider != set.Provider {
		_ = m.db.AICacheClear(ctx)
	}
	return nil
}

// AIAnswer — ответ модели для интерфейса.
type AIAnswer struct {
	Answer   string       `json:"answer"`
	Sections []ai.Section `json:"sections"`
	Model    string       `json:"model"`
	Cached   bool         `json:"cached"`
	// Prompt — то, что ушло к модели (после псевдонимизации): оператор
	// вправе видеть, что именно покинуло его сервер.
	Prompt string `json:"prompt"`
	// Notice — приписка «сгенерировано моделью, проверьте команды».
	Notice string `json:"notice"`
}

// AIExplain объясняет одну находку. hostNames — имена, которые надо
// спрятать при псевдонимизации (хосты и машины хаба).
func (m *Manager) AIExplain(ctx context.Context, kind string, fc ai.FindingContext, hostNames []string) (AIAnswer, error) {
	lang := msgs.FromContext(ctx)
	client, set, err := m.aiClient(ctx)
	if err != nil {
		return AIAnswer{}, err
	}

	mapper := ai.NewMapper(set.Anonymize)
	mapper.Learn("host", hostNames)
	user := mapper.Hide(ai.UserPrompt(fc, lang))
	system := ai.SystemFor(kind, lang)

	key := ai.CacheKey(kind, set.Model, string(lang), user)
	if cached, ok, err := m.db.AICacheGet(ctx, key); err == nil && ok {
		return m.aiAnswer(mapper.Reveal(cached), user, set, true), nil
	}

	used, _ := m.db.AIUsageToday(ctx)
	if ai.LimitReached(set, ai.Usage{Requests: used}) {
		return AIAnswer{}, msgs.Errorf("ai.limitReached", set.DailyLimit)
	}

	answer, err := client.Ask(ctx, system, user)
	if err != nil {
		return AIAnswer{}, err
	}
	_ = m.db.AIUsageAdd(ctx)
	_ = m.db.AICachePut(ctx, key, kind, set.Model, string(lang), answer)
	return m.aiAnswer(mapper.Reveal(answer), user, set, false), nil
}

// AIReviewMap разбирает карту ресурсов и сохраняет разбор.
func (m *Manager) AIReviewMap(ctx context.Context, scope string, lines []string, hostNames []string, author string) (AIAnswer, error) {
	lang := msgs.FromContext(ctx)
	client, set, err := m.aiClient(ctx)
	if err != nil {
		return AIAnswer{}, err
	}
	kind := ai.KindMap
	if scope == "hub" {
		kind = ai.KindHubReview
	}
	mapper := ai.NewMapper(set.Anonymize)
	mapper.Learn("host", hostNames)
	user := mapper.Hide(ai.MapPrompt(lines, lang))
	system := ai.SystemFor(kind, lang)

	// Архитектурный разбор не кэшируется по содержимому: карта меняется,
	// и смысл ревизии именно в том, чтобы получить свежий взгляд —
	// зато он сохраняется в историю (ai_reviews).
	used, _ := m.db.AIUsageToday(ctx)
	if ai.LimitReached(set, ai.Usage{Requests: used}) {
		return AIAnswer{}, msgs.Errorf("ai.limitReached", set.DailyLimit)
	}
	answer, err := client.Ask(ctx, system, user)
	if err != nil {
		return AIAnswer{}, err
	}
	_ = m.db.AIUsageAdd(ctx)
	revealed := mapper.Reveal(answer)
	if _, err := m.db.AIReviewAdd(ctx, store.AIReview{
		Scope: scope, Model: set.Model, Lang: string(lang), Answer: revealed, Author: author,
	}); err != nil {
		m.log.Warn("ai review not saved", "scope", scope, "err", err)
	}
	return m.aiAnswer(revealed, user, set, false), nil
}

func (m *Manager) aiAnswer(answer, prompt string, set ai.Settings, cached bool) AIAnswer {
	return AIAnswer{
		Answer:   answer,
		Sections: ai.ParseSections(answer),
		Model:    set.Model,
		Cached:   cached,
		Prompt:   prompt,
		Notice:   fmt.Sprintf(msgs.T(msgs.DefaultLang, "ai.generated"), ai.Describe(set)),
	}
}

// AIStatus — что показать в «О системе».
type AIStatus struct {
	ai.Settings
	UsageToday int `json:"usage_today"`
	CacheSize  int `json:"cache_size"`
}

// AIStatusFor собирает состояние для интерфейса.
func (m *Manager) AIStatusFor(ctx context.Context) AIStatus {
	set := m.AISettings(ctx)
	used, _ := m.db.AIUsageToday(ctx)
	size, _ := m.db.AICacheSize(ctx)
	return AIStatus{Settings: set, UsageToday: used, CacheSize: size}
}
