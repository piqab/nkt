package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/store"
)

// Конвейер создания кластера как явный список шагов (cluster_steps.go):
// порядок, отметки «выполнено», зависимости, Force у разрушительных шагов,
// гейт выбора оператора и проверка конфликта зависимостей.

// stepsSpec — kubeadm-кластер 3 control plane + 1 worker на одном хосте,
// wireguard и общий адрес: самый полный набор шагов.
func stepsSpec() ClusterSpec {
	return ClusterSpec{
		Name: "k8s", HostID: 7, Flavor: k8s.FlavorKubeadm, Topology: TopologyCP3, Workers: 1,
		Endpoint: "api.example.com", NetworkMode: NetworkWireGuard, ImageID: "ubuntu-24",
		Addons: []ClusterAddon{{Name: "longhorn"}},
	}
}

func stepsNodes(spec ClusterSpec) ([]clusterNode, string) {
	nodes := spec.nodes(func(int64) string { return "hv" })
	return nodes, "k8s-cp-1"
}

func TestClusterStepList(t *testing.T) {
	spec := stepsSpec()
	nodes, cp1 := stepsNodes(spec)

	// Новый кластер: полный порядок, ничего не выполнено.
	steps := clusterStepList(spec, nodes, cp1, nil)
	var ids []string
	for _, st := range steps {
		ids = append(ids, st.ID)
		if st.Done {
			t.Errorf("%s: выполнен в новом кластере", st.ID)
		}
	}
	want := []string{"preflight", "mesh", "vm:k8s-cp-1", "vm:k8s-cp-2", "vm:k8s-cp-3", "vm:k8s-w-1",
		"listener", "cp1", "join:k8s-cp-2", "join:k8s-cp-3", "join:k8s-w-1", "ready", "addons"}
	if !slices.Equal(ids, want) {
		t.Fatalf("порядок шагов:\n got %v\nwant %v", ids, want)
	}
	byID := map[string]ClusterStep{}
	for _, st := range steps {
		byID[st.ID] = st
	}
	// Зависимости: машина — от туннеля, cp1 — от своей машины и слушателя,
	// вход — от cp1 и своей машины, ready — от cp1 и всех входов,
	// дополнения — от ready.
	for id, deps := range map[string][]string{
		"vm:k8s-cp-2":   {"mesh"},
		"cp1":           {"vm:k8s-cp-1", "listener"},
		"join:k8s-cp-2": {"cp1", "vm:k8s-cp-2"},
		"join:k8s-w-1":  {"cp1", "vm:k8s-w-1"},
		"ready":         {"cp1", "join:k8s-cp-2", "join:k8s-cp-3", "join:k8s-w-1"},
		"addons":        {"ready"},
	} {
		if !slices.Equal(byID[id].Deps, deps) {
			t.Errorf("%s: зависимости %v, надо %v", id, byID[id].Deps, deps)
		}
	}
	// Разрушительный повтор — у cp1 и входов; у машин и готовности его нет.
	for _, id := range []string{"cp1", "join:k8s-cp-2", "join:k8s-cp-3", "join:k8s-w-1"} {
		if !byID[id].Force {
			t.Errorf("%s: повтор не помечен разрушительным", id)
		}
	}
	for _, id := range []string{"preflight", "mesh", "vm:k8s-cp-1", "listener", "ready", "addons"} {
		if byID[id].Force {
			t.Errorf("%s: помечен разрушительным напрасно", id)
		}
	}
	if byID["cp1"].Node != "k8s-cp-1" || byID["join:k8s-w-1"].Node != "k8s-w-1" {
		t.Error("у по-узловых шагов нет имени узла")
	}

	// Состояние продолжения: cp-1 стоит, машины заведены, туннель поднят,
	// вход cp-2 «выполнен» (тот самый случай дефекта), дополнения нет.
	done := &clusterResume{
		MeshUp:    true,
		Hosts:     map[string]int64{"k8s-cp-1": 11, "k8s-cp-2": 12, "k8s-cp-3": 13, "k8s-w-1": 14},
		Installed: map[string]bool{"k8s-cp-1": true, "k8s-cp-2": true},
		Addons:    map[string]bool{},
	}
	steps = clusterStepList(spec, nodes, cp1, done)
	byID = map[string]ClusterStep{}
	for _, st := range steps {
		byID[st.ID] = st
	}
	for _, id := range []string{"preflight", "mesh", "vm:k8s-cp-1", "vm:k8s-cp-2", "vm:k8s-cp-3", "vm:k8s-w-1", "listener", "cp1", "join:k8s-cp-2"} {
		if !byID[id].Done {
			t.Errorf("%s: не выполнен при таком Resume", id)
		}
	}
	for _, id := range []string{"join:k8s-cp-3", "join:k8s-w-1", "ready", "addons"} {
		if byID[id].Done {
			t.Errorf("%s: выполнен напрасно", id)
		}
	}

	// Дополнения выполнены, когда установлены все.
	done.Addons["longhorn:install"] = true
	done.Installed["k8s-cp-3"] = true
	done.Installed["k8s-w-1"] = true
	steps = clusterStepList(spec, nodes, cp1, done)
	for _, st := range steps {
		if st.ID == "ready" && !st.Done {
			t.Error("ready: все узлы стоят, а шаг не выполнен")
		}
		if st.ID == "addons" && !st.Done {
			t.Error("addons: дополнение установлено, а шаг не выполнен")
		}
	}

	// Без wireguard и без общего адреса: нет шага туннеля и слушателя,
	// у машин нет зависимости.
	spec.NetworkMode, spec.Endpoint = NetworkNAT, ""
	steps = clusterStepList(spec, nodes, cp1, nil)
	ids = nil
	for _, st := range steps {
		ids = append(ids, st.ID)
		if st.ID == "vm:k8s-w-1" && len(st.Deps) != 0 {
			t.Errorf("vm без туннеля зависит от %v", st.Deps)
		}
		if st.ID == "cp1" && !slices.Equal(st.Deps, []string{"vm:k8s-cp-1"}) {
			t.Errorf("cp1 без слушателя зависит от %v", st.Deps)
		}
	}
	if slices.Contains(ids, "mesh") || slices.Contains(ids, "listener") {
		t.Errorf("лишние шаги в nat без endpoint: %v", ids)
	}

	// Отметка Listener независима от cp-1: init упал после слушателя —
	// окно видит слушатель выполненным, а cp1 нет.
	dl := &clusterResume{Hosts: map[string]int64{"k8s-cp-1": 11}, Installed: map[string]bool{}, Listener: true}
	for _, st := range clusterStepList(stepsSpec(), nodes, cp1, dl) {
		if st.ID == "listener" && !st.Done {
			t.Error("listener: отметка в Resume есть, а шаг не выполнен")
		}
		if st.ID == "cp1" && st.Done {
			t.Error("cp1 выполнен только потому, что разложен слушатель")
		}
	}
}

