package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/ai"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
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

// Ответ остаётся у находки: повторное открытие — без запроса к модели;
// та же находка на другом хосте — «уже разбиралась», свой ответ — по
// force; правленая инструкция уходит модели вместо стандартной.
func TestAIExplainStoredAndSimilar(t *testing.T) {
	ctx := context.Background()
	var calls int
	var lastSystem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				lastSystem = m.Content
			}
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"## Что это значит\nответ"}}]}`)
	}))
	defer srv.Close()
	m, db := newTestManager(t)
	if err := m.SetAISettings(ctx, ai.Settings{Enabled: true, Provider: ai.ProviderOpenAI, BaseURL: srv.URL, Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	h1, _ := m.AddHost(ctx, "web-1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	h2, _ := m.AddHost(ctx, "web-2", "10.0.0.2", 22, "root", store.HostAuthPassword, "pw", false)
	fc := ai.FindingContext{Title: "Порт 6379 открыт наружу", Object: "0.0.0.0:6379"}

	first, err := m.AIExplain(ctx, ai.KindFinding, fc, nil, h1, false)
	if err != nil || calls != 1 || first.StoredAt != "" || first.Similar != nil {
		t.Fatalf("первый запрос: calls=%d, %+v, %v", calls, first, err)
	}
	again, err := m.AIExplain(ctx, ai.KindFinding, fc, nil, h1, false)
	if err != nil || calls != 1 || again.StoredAt == "" {
		t.Fatalf("повтор должен показать сохранённый ответ без запроса: calls=%d, %+v, %v", calls, again, err)
	}
	// Подробности меняются от сканирования к сканированию — ключ на них
	// не смотрит.
	fc2 := fc
	fc2.Detail = "другие подробности"
	if a, _ := m.AIExplain(ctx, ai.KindFinding, fc2, nil, h1, false); calls != 1 || a.StoredAt == "" {
		t.Fatalf("другие подробности не должны менять ключ: calls=%d", calls)
	}
	other, err := m.AIExplain(ctx, ai.KindFinding, fc, nil, h2, false)
	if err != nil || calls != 1 || other.Similar == nil || other.Similar.HostID != h1 || other.Similar.HostName != "web-1" {
		t.Fatalf("на другом хосте — «уже разбиралась» без запроса: calls=%d, %+v, %v", calls, other, err)
	}
	own, err := m.AIExplain(ctx, ai.KindFinding, fc, nil, h2, true)
	if err != nil || calls != 2 || own.Similar != nil || own.StoredAt != "" {
		t.Fatalf("force — свой запрос: calls=%d, %+v, %v", calls, own, err)
	}
	refs, _ := db.AIAnswerRefs(ctx)
	if len(refs) != 2 {
		t.Fatalf("сохранённых ответов %d, ожидалось 2", len(refs))
	}
	if err := m.AIAnswerDelete(ctx, ai.KindFinding, fc.Title, fc.Object, "", h2); err != nil {
		t.Fatal(err)
	}
	if a, _ := m.AIExplain(ctx, ai.KindFinding, fc, nil, h2, false); a.Similar == nil {
		t.Fatalf("после удаления снова «уже разбиралась» на web-1: %+v", a)
	}

	// Правленая инструкция.
	if err := m.SetAIPrompt(ctx, ai.PromptFinding, "ru", "Своя инструкция."); err != nil {
		t.Fatal(err)
	}
	if refs, _ := db.AIAnswerRefs(ctx); len(refs) != 0 {
		t.Fatalf("правка инструкции должна чистить ответы: осталось %d", len(refs))
	}
	if _, err := m.AIExplain(ctx, ai.KindFinding, fc, nil, h1, true); err != nil || !strings.Contains(lastSystem, "Своя инструкция.") {
		t.Fatalf("модель получила %q, %v", lastSystem, err)
	}
	var modified int
	for _, p := range m.AIPrompts(ctx) {
		if p.Modified {
			modified++
			if p.Kind != ai.PromptFinding || p.Lang != "ru" || p.Text != "Своя инструкция." || p.Default == "" {
				t.Errorf("инструкция = %+v", p)
			}
		}
	}
	if modified != 1 {
		t.Errorf("правленых инструкций %d, ожидалась 1", modified)
	}
	if d := m.AIPromptDiff(ctx, ai.PromptFinding, "ru", "Своя инструкция."); !strings.Contains(d, "+Своя инструкция.") {
		t.Errorf("дифф без добавленной строки:\n%s", d)
	}
	if d := m.AIPromptDiff(ctx, ai.PromptFinding, "ru", ai.SystemFor(ai.KindFinding, msgs.RU)); d != "" {
		t.Errorf("стандартный текст даёт непустой дифф:\n%s", d)
	}
	// Возврат к стандартной: текст, равный стандартному, — не override.
	if err := m.SetAIPrompt(ctx, ai.PromptFinding, "ru", ai.SystemFor(ai.KindFinding, msgs.RU)); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.AIPrompts(ctx) {
		if p.Modified {
			t.Errorf("после сброса осталась правка %s/%s", p.Kind, p.Lang)
		}
	}
	if err := m.SetAIPrompt(ctx, "nope", "ru", "x"); err == nil {
		t.Error("неизвестный вид инструкции принят")
	}
}
