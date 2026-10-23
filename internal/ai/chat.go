package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Диалог с вызовом инструментов (ассистент хаба): модель просит данные —
// хаб их читает и отдаёт ей результат, пока модель не ответит текстом.
// Инструменты здесь только описываются; что они делают — решает хаб.

// Tool — инструмент для модели: имя, описание и схема входа (JSON Schema).
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"input_schema"`
}

// ToolCall — вызов инструмента моделью.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ChatMessage — реплика диалога. Role: user, assistant (Text и/или
// Calls) или tool (результат вызова CallID).
type ChatMessage struct {
	Role   string     `json:"role"`
	Text   string     `json:"text,omitempty"`
	Calls  []ToolCall `json:"calls,omitempty"`
	CallID string     `json:"call_id,omitempty"`
	// Reasoning — рассуждения модели в режиме размышления (DeepSeek
	// reasoning_content): провайдер требует вернуть их в той же реплике
	// с вызовами на следующих ходах вопроса. Не показываются и не
	// хранятся — живут внутри одного вопроса.
	Reasoning string `json:"-"`
}

// ChatReply — ход модели: текст и (или) вызовы инструментов.
type ChatReply struct {
	Text  string
	Calls []ToolCall
	// Reasoning — reasoning_content ответа (OpenAI-совместимые провайдеры
	// с режимом размышления); вернуть в историю вместе с Calls.
	Reasoning string
}

// ToolsRejected — провайдер отверг запрос с инструментами (обычно
// локальная модель без function calling или сервер, не принимающий
// результаты инструментов): вызывающий переходит на ответ по сводке.
// Ошибка несёт ответ провайдера — его показывают, а не прячут.
func ToolsRejected(err error) bool {
	var me *msgs.Err
	return errors.As(err, &me) && me.Key == "ai.toolsRejected"
}

// Chat — один ход диалога с инструментами.
func (c *Client) Chat(ctx context.Context, system string, history []ChatMessage, tools []Tool) (ChatReply, error) {
	var (
		reply ChatReply
		err   error
	)
	switch c.set.Provider {
	case ProviderOpenAI:
		reply, err = c.chatOpenAI(ctx, system, history, tools)
	default:
		reply, err = c.chatAnthropic(ctx, system, history, tools)
	}
	var me *msgs.Err
	if err != nil && len(tools) > 0 && errors.As(err, &me) && me.Key == "ai.providerCodeAt" && len(me.Args) > 0 {
		if code, _ := me.Args[0].(int); code == http.StatusBadRequest || code == http.StatusUnprocessableEntity || code == http.StatusNotImplemented {
			return reply, msgs.Errorf("ai.toolsRejected", err)
		}
	}
	return reply, err
}

func (c *Client) chatAnthropic(ctx context.Context, system string, history []ChatMessage, tools []Tool) (ChatReply, error) {
	var messages []map[string]any
	add := func(role string, block map[string]any) {
		if n := len(messages); n > 0 && messages[n-1]["role"] == role {
			messages[n-1]["content"] = append(messages[n-1]["content"].([]map[string]any), block)
			return
		}
		messages = append(messages, map[string]any{"role": role, "content": []map[string]any{block}})
	}
	for _, m := range history {
		switch m.Role {
		case "assistant":
			if m.Text != "" {
				add("assistant", map[string]any{"type": "text", "text": m.Text})
			}
			for _, call := range m.Calls {
				args := call.Args
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				add("assistant", map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": args})
			}
		case "tool":
			add("user", map[string]any{"type": "tool_result", "tool_use_id": m.CallID, "content": m.Text})
		default:
			add("user", map[string]any{"type": "text", "text": m.Text})
		}
	}
	payload := map[string]any{"model": c.set.Model, "max_tokens": 4000, "system": system, "messages": messages}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ChatReply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if c.set.APIKey != "" {
		req.Header.Set("x-api-key", c.set.APIKey)
	}
	raw, err := c.do(req)
	if err != nil {
		return ChatReply{}, err
	}
	var out struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ChatReply{}, msgs.Errorf("ai.badResponse", err)
	}
	var reply ChatReply
	var b strings.Builder
	for _, part := range out.Content {
		switch part.Type {
		case "text":
			b.WriteString(part.Text)
		case "tool_use":
			reply.Calls = append(reply.Calls, ToolCall{ID: part.ID, Name: part.Name, Args: part.Input})
		}
	}
	reply.Text = b.String()
	if reply.Text == "" && len(reply.Calls) == 0 {
		return reply, msgs.Errorf("ai.emptyResponse")
	}
	return reply, nil
}

func (c *Client) chatOpenAI(ctx context.Context, system string, history []ChatMessage, tools []Tool) (ChatReply, error) {
	messages := []map[string]any{{"role": "system", "content": system}}
	for _, m := range history {
		switch m.Role {
		case "assistant":
			msg := map[string]any{"role": "assistant", "content": m.Text}
			if m.Reasoning != "" {
				msg["reasoning_content"] = m.Reasoning
			}
			if len(m.Calls) > 0 {
				var calls []map[string]any
				for _, call := range m.Calls {
					args := string(call.Args)
					if args == "" {
						args = "{}"
					}
					calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": args}})
				}
				msg["tool_calls"] = calls
			}
			messages = append(messages, msg)
		case "tool":
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": m.CallID, "content": m.Text})
		default:
			messages = append(messages, map[string]any{"role": "user", "content": m.Text})
		}
	}
	payload := map[string]any{"model": c.set.Model, "messages": messages, "stream": false}
	if len(tools) > 0 {
		var fns []map[string]any
		for _, t := range tools {
			fns = append(fns, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		payload["tools"] = fns
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatReply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.set.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.set.APIKey)
	}
	raw, err := c.do(req)
	if err != nil {
		return ChatReply{}, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ChatReply{}, msgs.Errorf("ai.badResponse", err)
	}
	if len(out.Choices) == 0 {
		return ChatReply{}, msgs.Errorf("ai.emptyResponse")
	}
	m := out.Choices[0].Message
	reply := ChatReply{Text: m.Content, Reasoning: m.ReasoningContent}
	for _, tc := range m.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		reply.Calls = append(reply.Calls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: args})
	}
	if strings.TrimSpace(reply.Text) == "" && len(reply.Calls) == 0 {
		return reply, msgs.Errorf("ai.emptyResponse")
	}
	return reply, nil
}
