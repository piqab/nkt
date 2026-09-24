package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
)

// Оба провайдера отвечают текстом, и клиент достаёт его из своего
// формата: Anthropic кладёт в content[].text, OpenAI — в
// choices[0].message.content.
func TestAskBothProviders(t *testing.T) {
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("путь запроса = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "k1" {
			t.Errorf("ключ = %q", got)
		}
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"## Что это значит\nвот так"}]}`)
	}))
	defer anthropic.Close()

	openai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("путь запроса = %q", r.URL.Path)
		}
		// Локальной модели ключ не нужен — заголовка быть не должно.
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("лишний ключ: %q", got)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"## What this means\nlike this"}}]}`)
	}))
	defer openai.Close()

	c := New(Settings{Provider: ProviderAnthropic, BaseURL: anthropic.URL, Model: "m", APIKey: "k1"})
	got, err := c.Ask(context.Background(), "sys", "user")
	if err != nil || !strings.Contains(got, "вот так") {
		t.Fatalf("anthropic: %q, %v", got, err)
	}

	c = New(Settings{Provider: ProviderOpenAI, BaseURL: openai.URL, Model: "llama"})
	got, err = c.Ask(context.Background(), "sys", "user")
	if err != nil || !strings.Contains(got, "like this") {
		t.Fatalf("openai: %q, %v", got, err)
	}
}

// Ошибка провайдера доходит до пользователя с кодом и первой строкой
// тела — «не удалось» без подробностей чинить нечем.
func TestAskProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()
	c := New(Settings{Provider: ProviderOpenAI, BaseURL: srv.URL, Model: "m"})
	_, err := c.Ask(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("ошибка = %v", err)
	}
}

// Псевдонимизация: к модели не уходят ни имена хостов, ни адреса, ни
// домены, а в ответе они возвращаются на место.
func TestAnonymizeRoundTrip(t *testing.T) {
	m := NewMapper(true)
	m.Learn("host", []string{"db-prod-1", "web-1"})
	text := "На db-prod-1 порт 5432 открыт для 203.0.113.9, сертификат api.example.com, письма на ops@example.com"
	hidden := m.Hide(text)
	for _, secret := range []string{"db-prod-1", "203.0.113.9", "api.example.com", "ops@example.com"} {
		if strings.Contains(hidden, secret) {
			t.Errorf("в запрос попало %q: %s", secret, hidden)
		}
	}
	if !strings.Contains(hidden, "host-") || !strings.Contains(hidden, "ip-") {
		t.Errorf("нет псевдонимов: %s", hidden)
	}
	// Модель отвечает псевдонимами — оператор должен увидеть своё.
	back := m.Reveal(hidden)
	if back != text {
		t.Errorf("обратная замена дала %q", back)
	}
	// Имена файлов и юнитов — не домены: они и есть предмет разговора.
	keep := NewMapper(true).Hide("проверьте nginx.conf и netknownsthat.service")
	if !strings.Contains(keep, "nginx.conf") || !strings.Contains(keep, "netknownsthat.service") {
		t.Errorf("спрятали имена файлов: %s", keep)
	}
	// Выключенная анонимизация ничего не трогает.
	if plain := NewMapper(false).Hide(text); plain != text {
		t.Errorf("выключенный маппер изменил текст: %s", plain)
	}
}

// Ключ кэша зависит от модели, языка и содержимого: один и тот же
// вопрос к другой модели — другой ответ.
func TestCacheKeyDiffers(t *testing.T) {
	a := CacheKey(KindFinding, "m1", "ru", "payload")
	if a == CacheKey(KindFinding, "m2", "ru", "payload") ||
		a == CacheKey(KindFinding, "m1", "en", "payload") ||
		a == CacheKey(KindVuln, "m1", "ru", "payload") ||
		a == CacheKey(KindFinding, "m1", "ru", "other") {
		t.Error("ключ кэша не различает вид, модель, язык или содержимое")
	}
	if a != CacheKey(KindFinding, "m1", "ru", "payload") {
		t.Error("ключ кэша неустойчив")
	}
}

// Ответ разбирается на разделы, даже если модель начала с абзаца без
// заголовка.
func TestParseSections(t *testing.T) {
	secs := ParseSections("вступление\n## Что это значит\nтекст 1\n## Что сделать\n1. шаг\n")
	if len(secs) != 3 || secs[0].Title != "" || secs[1].Title != "Что это значит" || !strings.Contains(secs[2].Body, "1. шаг") {
		t.Fatalf("разделы = %+v", secs)
	}
}

// Промпт не содержит пустых полей: «Файл: » сбивает модель с толку.
func TestUserPromptSkipsEmpty(t *testing.T) {
	p := UserPrompt(FindingContext{Title: "Порт 6379 открыт наружу", Severity: "critical"}, msgs.RU)
	if strings.Contains(p, "Файл") || strings.Contains(p, "Объект") {
		t.Errorf("пустые поля в промпте:\n%s", p)
	}
	if !strings.Contains(p, "Порт 6379") || !strings.Contains(p, "critical") {
		t.Errorf("промпт без сути:\n%s", p)
	}
	// Язык ответа задаётся инструкцией, а не догадкой модели.
	if !strings.Contains(SystemFor(KindFinding, msgs.EN), "Answer in English") {
		t.Error("английская инструкция не просит английский ответ")
	}
}

// Лимит считается по числу запросов за сутки; 0 — без лимита.
func TestLimitReached(t *testing.T) {
	if !LimitReached(Settings{DailyLimit: 2}, Usage{Requests: 2}) {
		t.Error("лимит не сработал")
	}
	if LimitReached(Settings{DailyLimit: 0}, Usage{Requests: 1000}) {
		t.Error("нулевой лимит должен значить «без лимита»")
	}
}

// Тело запроса к Anthropic — то, что ожидает их API: system отдельным
// полем, сообщение пользователя в messages.
func TestAnthropicRequestShape(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()
	c := New(Settings{Provider: ProviderAnthropic, BaseURL: srv.URL, Model: "claude", APIKey: "k"})
	if _, err := c.Ask(context.Background(), "инструкция", "вопрос"); err != nil {
		t.Fatal(err)
	}
	if body["system"] != "инструкция" || body["model"] != "claude" {
		t.Fatalf("тело запроса = %+v", body)
	}
	msgsList, _ := body["messages"].([]any)
	if len(msgsList) != 1 {
		t.Fatalf("сообщений = %d", len(msgsList))
	}
}
