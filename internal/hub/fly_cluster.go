package hub

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Автомат целей Traffic Fly для маршрутов, ведущих в кластер (1.17.28).
// Ingress кластера слушает на каждом узле (общий порт hostNetwork у
// Cilium), NodePort узел тоже держит на себе: значит пул целей обязан
// следовать за составом кластера. Узел прибыл — цель появилась, узел ушёл
// — цель ушла, иначе трафик клиентов молча упирается в машину, которой
// больше нет, а удаление машины из хаба маршрут не трогает.
//
// Маршрут привязан к кластеру именем (FlyRoute.Cluster — имя, а не id:
// документ переживает экспорт и импорт). Без привязки цели человеческие,
// автомат их не считает. Порт целей — тот, что назван у маршрута
// (TargetPort: `targets cluster … port …`, ingress кластера у маршрута
// токена), а не назван — по смыслу маршрута: ingress для клиентов у
// http/https, сам порт маршрута у tcp/udp (NodePort сохраняется
// насквозь).
//
// Перекладка — существующий механизм flyReconcileTargets: дифф, плавный
// вывод прежних, применение, ожидание живости. Автомат строит только «как
// должно быть» и зовёт его — из задания создания (узлы только что
// изменились), из задания удаления кластера (маршрутам некуда вести), из
// минутного наблюдения (состав тронули мимо хаба) и по кнопке. Наблюдение
// карту не перекладывает само: оно будит задание, чтобы журнал, дифф и
// история версий были там же, где у всякой правки Traffic Fly.

// flyClusterWhy — почему автомат не может построить пул целей.
type flyClusterWhy int

const (
	flyClusterOK flyClusterWhy = iota
	flyClusterNoCluster
	flyClusterHostsErr
	flyClusterNoIngress
	flyClusterNoPort
	flyClusterNoNodes
)

// clusterByName — кластер хаба по имени.
func (s *Server) clusterByName(ctx context.Context, name string) (store.Cluster, bool) {
	if name == "" {
		return store.Cluster{}, false
	}
	list, err := s.db.ListClusters(ctx)
	if err != nil {
		return store.Cluster{}, false
	}
	i := slices.IndexFunc(list, func(c store.Cluster) bool { return c.Name == name })
	if i < 0 {
		return store.Cluster{}, false
	}
	return list[i], true
}

// clusterFlyPort — порт, на котором узел кластера принимает трафик этого
// маршрута.
func (s *Server) clusterFlyPort(ctx context.Context, r FlyRoute) (int, flyClusterWhy) {
	if r.TargetPort > 0 {
		return r.TargetPort, flyClusterOK
	}
	switch r.Kind {
	case fly.KindHTTPS, fly.KindHTTP:
		// Без названного порта — ingress для клиентов кластера: слушать
		// нужно там, где слушает контроллер, а не где стоит 443 шлюза.
		in := s.clientIngresses(ctx)[r.Cluster]
		if in.Port < 1 {
			return 0, flyClusterNoIngress
		}
		return in.Port, flyClusterOK
	case fly.KindTCP, fly.KindUDP, fly.KindTCPUDP:
		// Диапазон портов идёт насквозь без подмены: цель принимает тот
		// же порт, что пришёл на шлюз.
		if r.Port < 1 {
			return 0, flyClusterNoPort
		}
		return r.Port, flyClusterOK
	}
	return 0, flyClusterNoPort
}

// clusterFlyTargets — пул целей, каким он должен быть: каждый узел
// кластера. Узел-машина — как машина своего хоста (адрес машины знает
// хост), узел-хост — сам хост. Номера целей — те же, что у шага сценария
// («n<хост>»), чтобы сверка не перекладывала то, что уже на месте.
func (s *Server) clusterFlyTargets(ctx context.Context, r FlyRoute) ([]FlyTarget, flyClusterWhy) {
	cl, ok := s.clusterByName(ctx, r.Cluster)
	if !ok {
		return nil, flyClusterNoCluster
	}
	port, why := s.clusterFlyPort(ctx, r)
	if why != flyClusterOK {
		return nil, why
	}
	hosts, err := s.db.ClusterHosts(ctx, cl.ID)
	if err != nil {
		return nil, flyClusterHostsErr
	}
	// Load-balancer machines only serve the API endpoint (HAProxy on 6443):
	// they run no kube-proxy or ingress controller, so a route target on
	// them would be permanently dead. Real nodes only.
	nodes := k8sNodeHosts(hosts)
	out := make([]FlyTarget, 0, len(nodes))
	for _, h := range nodes {
		t := FlyTarget{ID: fmt.Sprintf("n%d", h.ID), Label: h.Name, HostID: h.ID, Kind: "host", Port: port}
		if h.ParentID != 0 {
			t.HostID, t.Kind, t.Ref = h.ParentID, "vm", h.Name
		}
		out = append(out, t)
	}
	return out, flyClusterOK
}