// Гейт выбора оператора: бежим ровно по отмеченным; отмеченный И уже
// выполненный разрушительный шаг идёт с Force; Resume больше не решает,
// что пропустить.
func TestClusterGateSelection(t *testing.T) {
	spec := stepsSpec()
	nodes, cp1 := stepsNodes(spec)
	done := &clusterResume{
		MeshUp:    true,
		Hosts:     map[string]int64{"k8s-cp-1": 11, "k8s-cp-2": 12, "k8s-cp-3": 13, "k8s-w-1": 14},
		Installed: map[string]bool{"k8s-cp-1": true, "k8s-cp-2": true},
	}
	r := &ClusterRunner{}
	ctx := context.Background()

	// Повтор только входа cp-2 (прошлый раз «выполнился», но узел не стал
	// участником): cp1 и машины не трогаются, вход — принудительно.
	g := r.clusterBuildGate(ctx, spec, nodes, cp1, ClusterJobParams{Steps: []string{"join:k8s-cp-2"}}, done)
	if !g.selection {
		t.Error("выбор оператора не признан")
	}
	if !g.run["join:k8s-cp-2"] {
		t.Error("отмеченный шаг не бежит")
	}
	if !g.force["join:k8s-cp-2"] {
		t.Error("повтор выполненного входа не принудительный")
	}
	for _, id := range []string{"preflight", "mesh", "vm:k8s-cp-1", "listener", "cp1", "join:k8s-cp-3", "ready", "addons"} {
		if g.run[id] {
			t.Errorf("%s: бежит, хотя не отмечен", id)
		}
	}

	// Вход cp-3 (ещё не выполнен): force нет — обычный вход.
	g = r.clusterBuildGate(ctx, spec, nodes, cp1, ClusterJobParams{Steps: []string{"join:k8s-cp-3"}}, done)
	if !g.run["join:k8s-cp-3"] || g.force["join:k8s-cp-3"] {
		t.Errorf("cp-3: run=%v force=%v", g.run["join:k8s-cp-3"], g.force["join:k8s-cp-3"])
	}

	// Повтор cp1 — тоже принудительный; forceInstallSpec переносит Force
	// в спецификацию установки.
	g = r.clusterBuildGate(ctx, spec, nodes, cp1, ClusterJobParams{Steps: []string{"cp1"}}, done)
	if !g.force["cp1"] {
		t.Error("повтор выполненного cp1 не принудительный")
	}
	is := g.forceInstallSpec("cp1", k8s.InstallSpec{Flavor: k8s.FlavorKubeadm})
	if !is.Force {
		t.Error("Force не доехал до InstallSpec")
	}
	if is2 := g.forceInstallSpec("ready", k8s.InstallSpec{}); is2.Force {
		t.Error("Force поставлен шагу, который не помечен")
	}

	// Пустой выбор (Steps: []) — ничего не бежит: оператор снял всё.
	g = r.clusterBuildGate(ctx, spec, nodes, cp1, ClusterJobParams{Steps: []string{}}, done)
	for id, run := range g.run {
		if run {
			t.Errorf("%s: бежит при пустом выборе", id)
		}
	}
}

