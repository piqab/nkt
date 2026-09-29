package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/ai"
	"github.com/piqab/nkt/internal/control"
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
			TimeoutS   int    `json:"timeout_s"`
		}
		if json.Unmarshal([]byte(raw), &stored) == nil {
			set.Enabled = stored.Enabled
			set.Provider = stored.Provider
			set.BaseURL = stored.BaseURL
			set.Model = stored.Model
			set.Anonymize = stored.Anonymize
			set.DailyLimit = stored.DailyLimit
			set.TimeoutS = normalizeAITimeout(stored.TimeoutS)
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

// Границы времени ожидания: меньше десяти секунд не успеет ответить и
// облако, больше получаса — уже не «подумать», а зависнуть.
const (
	aiTimeoutMinS = 10
	aiTimeoutMaxS = 1800
)

// normalizeAITimeout — время ожидания в допустимых границах; 0 или
// мусор — значение по умолчанию.
func normalizeAITimeout(s int) int {
	if s <= 0 {
		return ai.DefaultTimeoutS
	}
	if s < aiTimeoutMinS {
		return aiTimeoutMinS
	}
	if s > aiTimeoutMaxS {
		return aiTimeoutMaxS
	}
	return s
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
		"timeout_s":   normalizeAITimeout(set.TimeoutS),
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

// AISimilar — та же находка уже разбиралась на другом хосте.
type AISimilar struct {
	HostID    int64  `json:"host_id"`
	HostName  string `json:"host_name"`
	CreatedAt string `json:"created_at"`
}

// AIAnswer — ответ модели для интерфейса.
type AIAnswer struct {
	Answer   string       `json:"answer"`
	Sections []ai.Section `json:"sections"`
	Model    string       `json:"model"`
	Cached   bool         `json:"cached"`
	// StoredAt — ответ сохранён раньше (для этой находки на этом хосте)
	// и показан без нового запроса; пусто — ответ только что получен.
	StoredAt string `json:"stored_at,omitempty"`
	// Similar — ответ принадлежит той же находке на другом хосте: своего
	// ещё нет, запрос делается отдельной кнопкой.
	Similar *AISimilar `json:"similar,omitempty"`
	// Prompt — то, что ушло к модели (после псевдонимизации): оператор
	// вправе видеть, что именно покинуло его сервер.
	Prompt string `json:"prompt"`
	// Notice — приписка «сгенерировано моделью, проверьте команды».
	Notice string `json:"notice"`
	// Request — запрос целиком, как ушёл модели; RequestMissing — ответ
	// сохранён до того, как запрос стали хранить (есть только Prompt).
	Request        *AIRequest `json:"request,omitempty"`
	RequestMissing bool       `json:"request_missing,omitempty"`
}

// AIRequest — запрос к модели целиком: кому, чем, что ушло.
type AIRequest struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	BaseURL   string `json:"base_url"`
	TimeoutS  int    `json:"timeout_s"`
	Anonymize bool   `json:"anonymize"`
	// System — инструкция; SystemModified — правленая оператором.
	System         string `json:"system"`
	SystemModified bool   `json:"system_modified"`
	// User — сообщение, как ушло (псевдонимы, секреты вырезаны).
	User string `json:"user"`
	// Aliases — что на что заменено; наружу — только администратору.
	Aliases []ai.Alias `json:"aliases,omitempty"`
}

// newAIRequest — запрос для показа и хранения.
func (m *Manager) newAIRequest(ctx context.Context, set ai.Settings, kind, system, user string, mapper *ai.Mapper) *AIRequest {
	lang := msgs.FromContext(ctx)
	_, modified := m.aiPromptOverrides(ctx)[ai.PromptKey(ai.PromptKindFor(kind), lang)]
	return &AIRequest{
		Provider: set.Provider, Model: set.Model, BaseURL: ai.NormalizeBaseURL(set.BaseURL), TimeoutS: set.TimeoutS,
		Anonymize: set.Anonymize, System: system, SystemModified: modified, User: user, Aliases: mapper.Aliases(user),
	}
}

func encodeAIRequest(r *AIRequest) string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

// storedAnswer — сохранённый ответ с его запросом.
func (m *Manager) storedAnswer(ctx context.Context, a store.AIAnswer) AIAnswer {
	out := m.aiAnswer(ctx, a.Answer, a.Model, a.Prompt, nil)
	var req AIRequest
	if a.Request != "" && json.Unmarshal([]byte(a.Request), &req) == nil {
		out.Request = &req
	} else {
		out.RequestMissing = true
	}
	return out
}

// AIExplain объясняет одну находку. hostNames — имена, которые надо
// спрятать при псевдонимизации (хосты и машины хаба). hostID — чья
// находка (0 — сам хаб).
//
// Ответ остаётся у находки: повторное открытие показывает сохранённый
// без запроса к модели. Та же находка на другом хосте показывается как
// «уже разбиралась» — тоже без запроса; свой ответ для этого хоста
// запрашивается отдельно (force), как и «спросить заново».
func (m *Manager) AIExplain(ctx context.Context, kind string, fc ai.FindingContext, hostNames []string, hostID int64, force bool) (AIAnswer, error) {
	lang := msgs.FromContext(ctx)
	client, set, err := m.aiClient(ctx)
	if err != nil {
		return AIAnswer{}, err
	}
	object := fc.Object
	if kind == ai.KindConfig && strings.TrimSpace(fc.Question) != "" {
		// Разные вопросы про одну программу — разные ответы.
		object += "?" + strings.TrimSpace(fc.Question)
	}
	key := ai.FindingKey(kind, fc.Title, object, fc.File)
	if !force {
		if own, ok, err := m.db.AIAnswerGet(ctx, key, hostID); err == nil && ok {
			out := m.storedAnswer(ctx, own)
			out.StoredAt = own.CreatedAt
			return out, nil
		}
		if other, ok, err := m.db.AIAnswerOther(ctx, key, hostID); err == nil && ok {
			out := m.storedAnswer(ctx, other)
			out.Similar = &AISimilar{HostID: other.HostID, HostName: m.aiHostName(ctx, other.HostID), CreatedAt: other.CreatedAt}
			return out, nil
		}
	}

	mapper := ai.NewMapper(set.Anonymize)
	if kind == ai.KindIP {
		// Проверяемый адрес чужой — он и есть предмет разбора.
		mapper.Keep(fc.Object)
	}
	mapper.Learn("host", hostNames)
	user := mapper.Hide(ai.UserPrompt(fc, lang))
	system := ai.SystemWith(m.aiPromptOverrides(ctx), kind, lang)

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
	req := m.newAIRequest(ctx, set, kind, system, user, mapper)
	if err := m.db.AIAnswerPut(ctx, store.AIAnswer{
		Key: key, HostID: hostID, Kind: kind, Title: fc.Title, Object: object, File: fc.File,
		Model: set.Model, Lang: string(lang), Prompt: user, Answer: revealed, Request: encodeAIRequest(req),
	}); err != nil {
		m.log.Warn("ai answer not saved", "err", err)
	}
	return m.aiAnswer(ctx, revealed, set.Model, user, req), nil
}

// aiHostName — имя хоста для пометки «уже разбиралась»; 0 — сам хаб.
func (m *Manager) aiHostName(ctx context.Context, hostID int64) string {
	if hostID == 0 {
		return "hub"
	}
	if h, err := m.db.HostByID(ctx, hostID); err == nil {
		return h.Name
	}
	return fmt.Sprintf("#%d", hostID)
}

// AIAnswerDelete убирает сохранённый ответ у находки.
func (m *Manager) AIAnswerDelete(ctx context.Context, kind, title, object, file string, hostID int64) error {
	return m.db.AIAnswerDelete(ctx, ai.FindingKey(kind, title, object, file), hostID)
}

// aiPromptKVKey — ключ правленой инструкции в базе: ai.prompt.finding/ru.
func aiPromptKVKey(promptKey string) string { return "ai.prompt." + promptKey }

// aiPromptOverrides — правленые оператором инструкции по ключу
// ai.PromptKey; отсутствующие — стандартные.
func (m *Manager) aiPromptOverrides(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, pk := range ai.PromptKinds {
		for _, lang := range []msgs.Lang{msgs.RU, msgs.EN} {
			key := ai.PromptKey(pk, lang)
			if raw, ok, err := m.db.KVGet(ctx, aiPromptKVKey(key)); err == nil && ok && strings.TrimSpace(raw) != "" {
				out[key] = raw
			}
		}
	}
	return out
}

// AIPromptInfo — инструкция для настроек: текущий текст, стандартный и
// признак правки.
type AIPromptInfo struct {
	Kind     string `json:"kind"`
	Lang     string `json:"lang"`
	Text     string `json:"text"`
	Default  string `json:"default"`
	Modified bool   `json:"modified"`
}

// AIPrompts — все редактируемые инструкции.
func (m *Manager) AIPrompts(ctx context.Context) []AIPromptInfo {
	over := m.aiPromptOverrides(ctx)
	var out []AIPromptInfo
	for _, pk := range ai.PromptKinds {
		for _, lang := range []msgs.Lang{msgs.RU, msgs.EN} {
			key := ai.PromptKey(pk, lang)
			def := ai.SystemFor(pk, lang)
			text, modified := over[key]
			if !modified {
				text = def
			}
			out = append(out, AIPromptInfo{Kind: pk, Lang: string(lang), Text: text, Default: def, Modified: modified})
		}
	}
	return out
}

// aiPromptLang — язык из запроса настроек; всё, что не en, — ru.
func aiPromptLang(lang string) msgs.Lang {
	if lang == "en" {
		return msgs.EN
	}
	return msgs.RU
}

// SetAIPrompt сохраняет правленую инструкцию; пустой текст или текст,
// равный стандартному, — возврат к стандартной. Сохранённые ответы
// чистятся: они получены другой инструкцией.
func (m *Manager) SetAIPrompt(ctx context.Context, promptKind, lang, text string) error {
	if !slices.Contains(ai.PromptKinds, promptKind) {
		return msgs.Errorf("ai.badPromptKind", promptKind)
	}
	l := aiPromptLang(lang)
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == strings.TrimSpace(ai.SystemFor(promptKind, l)) {
		text = ""
	}
	if err := m.db.KVSet(ctx, aiPromptKVKey(ai.PromptKey(promptKind, l)), text); err != nil {
		return err
	}
	return m.db.AICacheClear(ctx)
}

// AIPromptDiff — отличия текста от стандартной инструкции, unified diff.
func (m *Manager) AIPromptDiff(ctx context.Context, promptKind, lang, text string) string {
	l := aiPromptLang(lang)
	def := ai.SystemFor(promptKind, l)
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == strings.TrimSpace(def) {
		return ""
	}
	return control.UnifiedDiff(ctx, msgs.Tc(ctx, "ai.promptDefault"), msgs.Tc(ctx, "ai.promptYours"), def+"\n", text+"\n")
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
	system := ai.SystemWith(m.aiPromptOverrides(ctx), kind, lang)

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
	req := m.newAIRequest(ctx, set, kind, system, user, mapper)
	if _, err := m.db.AIReviewAdd(ctx, store.AIReview{
		Scope: scope, Model: set.Model, Lang: string(lang), Answer: revealed, Author: author, Request: encodeAIRequest(req),
	}); err != nil {
		m.log.Warn("ai review not saved", "scope", scope, "err", err)
	}
	return m.aiAnswer(ctx, revealed, set.Model, user, req), nil
}

// aiAnswer — ответ для интерфейса. model — модель, которая его дала (у
// сохранённого — своя, а не текущая из настроек).
func (m *Manager) aiAnswer(ctx context.Context, answer, model, prompt string, req *AIRequest) AIAnswer {
	sections := ai.ParseSections(answer)
	if sections == nil {
		// Пустой список, а не null: интерфейс перебирает разделы.
		sections = []ai.Section{}
	}
	return AIAnswer{
		Answer:   answer,
		Sections: sections,
		Model:    model,
		Prompt:   prompt,
		// Только имя модели: провайдер и адрес — в «показать запрос».
		Notice:  msgs.Tc(ctx, "ai.generated", model),
		Request: req,
	}
}

// AITestResult — итог проверки настроек.
type AITestResult struct {
	OK      bool   `json:"ok"`
	Model   string `json:"model"`
	TookMS  int64  `json:"took_ms"`
	Reply   string `json:"reply"`
	Message string `json:"message,omitempty"`
}

// AITest проверяет настройки живым запросом к модели.
//
// Проверяются именно переданные настройки, а не сохранённые: смысл
// кнопки в том, чтобы убедиться до сохранения. Пустой ключ означает
// «взять сохранённый» — заново набирать его ради проверки не нужно.
//
// Суточный лимит здесь не действует: невозможность проверить настройку
// в конце дня — худшее, чем один лишний запрос.
func (m *Manager) AITest(ctx context.Context, set ai.Settings, apiKey *string) (AITestResult, error) {
	if strings.TrimSpace(set.Model) == "" {
		return AITestResult{}, msgs.Errorf("ai.noModel")
	}
	set, err := m.aiFormSettings(ctx, set, apiKey)
	if err != nil {
		return AITestResult{}, err
	}
	// Короткий вопрос без контекста: проверяется доступность и ключ, а не
	// качество ответа, и платить за длинный разбор здесь незачем.
	started := time.Now()
	answer, err := ai.New(set).Ask(ctx, msgs.T(msgs.FromContext(ctx), "ai.testSystem"), msgs.T(msgs.FromContext(ctx), "ai.testUser"))
	took := time.Since(started).Milliseconds()
	_ = m.db.AIUsageAdd(ctx)
	if err != nil {
		return AITestResult{OK: false, Model: set.Model, TookMS: took, Message: msgs.Localize(msgs.FromContext(ctx), err)}, nil
	}
	reply := strings.TrimSpace(answer)
	if len(reply) > 200 {
		reply = reply[:200] + "…"
	}
	return AITestResult{OK: true, Model: set.Model, TookMS: took, Reply: reply}, nil
}

// aiFormSettings — настройки из формы, готовые к запросу: провайдер
// проверен, пустой ключ — сохранённый (заново набирать его ради проверки
// не нужно).
func (m *Manager) aiFormSettings(ctx context.Context, set ai.Settings, apiKey *string) (ai.Settings, error) {
	if !strings.HasPrefix(set.BaseURL, "http://") && !strings.HasPrefix(set.BaseURL, "https://") {
		return set, msgs.Errorf("ai.badURL")
	}
	switch set.Provider {
	case ai.ProviderAnthropic, ai.ProviderOpenAI:
	default:
		return set, msgs.Errorf("ai.badProvider", set.Provider)
	}
	set.TimeoutS = normalizeAITimeout(set.TimeoutS)
	if apiKey != nil && *apiKey != "" {
		set.APIKey = *apiKey
	} else if enc, ok, err := m.db.KVGet(ctx, aiKeyKVKey); err == nil && ok && enc != "" {
		key, err := secretbox.Decrypt(m.key, []byte(enc))
		if err != nil {
			return set, err
		}
		set.APIKey = string(key)
	}
	if set.APIKey == "" && !isLocalURL(set.BaseURL) {
		return set, msgs.Errorf("ai.noKey")
	}
	return set, nil
}

// AIModels — модели провайдера с настройками из формы (для выбора
// вместо ручного ввода). Суточный лимит не тратит: список бесплатный.
func (m *Manager) AIModels(ctx context.Context, set ai.Settings, apiKey *string) ([]ai.ModelInfo, error) {
	set, err := m.aiFormSettings(ctx, set, apiKey)
	if err != nil {
		return nil, err
	}
	// Список — быстрый запрос: минута с запасом и для медленного
	// локального сервера.
	if set.TimeoutS > 60 {
		set.TimeoutS = 60
	}
	list, err := ai.New(set).ListModels(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []ai.ModelInfo{}
	}
	return list, nil
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