// flyClusterSkip — маршруты, которые автомату не трогать: слушатель
// общего адреса ведёт только на control plane и строится своим
// помощником. Свой VIP и чужие порты кластеру не указ.
func flyClusterSkip(r FlyRoute, cluster string) bool {
	return r.ID == clusterListenerID(cluster)
}

// clusterFlyError — почему автомат не смог построить пул целей. Шагу
// сценария пустой пул — не «нечего делать», а непройденный шаг: здесь
// кластер только что был, а узлов автомат не видит.
func clusterFlyError(why flyClusterWhy, r FlyRoute) error {
	switch why {
	case flyClusterHostsErr:
		return msgs.Errorf("fly.clusterHosts", r.ID, r.Cluster)
	case flyClusterNoIngress:
		return msgs.Errorf("fly.clusterNoIngress", r.ID, r.Cluster)
	case flyClusterNoPort:
		return msgs.Errorf("fly.clusterNoPort", r.ID, r.Cluster)
	}
	return msgs.Errorf("fly.clusterNoCluster", r.ID, r.Cluster)
}

// flyPoolSame — совпадает ли пул с тем, что должно быть, по составу целей.
// Вывод (Drain) здесь не расхождение: выведенную вручную цель автомат не
// будит — она дожидается настоящего изменения состава.
func flyPoolSame(cur, want []FlyTarget) bool {
	if len(cur) != len(want) {
		return false
	}
	have := make(map[string]bool, len(cur))
	for _, t := range cur {
		have[flyTargetKey(t)] = true
	}
	for _, t := range want {
		if !have[flyTargetKey(t)] {
			return false
		}
	}
	return true
}

// flyClusterReconcile — привести пулы целей всех маршрутов кластера к его
// узлам. Маршрут, о котором автомат ничего не знает (ingress не задан,
// кластер пропал), — строчка в журнал, а не упавшее задание: создание
// кластера не должно гибнуть из-за карты Traffic Fly.
func (s *Server) flyClusterReconcile(ctx context.Context, jc flyJobLog, user, cluster string) error {
	d := s.flyDoc(ctx)
	touched, bound := 0, 0
	for _, r := range d.Routes {
		if r.Cluster != cluster {
			continue
		}
		bound++
		if flyClusterSkip(r, cluster) {
			continue
		}
		want, why := s.clusterFlyTargets(ctx, r)
		switch why {
		case flyClusterNoCluster:
			jc.Log("fly.clusterNoCluster", r.ID, cluster)
			continue
		case flyClusterHostsErr:
			jc.Log("fly.clusterHosts", r.ID, cluster)
			continue
		case flyClusterNoIngress:
			jc.Log("fly.clusterNoIngress", r.ID, cluster)
			continue
		case flyClusterNoPort:
			jc.Log("fly.clusterNoPort", r.ID, cluster)
			continue
		}
		if len(want) == 0 {
			// Узлов нет — целей нет; пустой пул шлюз не принимает, и
			// выключать маршрут из-за этого нельзя.
			jc.Log("fly.clusterNoNodes", r.ID, cluster)
			continue
		}
		touched++
		if err := s.flyReconcileTargets(ctx, jc, user, r.ID, want, true, msgs.Tc(ctx, "fly.clusterNote", cluster)); err != nil {
			return err
		}
	}
	switch {
	case touched > 0:
	case bound == 0:
		jc.Log("fly.clusterNone", cluster)
	default:
		// Маршруты есть, но ни один автомат перекладывать не стал
		// (слушатель общего адреса, ingress не задан) — причины уже
		// названы построчно выше.
		jc.Log("fly.clusterNothing", cluster)
	}
	return nil
}