// Cilium и балансировщик в списке шагов: CNI ставится между входами и
// готовностью (без CNI узлы легально NotReady), машины балансировщика не
// входят в кластер — у них нет шагов входа, и готовность их не ждёт.
func TestClusterStepListCiliumAndLB(t *testing.T) {
	spec := stepsSpec()
	spec.CNI = "cilium"
	spec.LBEnabled = true
	spec.LBCount = 1
	spec.LBPlacements = []Placement{{HostID: 9, Role: RoleLoadBalancer, Kind: KindVM, Count: 1}}
	nodes, cp1 := stepsNodes(spec)

	steps := clusterStepList(spec, nodes, cp1, nil)
	var ids []string
	for _, st := range steps {
		ids = append(ids, st.ID)
	}
	want := []string{"preflight", "mesh",
		"vm:k8s-lb-1", "vm:k8s-cp-1", "vm:k8s-cp-2", "vm:k8s-cp-3", "vm:k8s-w-1",
		"listener", "lb", "cp1", "join:k8s-cp-2", "join:k8s-cp-3", "join:k8s-w-1",
		"cilium", "ready", "addons"}
	if !slices.Equal(ids, want) {
		t.Fatalf("порядок шагов:\n got %v\nwant %v", ids, want)
	}
	// Машина балансировщика не входит в кластер: шага входа у неё нет.
	for _, id := range ids {
		if id == "join:k8s-lb-1" {
			t.Fatalf("у машины балансировщика есть шаг входа: %v", ids)
		}
	}
	byID := map[string]ClusterStep{}
	for _, st := range steps {
		byID[st.ID] = st
	}
	// Cilium зависит от cp1 и всех входов; готовность — ещё и от cilium.
	wantCiliumDeps := []string{"cp1", "join:k8s-cp-2", "join:k8s-cp-3", "join:k8s-w-1"}
	if !slices.Equal(byID["cilium"].Deps, wantCiliumDeps) {
		t.Errorf("cilium: зависимости %v, надо %v", byID["cilium"].Deps, wantCiliumDeps)
	}
	wantReadyDeps := []string{"cp1", "join:k8s-cp-2", "join:k8s-cp-3", "join:k8s-w-1", "cilium"}
	if !slices.Equal(byID["ready"].Deps, wantReadyDeps) {
		t.Errorf("ready: зависимости %v, надо %v", byID["ready"].Deps, wantReadyDeps)
	}
	// Без отметок ничего не выполнено, в том числе cilium и lb.
	if byID["cilium"].Done || byID["lb"].Done {
		t.Errorf("cilium/lb выполнены в новом кластере")
	}
	// Отметка Resume: cilium выполнен — шаг снят в окне повтора.
	done := &clusterResume{Hosts: map[string]int64{}, Installed: map[string]bool{}, Cilium: true}
	steps = clusterStepList(spec, nodes, cp1, done)
	for _, st := range steps {
		if st.ID == "cilium" && !st.Done {
			t.Errorf("cilium не выполнен при отметке Resume")
		}
	}
}

