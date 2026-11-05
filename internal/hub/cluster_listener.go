package hub

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// kubeadm собирает три control plane по общему адресу, но сам адрес не
// даёт: API каждого узла смотрит на localhost, и наружу его выносит
// балансировщик. хаб своего балансировщика не заводит — для этого есть
// Traffic Fly: маршрут по порту на шлюзе, целями которого становятся
// control plane кластера. Слушатель — шаг задания кластера: он ложится до
// первого control plane (kubeadm в конце init ждёт API именно по
// control-plane-endpoint — фаза apicheck) и снимается заданием удаления.
//
// Адрес слушателя — адрес шлюза, поэтому слушатель нужен, когда общий
// адрес указывает именно на него: совпадает с его адресом или резолвится в
// него. Свой VIP или внешний балансировщик хаб не трогает: узлы входят по
// адресу, который обслуживает кто-то другой, и чужой порт на шлюзе кластеру
// не помощник. Шлюз, выбранный в кластере руками, — указание ставить
// слушателя именно там, даже если имя ещё никуда не указывает.
//
// Белый список обязателен: адрес кластера на публичном шлюзе — это вход в
// kube-apiserver и через него в etcd всего кластера. Хаб кладёт в него
// наружные адреса хостов размещения (узлы входят и работают по общему
// адресу, их соединения приходят на шлюз с адресов этих хостов) и то, что
// добавил человек, — остальным вход закрыт.

// k8sAPIPort — порт API на узле: куда приходит DNAT служебного порта хоста.
// Публичный порт endpoint'а может быть другим — маршрут их и связывает.
const k8sAPIPort = 6443

// clusterListenerID — маршрут слушателя: «api-<кластер>». Имя кластера уже
// ограничено hostname-формой, поэтому ID проходит проверку Traffic Fly.
func clusterListenerID(cluster string) string { return "api-" + strings.ToLower(cluster) }

// listenerNode — узел глазами слушателя: где лежит и входит ли в пул целей.
type listenerNode struct {
	Name   string
	HostID int64 // хост, на котором узел (машина — на нём, узел-хост — он сам)
	VM     bool  // машина, а не сам хост
	CP     bool  // control plane: только они служат API
}

// listenerNodesFromSpec — узлы из размещения (сухой прогон: машин ещё нет).
func listenerNodesFromSpec(nodes []clusterNode) []listenerNode {
	out := make([]listenerNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, listenerNode{Name: n.Name, HostID: n.HostID, VM: n.Kind != KindHost, CP: n.Role == RoleControlPlane})
	}
	return out
}

// listenerNodesFromHosts — узлы из записей кластера (машины уже заведены).
func listenerNodesFromHosts(hosts []store.Host) []listenerNode {
	out := make([]listenerNode, 0, len(hosts))
	for _, h := range hosts {
		// У машины запись хоста своя, а живёт она на родителе.
		id, vm := h.ID, false
		if h.ParentID != 0 {
			id, vm = h.ParentID, true
		}
		out = append(out, listenerNode{Name: h.Name, HostID: id, VM: vm, CP: h.K8sRole == RoleControlPlane})
	}
	return out
}

// clusterListenerGateway — шлюз, названный в кластере: по edge id. Пустой
// выбор здесь не разбирается: какой шлюз брать без указания, решает
// clusterListenerDecide по адресу общего адреса.
func (s *Server) clusterListenerGateway(ctx context.Context, edgeID int64) (flyGatewayJSON, error) {
	for _, g := range s.flyGateways(ctx) {
		if g.EdgeID == edgeID {
			return g, nil
		}
	}
	return flyGatewayJSON{}, msgs.Errorf("hub.clusterListenerGatewayNotFound", edgeID)
}

// lookupHost — как хаб узнаёт адреса имени. Отдельная переменная, чтобы
// проверки слушателя можно было строить с предсказуемой зоной: живой DNS
// отвечает то три адреса, то ошибкой таймаута, и тест на «имя смотрит не на
// шлюз» превращался бы в лотерею.
var lookupHost = func(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// endpointAddrs — адреса, по которым отзывается хост endpoint'а: сам адрес,
// если это IP, иначе — его A-записи. Второй ответ — почему не удалось.
func endpointAddrs(ctx context.Context, host string) ([]string, string) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []string{a.String()}, ""
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := lookupHost(cctx, host)
	if err != nil {
		return nil, err.Error()
	}
	return ips, ""
}

// listenerDecision — решение о слушателе: нужен ли он, где и, если нет,
// что сказать в журнал.
type listenerDecision struct {
	need     bool
	gw       flyGatewayJSON
	key      string
	args     []any
	mismatch string
}

