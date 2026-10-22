package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Anthropic: вызов инструмента — tool_use, результат — tool_result в
// реплике user; отказ провайдера от инструментов — ErrToolsUnsupported.
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
	if _, err := c.Chat(context.Background(), "sys", history, tools); !errors.Is(err, ErrToolsUnsupported) {
		t.Fatalf("reject: %v", err)
	}
	if _, err := c.Chat(context.Background(), "sys", history, nil); err == nil || errors.Is(err, ErrToolsUnsupported) {
		t.Fatalf("reject without tools: %v", err)
	}
}