// Прежний режим (Steps == nil): пропуск решает Resume — старые задания,
// пополнение worker'ами и сухой прогон продолжают работать как раньше.
func TestClusterGateLegacy(t *testing.T) {
	spec := stepsSpec()
	spec.NetworkMode = NetworkNAT
	nodes, cp1 := stepsNodes(spec)
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db}
	r := NewClusterRunner(s)
	ctx := context.Background()

	// Ничего не сделано — бежит всё.
	done := &clusterResume{Hosts: map[string]int64{}, Installed: map[string]bool{}}
	g := r.clusterBuildGate(ctx, spec, nodes, cp1, ClusterJobParams{}, done)
	for _, id := range []string{"preflight", "vm:k8s-cp-1", "cp1", "join:k8s-cp-2", "ready", "addons"} {
		if !g.run[id] {
			t.Errorf("%s: не бежит на пустом Resume", id)
		}
	}
	if g.selection {
		t.Error("пустой Params.Steps принят за выбор оператора")
	}

	// cp-1 стоит, машина cp-2 жива: cp1 пропускается, машина cp-2 не
	// пересоздаётся, вход cp-2 пропускается (по Resume — прежний порядок),
	// а cp-3 бежит.
	h, err := db.CreateHost(ctx, "k8s-cp-2", "10.0.0.12", 22, "root", store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	done = &clusterResume{
		Hosts:     map[string]int64{"k8s-cp-1": 11, "k8s-cp-2": h},
		Installed: map[string]bool{"k8s-cp-1": true, "k8s-cp-2": true},
	}
	g = r.clusterBuildGate(ctx, spec, nodes, cp1, ClusterJobParams{}, done)
	if g.run["cp1"] {
		t.Error("cp1 бежит, хотя установлен")
	}
	if g.run["vm:k8s-cp-2"] {
		t.Error("машина cp-2 пересоздаётся при живой записи")
	}
	if !g.run["vm:k8s-cp-3"] {
		t.Error("машина cp-3 не создаётся")
	}
	if g.run["join:k8s-cp-2"] {
		t.Error("вход cp-2 бежит по Resume — прежний режим сохранён")
	}
	if !g.run["join:k8s-cp-3"] {
		t.Error("вход cp-3 не бежит")
	}
}