// flyClusterDropRoutes — кластер удаляют: его маршруты больше некуда
// вести, убираем их из карты. Слушатель общего адреса снимает свой
// помощник — здесь только привязанные к кластеру.
func (s *Server) flyClusterDropRoutes(ctx context.Context, jc flyJobLog, user, cluster string) error {
	doc := s.flyDoc(ctx)
	var kept []FlyRoute
	dropped := 0
	for _, r := range doc.Routes {
		if r.Cluster == cluster && !flyClusterSkip(r, cluster) {
			dropped++
			continue
		}
		kept = append(kept, r)
	}
	if dropped == 0 {
		return nil
	}
	saved, err := s.flySave(ctx, FlyDoc{Gateways: doc.Gateways, Routes: kept}, user, msgs.Tc(ctx, "fly.clusterDropNote", cluster))
	if err != nil {
		return err
	}
	jc.Log("fly.clusterDropped", cluster, dropped)
	return s.flyApplyRun(ctx, deployFlyLog{jc}, user, FlyApplyParams{Revision: saved.Revision})
}

// flyClusterDrift — кластеры, у которых пул целей маршрутов разошёлся с
// узлами. О том, чего автомат судить не умеет (ingress не задан, кластера
// нет), сообщает задание по кнопке; будить его каждую минуту незачем.
func (s *Server) flyClusterDrift(ctx context.Context, d FlyDoc) []string {
	var out []string
	for _, r := range d.Routes {
		if r.Cluster == "" || flyClusterSkip(r, r.Cluster) || slices.Contains(out, r.Cluster) {
			continue
		}
		want, why := s.clusterFlyTargets(ctx, r)
		if why != flyClusterOK || flyPoolSame(r.Targets, want) {
			continue
		}
		out = append(out, r.Cluster)
	}
	return out
}

// flyClusterCounts — сколько маршрутов кластера ведёт автомат (слушатель
// общего адреса — не здесь: у него свои цели и своя жизнь). Карточке
// кластера это говорит, есть ли что сверять.
func (s *Server) flyClusterCounts(ctx context.Context) map[string]int {
	out := map[string]int{}
	for _, r := range s.flyDoc(ctx).Routes {
		if r.Cluster == "" || flyClusterSkip(r, r.Cluster) {
			continue
		}
		out[r.Cluster]++
	}
	return out
}

// flyClusterNames — кластеры, у которых есть привязанные маршруты.
func (s *Server) flyClusterNames(ctx context.Context) []string {
	d := s.flyDoc(ctx)
	var out []string
	for _, r := range d.Routes {
		if r.Cluster != "" && !slices.Contains(out, r.Cluster) {
			out = append(out, r.Cluster)
		}
	}
	return out
}

// Ход наблюдения: одно расхождение не должно заводить задание каждую
// минуту — ни пока прежнее ещё в работе, ни пока свежая попытка не
// устарела. Кнопка администратора под эти замки не попадает: просивший
// человек ждёт проверки сейчас.
var (
	flyClusterBusy sync.Map // cluster → time.Time: задание стоит или идёт
	flyClusterLast sync.Map // cluster → time.Time: когда в последний раз будили
)

// flyClusterCooldown — как долго после попытки не будить снова: неудачная
// перекладка (шлюз недоступен) иначе плодила бы задание раз в минуту.
const flyClusterCooldown = 10 * time.Minute

// flyClusterStale — потолок отметки «в работе»: задание могло умереть
// вместе с хабом, и без потолка кластер остался бы заперт навечно.
const flyClusterStale = 30 * time.Minute

// flyClusterMu — выбор кластеров и запуск задания одним шагом: наблюдение
// и кнопка не должны заводить два задания на один кластер.
var flyClusterMu sync.Mutex

// KindFlyCluster — задание «цели кластерных маршрутов — по узлам».
const KindFlyCluster = "fly.cluster"

// FlyClusterParams — кластеры, чьи маршруты сверить.
type FlyClusterParams struct {
	Clusters []string `json:"clusters"`
}

// FlyClusterRunner — тело задания fly.cluster.
type FlyClusterRunner struct{ s *Server }

