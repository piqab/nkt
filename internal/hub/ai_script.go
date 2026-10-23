package hub

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/piqab/nkt/internal/ai"
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
)

// ИИ в редакторе сценариев: объяснить текст, написать команды по
// комментариям «# ИИ: …», написать сценарий по описанию задачи. Модель
// видит справку языка (ту же таблицу команд, что вкладка «Справка») и
// только имена объектов хаба — адреса, пароли и секреты не уходят. Ответ
// сразу проходит «Проверить»; ошибки — модели на одну повторную попытку.
// Сценарий ИИ не запускает: текст уходит в редактор через окно с диффом.

const (
	aiScriptExplain = "explain"
	aiScriptComment = "comment"
	aiScriptTask    = "task"
)

type aiScriptRequest struct {
	Mode      string `json:"mode"`
	Content   string `json:"content"`
	Selection string `json:"selection"`
	Task      string `json:"task"`
}

type aiScriptReply struct {
	Mode     string            `json:"mode"`
	Text     string            `json:"text,omitempty"`
	Script   string            `json:"script,omitempty"`
	Issues   []scriptIssueJSON `json:"issues"`
	Model    string            `json:"model"`
	Attempts int               `json:"attempts"`
}

// aiScriptLimit — предел текста сценария для модели.
const aiScriptLimit = 60000

var aiCodeBlock = regexp.MustCompile("(?s)```[a-zA-Z0-9_-]*\\n(.*?)```")

// aiScriptExtract — текст сценария из ответа: первый блок ```…```, без
// блока — ответ целиком.
func aiScriptExtract(answer string) string {
	if m := aiCodeBlock.FindStringSubmatch(answer); m != nil {
		return strings.TrimRight(m[1], "\n") + "\n"
	}
	return strings.TrimSpace(answer) + "\n"
}

// aiScriptReference — справка языка для модели: синтаксис, назначение и
// аргументы каждой команды, готовые сценарии целиком.
func aiScriptReference(lang msgs.Lang) string {
	en := lang == msgs.EN
	var b strings.Builder
	for _, c := range script.Commands {
		fmt.Fprintf(&b, "### %s\n%s\n", script.Localize(c.Syntax, en), msgs.T(lang, c.Summary))
		for _, a := range c.Args {
			fmt.Fprintf(&b, "- %s: %s\n", script.Localize(a.Name, en), msgs.T(lang, a.Desc))
		}
		fmt.Fprintf(&b, "```\n%s\n```\n\n", script.Localize(c.Example, en))
	}
	for _, e := range script.Examples {
		fmt.Fprintf(&b, "## %s\n%s\n```\n%s```\n\n", msgs.T(lang, e.Title), msgs.T(lang, e.Summary), script.Localize(e.Content, en))
	}
	return b.String()
}

// aiScriptNames — имена объектов хаба, на которые сценарий может
// ссылаться (без адресов и секретов).
func (s *Server) aiScriptNames(ctx context.Context) string {
	refs := s.refsNowCtx(ctx)
	list := func(m map[string]bool) string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		if len(out) == 0 {
			return "—"
		}
		slices.Sort(out)
		return strings.Join(out, ", ")
	}
	var gws []string
	for _, g := range s.flyGateways(ctx) {
		gws = append(gws, g.Name)
	}
	gw := "—"
	if len(gws) > 0 {
		gw = strings.Join(gws, ", ")
	}
	return fmt.Sprintf("hosts: %s\ngroups: %s\nprofiles: %s\npipelines: %s\nclusters: %s\nTraffic Fly gateways: %s\n",
		list(refs.Hosts), list(refs.Groups), list(refs.Profiles), list(refs.Pipelines), list(refs.Clusters), gw)
}

