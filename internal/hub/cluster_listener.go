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
// наружные адреса хостов размещения и то, что добавил человек, — остальным
// вход закрыт. Про «адрес хоста размещения» надо понимать буквально: в
// список попадает тот адрес, с которого соединение придёт на шлюз. У машины
// в NAT и в мосту это адрес её хоста, у машины в macvtap — адрес её
// собственного шлюза по умолчанию (egress сайта), и он совпадает с адресом
// хоста размещения только по случайности. Поэтому список проверяется по
// факту: если по общему адресу никто не прошёл, хаб говорит об этом, а не
// делает вид, что живой адрес обязателен кому-то ещё.

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
	// LB — load-balancer machine: it serves the API but never dials the
	// shared address itself, so it is neither a target nor a client.
	LB bool
	// Self — собственная запись узла в списке хостов: у машины она есть
	// (хаб заводит её сам, и по ней ходит к машине по SSH), у ещё не
	// созданной машины — нет. Узел-хост — это он сам.
	Self int64
	// Macvtap — машина включена в устройство хоста: хост с ней по сети не
	// говорит, и её адрес знает только гостевой агент.
	Macvtap bool
}

// hostTarget — узел, чей адрес шлюз знает сам: цель строится как «сам
// хост», то есть адрес узла в туннеле шлюза. У машины в macvtap другого
// пути нет — хост размещения до неё по сети не дойдёт, а адрес из
// гостевого агента шлюзу не виден.
func (n listenerNode) hostTarget() bool {
	return !n.VM || (n.Macvtap && n.Self != 0)
}

// ownHost — запись узла в списке хостов: у узла-хоста это он сам, у машины
// — её собственная запись (хаб заводит её при создании); 0 — машины ещё нет.
func (n listenerNode) ownHost() int64 {
	if n.Self != 0 {
		return n.Self
	}
	if !n.VM {
		return n.HostID
	}
	return 0
}

// macvtapMachine — машина размещения в режиме macvtap: устройства
// обязательны для каждой строки с машинами (ValidatePlacements), поэтому
// режим и есть признак; узел-хост машиной не бывает.
func macvtapMachine(spec ClusterSpec, vm bool) bool {
	return vm && spec.NetworkMode == NetworkMacvtap
}

// listenerNodesFromSpec — узлы из размещения (сухой прогон: машин ещё нет,
// и их записей хаб ещё не знает).
func listenerNodesFromSpec(nodes []clusterNode, spec ClusterSpec) []listenerNode {
	out := make([]listenerNode, 0, len(nodes))
	for _, n := range nodes {
		ln := listenerNode{Name: n.Name, HostID: n.HostID, VM: n.Kind != KindHost, CP: n.Role == RoleControlPlane,
			LB: n.Role == RoleLoadBalancer, Macvtap: macvtapMachine(spec, n.Kind != KindHost)}
		if n.Kind == KindHost {
			ln.Self = n.HostID
		}
		out = append(out, ln)
	}
	return out
}

// listenerNodesFromHosts — узлы из записей кластера (машины уже заведены).
func listenerNodesFromHosts(hosts []store.Host, spec ClusterSpec) []listenerNode {
	out := make([]listenerNode, 0, len(hosts))
	for _, h := range hosts {
		// У машины запись хоста своя, а живёт она на родителе.
		id, vm := h.ID, false
		if h.ParentID != 0 {
			id, vm = h.ParentID, true
		}
		out = append(out, listenerNode{Name: h.Name, HostID: id, VM: vm, CP: h.K8sRole == RoleControlPlane,
			LB: h.K8sRole == RoleLoadBalancer, Self: h.ID, Macvtap: macvtapMachine(spec, vm)})
	}
	return out
}

