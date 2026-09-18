package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Настоящее создание заводит запись кластера до проверок — проверка
// имени не должна считать её занятым именем. А когда проверки
// провалились до первой машины, запись и группа убираются, ошибка
// задания перечисляет провалы.
func TestClusterPreflightOwnRecordAndDiscard(t *testing.T) {
	ctx := context.Background()
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	s.jobs.Register(KindClusterCreate, NewClusterRunner(s))

	// Хост не в сети — проверки провалятся именно на этом, не на имени.
	hostID, err := db.CreateHost(ctx, "hv1", "192.0.2.10", 22, "root", "key", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	spec := ClusterSpec{Name: "lab", Flavor: "k3s", ImageID: "ubuntu-24.04", Placements: []Placement{{HostID: hostID, Role: RoleControlPlane, Kind: KindVM, Count: 1}}}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(spec)
	clID, err := db.CreateCluster(ctx, store.Cluster{Name: spec.Name, HostID: hostID, Flavor: spec.Flavor, SpecJSON: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.CreateHostGroup(ctx, spec.Name)

	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindClusterCreate, Title: "t", Queue: "cluster:1", Steps: 5, Params: ClusterJobParams{ClusterID: clID}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	var job store.Job
	for {
		job, _ = db.JobByID(ctx, id)
		if job.Done() || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job.Status != store.JobFailed {
		t.Fatalf("статус %s: %s", job.Status, job.Error)
	}
	lines, _ := db.JobLog(ctx, id, 0, 1000)
	var log []string
	for _, l := range lines {
		log = append(log, l.Text)
	}
	all := strings.Join(log, "\n")
	if strings.Contains(all, "уже есть") {
		t.Errorf("собственная запись принята за занятое имя:\n%s", all)
	}
	if !strings.Contains(job.Error, "не в сети") {
		t.Errorf("ошибка задания без перечня провалов: %s", job.Error)
	}
	if _, err := db.ClusterByID(ctx, clID); err == nil {
		t.Errorf("запись кластера должна быть удалена после провала проверок")
	}
	groups, _ := db.ListHostGroups(ctx)
	for _, g := range groups {
		if g == "lab" {
			t.Errorf("пустая группа lab должна быть удалена")
		}
	}
}
