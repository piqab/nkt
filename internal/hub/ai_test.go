package hub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/ai"
)

// Проверка настроек — живой запрос теми значениями, что сейчас в форме,
// а не сохранёнными: смысл кнопки в том, чтобы убедиться до сохранения.
func TestAITest(t *testing.T) {
	ctx := context.Background()

	t.Run("модель отвечает", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer k" {
				t.Errorf("ключ из формы не дошёл: %q", got)
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
		}))
		defer srv.Close()
		m, _ := newTestManager(t)
		key := "k"
		res, err := m.AITest(ctx, ai.Settings{Provider: ai.ProviderOpenAI, BaseURL: srv.URL, Model: "llama"}, &key)
		if err != nil {
			t.Fatalf("AITest: %v", err)
		}
		if !res.OK || res.Reply != "ok" || res.Model != "llama" {
			t.Fatalf("итог = %+v", res)
		}
	})

	t.Run("неверный ключ — не ошибка запроса, а отрицательный итог", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
		}))
		defer srv.Close()
		m, _ := newTestManager(t)
		key := "bad"
		res, err := m.AITest(ctx, ai.Settings{Provider: ai.ProviderAnthropic, BaseURL: srv.URL, Model: "claude"}, &key)
		// Интерфейсу нужен текст причины, а не 500 без подробностей.
		if err != nil {
			t.Fatalf("AITest вернул ошибку вместо итога: %v", err)
		}
		if res.OK || !strings.Contains(res.Message, "invalid api key") {
			t.Fatalf("итог = %+v", res)
		}
	})

	t.Run("адрес недоступен", func(t *testing.T) {
		m, _ := newTestManager(t)
		key := "k"
		// Порт 1 закрыт всегда — «нет связи» без ожидания таймаута.
		res, err := m.AITest(ctx, ai.Settings{Provider: ai.ProviderOpenAI, BaseURL: "http://127.0.0.1:1", Model: "m"}, &key)
		if err != nil {
			t.Fatalf("AITest: %v", err)
		}
		if res.OK || res.Message == "" {
			t.Fatalf("итог = %+v", res)
		}
	})

	t.Run("облачная модель без ключа", func(t *testing.T) {
		m, _ := newTestManager(t)
		empty := ""
		if _, err := m.AITest(ctx, ai.Settings{Provider: ai.ProviderAnthropic, BaseURL: "https://api.anthropic.com", Model: "claude"}, &empty); err == nil {
			t.Fatal("ожидали отказ «нет ключа»")
		}
	})

	t.Run("сохранённый ключ подставляется", func(t *testing.T) {
		var seen string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("x-api-key")
			_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}]}`)
		}))
		defer srv.Close()
		m, _ := newTestManager(t)
		stored := "saved-key"
		if err := m.SetAISettings(ctx, ai.Settings{
			Enabled: true, Provider: ai.ProviderAnthropic, BaseURL: srv.URL, Model: "claude",
		}, &stored); err != nil {
			t.Fatalf("SetAISettings: %v", err)
		}
		// Пустой ключ в форме — «оставить прежний», набирать заново не нужно.
		if _, err := m.AITest(ctx, ai.Settings{Provider: ai.ProviderAnthropic, BaseURL: srv.URL, Model: "claude"}, nil); err != nil {
			t.Fatalf("AITest: %v", err)
		}
		if seen != stored {
			t.Fatalf("ушёл ключ %q", seen)
		}
	})

	t.Run("лимит проверке не мешает", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
		}))
		defer srv.Close()
		m, db := newTestManager(t)
		_ = db.AIUsageAdd(ctx)
		key := "k"
		res, err := m.AITest(ctx, ai.Settings{Provider: ai.ProviderOpenAI, BaseURL: srv.URL, Model: "m", DailyLimit: 1}, &key)
		if err != nil || !res.OK {
			t.Fatalf("исчерпанный лимит не должен мешать проверке: %+v, %v", res, err)
		}
	})
}
