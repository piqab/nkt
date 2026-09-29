package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListModelsAnthropicPaging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-old","display_name":"Old","created_at":"2025-01-01T00:00:00Z"}],"has_more":true,"last_id":"claude-old"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-new","display_name":"New","created_at":"2026-05-01T00:00:00Z"}],"has_more":false,"last_id":"claude-new"}`))
	}))
	defer srv.Close()
	list, err := New(Settings{Provider: ProviderAnthropic, BaseURL: srv.URL + "/", APIKey: "k"}).ListModels(context.Background())
	if err != nil || len(list) != 2 || list[0].ID != "claude-new" || list[0].Name != "New" || !list[1].Chat {
		t.Fatalf("%+v %v", list, err)
	}
}

func TestListModelsOpenAIAndOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5","created":1760000000},{"id":"text-embedding-3-small","created":1700000000},{"id":"llama3.1:8b","created":0}]}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.1:8b","model":"llama3.1:8b","size":4920753328,"modified_at":"2026-03-01T10:00:00.123456Z"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	list, err := New(Settings{Provider: ProviderOpenAI, BaseURL: srv.URL}).ListModels(context.Background())
	if err != nil || len(list) != 3 {
		t.Fatalf("%+v %v", list, err)
	}
	byID := map[string]ModelInfo{}
	for _, m := range list {
		byID[m.ID] = m
	}
	if IsChatModel("nomic-embed-text") || IsChatModel("bge-reranker-v2") || !IsChatModel("qwen2.5:14b") {
		t.Fatal("chat model heuristics")
	}
	if !byID["gpt-5"].Chat || byID["text-embedding-3-small"].Chat || byID["llama3.1:8b"].Size != 4920753328 || byID["llama3.1:8b"].Created == "" {
		t.Fatalf("%+v", byID)
	}
}

// Сервер без /v1/models (старый Ollama) — список из /api/tags.
func TestListModelsOllamaFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:7b","size":123}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	list, err := New(Settings{Provider: ProviderOpenAI, BaseURL: srv.URL}).ListModels(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != "qwen2.5:7b" || list[0].Size != 123 {
		t.Fatalf("%+v %v", list, err)
	}
	// Ни того, ни другого — ошибка провайдера.
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	if _, err := New(Settings{Provider: ProviderOpenAI, BaseURL: bad.URL}).ListModels(context.Background()); err == nil {
		t.Fatal("no error for a server without a model list")
	}
}

// Адрес с «/v1» на конце (как его дают llama.cpp, LM Studio, OpenRouter)
// не превращается в …/v1/v1/…; 404 называет адрес запроса.
func TestBaseURLWithV1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"File Not Found","type":"not_found_error","code":404}}`))
		}
	}))
	defer srv.Close()
	c := New(Settings{Provider: ProviderOpenAI, BaseURL: srv.URL + "/v1/", Model: "local-model"})
	if list, err := c.ListModels(context.Background()); err != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	if out, err := c.Ask(context.Background(), "s", "u"); err != nil || out != "ok" {
		t.Fatalf("%q %v", out, err)
	}
	if got := NormalizeBaseURL(" https://openrouter.ai/api/v1/ "); got != "https://openrouter.ai/api" {
		t.Fatal(got)
	}
	bad := New(Settings{Provider: ProviderOpenAI, BaseURL: srv.URL + "/nope", Model: "m"})
	if _, err := bad.Ask(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "/nope/v1/chat/completions") {
		t.Fatalf("error must name the URL: %v", err)
	}
}
