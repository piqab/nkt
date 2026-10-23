package hub

import (
	"context"
	"strings"

	"github.com/piqab/nkt/internal/ai"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
)

// aiFixKinds — разборы, к которым модель добавляет сценарий исправления.
var aiFixKinds = map[string]bool{ai.KindFinding: true, ai.KindVuln: true, ai.KindConfigError: true, ai.KindMalware: true}

// aiOnHostSyntax — краткий синтаксис команд сценария, которые действуют на
// хосте: модели для блока исправления хватает его, полная справка не нужна.
func aiOnHostSyntax(lang msgs.Lang) string {
	en := lang == msgs.EN
	var b strings.Builder
	for _, c := range script.Commands {
		if c.OnHost || c.Kind == script.KindWait {
			b.WriteString(script.Localize(c.Syntax, en))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// fixAnswerScript — сценарий в ответе модели (блок ```nkt) проходит
// «Проверить»; с ошибками — модели одна попытка исправить (ошибки по
// строкам), исправленный блок подставляется, если ошибок стало меньше.
// answer — уже с настоящими именами; к модели он уходит через mapper.
// has — в ответе есть сценарий; issues — что осталось после попытки.
func (m *Manager) fixAnswerScript(ctx context.Context, client *ai.Client, set ai.Settings, mapper *ai.Mapper, answer string) (out string, issues []script.Issue, has bool, retried bool) {
	out = answer
	mm := nktBlock.FindStringSubmatchIndex(answer)
	if mm == nil || m.scriptCheck == nil {
		return out, nil, mm != nil, false
	}
	block := answer[mm[2]:mm[3]]
	issues = m.scriptCheck(ctx, block)
	if len(issues) == 0 {
		return out, nil, true, false
	}
	used, _ := m.db.AIUsageToday(ctx)
	if ai.LimitReached(set, ai.Usage{Requests: used}) {
		return out, issues, true, false
	}
	lang := msgs.FromContext(ctx)
	names := ""
	if m.scriptNames != nil {
		names = m.scriptNames(ctx)
	}
	system := ai.ScriptSystemPrompt(aiScriptTask, lang, aiScriptReference(lang), names)
	user := ai.ScriptFixPrompt(lang, ai.RedactSecrets(block), issuesText(lang, issues))
	again, err := client.Ask(ctx, mapper.Hide(system), mapper.Hide(user))
	if err != nil {
		return out, issues, true, false
	}
	_ = m.db.AIUsageAdd(ctx)
	fixed := aiScriptExtract(mapper.Reveal(again))
	if left := m.scriptCheck(ctx, fixed); len(left) < len(issues) {
		out = answer[:mm[2]] + fixed + answer[mm[3]:]
		issues = left
	}
	return out, issues, true, true
}