// clusterListenerDecide — нужен ли слушатель на шлюзе. Ошибку выбора шлюза
// здесь не отдаём: без указанного шлюза это обычный «общий адрес обслуживает
// не хаб», и задание не должно падать из-за того, чего не просили.
func (r *ClusterRunner) clusterListenerDecide(ctx context.Context, spec ClusterSpec) listenerDecision {
	look := listenerLookEndpoint(ctx, spec.Endpoint)
	gws := r.s.flyGateways(ctx)
	if len(gws) == 0 {
		return listenerDecision{key: "hub.clusterListenerSkipNoGateway", args: []any{msgs.Errorf("hub.clusterListenerNoGateway")}}
	}
	if spec.Gateway != 0 {
		// Шлюз выбран руками: ставим слушатель там — не найдётся или не
		// будет связан, задание об этом скажет.
		gw, _ := r.s.clusterListenerGateway(ctx, spec.Gateway)
		return listenerDecision{need: true, gw: gw, mismatch: look.mismatch(gw)}
	}
	if len(gws) > 1 {
		// Шлюзов несколько, а названного нет: адрес выбирает сам себя, если
		// указывает ровно на одного из них. Ни на кого — не угадываем:
		// молча выбранный шлюз означал бы адрес кластера не там, где его
		// ждут.
		var matched []flyGatewayJSON
		for _, g := range gws {
			if look.mismatch(g) == "" {
				matched = append(matched, g)
			}
		}
		if len(matched) == 1 {
			return listenerDecision{need: true, gw: matched[0]}
		}
		return listenerDecision{key: "hub.clusterListenerSkipNoGateway", args: []any{msgs.Errorf("fly.pipelineGatewayAmbiguous", len(gws))}}
	}
	if m := look.mismatch(gws[0]); m != "" {
		return listenerDecision{gw: gws[0], mismatch: m, key: "hub.clusterListenerSkipElsewhere", args: []any{spec.Endpoint, gws[0].Address, m}}
	}
	return listenerDecision{need: true, gw: gws[0]}
}

// listenerLook — что видно по адресу общего адреса: его IP (или причина, по
// которой имя не разобралось). Разбирается один раз на все шлюзы: с
// несколькими имя стоило бы того же одного запроса.
type listenerLook struct {
	ips []string
	why string
}

func listenerLookEndpoint(ctx context.Context, endpoint string) listenerLook {
	host, _, err := k8s.EndpointHostPort(endpoint)
	if err != nil {
		return listenerLook{why: endpoint}
	}
	ips, why := endpointAddrs(ctx, host)
	return listenerLook{ips: ips, why: why}
}

// mismatch — пусто, когда один из адресов endpoint'а есть адрес этого шлюза;
// иначе — что нашлось вместо него (для журнала и «!» в сухом прогоне).
func (l listenerLook) mismatch(gw flyGatewayJSON) string {
	if l.why != "" {
		return l.why
	}
	for _, ip := range l.ips {
		if a, err := netip.ParseAddr(ip); err == nil && a.String() == gw.Address {
			return ""
		}
	}
	return strings.Join(l.ips, ", ")
}

// listenerPublicAddrs — наружные адреса хоста для белого списка: адрес
// хоста может быть и именем (хаб подключается по нему), поэтому резолвится;
// остаются только IPv4, видимые извне — частные и loopback шлюз не
// пропустит никогда.
func listenerPublicAddrs(ctx context.Context, addr string) (out []string, ok bool) {
	if addr == "" {
		return nil, false
	}
	list := []string{addr}
	if net.ParseIP(addr) == nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ips, err := lookupHost(cctx, addr)
		if err != nil {
			return nil, false
		}
		list = ips
	}
	for _, s := range list {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
			continue
		}
		out = append(out, a.String())
	}
	return out, len(out) > 0
}

// clusterListenerAllow — кому разрешён вход: наружные адреса хостов
// размещения плюс поле «кому ещё». unknown — хосты, чей адрес снаружи не
// известен (за NAT или не резолвится): их узлы окажутся заперты, и это
// надо показать, а не молча собрать маршрут.
func (r *ClusterRunner) clusterListenerAllow(ctx context.Context, nodes []listenerNode, extra []string) (allow []string, unknown []string) {
	seen := map[string]bool{}
	add := func(a string) {
		if !seen[a] {
			seen[a] = true
			allow = append(allow, a)
		}
	}
	done := map[int64]bool{}
	for _, n := range nodes {
		if done[n.HostID] {
			continue
		}
		done[n.HostID] = true
		h, err := r.m.db.HostByID(ctx, n.HostID)
		if err != nil {
			unknown = append(unknown, fmt.Sprintf("host-%d", n.HostID))
			continue
		}
		addrs, ok := listenerPublicAddrs(ctx, h.Addr)
		if !ok {
			unknown = append(unknown, h.Name)
			continue
		}
		for _, a := range addrs {
			add(a)
		}
	}
	for _, a := range extra {
		add(a)
	}
	return allow, unknown
}