// handleAIScript — POST /hub/ai/script.
func (s *Server) handleAIScript(w http.ResponseWriter, r *http.Request) {
	var req aiScriptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	lang := msgs.FromContext(ctx)
	switch req.Mode {
	case aiScriptExplain:
		if strings.TrimSpace(req.Selection) == "" && strings.TrimSpace(req.Content) == "" {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ai.scriptEmpty"))
			return
		}
	case aiScriptComment:
		if !aiCommentRe.MatchString(req.Content) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ai.scriptNoComment"))
			return
		}
	case aiScriptTask:
		if strings.TrimSpace(req.Task) == "" {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ai.scriptEmpty"))
			return
		}
	default:
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ai.badResponse", req.Mode))
		return
	}
	if len(req.Content)+len(req.Selection)+len(req.Task) > aiScriptLimit {
		writeErr(w, r, http.StatusRequestEntityTooLarge, msgs.Errorf("ai.scriptTooLong", aiScriptLimit))
		return
	}
	m := s.hub
	client, set, err := m.aiClient(ctx)
	if err != nil {
		fail(w, r, err)
		return
	}
	s.aiExtendDeadline(w, set.TimeoutS)
	system := ai.ScriptSystemPrompt(req.Mode, lang, aiScriptReference(lang), s.aiScriptNames(ctx))
	user := ai.ScriptUserPrompt(req.Mode, lang, ai.RedactSecrets(req.Content), ai.RedactSecrets(req.Selection), req.Task)
	mapper := ai.NewMapper(set.Anonymize)
	mapper.Learn("host", s.aiKnownNames(ctx))
	ask := func(user string) (string, error) {
		used, _ := m.db.AIUsageToday(ctx)
		if ai.LimitReached(set, ai.Usage{Requests: used}) {
			return "", msgs.Errorf("ai.limitReached", set.DailyLimit)
		}
		answer, err := client.Ask(ctx, mapper.Hide(system), mapper.Hide(user))
		if err != nil {
			return "", err
		}
		_ = m.db.AIUsageAdd(ctx)
		return mapper.Reveal(answer), nil
	}
	reply := aiScriptReply{Mode: req.Mode, Model: set.Model, Issues: []scriptIssueJSON{}}
	answer, err := ask(user)
	if err != nil {
		fail(w, r, err)
		return
	}
	reply.Attempts = 1
	if req.Mode == aiScriptExplain {
		reply.Text = answer
		s.db.Audit(ctx, auth.Username(ctx), "ai.script", req.Mode, "ok", nil)
		writeJSON(w, http.StatusOK, reply)
		return
	}
	text := aiScriptExtract(answer)
	issues := s.aiScriptCheck(ctx, text)
	if len(issues) > 0 {
		// Одна повторная попытка: модели — её текст и ошибки разбора.
		retry := ai.ScriptFixPrompt(lang, text, issuesText(lang, issues))
		if again, err := ask(retry); err == nil {
			reply.Attempts = 2
			if fixed := aiScriptExtract(again); len(s.aiScriptCheck(ctx, fixed)) < len(issues) {
				text = fixed
				issues = s.aiScriptCheck(ctx, fixed)
			}
		}
	}
	reply.Script = text
	reply.Issues = issuesJSON(lang, issues)
	s.db.Audit(ctx, auth.Username(ctx), "ai.script", req.Mode, "ok", map[string]any{"issues": len(issues), "attempts": reply.Attempts})
	writeJSON(w, http.StatusOK, reply)
}

// aiCommentRe — строка-задание для модели: «# ИИ: …» или «# AI: …».
var aiCommentRe = regexp.MustCompile(`(?mi)^\s*#\s*(ИИ|AI)\s*:`)

// aiScriptCheck — «Проверить» для текста модели.
func (s *Server) aiScriptCheck(ctx context.Context, text string) []script.Issue {
	sc, issues := script.Parse(text, nil)
	if len(issues) == 0 {
		// Сценарию от модели и предупреждения — ошибки: пусть исправит.
		refs := s.refsNowCtx(ctx)
		issues = append(script.Check(sc, refs), script.Warnings(sc, refs)...)
	}
	return issues
}

func issuesText(lang msgs.Lang, issues []script.Issue) string {
	var b strings.Builder
	for _, i := range issues {
		fmt.Fprintf(&b, "line %d: %s\n", i.Line, msgs.Localize(lang, i.Err))
	}
	return b.String()
}