func TestClusterCheckSelection(t *testing.T) {
	spec := stepsSpec()
	nodes, cp1 := stepsNodes(spec)
	done := &clusterResume{
		MeshUp:    true,
		Hosts:     map[string]int64{"k8s-cp-1": 11, "k8s-cp-2": 12, "k8s-cp-3": 13, "k8s-w-1": 14},
		Installed: map[string]bool{"k8s-cp-1": true},
	}
	steps := clusterStepList(spec, nodes, cp1, done)

	// Пустой выбор — отказ.
	if err := clusterCheckSelection(steps, nil); err == nil {
		t.Error("пустой выбор принят")
	}
	// Неизвестный шаг — отказ.
	err := clusterCheckSelection(steps, []string{"join:k8s-cp-2", "vm:нет-такой"})
	if err == nil || !strings.Contains(err.Error(), "vm:нет-такой") {
		t.Errorf("неизвестный шаг: %v", err)
	}
	// Вход без cp1, когда cp1 не отмечен и не выполнен — конфликт.
	done2 := &clusterResume{MeshUp: true, Hosts: done.Hosts, Installed: map[string]bool{}, Listener: true}
	steps2 := clusterStepList(spec, nodes, cp1, done2)
	err = clusterCheckSelection(steps2, []string{"join:k8s-cp-2"})
	if err == nil || !strings.Contains(err.Error(), "join:k8s-cp-2") {
		t.Errorf("конфликт не найден: %v", err)
	}
	// Тот же выбор, но cp1 выполнен — не конфликт.
	if err := clusterCheckSelection(steps, []string{"join:k8s-cp-2"}); err != nil {
		t.Errorf("вход при готовом cp1 отбит: %v", err)
	}
	// Отмеченная зависимость снимает конфликт: цепочка «вход cp-2» при
	// неготовом cp1 — слушатель разложен (Listener), туннель поднят, машина
	// есть; не хватает только cp1 и самой машины cp-2 в выборе… машина
	// выполнена, cp1 отмечаем.
	if err := clusterCheckSelection(steps2, []string{"cp1", "join:k8s-cp-2"}); err != nil {
		t.Errorf("полная цепочка отбита: %v", err)
	}
	// Без слушателя в Resume и без него в выборе cp1 конфликтует.
	doneNL := &clusterResume{MeshUp: true, Hosts: done.Hosts, Installed: map[string]bool{}}
	stepsNL := clusterStepList(spec, nodes, cp1, doneNL)
	err = clusterCheckSelection(stepsNL, []string{"cp1"})
	if err == nil || !strings.Contains(err.Error(), "cp1") {
		t.Errorf("cp1 без слушателя принят: %v", err)
	}
	// validateClusterSteps: машина без туннеля, когда туннель не поднят и
	// не отмечен.
	done3 := &clusterResume{Hosts: map[string]int64{}, Installed: map[string]bool{}}
	steps3 := clusterStepList(spec, nodes, cp1, done3)
	conflicts := validateClusterSteps(steps3, map[string]bool{"vm:k8s-cp-1": true})
	if !slices.Equal(conflicts, []string{"vm:k8s-cp-1"}) {
		t.Errorf("конфликты: %v", conflicts)
	}
}

