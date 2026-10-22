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

// «Проверить инструменты»: вызвала ли модель пробный инструмент — у обоих
// провайдеров; итог запоминается для пары «модель + адрес».
func TestAIToolsTest(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestManager(t)
	mode := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "get_hub_time") {
			http.Error(w, "no tool", http.StatusBadRequest)
			return
		}
		switch mode {
		case "openai-tools":
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"1","type":"function","function":{"name":"get_hub_time","arguments":"{}"}}]}}]}`)
		case "openai-text":
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"It is noon."}}]}`)
		case "openai-reject":
			http.Error(w, `{"error":"tools are not supported"}`, http.StatusBadRequest)
		case "anthropic-tools":
			_, _ = io.WriteString(w, `{"content":[{"type":"tool_use","id":"t","name":"get_hub_time","input":{}}]}`)
		case "anthropic-text":
			_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"noon"}]}`)
		}
	}))
	defer srv.Close()
	key := ""
	for _, c := range []struct {
		mode, provider, want string
	}{
		{"openai-tools", ai.ProviderOpenAI, "tools"},
		{"openai-text", ai.ProviderOpenAI, "no_tools"},
		{"openai-reject", ai.ProviderOpenAI, "no_tools"},
		{"anthropic-tools", ai.ProviderAnthropic, "tools"},
		{"anthropic-text", ai.ProviderAnthropic, "no_tools"},
	} {
		mode = c.mode
		set := ai.Settings{Provider: c.provider, BaseURL: srv.URL + "/v1", Model: "m-" + c.mode}
		res, err := m.AIToolsTest(ctx, set, &key)
		if err != nil || res.Status != c.want {
			t.Errorf("%s: %+v %v", c.mode, res, err)
		}
		if st := m.aiToolsFor(ctx, set); st == nil || st.Status != c.want || st.Origin != "test" {
			t.Errorf("%s: recorded %+v", c.mode, st)
		}
		other := set
		other.Model = "another"
		if m.aiToolsFor(ctx, other) != nil {
			t.Errorf("%s: state leaks to another model", c.mode)
		}
	}
	srv.Close()
	res, err := m.AIToolsTest(ctx, ai.Settings{Provider: ai.ProviderOpenAI, BaseURL: srv.URL, Model: "x", TimeoutS: 2}, &key)
	if err != nil || res.Status != "error" || res.Message == "" {
		t.Errorf("down provider: %+v %v", res, err)
	}
}
