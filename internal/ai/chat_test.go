package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Anthropic: вызов инструмента — tool_use, результат — tool_result в
// реплике user; отказ провайдера от инструментов — ToolsRejected с ответом провайдера.
func TestChatAnthropic(t *testing.T) {
	var got map[string]any
	reject := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		if reject {
			http.Error(w, `{"error":"tools"}`, http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"смотрю"},{"type":"tool_use","id":"t2","name":"host_findings","input":{"host_id":3}}]}`)
	}))
	defer srv.Close()
	c := New(Settings{Provider: ProviderAnthropic, BaseURL: srv.URL, Model: "m"})
	history := []ChatMessage{
		{Role: "user", Text: "что с web?"},
		{Role: "assistant", Calls: []ToolCall{{ID: "t1", Name: "list_hosts"}}},
		{Role: "tool", CallID: "t1", Text: `[{"id":3}]`},
	}
	tools := []Tool{{Name: "list_hosts", Description: "d", Schema: map[string]any{"type": "object"}}}
	reply, err := c.Chat(context.Background(), "sys", history, tools)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "смотрю" || len(reply.Calls) != 1 || reply.Calls[0].Name != "host_findings" || string(reply.Calls[0].Args) != `{"host_id":3}` {
		t.Fatalf("reply: %+v", reply)
	}
	raw, _ := json.Marshal(got["messages"])
	s := string(raw)
	if !strings.Contains(s, `{"id":"t1","input":{},"name":"list_hosts","type":"tool_use"}`) || !strings.Contains(s, `"tool_use_id":"t1","type":"tool_result"`) {
		t.Fatalf("messages: %s", s)
	}
	if got["tools"] == nil {
		t.Fatal("no tools sent")
	}
	reject = true
	if _, err := c.Chat(context.Background(), "sys", history, tools); !ToolsRejected(err) || !strings.Contains(err.Error(), `{"error":"tools"}`) {
		t.Fatalf("reject: %v", err)
	}
	if _, err := c.Chat(context.Background(), "sys", history, nil); err == nil || ToolsRejected(err) {
		t.Fatalf("reject without tools: %v", err)
	}
}

// DeepSeek в режиме размышления: reasoning_content ответа с вызовом
// инструмента должен вернуться в той же реплике ассистента на следующем
// ходе — иначе 400 «reasoning_content … must be passed back».
func TestChatOpenAIReasoningPassedBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if m["role"] == "assistant" && m["tool_calls"] != nil && m["reasoning_content"] != "думаю: нужен список хостов" {
				http.Error(w, `{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API."}}`, http.StatusBadRequest)
				return
			}
		}
		if len(body.Messages) == 2 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","reasoning_content":"думаю: нужен список хостов","tool_calls":[{"id":"c1","type":"function","function":{"name":"list_hosts","arguments":"{}"}}]}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"один хост","reasoning_content":"готово"}}]}`)
	}))
	defer srv.Close()
	c := New(Settings{Provider: ProviderOpenAI, BaseURL: srv.URL, Model: "deepseek-reasoner"})
	tools := []Tool{{Name: "list_hosts", Description: "d", Schema: map[string]any{"type": "object"}}}
	history := []ChatMessage{{Role: "user", Text: "сколько хостов?"}}
	first, err := c.Chat(context.Background(), "sys", history, tools)
	if err != nil || len(first.Calls) != 1 || first.Reasoning == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	history = append(history, ChatMessage{Role: "assistant", Text: first.Text, Calls: first.Calls, Reasoning: first.Reasoning},
		ChatMessage{Role: "tool", CallID: "c1", Text: `[{"id":1}]`})
	second, err := c.Chat(context.Background(), "sys", history, tools)
	if err != nil || second.Text != "один хост" {
		t.Fatalf("second: %+v %v", second, err)
	}
}