// GET /hub/clusters/{id}/steps: чек-лист последнего задания с отметками
// «выполнено»; задание ищется разбором Params, а не подстрокой с запятой
// (единственное поле маршалится без неё — старый поиск возвращал пусто).
func TestClusterStepsHandler(t *testing.T) {
	s, _, hosts := listenerFixture(t, "203.0.113.21")
	ctx := context.Background()

	spec := stepsSpec()
	spec.HostID = hosts[0]
	spec.NetworkMode = NetworkNAT
	spec.Addons = nil
	raw, _ := json.Marshal(spec)
	clID, err := s.db.CreateCluster(ctx, store.Cluster{Name: spec.Name, HostID: hosts[0], Flavor: spec.Flavor, Topology: spec.Topology, Workers: 1, SpecJSON: string(raw)})
	if err != nil {
		t.Fatal(err)
	}

	// Задание: params без запятой (одно поле), упало, Resume — cp-1 стоит.
	resume, _ := json.Marshal(clusterResume{
		Hosts:     map[string]int64{"k8s-cp-1": hosts[0], "k8s-cp-2": hosts[0], "k8s-cp-3": hosts[0], "k8s-w-1": hosts[0]},
		Installed: map[string]bool{"k8s-cp-1": true},
	})
	jobID, err := s.db.CreateJob(ctx, store.Job{
		Kind: KindClusterCreate, Title: "кластер", Queue: "cluster:1", Status: store.JobFailed,
		Params: `{"cluster_id":` + itoa64(clID) + `}`, Resume: string(resume), Steps: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Сухой прогон того же кластера — не он.
	if _, err := s.db.CreateJob(ctx, store.Job{
		Kind: KindClusterCreate, Title: "dry", Queue: "cluster-dry:1", Status: store.JobFailed,
		Params: `{"cluster_id":` + itoa64(clID) + `,"dry_run":true}`, Steps: 1,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hub/clusters/"+itoa64(clID)+"/steps", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", itoa64(clID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	s.handleClusterSteps(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		JobID    int64         `json:"job_id"`
		Steps    []ClusterStep `json:"steps"`
		CanRetry bool          `json:"can_retry"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.JobID != jobID {
		t.Errorf("задание %d, надо %d (подстрока с запятой его бы не нашла)", out.JobID, jobID)
	}
	if !out.CanRetry {
		t.Error("упавшее задание не продолжить")
	}
	byID := map[string]ClusterStep{}
	for _, st := range out.Steps {
		byID[st.ID] = st
	}
	if !byID["cp1"].Done || !byID["vm:k8s-cp-1"].Done {
		t.Errorf("выполненные шаги не отмечены: %s", rec.Body.String())
	}
	if byID["join:k8s-cp-2"].Done {
		t.Error("невыполненный вход отмечен")
	}
	if _, ok := byID["mesh"]; ok {
		t.Error("шаг туннеля в nat-режиме")
	}
	if _, ok := byID["addons"]; ok {
		t.Error("шаг дополнений без дополнений")
	}

	// Предпросмотр создания: все шаги не выполнены.
	body, _ := json.Marshal(spec)
	rec = httptest.NewRecorder()
	s.handleClusterStepsPreview(rec, httptest.NewRequest(http.MethodPost, "/hub/clusters/steps-preview", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: код %d: %s", rec.Code, rec.Body.String())
	}
	out.Steps = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Steps) == 0 {
		t.Fatal("preview: шагов нет")
	}
	for _, st := range out.Steps {
		if st.Done {
			t.Errorf("preview: %s выполнен", st.ID)
		}
	}
}

// «Продолжить» с выбором: конфликтный, неизвестный и пустой выбор бэкенд
// отбивает до заведения задания (окно — не единственный вход).
func TestClusterRetryValidation(t *testing.T) {
	s, _, hosts := listenerFixture(t, "203.0.113.21")
	ctx := context.Background()
	spec := stepsSpec()
	spec.HostID = hosts[0]
	spec.NetworkMode = NetworkNAT
	raw, _ := json.Marshal(spec)
	clID, err := s.db.CreateCluster(ctx, store.Cluster{Name: spec.Name, HostID: hosts[0], Flavor: spec.Flavor, Topology: spec.Topology, Workers: 1, SpecJSON: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.CreateJob(ctx, store.Job{
		Kind: KindClusterCreate, Title: "кластер", Queue: "cluster:1", Status: store.JobFailed,
		Params: `{"cluster_id":` + itoa64(clID) + `}`, Steps: 10,
	}); err != nil {
		t.Fatal(err)
	}
	post := func(bodyJSON string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/hub/clusters/"+itoa64(clID)+"/retry", strings.NewReader(bodyJSON))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", itoa64(clID))
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		s.handleClusterRetry(rec, req)
		return rec.Code
	}
	if code := post(`{"steps":["vm:нет-такой"]}`); code != http.StatusBadRequest {
		t.Errorf("неизвестный шаг: код %d", code)
	}
	if code := post(`{"steps":[]}`); code != http.StatusBadRequest {
		t.Errorf("пустой выбор: код %d", code)
	}
	// cp1 не выполнен (Resume пуст) — вход без него конфликтен.
	if code := post(`{"steps":["join:k8s-cp-2"]}`); code != http.StatusBadRequest {
		t.Errorf("конфликт: код %d", code)
	}
}
