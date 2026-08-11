package parse

import (
	"context"
	"testing"
)

func TestPodmanListsContainersAndPods(t *testing.T) {
	res := Podman(context.Background(), fixtureCollector(t))
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}
	if !res.Status.Available {
		t.Fatal("Status.Available = false, ожидалось true")
	}

	byName := map[string]bool{}
	for _, ct := range res.Containers {
		byName[ct.Name] = true
	}
	for _, want := range []string{"monitoring-grafana", "monitoring-prometheus", "adhoc-backup"} {
		if !byName[want] {
			t.Errorf("контейнер %s не найден среди %v", want, byName)
		}
	}
	if len(res.Containers) != 3 {
		t.Fatalf("len(Containers) = %d, ожидалось 3", len(res.Containers))
	}

	for _, ct := range res.Containers {
		if ct.Name != "monitoring-grafana" {
			continue
		}
		if ct.Pod != "monitoring" {
			t.Errorf("Pod = %q, ожидалось \"monitoring\"", ct.Pod)
		}
		if ct.State != "running" {
			t.Errorf("State = %q, ожидалось running", ct.State)
		}
		if len(ct.Ports) != 1 || ct.Ports[0].HostPort != 3000 {
			t.Errorf("Ports = %+v, ожидался один маппинг на 3000", ct.Ports)
		}
	}

	for _, ct := range res.Containers {
		if ct.Name == "adhoc-backup" && ct.Pod != "" {
			t.Errorf("adhoc-backup не должен принадлежать поду, получено %q", ct.Pod)
		}
	}
}
