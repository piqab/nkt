package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/ai"
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// Ассистент хаба: разговор с моделью о парке хостов. Данные модель
// читает сама — инструментами чтения из таблицы MCP (mcpTools), каждый —
// GET к API хаба от имени того, кто спрашивает: видно ровно то, что ему
// видно в интерфейсе. Менять модель ничего не может: изменение приходит
// блоком ```nkt, хаб сразу прогоняет его через «Проверить», а открывает,
// проверяет и запускает сценарий человек.

type assistantRequest struct {
	Messages []struct {
		Role string `json:"role"`
		Text string `json:"text"`
	} `json:"messages"`
}

type assistantStep struct {
	Tool  string          `json:"tool"`
	Args  json.RawMessage `json:"args"`
	Error bool            `json:"error"`
}

type assistantReply struct {
	Answer string            `json:"answer"`
	Steps  []assistantStep   `json:"steps"`
	Issues []scriptIssueJSON `json:"issues"`
	// Script — есть ли в ответе сценарий (блок ```nkt).
	Script bool `json:"script"`
	// ScriptRetried — сценарий не прошёл проверку, модель исправляла.
	ScriptRetried bool `json:"script_retried,omitempty"`
	// Mode — tools (модель читала сама) или summary (модель без
	// инструментов, ответ по сводке).
	Mode string `json:"mode"`
	// ToolsError — почему ответ по сводке: что ответил провайдер на запрос
	// с инструментами (или с их результатами).
	ToolsError string `json:"tools_error,omitempty"`
	Model      string `json:"model"`
	Rounds     int    `json:"rounds"`
}

const (
	// assistantRounds — ходов модели на один вопрос.
	assistantRounds = 10
	// assistantCallsPerRound — вызовов инструментов за ход.
	assistantCallsPerRound = 8
	// assistantResultLimit — результат инструмента для модели.
	assistantResultLimit = 40 << 10
	// assistantHistoryLimit — реплик и символов разговора.
	assistantHistoryMsgs  = 40
	assistantHistoryChars = 120000
	// assistantTotal — предел времени на вопрос.
	assistantTotal = 15 * time.Minute
)

// assistantTools — инструменты чтения (GET) из таблицы MCP.
func assistantTools() []ai.Tool {
	var out []ai.Tool
	for _, t := range mcpTools {
		if !t.ReadOnly || t.Method != http.MethodGet {
			continue
		}
		d := t.describe()
		out = append(out, ai.Tool{Name: t.Name, Description: t.Description, Schema: d["inputSchema"].(map[string]any)})
	}
	return out
}

// nktBlock — сценарий в ответе модели.
var nktBlock = regexp.MustCompile("(?s)```nkt[^\\n]*\\n(.*?)```")

