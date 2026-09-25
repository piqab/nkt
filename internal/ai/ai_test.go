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

// Секреты вырезаются всегда — и с выключенной псевдонимизацией: пароль
// из конфигурации или токен из вывода команды модели не нужны.
func TestRedactSecrets(t *testing.T) {
	in := strings.Join([]string{
		"password = \"hunter2\"",
		"DB_PASSWORD: s3cr3t",
		"api_key=sk-abcdefghijklmnopqrstuvwxyz0123",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijklmnop",
		"url: postgres://app:pa55@db.internal:5432/app",
		"ssl_password_file /etc/nginx/pw.txt",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----",
		"AKIAIOSFODNN7EXAMPLE",
	}, "\n")
	out := NewMapper(false).Hide(in)
	for _, secret := range []string{"hunter2", "s3cr3t", "sk-abcdefghijklmnopqrstuvwxyz0123", "eyJhbGciOiJIUzI1NiJ9", "pa55@", "MIIBOgIBAAJBAK", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(out, secret) {
			t.Errorf("секрет %q ушёл в запрос:\n%s", secret, out)
		}
	}
	// Имена ключей и путь к файлу пароля — не секреты, они и есть предмет.
	for _, keep := range []string{"password = \"<secret>", "DB_PASSWORD: <secret>", "ssl_password_file /etc/nginx/pw.txt", "postgres://app:<secret>@", "<private-key>"} {
		if !strings.Contains(out, keep) {
			t.Errorf("ожидали %q в:\n%s", keep, out)
		}
	}
}

// Ошибка правки конфигурации: в запросе — вывод проверки, дифф и своя
// постановка задачи.
func TestUserPromptConfigError(t *testing.T) {
	p := UserPrompt(FindingContext{
		Kind: KindConfigError, Title: "nginx: [emerg] unknown directive", File: "/etc/nginx/nginx.conf",
		Output: "nginx: [emerg] unknown directive \"servr\" in /etc/nginx/nginx.conf:12",
		Diff:   "--- a\n+++ b\n@@ -12 +12 @@\n-server {\n+servr {",
	}, msgs.RU)
	for _, want := range []string{"Вывод проверки", "```diff", "+servr {", "Задача: правка файла"} {
		if !strings.Contains(p, want) {
			t.Errorf("нет %q в промпте:\n%s", want, p)
		}
	}
}

// Помощь по конфигурации: текст файла в блоке, обрезка длинного, вопрос
// оператора; хэши паролей и ключи WireGuard/kubeconfig вырезаются.
func TestConfigPromptAndSecrets(t *testing.T) {
	long := strings.Repeat("server_tokens off;\n", MaxConfigContent/10)
	p := UserPrompt(FindingContext{Kind: KindConfig, Title: "/etc/nginx/nginx.conf", Service: "nginx", Content: long, Question: "reverse proxy на :3000"}, msgs.RU)
	for _, want := range []string{"Конфигурация:", "```", "файл обрезан", "Вопрос оператора: reverse proxy на :3000"} {
		if !strings.Contains(p, want) {
			t.Errorf("нет %q в промпте", want)
		}
	}
	if len(p) > MaxConfigContent+2000 {
		t.Errorf("промпт не обрезан: %d байт", len(p))
	}
	if !strings.Contains(SystemFor(KindConfig, msgs.EN), "What is configured") {
		t.Error("инструкция config не выбрана")
	}
	in := strings.Join([]string{
		"PrivateKey = wG5m2j8c9Xk3LpQ7rT1vB4nH6sD8fJ0aZ2yC5uE9iK4=",
		"PresharedKey = abc123def456ghi789",
		"psk=\"WiFiPass123\"",
		"client-key-data: LS0tLS1CRUdJTiBQUklWQVRF",
		"admin:$apr1$Xy9kQ2Lm$0uPz1Vb6rT3eW8nH4jK5c/",
		"root:$6$rounds=5000$saltsalt$hashhashhash:19000:0:99999:7:::",
		"ssl_certificate /etc/ssl/site.pem;",
	}, "\n")
	out := RedactSecrets(in)
	for _, secret := range []string{"wG5m2j8c9Xk3", "abc123def456", "WiFiPass123", "LS0tLS1CRUdJTiBQUklWQVRF", "$apr1$Xy9kQ2Lm", "$6$rounds"} {
		if strings.Contains(out, secret) {
			t.Errorf("секрет %q ушёл в запрос:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "ssl_certificate /etc/ssl/site.pem") || !strings.Contains(out, "admin:<hash>") {
		t.Errorf("лишнее вырезано или хэш не помечен:\n%s", out)
	}
}