// clusterListenerRoute — маршрут слушателя: порт из endpoint'а, цели —
// control plane, белый список — узлы размещения и «кому ещё».
func (r *ClusterRunner) clusterListenerRoute(ctx context.Context, spec ClusterSpec, gw flyGatewayJSON, nodes []listenerNode) (FlyRoute, []string, []string, error) {
	_, port, err := k8s.EndpointHostPort(spec.Endpoint)
	if err != nil {
		return FlyRoute{}, nil, nil, err
	}
	allow, unknown := r.clusterListenerAllow(ctx, nodes, spec.AllowExtra)
	if len(allow) > fly.MaxAllow {
		return FlyRoute{}, nil, nil, msgs.Errorf("hub.clusterTooManyAllows", len(allow), fly.MaxAllow)
	}
	route := FlyRoute{
		ID: clusterListenerID(spec.Name), Gateway: gw.EdgeID, Kind: fly.KindTCP, Port: port,
		// Один клиент — один control plane: соединения с API живущие
		// (watch'и), и мотать их между узлами смысла нет.
		Algorithm: fly.AlgoSourceHash,
		Check:     fly.Check{Kind: fly.CheckTCP},
		Allow:     allow,
	}
	for _, n := range nodes {
		if !n.CP {
			continue
		}
		t := FlyTarget{ID: n.Name, HostID: n.HostID, Kind: "host", Port: k8sAPIPort}
		if n.VM {
			t.Kind, t.Ref = "vm", n.Name
		}
		route.Targets = append(route.Targets, t)
	}
	return route, allow, unknown, nil
}

// clusterListenerSame — слушатель уже такой: ни записывать, ни применять
// нечего (задание продолжается после перезапуска, worker'ы добавляются на
// тех же хостах).
func clusterListenerSame(cur, want FlyRoute) bool {
	if cur.ID != want.ID || cur.Gateway != want.Gateway || cur.Kind != want.Kind ||
		cur.Port != want.Port || cur.Algorithm != want.Algorithm || cur.Check != want.Check ||
		!slices.Equal(cur.Allow, want.Allow) || len(cur.Targets) != len(want.Targets) {
		return false
	}
	for i := range cur.Targets {
		if cur.Targets[i] != want.Targets[i] {
			return false
		}
	}
	return true
}

// clusterListenerStep — шаг задания «слушатель общего адреса». Отвечает,
// ждать ли адрес при входах узлов: пустой пул живых целей — и вход упрётся
// в «connection refused» без всякой подсказки.
func (r *ClusterRunner) clusterListenerStep(ctx context.Context, jc *jobs.Context, cl store.Cluster, spec ClusterSpec) (bool, error) {
	d := r.clusterListenerDecide(ctx, spec)
	if !d.need {
		jc.Log(d.key, d.args...)
		return false, nil
	}
	if d.mismatch != "" {
		// Шлюз назван в кластере, но адрес туда не указывает: слушатель
		// ставится по указанию человека, а что видно — в журнал.
		jc.Log("hub.clusterListenerForced", spec.Endpoint, d.gw.Address, d.mismatch)
	}
	if d.gw.EdgeID == 0 {
		// Названного шлюза на хабе нет — не «несвязан», а именно нет:
		// хост могли убрать, а кластер остался с его id.
		return false, msgs.Errorf("hub.clusterListenerGatewayNotFound", spec.Gateway)
	}
	if !d.gw.Connected {
		return false, msgs.Errorf("hub.clusterListenerOffline", d.gw.Name)
	}
	hosts, err := r.m.db.ClusterHosts(ctx, cl.ID)
	if err != nil {
		return false, err
	}
	route, allow, unknown, err := r.clusterListenerRoute(ctx, spec, d.gw, listenerNodesFromHosts(hosts))
	if err != nil {
		return false, err
	}
	if len(route.Targets) == 0 {
		return false, msgs.Errorf("hub.clusterListenerNoTargets", spec.Name)
	}
	if len(allow) == 0 {
		// Пустой список у Traffic Fly — не запрет, а «все»: такой слушатель
		// открывал бы API кластера всему интернету.
		return false, msgs.Errorf("hub.clusterListenerEmptyAllow", route.ID)
	}
	user := jc.Job.Author
	note := msgs.T(jc.Lang(), "hub.clusterListenerNote", spec.Name)
	doc := r.s.flyDoc(ctx)
	idx := slices.IndexFunc(doc.Routes, func(x FlyRoute) bool { return x.ID == route.ID })
	if idx >= 0 {
		// Человеческие настройки маршрута хаб не перетирает.
		cur := doc.Routes[idx]
		route.Label, route.MaxConns, route.Rate, route.DNS = cur.Label, cur.MaxConns, cur.Rate, cur.DNS
		if clusterListenerSame(cur, route) {
			jc.Log("hub.clusterListenerUnchanged", route.ID)
			return true, nil
		}
	}
	for _, name := range unknown {
		// Узлы этого хоста окажутся заперты: его адрес снаружи неизвестен.
		jc.Log("hub.clusterListenerPrivate", name)
	}
	routes := slices.Clone(doc.Routes)
	if idx < 0 {
		routes = append(routes, route)
	} else {
		routes[idx] = route
	}
	saved, err := r.s.flySave(ctx, FlyDoc{Gateways: doc.Gateways, Routes: routes}, user, note)
	if err != nil {
		return false, err
	}
	jc.Log("hub.clusterListenerApplied", route.ID, d.gw.Name, route.Port, len(route.Targets), len(allow))
	return true, r.s.flyApplyRun(ctx, deployFlyLog{jc}, user, FlyApplyParams{Revision: saved.Revision})
}