// handleAssistant — POST /hub/assistant.
func (s *Server) handleAssistant(w http.ResponseWriter, r *http.Request) {
	if !s.experimental(r.Context()).Assistant {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("ai.assistantDisabled"))
		return
	}
	var req assistantRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	total := 0
	for _, m := range req.Messages {
		total += len(m.Text)
		if m.Role != "user" && m.Role != "assistant" {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ai.badResponse", m.Role))
			return
		}
	}
	if len(req.Messages) == 0 || req.Messages[len(req.Messages)-1].Role != "user" || strings.TrimSpace(req.Messages[len(req.Messages)-1].Text) == "" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ai.scriptEmpty"))
		return
	}
	if len(req.Messages) > assistantHistoryMsgs || total > assistantHistoryChars {
		writeErr(w, r, http.StatusRequestEntityTooLarge, msgs.Errorf("ai.assistantTooLong"))
		return
	}
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeErr(w, r, http.StatusUnauthorized, msgs.Errorf("auth.loginRequired"))
		return
	}
	m := s.hub
	client, set, err := m.aiClient(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	s.aiExtendDeadline(w, int(assistantTotal/time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), assistantTotal)
	defer cancel()
	lang := msgs.FromContext(ctx)
	who := assistantWho{user: u, lang: lang, remote: r.RemoteAddr}

	mapper := ai.NewMapper(set.Anonymize)
	mapper.Learn("host", s.aiKnownNames(ctx))
	var history []ai.ChatMessage
	for _, msg := range req.Messages {
		history = append(history, ai.ChatMessage{Role: msg.Role, Text: mapper.Hide(msg.Text)})
	}
	names := s.aiScriptNames(ctx)
	reference := aiScriptReference(lang)
	spend := func() error {
		used, _ := m.db.AIUsageToday(ctx)
		if ai.LimitReached(set, ai.Usage{Requests: used}) {
			return msgs.Errorf("ai.limitReached", set.DailyLimit)
		}
		return nil
	}

	reply := assistantReply{Mode: "tools", Model: set.Model, Steps: []assistantStep{}, Issues: []scriptIssueJSON{}}
	system := mapper.Hide(ai.AssistantSystemPrompt(lang, reference, names, ""))
	tools := assistantTools()
	question := history
	answer := ""
	for round := 0; round < assistantRounds; round++ {
		if err := spend(); err != nil {
			fail(w, r, err)
			return
		}
		// Последний ход — без инструментов: модель обязана ответить.
		offer := tools
		if round == assistantRounds-1 {
			offer = nil
		}
		turn, err := client.Chat(ctx, system, history, offer)
		if ai.ToolsRejected(err) {
			// Провайдер не принял инструменты — сразу или уже на ходе с их
			// результатами (сервер, который зовёт инструмент, но не
			// понимает ответ на него). Отвечаем по сводке, причину —
			// в ответ: без неё «по сводке» не объяснить.
			reason := err
			if round > 0 {
				reason = msgs.Errorf("ai.toolsResultRejected", round+1, err)
			}
			reply.Mode = "summary"
			reply.ToolsError = mapper.Reveal(msgs.Localize(lang, reason))
			system = mapper.Hide(ai.AssistantSystemPrompt(lang, reference, names, s.assistantSnapshot(ctx, who)))
			if round > 0 {
				if err = spend(); err != nil {
					fail(w, r, err)
					return
				}
			}
			turn, err = client.Chat(ctx, system, question, nil)
			if err != nil {
				err = msgs.Errorf("ai.assistantSummaryFailed", err, reason)
			}
		}
		if err != nil {
			fail(w, r, err)
			return
		}
		_ = m.db.AIUsageAdd(ctx)
		reply.Rounds = round + 1
		if len(turn.Calls) == 0 || reply.Mode == "summary" {
			answer = turn.Text
			break
		}
		if len(turn.Calls) > assistantCallsPerRound {
			turn.Calls = turn.Calls[:assistantCallsPerRound]
		}
		history = append(history, ai.ChatMessage{Role: "assistant", Text: turn.Text, Calls: turn.Calls})
		for _, call := range turn.Calls {
			text, isErr := s.assistantCall(ctx, who, call.Name, json.RawMessage(mapper.Reveal(string(call.Args))))
			reply.Steps = append(reply.Steps, assistantStep{Tool: call.Name, Args: call.Args, Error: isErr})
			history = append(history, ai.ChatMessage{Role: "tool", CallID: call.ID, Text: mapper.Hide(text)})
		}
	}
	if strings.TrimSpace(answer) == "" {
		answer = msgs.T(lang, "ai.assistantNoAnswer")
	}
	// Сценарий в ответе проходит «Проверить»; с ошибками — модели одна
	// попытка исправить.
	var issues []script.Issue
	reply.Answer, issues, reply.Script, reply.ScriptRetried = m.fixAnswerScript(ctx, client, set, mapper, mapper.Reveal(answer))
	reply.Issues = issuesJSON(lang, issues)
	// Что модель показала об инструментах — в настройки ИИ («умеет» или
	// «отвечает по сводке»); ответ без вызовов ничего не доказывает.
	switch {
	case reply.Mode == "summary":
		m.aiRecordTools(ctx, set, "no_tools", "assistant")
	case len(reply.Steps) > 0:
		m.aiRecordTools(ctx, set, "tools", "assistant")
	}
	s.db.Audit(ctx, auth.Username(ctx), "ai.assistant", reply.Mode, "ok", map[string]any{"rounds": reply.Rounds, "tools": len(reply.Steps)})
	writeJSON(w, http.StatusOK, reply)
}

// assistantCall — инструмент чтения от имени пользователя: GET к API хаба
// с ним же в контексте (RequireAuth/RequireAdmin — те же, что у кнопок).
func (s *Server) assistantCall(ctx context.Context, who assistantWho, name string, rawArgs json.RawMessage) (string, bool) {
	var tool *mcpTool
	for i := range mcpTools {
		if mcpTools[i].Name == name && mcpTools[i].ReadOnly && mcpTools[i].Method == http.MethodGet {
			tool = &mcpTools[i]
		}
	}
	if tool == nil {
		return "unknown tool: " + name, true
	}
	args := map[string]any{}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return "arguments: " + err.Error(), true
		}
	}
	path, _, err := tool.build(args)
	if err != nil {
		return err.Error(), true
	}
	code, out := s.assistantGet(ctx, who, path)
	if len(out) > assistantResultLimit {
		out = out[:assistantResultLimit] + "\n…(truncated)"
	}
	if code >= 400 {
		return fmt.Sprintf("HTTP %d: %s", code, out), true
	}
	return out, false
}

// assistantGet — GET к роутеру хаба от имени пользователя. Контекст —
// новый (с пользователем, языком и отменой исходного), без маршрута chi
// исходного запроса.
// assistantWho — кто спрашивает: пользователь, язык, адрес.
type assistantWho struct {
	user   store.User
	lang   msgs.Lang
	remote string
}

func (s *Server) assistantGet(ctx context.Context, who assistantWho, path string) (int, string) {
	sub, cancel := context.WithCancel(auth.WithUser(context.Background(), who.user))
	defer cancel()
	defer context.AfterFunc(ctx, cancel)()
	req, err := http.NewRequestWithContext(sub, http.MethodGet, "/api"+path, nil)
	if err != nil {
		return http.StatusBadRequest, err.Error()
	}
	req.RemoteAddr = who.remote
	req.Header.Set("X-NKT-Lang", string(who.lang))
	rec := httptest.NewRecorder()
	s.apiRouter.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// assistantSnapshot — сводка для модели без инструментов: хосты и
// последние оповещения.
func (s *Server) assistantSnapshot(ctx context.Context, who assistantWho) string {
	var b strings.Builder
	for _, path := range []string{"/hub/hosts", "/hub/events?limit=30", "/hub/pipelines", "/hub/migrations"} {
		code, out := s.assistantGet(ctx, who, path)
		if code >= 400 {
			continue
		}
		if len(out) > assistantResultLimit {
			out = out[:assistantResultLimit] + "\n…(truncated)"
		}
		fmt.Fprintf(&b, "### GET %s\n%s\n", path, out)
	}
	return b.String()
}
