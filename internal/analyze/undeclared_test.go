package analyze

import (
	"testing"

	"github.com/piqab/nkt/internal/model"
)

// Порт из конфига nginx, который держит другая программа (nkt-edge, пока
// nginx остановлен), — не «описан»: он виден в «Остальных сервисах».
func TestUndeclaredByOwner(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{{ID: "n1", Service: model.ServiceNginx, Address: "0.0.0.0", Port: 443, Protocol: "tcp"}},
		Listeners: []model.Listener{
			{Protocol: "tcp", Address: "0.0.0.0", Port: 443, Process: "nkt-edge", PID: 7},
			{Protocol: "tcp", Address: "::", Port: 443, Process: "nginx", PID: 8},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 443, PID: 9},
		},
	}
	got := UndeclaredListeners(snap)
	if len(got) != 1 || got[0].Process != "nkt-edge" {
		t.Fatalf("%+v", got)
	}
}
