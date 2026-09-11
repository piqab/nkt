package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/statediff"
)

func saveSnap(t *testing.T, s *Server, snap model.Snapshot) {
	t.Helper()
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, _, err := s.db.SaveSnapshot(context.Background(), snap.Digest, string(raw)); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
}

func getChanges(t *testing.T, s *Server) (changes []statediff.Change, first bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleChanges(rec, httptest.NewRequest(http.MethodGet, "/api/changes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /changes: код %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Changes []statediff.Change `json:"changes"`
		First   bool               `json:"first"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	return body.Changes, body.First
}

// Полный ход: первый снимок сравнивать не с чем, второй показывает
// расхождение, «принять» его закрывает, а следующая правка показывается
// снова.
func TestChangesAcknowledgeCycle(t *testing.T) {
	s := newTestServerWithDB(t)

	saveSnap(t, s, model.Snapshot{
		Digest:   "1",
		Services: []model.ServiceUnit{{Name: "nginx", Installed: true, ActiveState: "active", Enabled: "enabled"}},
	})

	changes, first := getChanges(t, s)
	if len(changes) != 0 || !first {
		t.Fatalf("на единственном снимке = %+v (first=%v)", changes, first)
	}

	saveSnap(t, s, model.Snapshot{
		Digest:   "2",
		Services: []model.ServiceUnit{{Name: "nginx", Installed: true, ActiveState: "inactive", Enabled: "enabled"}},
	})

	changes, _ = getChanges(t, s)
	if len(changes) != 1 || changes[0].Kind != statediff.KindService || changes[0].Now != "inactive/enabled" {
		t.Fatalf("остановка службы не показана: %+v", changes)
	}

	// «Принять» — и то же самое расхождение больше не показывается.
	rec := httptest.NewRecorder()
	s.handleChangesAck(rec, httptest.NewRequest(http.MethodPost, "/api/changes/ack", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /changes/ack: код %d (%s)", rec.Code, rec.Body.String())
	}
	if changes, _ := getChanges(t, s); len(changes) != 0 {
		t.Fatalf("после «принять» изменения показаны снова: %+v", changes)
	}

	// А новая правка — показывается.
	saveSnap(t, s, model.Snapshot{
		Digest: "3",
		Services: []model.ServiceUnit{
			{Name: "nginx", Installed: true, ActiveState: "inactive", Enabled: "enabled"},
			{Name: "docker", Installed: true, ActiveState: "active", Enabled: "enabled"},
		},
	})
	changes, _ = getChanges(t, s)
	if len(changes) != 1 || changes[0].Key != "docker" || changes[0].Action != statediff.Appeared {
		t.Fatalf("новая правка не показана: %+v", changes)
	}
}

// Снимков нет вовсе — это не ошибка: первое сканирование ещё впереди.
func TestChangesWithoutSnapshots(t *testing.T) {
	s := newTestServerWithDB(t)
	if changes, _ := getChanges(t, s); len(changes) != 0 {
		t.Errorf("на пустой базе = %+v", changes)
	}
}
