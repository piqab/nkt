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
	"github.com/piqab/nkt/internal/store"
)

// ИИ в редакторе сценариев: модели — справка языка и имена хостов; ответ
// с ошибкой «Проверить» уходит модели на одну повторную попытку; объяснение
// — текстом.
func TestAIScript(t *testing.T) {
	ctx := context.Background()
	var calls int
	var systems, users []string
	answers := []string{
		"```\n# ставим htop\non web9 packages install htop\n```",
		"```\n# ставим htop\non web-1 packages install htop\n```",
		"Строка ставит пакет htop на web-1.",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				systems = append(systems, m.Content)
			} else {
				users = append(users, m.Content)
			}
		}
		a, _ := json.Marshal(answers[calls])
		calls++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(a)+`}}]}`)
	}))
	defer srv.Close()
	m, db := newTestManager(t)
	if err := m.SetAISettings(ctx, ai.Settings{Enabled: true, Provider: ai.ProviderOpenAI, BaseURL: srv.URL, Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddHost(ctx, "web-1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false); err != nil {
		t.Fatal(err)
	}
	s := &Server{hub: m, db: db}
	call := func(body string) (int, aiScriptReply) {
		rec := httptest.NewRecorder()
		s.handleAIScript(rec, httptest.NewRequest("POST", "/api/hub/ai/script", strings.NewReader(body)))
		var rep aiScriptReply
		_ = json.Unmarshal(rec.Body.Bytes(), &rep)
		return rec.Code, rep
	}
	code, rep := call(`{"mode":"task","task":"поставить htop на web-1"}`)
	if code != 200 || calls != 2 || rep.Attempts != 2 || len(rep.Issues) != 0 || !strings.Contains(rep.Script, "on web-1 packages install htop") {
		t.Fatalf("task: %d calls=%d %+v", code, calls, rep)
	}
	if !strings.Contains(systems[0], "web-1") || !strings.Contains(systems[0], "packages install") || !strings.Contains(systems[0], "pipeline") {
		t.Errorf("в инструкции нет справки или имён:\n%.400s", systems[0])
	}
	if !strings.Contains(users[1], "web9") {
		t.Errorf("повтор без ошибок проверки: %s", users[1])
	}
	code, rep = call(`{"mode":"explain","content":"on web-1 packages install htop\n"}`)
	if code != 200 || rep.Text == "" || rep.Script != "" {
		t.Fatalf("explain: %d %+v", code, rep)
	}
	if code, _ := call(`{"mode":"comment","content":"on web-1 packages install htop\n"}`); code != 400 {
		t.Errorf("comment без «# ИИ:» — %d", code)
	}
	if calls != 3 {
		t.Errorf("запросов к модели %d", calls)
	}
}

// Разбор находки на хосте просит у модели и сценарий исправления (блок
// ```nkt с командами для этого хоста); без хоста — нет.
func TestAIExplainFixScriptHint(t *testing.T) {
	ctx := context.Background()
	var system string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				system = m.Content
			}
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	m, _ := newTestManager(t)
	if err := m.SetAISettings(ctx, ai.Settings{Enabled: true, Provider: ai.ProviderOpenAI, BaseURL: srv.URL, Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	h, _ := m.AddHost(ctx, "web-1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if _, err := m.AIExplain(ctx, ai.KindFinding, ai.FindingContext{Title: "Порт 6379 открыт"}, nil, h, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "```nkt") || !strings.Contains(system, "web-1") || !strings.Contains(system, "firewall") {
		t.Errorf("нет просьбы о сценарии исправления:\n%.300s", system[max(0, len(system)-600):])
	}
	if _, err := m.AIExplain(ctx, ai.KindFinding, ai.FindingContext{Title: "Порт 6380 открыт"}, nil, 0, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(system, "```nkt") {
		t.Error("у разбора без хоста — просьба о сценарии")
	}
}

// Сценарий исправления с ошибкой «Проверить» — модели одна попытка
// исправить; сохраняется и показывается исправленный, с итогом проверки.
func TestAIExplainFixScriptRetry(t *testing.T) {
	ctx := context.Background()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls++
		answer := "## Что сделать\nПерезапустить Redis.\n\n```nkt\n# Redis\non web9 service redis-server restart\n```\n"
		if strings.Contains(body.Messages[len(body.Messages)-1].Content, "нашла ошибки") {
			answer = "```\n# Redis\non web-1 service redis-server restart\n```"
		}
		a, _ := json.Marshal(answer)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(a)+`}}]}`)
	}))
	defer srv.Close()
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db}
	m.scriptCheck, m.scriptNames = s.aiScriptCheck, s.aiScriptNames
	if err := m.SetAISettings(ctx, ai.Settings{Enabled: true, Provider: ai.ProviderOpenAI, BaseURL: srv.URL, Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	h, _ := m.AddHost(ctx, "web-1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	res, err := m.AIExplain(ctx, ai.KindFinding, ai.FindingContext{Title: "Redis слушает все адреса"}, nil, h, true)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(res.Answer, "on web-1 service redis-server restart") || strings.Contains(res.Answer, "web9") ||
		!res.Script || len(res.ScriptIssues) != 0 || !strings.Contains(res.Answer, "Перезапустить Redis") {
		t.Fatalf("calls=%d %+v", calls, res)
	}
	// Сохранённый ответ — тот же, исправленный.
	again, err := m.AIExplain(ctx, ai.KindFinding, ai.FindingContext{Title: "Redis слушает все адреса"}, nil, h, false)
	if err != nil || calls != 2 || !strings.Contains(again.Answer, "on web-1") || !again.Script {
		t.Fatalf("stored: calls=%d %+v %v", calls, again, err)
	}
}
