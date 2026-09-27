package k8s

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

func fixtureManager() *Manager {
	return New(collect.NewFixtures(filepath.Join("..", "..", "fixtures", "host")), nil)
}

func TestResourcesFromFixtures(t *testing.T) {
	m := fixtureManager()
	ctx := context.Background()
	for kind := range Kinds {
		res, err := m.Resources(ctx, kind, "")
		if err != nil {
			t.Errorf("%s: %v", kind, err)
			continue
		}
		if len(res.Rows) == 0 {
			t.Errorf("%s: пусто", kind)
		}
	}
	deps, _ := m.Resources(ctx, "deployments", "shop")
	if len(deps.Rows) != 1 || deps.Rows[0].Name != "api" || deps.Rows[0].Cols["ready"] != "2/3" || deps.Rows[0].Status != "warn" {
		t.Errorf("deployments shop: %+v", deps.Rows)
	}
	// Фильтр namespace не трогает кластерные виды.
	if nodes, _ := m.Resources(ctx, "nodes", "shop"); len(nodes.Rows) != 3 {
		t.Errorf("nodes: %d", len(nodes.Rows))
	}
	sec, _ := m.Resources(ctx, "secrets", "shop")
	if sec.Rows[0].Cols["keys"] != "password, username" {
		t.Errorf("secret keys: %+v", sec.Rows[0])
	}
	for _, r := range sec.Rows {
		for _, v := range r.Cols {
			if strings.Contains(v, "ZGVtby1TM2NyZXQh") || strings.Contains(v, "demo-S3cret") {
				t.Fatal("значение секрета в списке")
			}
		}
	}
	svc, _ := m.Resources(ctx, "services", "kube-system")
	if svc.Rows[0].Cols["external"] != "192.168.122.31" || !strings.Contains(svc.Rows[0].Cols["ports"], "443:31443/TCP") {
		t.Errorf("service: %+v", svc.Rows[0].Cols)
	}
	if _, err := m.Resources(ctx, "rm -rf", ""); err == nil {
		t.Error("неизвестный вид принят")
	}
}

func TestCustomResources(t *testing.T) {
	m := fixtureManager()
	ctx := context.Background()
	crds, err := m.CRDs(ctx)
	if err != nil || len(crds) != 3 {
		t.Fatalf("%v %+v", err, crds)
	}
	var cert CRD
	for _, c := range crds {
		if c.Name == "certificates.cert-manager.io" {
			cert = c
		}
	}
	// Ready с фильтром [?()] пропущен, Secret и Age (Age — своей колонкой) нет.
	if len(cert.Columns) != 1 || cert.Columns[0].Label != "Secret" {
		t.Fatalf("columns: %+v", cert.Columns)
	}
	res, err := m.Resources(ctx, "cr:certificates.cert-manager.io", "shop")
	if err != nil || len(res.Rows) != 1 || res.Rows[0].Status != "error" || res.Rows[0].Cols["pc0"] != "api-tls" {
		t.Fatalf("%v %+v", err, res.Rows)
	}
	if _, err := m.Resources(ctx, "cr:nonexistent.example.com", ""); err == nil {
		t.Error("несуществующий CRD принят")
	}
}

func TestSecretData(t *testing.T) {
	m := fixtureManager()
	data, err := m.Data(context.Background(), "secrets", "shop", "db-credentials")
	if err != nil || data["password"] != "demo-S3cret!" || data["username"] != "shop" {
		t.Fatalf("%v %v", err, data)
	}
	if _, err := m.Data(context.Background(), "secrets", "shop", "nope"); err == nil {
		t.Error("нет такого секрета")
	}
	if _, err := m.Data(context.Background(), "pods", "shop", "x"); err == nil {
		t.Error("вид не проверен")
	}
}

func TestTopColumns(t *testing.T) {
	m := fixtureManager()
	pods, err := m.Resources(context.Background(), "pods", "shop")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range pods.Rows {
		if r.Name == "api-7c9d8-a1b2c" && r.Cols["cpu"] == "48m" && r.Cols["memory"] == "182Mi" {
			found = true
		}
	}
	if !found {
		t.Errorf("нет top у подов: %+v", pods.Rows)
	}
	nodes, _ := m.Resources(context.Background(), "nodes", "")
	for _, r := range nodes.Rows {
		if r.Name == "lab-cp-1" && r.Cols["cpu"] != "412m (20%)" {
			t.Errorf("top узла: %q", r.Cols["cpu"])
		}
	}
}
