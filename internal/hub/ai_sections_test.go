package hub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/ai"
)

// Пустой ответ модели — пустой список разделов, а не null: интерфейс
// перебирает разделы, и null ронял окно ответа.
func TestAIAnswerSectionsNeverNull(t *testing.T) {
	m := &Manager{}
	raw, _ := json.Marshal(m.aiAnswer("", "prompt", ai.Settings{}, false))
	if !strings.Contains(string(raw), `"sections":[]`) {
		t.Fatalf("%s", raw)
	}
}
