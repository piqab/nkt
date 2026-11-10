package hub

import (
	"context"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Конвейер создания кластера как явный список действий, который оператор
// видит и правит в окне шагов (см. web/src/components/ClusterSteps.tsx).
//
// Раньше «что пропустить» решал непрозрачный Resume задания: шаг не
// повторялся, если в Resume стояла отметка «сделано», и оператор не мог ни
// увидеть эти отметки, ни поправить их. Отсюда дефект: вход cp-2, который в
// прошлый раз отчитался успехом по локальному kubelet.conf, но участником
// etcd так и не стал, помечался сделанным, и «попробовать снова» шло дальше —
// к дополнениям, а не к повторному входу.
//
// Теперь список шагов явный. Окно создания показывает все шаги отмеченными;
// окно повтора — с выполненными снятыми, и оператор может отметить их заново
// (тогда шаг исполняется принудительно, с Force-сбросом узла). Исполнитель
// бежит ровно по отмеченным шагам; Resume остаётся источником данных (id
// машин, адреса), но решение «бежать или нет» принимает выбор оператора.

// Идентификаторы одиночных шагов. По-узловые шаги несут имя узла:
// «vm:<имя>», «join:<имя>».
const (
	stepPreflight = "preflight"
	stepMesh      = "mesh"
	stepListener  = "listener"
	stepLB        = "lb"
	stepCP1       = "cp1"
	stepReady     = "ready"
	stepCilium    = "cilium"
	stepAddons    = "addons"
)

func vmStepID(name string) string   { return "vm:" + name }
func joinStepID(name string) string { return "join:" + name }

// ClusterStep — одно действие конвейера для окна шагов.
type ClusterStep struct {
	// ID — уникальный идентификатор; по нему исполнитель и окно понимают
	// друг друга («vm:k8s-cp-2», «cp1», «addons»).
	ID string `json:"id"`
	// Kind — тип шага для подписи в окне: preflight|mesh|vm|listener|cp1|
	// join|ready|addons. Подпись берёт i18n фронта по Kind, аргумент — Node.
	Kind string `json:"kind"`
	// Node — имя узла для по-узловых шагов (vm, join) и для cp1.
	Node string `json:"node,omitempty"`
	// Done — шаг уже выполнен по состоянию кластера (Resume задания или
	// записи машин). В окне повтора такие сняты; отметив их заново,
	// оператор запускает принудительный повтор.
	Done bool `json:"done"`
	// Deps — жёсткие зависимости: идентификаторы шагов, без которых этот
	// шаг не имеет смысла (отмеченный шаг с неудовлетворённой зависимостью
	// окно подсвечивает конфликтом). Зависимость считается удовлетворённой,
	// если она отмечена ИЛИ уже выполнена.
	Deps []string `json:"deps,omitempty"`
	// Force — повтор этого шага разрушителен: узел сбрасывается и роль
	// ставится заново (kubeadm reset / удаление k3s). Окно предупреждает об
	// этом, когда оператор отмечает уже выполненный такой шаг.
	Force bool `json:"force,omitempty"`
}

// clusterResolveNodes разворачивает размещение в узлы и находит имя первого
// control plane — ровно так, как это делает исполнитель (включая ветку
// пополнения worker'ами). Общий код, чтобы окно шагов и исполнитель не
// расходились в именах узлов.
func clusterResolveNodes(ctx context.Context, db *store.DB, cl store.Cluster, spec ClusterSpec, p ClusterJobParams) (nodes []clusterNode, cp1Name string, err error) {
	hostName := func(id int64) string {
		if h, e := db.HostByID(ctx, id); e == nil {
			return h.Name
		}
		return fmt.Sprintf("host-%d", id)
	}
	nodes = spec.nodes(hostName)
	if p.AddWorkers > 0 {
		existing, _ := db.ClusterHosts(ctx, cl.ID)
		n := 0
		for _, h := range existing {
			if h.K8sRole == RoleWorker {
				n++
			}
		}
		tmpl := nodes[0]
		for _, nd := range nodes {
			if nd.Role == RoleWorker && nd.Kind == KindVM {
				tmpl = nd
			}
		}
		nodes = nil
		for i := n + 1; i <= n+p.AddWorkers; i++ {
			nd := tmpl
			nd.Name, nd.Role, nd.Kind = fmt.Sprintf("%s-w-%d", spec.Name, i), RoleWorker, KindVM
			nodes = append(nodes, nd)
		}
	}
	var cps []clusterNode
	for _, nd := range nodes {
		if nd.Role == RoleControlPlane {
			cps = append(cps, nd)
		}
	}
	if len(cps) > 0 {
		cp1Name = cps[0].Name
	} else if existing, _ := db.ClusterHosts(ctx, cl.ID); len(existing) > 0 {
		cp1Name = existing[0].Name
	} else if len(nodes) > 0 {
		cp1Name = nodes[0].Name
	} else {
		return nil, "", fmt.Errorf("no nodes")
	}
	return nodes, cp1Name, nil
}

// clusterStepList строит упорядоченный список шагов конвейера для окна.
// done — состояние продолжения (может быть нулевым для нового создания).
func clusterStepList(spec ClusterSpec, nodes []clusterNode, cp1Name string, done *clusterResume) []ClusterStep {
	if done == nil {
		done = &clusterResume{Hosts: map[string]int64{}, Installed: map[string]bool{}}
	}
	if done.Hosts == nil {
		done.Hosts = map[string]int64{}
	}
	if done.Installed == nil {
		done.Installed = map[string]bool{}
	}
	out := []ClusterStep{}
	// Проверки — до первой машины. Считаем выполненными, если машины уже
	// заведены (значит, проверки в прошлый раз прошли).
	out = append(out, ClusterStep{ID: stepPreflight, Kind: "preflight", Done: len(done.Hosts) > 0})
	// Туннель между хостами — только в режиме wireguard.
	if spec.NetworkMode == NetworkWireGuard {
		out = append(out, ClusterStep{ID: stepMesh, Kind: "mesh", Done: done.MeshUp})
	}
	// Машина каждого узла.
	for _, nd := range nodes {
		var deps []string
		if spec.NetworkMode == NetworkWireGuard {
			deps = append(deps, stepMesh)
		}
		out = append(out, ClusterStep{
			ID: vmStepID(nd.Name), Kind: "vm", Node: nd.Name,
			Done: done.Hosts[nd.Name] != 0, Deps: deps,
		})
	}
	// Слушатель общего адреса — только при заданном Endpoint. Выполнен, если
	// разложен хабом (отметка Listener в Resume; у старых заданий её нет —
	// тогда признаком служит установленный cp-1: init идёт после слушателя и
	// без живого адреса не проходит).
	if spec.Endpoint != "" {
		out = append(out, ClusterStep{ID: stepListener, Kind: "listener", Done: done.Listener || done.Installed[cp1Name]})
	}
	// Балансировщик — установка HAProxy (+ keepalived при HA-паре) на машинах
	// lb-1, lb-2. Зависит от всех vm:cp-* (адреса cp нужны для HAProxy-конфига).
	// Выполнен, если в Resume есть отметка LB.
	if spec.LBEnabled {
		var lbDeps []string
		for _, nd := range nodes {
			if nd.Role == RoleControlPlane {
				lbDeps = append(lbDeps, vmStepID(nd.Name))
			}
		}
		out = append(out, ClusterStep{ID: stepLB, Kind: "lb", Done: done.LB, Deps: lbDeps})
	}
	// Первый control plane: init + CNI. Зависит от своей машины, (при общем
	// адресе) от слушателя, и (при LB) от шага lb. Повтор разрушителен — Force.
	cp1Deps := []string{vmStepID(cp1Name)}
	if spec.Endpoint != "" {
		cp1Deps = append(cp1Deps, stepListener)
	}
	if spec.LBEnabled {
		cp1Deps = append(cp1Deps, stepLB)
	}
	out = append(out, ClusterStep{
		ID: stepCP1, Kind: "cp1", Node: cp1Name,
		Done: done.Installed[cp1Name], Deps: cp1Deps, Force: true,
	})
	// Присоединение остальных узлов: сначала control plane, потом worker'ы
	// (nodes уже в этом порядке). Каждый зависит от cp1 и от своей машины.
	for _, nd := range nodes {
		if nd.Name == cp1Name {
			continue
		}
		out = append(out, ClusterStep{
			ID: joinStepID(nd.Name), Kind: "join", Node: nd.Name,
			Done: done.Installed[nd.Name], Deps: []string{stepCP1, vmStepID(nd.Name)}, Force: true,
		})
	}
	// Готовность, проброс, kubeconfig. Зависит от cp1 и всех входов.
	readyDeps := []string{stepCP1}
	allInstalled := done.Installed[cp1Name]
	for _, nd := range nodes {
		if nd.Name == cp1Name {
			continue
		}
		readyDeps = append(readyDeps, joinStepID(nd.Name))
		if !done.Installed[nd.Name] {
			allInstalled = false
		}
	}
	out = append(out, ClusterStep{ID: stepReady, Kind: "ready", Done: allInstalled, Deps: readyDeps})
	// Cilium — отдельный шаг после ready (все ноды присоединены). Ставится
	// только если выбран в форме (spec.CNI == "cilium"). Зависит от ready.
	if spec.CNI == "cilium" {
		out = append(out, ClusterStep{
			ID: stepCilium, Kind: "cilium", Done: done.Cilium,
			Deps: []string{stepReady},
		})
	}
	// Дополнения (Longhorn) — только если они заданы.
	if len(spec.Addons) > 0 {
		out = append(out, ClusterStep{
			ID: stepAddons, Kind: "addons", Done: clusterAddonsDone(spec, done),
			Deps: []string{stepReady},
		})
	}
	return out
}

// clusterAddonsDone — все дополнения spec установлены (по Resume).
func clusterAddonsDone(spec ClusterSpec, done *clusterResume) bool {
	if len(spec.Addons) == 0 {
		return false
	}
	for _, a := range spec.Addons {
		if !done.Addons[a.Name+":install"] {
			return false
		}
	}
	return true
}

// clusterStepDone — выполнен ли шаг по состоянию Resume (для расчёта Force
// при повторе: отмеченный И выполненный шаг исполняется принудительно).
func clusterStepDone(id string, spec ClusterSpec, nodes []clusterNode, cp1Name string, done *clusterResume) bool {
	switch {
	case id == stepPreflight:
		return len(done.Hosts) > 0
	case id == stepMesh:
		return done.MeshUp
	case id == stepListener:
		return done.Listener || done.Installed[cp1Name]
	case id == stepLB:
		return done.LB
	case id == stepCP1:
		return done.Installed[cp1Name]
	case id == stepReady:
		if !done.Installed[cp1Name] {
			return false
		}
		for _, nd := range nodes {
			if nd.Name != cp1Name && !done.Installed[nd.Name] {
				return false
			}
		}
		return true
	case id == stepCilium:
		return done.Cilium
	case id == stepAddons:
		return clusterAddonsDone(spec, done)
	}
	if len(id) > 3 && id[:3] == "vm:" {
		return done.Hosts[id[3:]] != 0
	}
	if len(id) > 5 && id[:5] == "join:" {
		return done.Installed[id[5:]]
	}
	return false
}

// clusterGate — какие шаги исполнять и какие из них принудительно. Строится
// один раз перед конвейером: либо из выбора оператора (окно шагов), либо из
// Resume (старый путь — задания без выбора, пополнение worker'ами).
type clusterGate struct {
	run   map[string]bool
	force map[string]bool
	// selection — задан ли явный выбор оператора (тогда Resume не решает,
	// что пропустить; он остаётся только источником данных).
	selection bool
}

// clusterBuildGate готовит гейт. p.Steps != nil — режим выбора оператора:
// бежим ровно по отмеченным, а отмеченный И уже выполненный шаг идёт с Force.
// p.Steps == nil — прежний режим: отметки Resume решают, что пропустить.
func (r *ClusterRunner) clusterBuildGate(ctx context.Context, spec ClusterSpec, nodes []clusterNode, cp1Name string, p ClusterJobParams, done *clusterResume) clusterGate {
	g := clusterGate{run: map[string]bool{}, force: map[string]bool{}}
	steps := clusterStepList(spec, nodes, cp1Name, done)
	if p.Steps != nil {
		g.selection = true
		sel := map[string]bool{}
		for _, id := range p.Steps {
			sel[id] = true
		}
		for _, st := range steps {
			g.run[st.ID] = sel[st.ID]
			// Повтор уже выполненного разрушительного шага — принудительный.
			if sel[st.ID] && st.Force && clusterStepDone(st.ID, spec, nodes, cp1Name, done) {
				g.force[st.ID] = true
			}
		}
		return g
	}
	// Прежний режим: повторяем то, что не отмечено сделанным в Resume.
	for _, st := range steps {
		switch {
		case st.ID == stepPreflight:
			g.run[st.ID] = len(done.Hosts) == 0 && p.AddWorkers == 0
		case st.ID == stepMesh:
			g.run[st.ID] = !done.MeshUp
		case st.Kind == "vm":
			// Машина есть и запись хоста жива — не пересоздаём.
			exists := false
			if id := done.Hosts[st.Node]; id != 0 {
				if _, err := r.m.db.HostByID(ctx, id); err == nil {
					exists = true
				}
			}
			g.run[st.ID] = !exists
		case st.ID == stepListener:
			g.run[st.ID] = true
		case st.ID == stepLB:
			g.run[st.ID] = !done.LB
		case st.ID == stepCP1:
			g.run[st.ID] = !done.Installed[cp1Name]
		case st.Kind == "join":
			g.run[st.ID] = !done.Installed[st.Node]
		case st.ID == stepReady:
			g.run[st.ID] = true
		case st.ID == stepCilium:
			g.run[st.ID] = !done.Cilium
		case st.ID == stepAddons:
			g.run[st.ID] = true
		}
	}
	return g
}

// forceInstallSpec ставит Force в спецификацию установки роли, если шаг
// помечен принудительным.
func (g clusterGate) forceInstallSpec(id string, is k8s.InstallSpec) k8s.InstallSpec {
	if g.force[id] {
		is.Force = true
	}
	return is
}

// clusterCheckSelection проверяет выбор оператора, пришедший с окна шагов:
// выбор непустой, идентификаторы известны, жёсткие зависимости отмеченных
// шагов удовлетворены (отмечены ИЛИ выполнены). Окно не пускает конфликтный
// выбор само (кнопка «Продолжить» выключена), но и хаб обязан отказать —
// окно не единственный вход (API, скрипт).
func clusterCheckSelection(steps []ClusterStep, sel []string) error {
	if len(sel) == 0 {
		return msgs.Errorf("hub.clusterStepsEmpty")
	}
	known := map[string]bool{}
	for _, st := range steps {
		known[st.ID] = true
	}
	selm := map[string]bool{}
	var unknown []string
	for _, id := range sel {
		if !known[id] {
			unknown = append(unknown, id)
		}
		selm[id] = true
	}
	if len(unknown) > 0 {
		return msgs.Errorf("hub.clusterStepsUnknown", strings.Join(unknown, ", "))
	}
	if conflicts := validateClusterSteps(steps, selm); len(conflicts) > 0 {
		return msgs.Errorf("hub.clusterStepsConflict", strings.Join(conflicts, ", "))
	}
	return nil
}

// validateClusterSteps проверяет выбор оператора: отмеченный шаг обязан иметь
// удовлетворённые жёсткие зависимости (отмечены ИЛИ выполнены). Возвращает
// идентификаторы шагов с конфликтами — окно подсвечивает их, а исполнитель
// отказывает, пока конфликт не снят.
func validateClusterSteps(steps []ClusterStep, sel map[string]bool) []string {
	done := map[string]bool{}
	for _, st := range steps {
		done[st.ID] = st.Done
	}
	var conflicts []string
	for _, st := range steps {
		if !sel[st.ID] {
			continue
		}
		for _, dep := range st.Deps {
			if sel[dep] || done[dep] {
				continue
			}
			conflicts = append(conflicts, st.ID)
			break
		}
	}
	return conflicts
}
