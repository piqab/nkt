package hub

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/jobs"
)

// Сводка находок кластеров — через настоящий SSH-туннель к nkt в fixtures.
func TestClustersFindings(t *testing.T) {
	db, key, _, _, ctx := fixtureClusterHub(t, "lab-cp-1")
	m := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}

	rec := httptest.NewRecorder()
	s.handleClustersFindings(rec, httptest.NewRequest("GET", "/?lang=en", nil).WithContext(ctx))
	var fs struct {
		Clusters []ClusterFindings `json:"clusters"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &fs)
	if len(fs.Clusters) != 1 || fs.Clusters[0].Error != "" || len(fs.Clusters[0].Findings) == 0 {
		t.Fatalf("находки: %s", rec.Body.String())
	}
	for _, f := range fs.Clusters[0].Findings {
		if f.Service != "kubernetes" {
			t.Errorf("чужая находка: %+v", f)
		}
	}
}
