package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Пустой ответ модели — пустой список разделов, а не null: интерфейс
// перебирает разделы, и null ронял окно ответа.
func TestAIAnswerSectionsNeverNull(t *testing.T) {
	m := &Manager{}
	raw, _ := json.Marshal(m.aiAnswer(context.Background(), "", "", "prompt", nil))
	if !strings.Contains(string(raw), `"sections":[]`) {
		t.Fatalf("%s", raw)
	}
}
