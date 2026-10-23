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
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// Ассистент: модель читает данные инструментами (от имени пользователя,
// адреса скрыты), ответ со сценарием проходит «Проверить»; модель без
// инструментов отвечает по сводке.
func TestAssistant(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	_, _ = db.CreateHost(ctx, "web1", "203.0.113.10", 22, "root", store.HostAuthPassword, []byte("x"))

	type msg struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
	}
	var bodies [][]msg
	var withTools []bool
	toolsUnsupported := false
	resultRejected, summaryFails := false, false
	step := 0
	prov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []msg `json:"messages"`
			Tools    []any `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body.Messages)
		withTools = append(withTools, len(body.Tools) > 0)
		hasToolMsg := false
		for _, m := range body.Messages {
			hasToolMsg = hasToolMsg || m.Role == "tool"
		}
		if resultRejected {
			switch {
			case hasToolMsg:
				http.Error(w, `{"error":"unexpected role tool"}`, http.StatusBadRequest)
			case len(body.Tools) > 0:
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"list_hosts","arguments":"{}"}}]}}]}`)
			case summaryFails:
				http.Error(w, `{"error":"overloaded"}`, http.StatusServiceUnavailable)
			default:
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"По сводке: один хост."}}]}`)
			}
			return
		}
		if toolsUnsupported && len(body.Tools) > 0 {
			http.Error(w, `{"error":"tools not supported"}`, http.StatusBadRequest)
			return
		}
		if toolsUnsupported {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"По сводке: один хост."}}]}`)
			return
		}
		// Попытка исправить сценарий: модели — ошибки «Проверить».
		for _, m := range body.Messages {
			if m.Role == "user" && (strings.Contains(m.Content, "нашла ошибки") || strings.Contains(m.Content, "found errors")) {
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"`+"```\\non web1 packages install htop\\n```"+`"}}]}`)
				return
			}
		}
		step++
		switch step {
		case 1:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"list_hosts","arguments":"{}"}}]}}]}`)
		case 2:
			args, _ := json.Marshal(`{"host_id":-1}`)
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"c2","type":"function","function":{"name":"host_findings","arguments":`+string(args)+`}},{"id":"c3","type":"function","function":{"name":"run_script","arguments":"{}"}}]}}]}`)
		default:
			// Сценарий с ошибкой (нет хоста web9) — хаб попросит исправить.
			a, _ := json.Marshal("На web1 стоит поставить htop:\n\n```nkt\non web9 packages install htop\n```\n")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(a)+`}}]}`)
		}
	}))
	defer prov.Close()
	if err := srv.hub.SetAISettings(ctx, ai.Settings{Enabled: true, Provider: ai.ProviderOpenAI, BaseURL: prov.URL, Model: "m", Anonymize: true}, nil); err != nil {
		t.Fatal(err)
	}

	do := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if set != nil {
			set(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("POST", "/api/auth/login", `{"username":"admin","password":"admin-password-1234"}`, nil)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	session := func(r *http.Request) { r.AddCookie(cookie) }
	// Ассистент выключен по умолчанию.
	if rec := do("POST", "/api/hub/assistant", `{"messages":[{"role":"user","text":"x"}]}`, session); rec.Code != http.StatusForbidden {
		t.Fatalf("assistant off: %d %s", rec.Code, rec.Body)
	}
	if rec := do("PUT", "/api/hub/experimental", `{"map":false,"clusters":false,"assistant":true,"mcp":false}`, session); rec.Code != 200 {
		t.Fatalf("turn assistant on: %d %s", rec.Code, rec.Body)
	}
	ask := func() assistantReply {
		t.Helper()
		rec := do("POST", "/api/hub/assistant", `{"messages":[{"role":"user","text":"что не так с web1?"}]}`, session)
		if rec.Code != 200 {
			t.Fatalf("assistant: %d %s", rec.Code, rec.Body)
		}
		var rep assistantReply
		_ = json.Unmarshal(rec.Body.Bytes(), &rep)
		return rep
	}

	rep := ask()
	if rep.Mode != "tools" || rep.Rounds != 3 || len(rep.Steps) != 3 || !rep.Script || len(rep.Issues) != 0 || !rep.ScriptRetried {
		t.Fatalf("reply: %+v", rep)
	}
	if rep.Steps[0].Tool != "list_hosts" || rep.Steps[1].Error || !rep.Steps[2].Error {
		t.Fatalf("steps: %+v", rep.Steps)
	}
	if !strings.Contains(rep.Answer, "on web1 packages install htop") {
		t.Fatalf("answer: %s", rep.Answer)
	}
	// Второй ход модели видит результат list_hosts — без адреса и имени.
	var hostsResult string
	for _, m := range bodies[1] {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			hostsResult = m.Content
		}
	}
	if hostsResult == "" || strings.Contains(hostsResult, "203.0.113.10") || strings.Contains(hostsResult, `"web1"`) {
		t.Fatalf("list_hosts for the model: %.300s", hostsResult)
	}
	// Действие не предлагается и не выполняется.
	for _, m := range bodies[2] {
		if m.Role == "tool" && m.ToolCallID == "c3" && !strings.Contains(m.Content, "unknown tool") {
			t.Fatalf("run_script: %s", m.Content)
		}
	}

	// Модель без инструментов — сводка в инструкции.
	toolsUnsupported = true
	bodies, withTools = nil, nil
	rep = ask()
	if rep.Mode != "summary" || len(withTools) != 2 || !withTools[0] || withTools[1] || !strings.Contains(bodies[1][0].Content, "/hub/hosts") {
		t.Fatalf("summary: %+v tools=%v", rep, withTools)
	}
	if !strings.Contains(rep.ToolsError, "tools not supported") {
		t.Fatalf("summary reason: %q", rep.ToolsError)
	}

	// Инструмент модель вызвала, а запрос с его результатом провайдер не
	// принял: не «tools unsupported», а ответ по сводке с причиной; сводка
	// идёт с исходным вопросом, без ходов с инструментами.
	toolsUnsupported, resultRejected = false, true
	bodies, withTools = nil, nil
	rep = ask()
	if rep.Mode != "summary" || len(rep.Steps) != 1 || len(bodies) != 3 || withTools[2] || len(bodies[2]) != 2 ||
		!strings.Contains(rep.ToolsError, "unexpected role tool") || !strings.Contains(rep.ToolsError, "(ход 2)") {
		t.Fatalf("result rejected: %+v bodies=%d", rep, len(bodies))
	}
	// Сводка тоже не удалась — видны обе причины.
	summaryFails = true
	rec = do("POST", "/api/hub/assistant", `{"messages":[{"role":"user","text":"что не так с web1?"}]}`, session)
	if rec.Code == 200 || !strings.Contains(rec.Body.String(), "overloaded") || !strings.Contains(rec.Body.String(), "unexpected role tool") {
		t.Fatalf("summary failed: %d %s", rec.Code, rec.Body)
	}

	// Токену — нет (ассистент только в интерфейсе).
	tok := do("POST", "/api/hub/tokens", `{"name":"t","role":"admin"}`, session)
	var c struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(tok.Body.Bytes(), &c)
	if rec := do("POST", "/api/hub/assistant", `{"messages":[{"role":"user","text":"x"}]}`, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}); rec.Code != http.StatusForbidden {
		t.Fatalf("token: %d", rec.Code)
	}
}