// listenerNames — имена control plane по признаку узла: одной функцией,
// потому что и журнал, и сухой прогон называют одни и те же узлы.
func listenerNames(nodes []listenerNode, take func(listenerNode) bool) []string {
	var out []string
	for _, n := range nodes {
		if n.CP && take(n) {
			out = append(out, n.Name)
		}
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
	d := r.listenerDecideByAddress(ctx, spec)
	// Шлюз, на котором стоят macvtap-машины этого кластера, не может
	// говорить со своими гостями (ограничение macvlan), и слушатель общего
	// адреса на нём — путь, которого нет. Отказа заданию это не делает:
	// адресом может управлять кто-то вне хаба, — но хаб его не ставит и
	// говорит, чем заменить.
	if d.need && clusterHostsMacvtap(spec, d.gw.HostID) {
		return listenerDecision{gw: d.gw, mismatch: d.mismatch,
			key: "hub.clusterListenerSkipMacvtapHost", args: []any{d.gw.Name, spec.Endpoint}}
	}
	return d
}

// clusterHostsMacvtap — размещает ли хост машины кластера в macvtap.
func clusterHostsMacvtap(spec ClusterSpec, hostID int64) bool {
	if spec.NetworkMode != NetworkMacvtap || hostID == 0 {
		return false
	}
	for _, pl := range spec.Placements {
		if pl.HostID == hostID && pl.Kind == KindVM && pl.Nic != "" {
			return true
		}
	}
	return false
}

// listenerDecideByAddress — выбор шлюза по адресу общего адреса.
func (r *ClusterRunner) listenerDecideByAddress(ctx context.Context, spec ClusterSpec) listenerDecision {
	look := listenerLookEndpoint(ctx, spec.Endpoint)
	gws := r.s.flyGateways(ctx)
	if len(gws) == 0 {
		return listenerDecision{key: "hub.clusterListenerSkipNoGateway", args: []any{msgs.Errorf("hub.clusterListenerNoGateway")}}
	}
	if spec.Gateway != 0 {
		// Шлюз выбран руками: ставим слушатель там — не найдётся или не
		// будет связан, задание об этом скажет.
		gw, _ := r.s.clusterListenerGateway(ctx, spec.Gateway)
		return listenerDecision{need: true, gw: gw, mismatch: look.mismatch(gw, spec.EndpointInternal)}
	}
	if len(gws) > 1 {
		// Шлюзов несколько, а названного нет: адрес выбирает сам себя, если
		// указывает ровно на одного из них. Ни на кого — не угадываем:
		// молча выбранный шлюз означал бы адрес кластера не там, где его
		// ждут.
		var matched []flyGatewayJSON
		for _, g := range gws {
			if look.mismatch(g, spec.EndpointInternal) == "" {
				matched = append(matched, g)
			}
		}
		if len(matched) == 1 {
			return listenerDecision{need: true, gw: matched[0]}
		}
		return listenerDecision{key: "hub.clusterListenerSkipNoGateway", args: []any{msgs.Errorf("fly.pipelineGatewayAmbiguous", len(gws))}}
	}
	if m := look.mismatch(gws[0], spec.EndpointInternal); m != "" {
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
// Внутренний путь сравнивает с план-адресом: endpoint им и является.
func (l listenerLook) mismatch(gw flyGatewayJSON, internal bool) string {
	if l.why != "" {
		return l.why
	}
	want := gw.Address
	if internal && gw.Plan != "" {
		want = gw.Plan
	}
	for _, ip := range l.ips {
		if a, err := netip.ParseAddr(ip); err == nil && a.String() == want {
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
		// Internal path: the shared address is the gateway's plan address,
		// served inside the tunnel only — the port stays closed outside.
		Internal: spec.EndpointInternal,
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
		// Форма цели решает, кто отвечает за адрес. «Сам хост» — адрес узла в
		// туннеле шлюза: так узел-хост и так машина в macvtap (до неё хост
		// размещения по сети не дойдёт). «Машина» — хост размещения сам
		// ищет её адрес (аренда DHCP, ARP, гостевой агент) и DNAT'ит на
		// него служебный порт: так машины в NAT, мосту и wireguard.
		t := FlyTarget{ID: n.Name, Kind: "host", Port: k8sAPIPort}
		if n.hostTarget() {
			t.HostID = n.ownHost()
		} else {
			t.HostID, t.Kind, t.Ref = n.HostID, "vm", n.Name
		}
		route.Targets = append(route.Targets, t)
	}
	if spec.EndpointInternal {
		// Internal path: every kubelet dials the shared address, not just
		// the control planes. Non-CP nodes are no targets, but they need a
		// tunnel to the gateway for their marked traffic — they become pure
		// clients of the route. Only nodes that are fly clients themselves
		// qualify (host nodes and macvtap machines); a VM in NAT or on a
		// bridge has no tunnel of its own, and its traffic passes its host
		// as a forward, which the output-hook marking never sees.
		for _, n := range nodes {
			if n.CP || n.LB || !n.hostTarget() {
				continue
			}
			if id := n.ownHost(); id != 0 {
				route.Clients = append(route.Clients, id)
			}
		}
	}
	return route, allow, unknown, nil
}

// listenerKeepDrain — человеческий вывод целей: цель, которую убрали из
// пула на карте Traffic Fly, хаб возвращает обратно только вместе с явным
// изменением состава. Молча снять чужое решение «эта цель не участвует»
// значит вернуть в пул узел, который кто-то только что выключил.
func listenerKeepDrain(cur, want FlyRoute) FlyRoute {
	for i := range want.Targets {
		for _, t := range cur.Targets {
			if t.ID == want.Targets[i].ID && t.Drain {
				want.Targets[i].Drain = true
			}
		}
	}
	return want
}

// clusterListenerSame — слушатель уже такой: ни записывать, ни применять
// нечего (задание продолжается после перезапуска, worker'ы добавляются на
// тех же хостах).
func clusterListenerSame(cur, want FlyRoute) bool {
	if cur.ID != want.ID || cur.Gateway != want.Gateway || cur.Kind != want.Kind ||
		cur.Port != want.Port || cur.Algorithm != want.Algorithm || cur.Check != want.Check ||
		!slices.Equal(cur.Allow, want.Allow) || !slices.Equal(cur.Clients, want.Clients) ||
		cur.Internal != want.Internal || len(cur.Targets) != len(want.Targets) {
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
	nodes := listenerNodesFromHosts(hosts, spec)
	route, allow, unknown, err := r.clusterListenerRoute(ctx, spec, d.gw, nodes)
	if err != nil {
		return false, err
	}
	if len(route.Targets) == 0 {
		return false, msgs.Errorf("hub.clusterListenerNoTargets", spec.Name)
	}
	doc := r.s.flyDoc(ctx)
	if spec.EndpointInternal {
		// Nodes on the internal path arrive through the tunnel, so the
		// gateway sees their plan address as the source: without the
		// gateway's plan subnet in the allow list the listener would deny
		// the cluster's own nodes.
		if g, ok := doc.gateway(d.gw.EdgeID); ok {
			sub := fmt.Sprintf("10.77.%d.0/24", g.Index)
			if !slices.Contains(route.Allow, sub) {
				route.Allow = append(route.Allow, sub)
			}
		}
		if slices.ContainsFunc(nodes, func(n listenerNode) bool { return !n.LB && !n.hostTarget() }) {
			// VMs in NAT or on a bridge have no tunnel of their own: they
			// keep dialing the shared address by the default route.
			jc.Log("hub.clusterListenerInternalNoVMs", route.ID)
		}
		jc.Log("hub.clusterListenerInternal", route.ID, len(route.Targets)+len(route.Clients))
	}
	if len(allow) == 0 && len(route.Allow) == 0 {
		// Пустой список у Traffic Fly — не запрет, а «все»: такой слушатель
		// открывал бы API кластера всему интернету.
		return false, msgs.Errorf("hub.clusterListenerEmptyAllow", route.ID)
	}
	// Узлы, чей адрес шлюз узнаёт сам: хост размещения до macvtap-машины по
	// сети не дойдёт, поэтому цель строится «сам хост», а не «машина».
	if form := listenerNames(nodes, func(n listenerNode) bool { return n.Macvtap }); len(form) > 0 {
		jc.Log("hub.clusterListenerHostForm", route.ID, strings.Join(form, ", "))
		// Белый список собран из адресов хостов размещения, а macvtap-машина
		// приходит к шлюзу со своего шлюза по умолчанию — наружный адрес её
		// сайта. Совпадение с адресом хоста не гарантия, поэтому об этом
		// говорится заранее, а не тогда, когда вход будет отбит.
		jc.Log("hub.clusterListenerAllowMacvtap", strings.Join(allow, ", "))
	}
	for _, t := range route.Targets {
		if t.Drain {
			// Цель выведена человеком на карте: через неё соединения не идут,
			// и ждать её живости задание не будет.
			jc.Log("hub.clusterListenerTargetDrain", route.ID, t.ID)
		}
	}
	user := jc.Job.Author
	note := msgs.T(jc.Lang(), "hub.clusterListenerNote", spec.Name)
	idx := slices.IndexFunc(doc.Routes, func(x FlyRoute) bool { return x.ID == route.ID })
	if idx >= 0 {
		// Человеческие настройки маршрута хаб не перетирает.
		cur := doc.Routes[idx]
		route.Label, route.MaxConns, route.Rate, route.DNS = cur.Label, cur.MaxConns, cur.Rate, cur.DNS
		route = listenerKeepDrain(cur, route)
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

// clusterListenerAlive — ждёт, пока через слушатель начнёт служить хоть одна
// из целей ids, и пишет, что мешает остальным. Ждёт не дольше, чем живёт
// проверка целей шлюзом. Возвращает число служащих целей: 0 — общий адрес не
// работает, меньше, чем целей, — отказоустойчивость ниже той, что обещает
// кластер из трёх control plane.
func (r *ClusterRunner) clusterListenerAlive(ctx context.Context, jc *jobs.Context, routeID string, ids []string) int {
	if len(ids) == 0 {
		return 0
	}
	statuses := r.s.flyStatuses(ctx)
	checks := flyTargetChecks(statuses, routeID, ids)
	deadline := time.Now().Add(flyAliveWait)
	for flyServing(checks) == 0 && time.Now().Before(deadline) {
		if !sleepCtx(ctx, 5*time.Second) {
			break
		}
		statuses = r.s.flyStatuses(ctx)
		checks = flyTargetChecks(statuses, routeID, ids)
	}
	return r.clusterListenerReport(ctx, jc, statuses, routeID, ids)
}

// clusterListenerReport — что сводка шлюза говорит о целях слушателя:
// сколько служит и почему не служат остальные. Отделена от ожидания,
// потому что её и проверяют, и по ней человек разбирает отказ: «общий
// адрес не отвечает» без этой строки — отказ, который ничего не объясняет.
func (r *ClusterRunner) clusterListenerReport(ctx context.Context, jc *jobs.Context, statuses []flyGatewayStatusJSON, routeID string, ids []string) int {
	rs, why, ok := flyRouteStats(statuses, routeID)
	if !ok {
		// Сводки нет — говорить о живости целей нечего: хаб не знает даже
		// того, разложено ли решение вообще.
		jc.Log("hub.clusterListenerRouteMissing", routeID, why)
		return 0
	}
	checks := flyTargetChecks(statuses, routeID, ids)
	serving := 0
	for _, id := range ids {
		c := checks[id]
		switch {
		case c.Serving:
			serving++
		case c.Drain:
			// Выведенную цель шаг уже назвал: она не мёртвая, её нет в пуле.
		case !c.Seen:
			jc.Log("hub.clusterListenerTargetMissing", routeID, id)
		default:
			jc.Log("hub.clusterListenerTargetDead", routeID, id, c.Err)
		}
	}
	if serving > 0 && serving < len(ids) {
		jc.Log("hub.clusterListenerPartial", routeID, serving, len(ids))
	}
	if serving == 0 && !flyTouched(rs) {
		// Живость проверяет сам шлюз из себя, и белый список на неё не
		// влияет: «цель жива» при нулевом трафике — это про путь, а не про
		// вход. Нулевой трафик и есть то, что видит оператор: адрес есть,
		// соединение отбивается.
		applied, _ := r.appliedListener(ctx, routeID)
		jc.Log("hub.clusterListenerNoTraffic", routeID, strings.Join(applied.Allow, ", "))
	}
	return serving
}

// appliedListener — применённый маршрут слушателя: не пересобранное
// предположение о том, что хаб хотел разложить, а ровно то, что лежит в
// документе и уехало на шлюз.
func (r *ClusterRunner) appliedListener(ctx context.Context, routeID string) (FlyRoute, bool) {
	for _, rt := range r.s.flyDoc(ctx).Routes {
		if rt.ID == routeID {
			return rt, true
		}
	}
	return FlyRoute{}, false
}

// clusterListenerTargets — цели control plane применённого слушателя: чей
// живости ждать. По документу, а не по размещению: состав мог поменять
// человек на карте, и задание разбирается с тем, что разложено.
func (r *ClusterRunner) clusterListenerTargets(ctx context.Context, routeID string) []string {
	rt, ok := r.appliedListener(ctx, routeID)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(rt.Targets))
	for _, t := range rt.Targets {
		ids = append(ids, t.ID)
	}
	return ids
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
	specNodes := listenerNodesFromSpec(nodes, spec)
	route, allow, unknown, err := r.clusterListenerRoute(ctx, spec, d.gw, specNodes)
	if err != nil {
		add(false, false, "hub.preflightListenerRoute", err)
		return
	}
	names := make([]string, 0, len(route.Targets))
	for _, t := range route.Targets {
		names = append(names, t.ID)
	}
	add(len(route.Targets) > 0, false, "hub.preflightListenerTargets", route.ID, route.Port, strings.Join(names, ", "))
	// Машин в macvtap ещё нет, поэтому цель для них разложится после
	// создания: «сам хост», адрес — туннель шлюза. Сказать это в сухом
	// прогоне значит не оставлять вопроса «почему в списке не адрес машины».
	if form := listenerNames(specNodes, func(n listenerNode) bool { return n.Macvtap }); len(form) > 0 {
		add(true, false, "hub.preflightListenerHostForm", strings.Join(form, ", "))
		add(true, true, "hub.clusterListenerAllowMacvtap", strings.Join(allow, ", "))
	}
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
