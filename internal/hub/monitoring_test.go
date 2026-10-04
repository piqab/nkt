package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/monitor"
	"github.com/piqab/nkt/internal/store"
)

// Сбор с машины хаба → история → прогноз заполнения демо-диска «/» →
// оповещение «прогноз» один раз → сводка раздела.
func TestMonitoringCollectAnalyze(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	if _, err := monitor.BackfillDemoHostHistory(ctx, db, 14); err != nil {
		t.Fatal(err)
	}
	srv.collectMonitoring(ctx)
	if last, _ := db.MonLastHour(ctx, localHostID); last == "" {
		t.Fatalf("no history collected: %+v", srv.mon.hosts[localHostID])
	}
	var disk *Insight
	for i, in := range srv.mon.insights {
		if in.Kind == "disk_full" && in.Subject == "/" {
			disk = &srv.mon.insights[i]
		}
	}
	if disk == nil || disk.Value < 5 || disk.Value > 60 || disk.Severity != "info" {
		t.Fatalf("disk forecast: %+v", srv.mon.insights)
	}
	// Порог предупреждения выше прогноза — оповещение, и только одно.
	raw, _ := json.Marshal(MonSettings{DiskWarnDays: 60, DiskCritDays: 1, MemPct: 90, CPUPct: 85, LeakDays: 3, LeakGrowthPct: 20, AvailDropPP: 2})
	_ = db.KVSet(ctx, monSettingsKV, string(raw))
	srv.analyzeMonitoring(ctx)
	srv.analyzeMonitoring(ctx)
	events, _ := db.ListHostEvents(ctx, 50)
	n := 0
	for _, e := range events {
		if e.Kind == store.EventForecast {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("forecast events: %d", n)
	}

	req := httptest.NewRequest(http.MethodGet, "/hub/monitoring/overview?range=7d", nil).
		WithContext(auth.WithUser(ctx, store.User{Username: "admin", Role: store.RoleAdmin}))
	w := httptest.NewRecorder()
	srv.handleMonitoringOverview(w, req)
	var out struct {
		Hosts []struct {
			ID      int64 `json:"id"`
			HasData bool  `json:"has_data"`
			Disks   []struct {
				Mount string   `json:"mount"`
				ETA   *float64 `json:"eta_days"`
			} `json:"disks"`
			MemTotal float64 `json:"mem_total"`
		} `json:"hosts"`
		Insights []Insight `json:"insights"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Hosts) == 0 || !out.Hosts[0].HasData || out.Hosts[0].MemTotal == 0 {
		t.Fatalf("overview: %s", w.Body.String()[:300])
	}
	found := false
	for _, d := range out.Hosts[0].Disks {
		if d.Mount == "/" && d.ETA != nil {
			found = true
		}
	}
	if !found || len(out.Insights) == 0 || out.Insights[0].Text == "" {
		t.Fatalf("disk eta or insight text missing: %s", w.Body.String()[:400])
	}
}

// Память контейнера растёт трое суток без откатов — «похоже на утечку»;
// ровная — нет.
func TestMonitoringLeakDetection(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	var rows []store.MonRow
	for i := 72; i >= 1; i-- {
		at := now.Add(-time.Duration(i) * time.Hour).Format("2006-01-02T15")
		grow := float64(200<<20) * (1 + float64(72-i)/72*0.6)
		rows = append(rows,
			store.MonRow{Source: "docker", Subject: "leaky", Metric: "mem_bytes", At: at, Avg: grow, Max: grow, Sum: grow},
			store.MonRow{Source: "docker", Subject: "steady", Metric: "mem_bytes", At: at, Avg: 300 << 20, Max: 300 << 20, Sum: 300 << 20})
	}
	if err := db.MonUpsertHourly(ctx, localHostID, rows, false); err != nil {
		t.Fatal(err)
	}
	srv.analyzeMonitoring(ctx)
	var leaky, steady bool
	for _, in := range srv.mon.insights {
		if in.Kind == "mem_leak" {
			leaky = leaky || in.Subject == "leaky"
			steady = steady || in.Subject == "steady"
		}
	}
	if !leaky || steady {
		t.Fatalf("leak: leaky=%v steady=%v %+v", leaky, steady, srv.mon.insights)
	}
}

// Control plane отдаёт состав кластера: узлы, включая рабочие, видны
// отдельными рядами с кластером, а поды — со своим узлом.
func TestMonitoringK8sNodes(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	if _, err := monitor.BackfillDemoHistory(ctx, db, 2); err != nil {
		t.Fatal(err)
	}
	srv.collectMonitoring(ctx)
	req := httptest.NewRequest(http.MethodGet, "/hub/monitoring/overview?range=7d", nil).
		WithContext(auth.WithUser(ctx, store.User{Username: "admin", Role: store.RoleAdmin}))
	w := httptest.NewRecorder()
	srv.handleMonitoringOverview(w, req)
	var out struct {
		Clusters []struct {
			Name  string `json:"name"`
			Nodes []struct {
				Name         string `json:"name"`
				ControlPlane bool   `json:"control_plane"`
			} `json:"nodes"`
		} `json:"clusters"`
		Workloads []struct {
			Source  string `json:"source"`
			Subject string `json:"subject"`
			Cluster string `json:"cluster"`
			Node    string `json:"node"`
		} `json:"workloads"`
		Hosts []struct {
			K8sRole string `json:"k8s_role"`
			K8sNode string `json:"k8s_node"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Clusters) != 1 || len(out.Clusters[0].Nodes) != 3 || !out.Clusters[0].Nodes[0].ControlPlane {
		t.Fatalf("clusters: %+v", out.Clusters)
	}
	var worker, pod bool
	for _, wl := range out.Workloads {
		if wl.Source == monitor.SourceK8sNode && wl.Subject == "lab-w-1" && wl.Cluster != "" {
			worker = true
		}
		if wl.Source == monitor.SourceK8s && wl.Subject == "shop/api-7c9d8-a1b2c" && wl.Node != "" {
			pod = true
		}
	}
	if len(out.Hosts) == 0 || out.Hosts[0].K8sRole != RoleControlPlane || out.Hosts[0].K8sNode != "lab-cp-1" {
		t.Fatalf("control plane не подписан: %+v", out.Hosts)
	}
	if !worker || !pod {
		t.Fatalf("workloads: worker=%v pod=%v %+v", worker, pod, out.Workloads)
	}
}
