package k8s

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestActFromFixtures(t *testing.T) {
	m := fixtureManager()
	ctx := context.Background()
	if _, err := m.Act(ctx, ActionRequest{Kind: "deployments", Namespace: "shop", Name: "api", Action: "scale", Replicas: 2}); err != nil {
		t.Fatalf("scale: %v", err)
	}
	// Объекта нет в листинге — до kubectl не доходит.
	if _, err := m.Act(ctx, ActionRequest{Kind: "deployments", Namespace: "shop", Name: "api; rm -rf /", Action: "restart"}); err == nil {
		t.Error("чужое имя принято")
	}
	if _, err := m.Act(ctx, ActionRequest{Kind: "deployments", Namespace: "default", Name: "api", Action: "restart"}); err == nil {
		t.Error("другой namespace принят")
	}
	if _, err := m.Act(ctx, ActionRequest{Kind: "pods", Namespace: "shop", Name: "x", Action: "scale"}); err == nil {
		t.Error("scale пода принят")
	}
	if _, err := m.Act(ctx, ActionRequest{Kind: "deployments", Namespace: "shop", Name: "api", Action: "scale", Replicas: MaxReplicas + 1}); err == nil {
		t.Error("реплик больше предела")
	}
	if _, err := m.Act(ctx, ActionRequest{Kind: "nodes", Name: "lab-w-2", Action: "cordon"}); err != nil {
		t.Errorf("cordon: %v", err)
	}
	if _, err := m.CreateNamespace(ctx, "Bad_Name"); err == nil {
		t.Error("недопустимое имя namespace принято")
	}
	txt, err := m.RolloutHistory(ctx, "deployments", "shop", "api")
	if err != nil || !strings.Contains(txt, "REVISION") {
		t.Errorf("history: %q %v", txt, err)
	}
}

func TestFindPodContainers(t *testing.T) {
	m := fixtureManager()
	pods, err := m.Resources(context.Background(), "pods", "")
	if err != nil || len(pods.Rows) == 0 {
		t.Fatalf("pods: %v", err)
	}
	p := pods.Rows[0]
	tg, err := m.Find(context.Background(), "pods", p.Namespace, p.Name)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := tg.Container(""); !ok || c == "" {
		t.Errorf("нет контейнера по умолчанию: %v", tg.Containers())
	}
	if _, ok := tg.Container("nope"); ok {
		t.Error("чужой контейнер принят")
	}
	args := LogsArgs(tg, "c", 50, true, false)
	if args[0] != "logs" || !strings.Contains(strings.Join(args, " "), "--tail=50 --timestamps -f") {
		t.Errorf("logs args: %v", args)
	}
}

func TestManualJobName(t *testing.T) {
	now := time.Unix(1790000000, 0)
	if got := ManualJobName("backup", now); got != "backup-manual-0000" && !strings.HasPrefix(got, "backup-manual-") {
		t.Errorf("имя: %q", got)
	}
	long := strings.Repeat("a", 70)
	if got := ManualJobName(long, now); len(got) > 63 {
		t.Errorf("длина %d", len(got))
	}
}

func TestRBACAndHPA(t *testing.T) {
	m := fixtureManager()
	ctx := context.Background()
	sa, err := m.Resources(ctx, "serviceaccounts", "shop")
	if err != nil {
		t.Fatal(err)
	}
	var ci, def bool
	for _, r := range sa.Rows {
		if r.Name == "ci-deployer" && r.Cols["bound_roles"] == "cluster-admin (cluster)" && r.Status == "warn" {
			ci = true
		}
		if r.Name == "default" && r.Cols["bound_roles"] == "app-reader" {
			def = true
		}
	}
	if !ci || !def {
		t.Errorf("роли SA: %+v", sa.Rows)
	}
	hpa, _ := m.Resources(ctx, "hpa", "shop")
	if len(hpa.Rows) != 1 || hpa.Rows[0].Cols["metrics"] != "cpu 41%/70%" || hpa.Rows[0].Cols["target"] != "Deployment/api" {
		t.Errorf("hpa: %+v", hpa.Rows)
	}
	if _, err := m.Act(ctx, ActionRequest{Kind: "hpa", Namespace: "shop", Name: "api", Action: "bounds", Min: 2, Max: 8}); err != nil {
		t.Errorf("bounds: %v", err)
	}
	if _, err := m.Act(ctx, ActionRequest{Kind: "hpa", Namespace: "shop", Name: "api", Action: "bounds", Min: 5, Max: 2}); err == nil {
		t.Error("max < min принят")
	}
}
