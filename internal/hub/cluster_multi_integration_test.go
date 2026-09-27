package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Сводка находок кластеров и Helm-релиз во все выбранные кластеры — через
// настоящий SSH-туннель к nkt в fixtures.
func TestClustersFindingsAndHelmMulti(t *testing.T) {
	db, key, _, clusterID, ctx := fixtureClusterHub(t, "lab-cp-1")
	m := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	s.jobs.Register(KindHelmMulti, NewHelmMultiRunner(m))

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

	body := fmt.Sprintf(`{"clusters":[%d],"release":{"repo_name":"bitnami","repo_url":"https://charts.bitnami.com/bitnami","chart":"redis","release":"cache","namespace":"shop"}}`, clusterID)
	rec = httptest.NewRecorder()
	s.handleHelmMulti(rec, httptest.NewRequest("POST", "/", bytes.NewReader([]byte(body))).WithContext(ctx))
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.JobID == 0 {
		t.Fatalf("старт: %d %s", rec.Code, rec.Body.String())
	}
	var job store.Job
	for {
		job, _ = db.JobByID(ctx, started.JobID)
		if job.Done() || ctx.Err() != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	log := jobLogText(t, ctx, db, started.JobID)
	if job.Status != store.JobSucceeded || !strings.Contains(log, "upgrade --install cache") {
		t.Fatalf("статус %s: %s\n%s", job.Status, job.Error, log)
	}
}
