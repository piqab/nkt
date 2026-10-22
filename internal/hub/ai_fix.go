package hub

import (
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