// clusterListenerRemoveRoute — снять слушатель кластера: маршрут убирается
// из документа, конфигурация применяется. Маршрута нет — нечего снимать.
func (s *Server) clusterListenerRemoveRoute(ctx context.Context, jc *jobs.Context, user, routeID, note string) error {
	doc := s.flyDoc(ctx)
	idx := slices.IndexFunc(doc.Routes, func(x FlyRoute) bool { return x.ID == routeID })
	if idx < 0 {
		return nil
	}
	routes := slices.Clone(doc.Routes[:idx])
	routes = append(routes, doc.Routes[idx+1:]...)
	saved, err := s.flySave(ctx, FlyDoc{Gateways: doc.Gateways, Routes: routes}, user, note)
	if err != nil {
		return err
	}
	jc.Log("hub.clusterListenerRemoved", routeID)
	return s.flyApplyRun(ctx, deployFlyLog{jc}, user, FlyApplyParams{Revision: saved.Revision})
}

// listenerPreflight — проверки слушателя в сухом прогоне и первым шагом
// настоящего создания: тот же путь, что делает задание, только ничего не
// записывается и не применяется.
func (r *ClusterRunner) listenerPreflight(ctx context.Context, spec ClusterSpec, nodes []clusterNode, add func(ok, warn bool, key string, args ...any)) {
	d := r.clusterListenerDecide(ctx, spec)
	if !d.need {
		// Слушателя не будет — предупреждение, не провал: общим адресом
		// вполне может управлять кто-то вне хаба.
		add(true, true, d.key, d.args...)
		return
	}
	switch {
	case d.gw.EdgeID == 0:
		add(false, false, "hub.clusterListenerGatewayNotFound", spec.Gateway)
		return
	case !d.gw.Connected:
		add(false, false, "hub.clusterListenerOffline", d.gw.Name)
	default:
		add(true, false, "hub.preflightListenerGateway", d.gw.Name, d.gw.Address)
	}
	if d.mismatch != "" {
		add(true, true, "hub.preflightListenerMismatch", spec.Endpoint, d.gw.Address, d.mismatch)
	}
	route, allow, unknown, err := r.clusterListenerRoute(ctx, spec, d.gw, listenerNodesFromSpec(nodes))
	if err != nil {
		add(false, false, "hub.preflightListenerRoute", err)
		return
	}
	names := make([]string, 0, len(route.Targets))
	for _, t := range route.Targets {
		names = append(names, t.ID)
	}
	add(len(route.Targets) > 0, false, "hub.preflightListenerTargets", route.ID, route.Port, strings.Join(names, ", "))
	for _, name := range unknown {
		add(true, true, "hub.clusterListenerPrivate", name)
	}
	add(len(allow) > 0, len(allow) == 0, "hub.preflightListenerAllow", len(allow), strings.Join(allow, ", "))
	if len(allow) == 0 {
		return
	}
	// Предпросмотр документа: порт слушателя не должен спорить с чужим
	// маршрутом того же шлюза — flyValidate это видит заранее.
	doc := r.s.flyDoc(ctx)
	next := FlyDoc{Gateways: doc.Gateways, Routes: slices.Clone(doc.Routes), Alloc: doc.Alloc}
	if i := slices.IndexFunc(next.Routes, func(x FlyRoute) bool { return x.ID == route.ID }); i >= 0 {
		next.Routes[i] = route
	} else {
		next.Routes = append(next.Routes, route)
	}
	next.normalize()
	add(r.s.flyValidate(ctx, next) == nil, false, "hub.preflightListenerFree", route.Port)
}