// NewFlyClusterRunner строит исполнителя.
func NewFlyClusterRunner(s *Server) *FlyClusterRunner { return &FlyClusterRunner{s: s} }

// Run — сверка пула целей по каждому кластеру.
func (r *FlyClusterRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p FlyClusterParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	defer func() {
		for _, c := range p.Clusters {
			flyClusterBusy.Delete(c)
			flyClusterLast.Store(c, time.Now())
		}
	}()
	user := r.s.actingUser(ctx, jc.Job.Author, r.s.firstAdmin(ctx))
	for i, c := range p.Clusters {
		jc.StepKey(i+1, len(p.Clusters), "fly.clusterStep", c)
		if err := r.s.flyClusterReconcile(ctx, jc, user, c); err != nil {
			return err
		}
	}
	return nil
}

// startFlyCluster — задание перекладки целей кластерных маршрутов; false —
// такое уже в работе, второго незачем. cool — с оглядкой на недавнюю
// попытку (ход наблюдения); кнопка администратора зовёт без неё.
func (s *Server) startFlyCluster(ctx context.Context, clusters []string, user string, cool bool) (int64, bool) {
	flyClusterMu.Lock()
	defer flyClusterMu.Unlock()
	now := time.Now()
	var fresh []string
	for _, c := range clusters {
		if c == "" {
			continue
		}
		if at, ok := flyClusterBusy.Load(c); ok && now.Sub(at.(time.Time)) < flyClusterStale {
			continue
		}
		if cool {
			if last, ok := flyClusterLast.Load(c); ok && now.Sub(last.(time.Time)) < flyClusterCooldown {
				continue
			}
		}
		flyClusterBusy.Store(c, now)
		fresh = append(fresh, c)
	}
	if len(fresh) == 0 {
		return 0, false
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindFlyCluster,
		TitleKey: "fly.clusterJob", TitleArgs: []any{strings.Join(fresh, ", ")},
		Queue: "fly", Author: user, Steps: len(fresh), Params: FlyClusterParams{Clusters: fresh}})
	if err != nil {
		for _, c := range fresh {
			flyClusterBusy.Delete(c)
		}
		return 0, false
	}
	return id, true
}

// noteFlyClusterDrift — шаг минутного наблюдения: состав узлов кластера
// разошёлся с пулом целей (машину убрали мимо задания, узла добавили
// руками) — будим задание, оно и перекладывает, и пишет журнал.
func (s *Server) noteFlyClusterDrift(ctx context.Context, d FlyDoc) {
	list := s.flyClusterDrift(ctx, d)
	if len(list) == 0 {
		return
	}
	s.startFlyCluster(ctx, list, s.firstAdmin(ctx), true)
}

// handleFlyCluster — POST /hub/fly/cluster {"cluster":"lab"}: сверить цели
// кластерных маршрутов сейчас (администратор; без имени — все кластеры, у
// которых есть привязанные маршруты).
func (s *Server) handleFlyCluster(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if u, ok := auth.UserFromContext(ctx); !ok || !u.IsAdmin() {
		writeError(w, http.StatusForbidden, msgs.Tc(ctx, "auth.adminOnly"))
		return
	}
	var req struct {
		Cluster string `json:"cluster"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cluster := strings.TrimSpace(req.Cluster)
	var list []string
	if cluster != "" {
		if _, ok := s.clusterByName(ctx, cluster); !ok {
			writeError(w, http.StatusNotFound, msgs.Tc(ctx, "fly.clusterUnknown", cluster))
			return
		}
		list = []string{cluster}
	} else if list = s.flyClusterNames(ctx); len(list) == 0 {
		// Ни одного привязанного маршрута — работы нет, и задание
		// заводить незачем.
		writeJSON(w, http.StatusOK, map[string]any{"job_id": 0})
		return
	}
	user := auth.Username(ctx)
	id, started := s.startFlyCluster(ctx, list, user, false)
	s.db.Audit(ctx, user, "fly.cluster", strings.Join(list, ","), auditOutcome(nil), map[string]any{"started": started})
	if !started {
		writeError(w, http.StatusConflict, msgs.Tc(ctx, "fly.clusterBusy", strings.Join(list, ", ")))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}
