package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/store"
)

// Автомат целей Traffic Fly: пул привязанного к кластеру маршрута обязан
// следовать за составом его узлов. Проверки идут по карте целиком — до
// применения (шлюза в работе нет, и падение применения не должно прятать
// раскладку), а расхождение автомат видит по составу целей, не по выводу.

// flyClusterEnv — хаб со шлюзом fly и кластером «lab»: hv1 — узел-хост,
// lab-w-1 — машина на нём; ingress клиентов — 8080. hv2 к кластеру не
// относится: цель на нём автомат обязан убирать.
type flyClusterEnv struct {
	s    *Server
	gw   int64
	cl   int64
	hv   []int64
	host int64
	vm   int64
}

func flyClusterEnvNew(t *testing.T) *flyClusterEnv {
	t.Helper()
	s, gw, hosts := listenerFixture(t, "192.0.2.31", "192.0.2.32")
	ctx := context.Background()
	cl, err := s.db.CreateCluster(ctx, store.Cluster{Name: "lab", HostID: hosts[0], Flavor: "kubeadm", Topology: TopologyCP3})
	if err != nil {
		t.Fatal(err)
	}
	e := &flyClusterEnv{s: s, gw: gw, cl: cl, hv: hosts, host: hosts[0]}
	if err := s.db.SetHostCluster(ctx, hosts[0], cl, RoleControlPlane); err != nil {
		t.Fatal(err)
	}
	e.vm = e.machine(t, "lab-w-1", hosts[0], RoleWorker)
	e.setIngress(t, "lab", 8080)
	return e
}

// machine — узел-машина кластера «lab», живущий на хосте host.
func (e *flyClusterEnv) machine(t *testing.T, name string, host int64, role string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := e.s.db.CreateHost(ctx, name, "10.101.1.2", 22, "nkt", store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.db.SetHostParent(ctx, id, host); err != nil {
		t.Fatal(err)
	}
	if err := e.s.db.SetHostCluster(ctx, id, e.cl, role); err != nil {
		t.Fatal(err)
	}
	return id
}

// cluster — ещё один кластер, пустой (узлов нет — целей быть не должно).
func (e *flyClusterEnv) cluster(t *testing.T, name string) int64 {
	t.Helper()
	id, err := e.s.db.CreateCluster(context.Background(), store.Cluster{Name: name, HostID: e.host, Flavor: "kubeadm", Topology: TopologyCP3})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// leave — узел вышел из кластера (так его убирают мимо задания хаба).
func (e *flyClusterEnv) leave(t *testing.T, id int64) {
	t.Helper()
	if err := e.s.db.SetHostCluster(context.Background(), id, 0, ""); err != nil {
		t.Fatal(err)
	}
}

// setIngress — ingress для клиентов кластера (port 0 — выключить).
func (e *flyClusterEnv) setIngress(t *testing.T, cluster string, port int) {
	t.Helper()
	ctx := context.Background()
	all := e.s.clientIngresses(ctx)
	if port < 1 {
		delete(all, cluster)
	} else {
		all[cluster] = ClientIngress{Node: "hv1", Port: port}
	}
	raw, _ := json.Marshal(all)
	if err := e.s.db.KVSet(ctx, clientIngressKV, string(raw)); err != nil {
		t.Fatal(err)
	}
}

// save — карта с этими маршрутами.
func (e *flyClusterEnv) save(t *testing.T, routes ...FlyRoute) {
	t.Helper()
	if _, err := e.s.flySave(context.Background(), FlyDoc{Routes: routes}, "admin", "test"); err != nil {
		t.Fatalf("карта не сохраняется: %v", err)
	}
}

// route — маршрут из текущей карты.
func (e *flyClusterEnv) route(t *testing.T, id string) FlyRoute {
	t.Helper()
	d := e.s.flyDoc(context.Background())
	i := slices.IndexFunc(d.Routes, func(r FlyRoute) bool { return r.ID == id })
	if i < 0 {
		t.Fatalf("маршрута %q в карте нет: %+v", id, d.Routes)
	}
	return d.Routes[i]
}

// revision — номер текущей версии карты.
func (e *flyClusterEnv) revision() int64 { return e.s.flyDoc(context.Background()).Revision }

// hasTarget — есть ли в пуле цель с таким ключом (и не выведенная ли).
func hasTarget(ts []FlyTarget, want FlyTarget) (FlyTarget, bool) {
	i := slices.IndexFunc(ts, func(tg FlyTarget) bool { return flyTargetKey(tg) == flyTargetKey(want) })
	if i < 0 {
		return FlyTarget{}, false
	}
	return ts[i], true
}

// flyHTTPSRoute — маршрут ingress'а клиентов; targetPort — названный порт
// целей (0 — автомат берёт ingress кластера).
func flyHTTPSRoute(gw int64, id, cluster string, targetPort int, targets ...FlyTarget) FlyRoute {
	return FlyRoute{ID: id, Gateway: gw, Kind: fly.KindHTTPS, Names: []string{id + ".example.com"}, Port: 443, TLS: true,
		Algorithm: fly.AlgoLeastConn, Check: fly.Check{Kind: fly.CheckTCP}, Cluster: cluster, TargetPort: targetPort, Targets: targets}
}

// flyTCPRoute — NodePort насквозь: узел принимает тот же порт, что пришёл
// на шлюз.
func flyTCPRoute(gw int64, id, cluster string, port int, targets ...FlyTarget) FlyRoute {
	return FlyRoute{ID: id, Gateway: gw, Kind: fly.KindTCP, Port: port, Algorithm: fly.AlgoSourceHash,
		Check: fly.Check{Kind: fly.CheckTCP}, Cluster: cluster, Targets: targets}
}

// Порт, на который узел принимает трафик маршрута: названный — воля
// человека, и он не переставляется выводом из ingress'а.
func TestClusterFlyPort(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	if p, why := e.s.clusterFlyPort(ctx, flyHTTPSRoute(e.gw, "shop", "lab", 30080)); p != 30080 || why != flyClusterOK {
		t.Errorf("названный порт: %d, %v", p, why)
	}
	if p, why := e.s.clusterFlyPort(ctx, flyHTTPSRoute(e.gw, "shop", "lab", 0)); p != 8080 || why != flyClusterOK {
		t.Errorf("ingress клиентов: %d, %v", p, why)
	}
	r := flyHTTPSRoute(e.gw, "shop", "lab", 0)
	r.Kind = fly.KindHTTP
	if p, why := e.s.clusterFlyPort(ctx, r); p != 8080 || why != flyClusterOK {
		t.Errorf("http: %d, %v", p, why)
	}
	// Ingress не задан — автомат не знает, куда вести трафик.
	if _, why := e.s.clusterFlyPort(ctx, flyHTTPSRoute(e.gw, "shop", "plain", 0)); why != flyClusterNoIngress {
		t.Errorf("кластер без ingress: %v", why)
	}
	// NodePort и диапазон — порт маршрута насквозь.
	if p, why := e.s.clusterFlyPort(ctx, flyTCPRoute(e.gw, "game", "lab", 30567)); p != 30567 || why != flyClusterOK {
		t.Errorf("tcp: %d, %v", p, why)
	}
	ru := flyTCPRoute(e.gw, "game", "lab", 25565)
	ru.Kind = fly.KindUDP
	if p, why := e.s.clusterFlyPort(ctx, ru); p != 25565 || why != flyClusterOK {
		t.Errorf("udp: %d, %v", p, why)
	}
	rng := flyTCPRoute(e.gw, "game", "lab", 25565)
	rng.Kind, rng.PortTo = fly.KindTCPUDP, 25570
	if p, why := e.s.clusterFlyPort(ctx, rng); p != 25565 || why != flyClusterOK {
		t.Errorf("tcp/udp с диапазоном: %d, %v", p, why)
	}
	// Порт ни назван, ни не выводим — цели на нулевом порту шлюз не принимает.
	if _, why := e.s.clusterFlyPort(ctx, flyTCPRoute(e.gw, "game", "lab", 0)); why != flyClusterNoPort {
		t.Errorf("tcp без порта: %v", why)
	}
	if _, why := e.s.clusterFlyPort(ctx, FlyRoute{ID: "x", Cluster: "lab", Kind: "sctp", Port: 30000}); why != flyClusterNoPort {
		t.Errorf("неизвестный вид: %v", why)
	}
}

// Пул целей — узлы кластера: машина как машина своего хоста, узел-хост как
// хост; номера целей — «n<хост>», чтобы сверка не перекладывала то, что
// уже на месте.
func TestClusterFlyTargets(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	got, why := e.s.clusterFlyTargets(ctx, flyHTTPSRoute(e.gw, "shop", "lab", 0))
	if why != flyClusterOK || len(got) != 2 {
		t.Fatalf("пул: %+v, %v", got, why)
	}
	host, ok := hasTarget(got, FlyTarget{HostID: e.host, Kind: "host", Port: 8080})
	if !ok || host.ID != fmt.Sprintf("n%d", e.host) || host.Label != "hv1" {
		t.Errorf("узел-хост: %+v", host)
	}
	vm, ok := hasTarget(got, FlyTarget{HostID: e.host, Kind: "vm", Ref: "lab-w-1", Port: 8080})
	if !ok || vm.ID != fmt.Sprintf("n%d", e.vm) || vm.Label != "lab-w-1" {
		t.Errorf("узел-машина: %+v", vm)
	}
	// A load-balancer machine must not become a target: HAProxy serves
	// 6443 only, so nodePort/ingress traffic would hit a dead port.
	e.machine(t, "lab-lb-1", e.host, RoleLoadBalancer)
	got, why = e.s.clusterFlyTargets(ctx, flyHTTPSRoute(e.gw, "shop", "lab", 0))
	if why != flyClusterOK || len(got) != 2 {
		t.Fatalf("пул с балансировщиком: %+v, %v", got, why)
	}
	if _, ok := hasTarget(got, FlyTarget{HostID: e.host, Kind: "vm", Ref: "lab-lb-1", Port: 8080}); ok {
		t.Error("lb-машина не должна быть целью")
	}
	// Кластера нет — это не «пустой пул», а нечего вести.
	if _, why := e.s.clusterFlyTargets(ctx, flyHTTPSRoute(e.gw, "shop", "ghost", 0)); why != flyClusterNoCluster {
		t.Errorf("чужой кластер: %v", why)
	}
	// Кластер без узлов — пустой пул (о нём сообщение в журнале задания).
	empty := e.cluster(t, "empty")
	_ = empty
	if got, why := e.s.clusterFlyTargets(ctx, flyTCPRoute(e.gw, "game", "empty", 30000)); why != flyClusterOK || len(got) != 0 {
		t.Errorf("пустой кластер: %+v, %v", got, why)
	}
	// Порт назван — цели смотрят на него, а не на ingress.
	got, _ = e.s.clusterFlyTargets(ctx, flyHTTPSRoute(e.gw, "shop", "lab", 30080))
	for _, tg := range got {
		if tg.Port != 30080 {
			t.Errorf("порт цели %s: %d", tg.ID, tg.Port)
		}
	}
}

func TestFlyPoolSame(t *testing.T) {
	a := []FlyTarget{{HostID: 1, Kind: "host", Port: 80}, {HostID: 2, Kind: "vm", Ref: "w", Port: 80}}
	b := []FlyTarget{{HostID: 2, Kind: "vm", Ref: "w", ID: "x", Port: 80}, {HostID: 1, Kind: "host", ID: "y", Port: 80, Drain: true}}
	if !flyPoolSame(a, b) {
		t.Error("тот же состав с выводом и другими id — не совпал")
	}
	if flyPoolSame(a, a[:1]) {
		t.Error("меньший пул совпал")
	}
	if flyPoolSame(a, []FlyTarget{{HostID: 1, Kind: "host", Port: 8080}, {HostID: 2, Kind: "vm", Ref: "w", Port: 80}}) {
		t.Error("другой порт совпал")
	}
	if flyPoolSame(a, []FlyTarget{{HostID: 1, Kind: "vm", Ref: "hv1", Port: 80}, {HostID: 2, Kind: "vm", Ref: "w", Port: 80}}) {
		t.Error("хост вместо машины совпал")
	}
	if flyPoolSame(a, nil) || flyPoolSame(nil, a) {
		t.Error("пустой пул совпал с непустым")
	}
}

// Состав узлов изменился — пулы привязанных маршрутов перекладываются:
// прежняя цель выводится, узлы кластера появляются все, слушатель общего
// адреса и человеческие цели остаются как были.
func TestFlyClusterReconcileFollowsNodes(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	want, why := e.s.clusterFlyTargets(ctx, flyHTTPSRoute(e.gw, "fresh", "lab", 0))
	if why != flyClusterOK {
		t.Fatal(why)
	}
	stale := FlyTarget{ID: "old", Label: "hv2", HostID: e.hv[1], Kind: "host", Port: 8080}
	e.save(t,
		// уже на месте: перекладывать нечего
		flyHTTPSRoute(e.gw, "fresh", "lab", 0, want...),
		// устарел: цель смотрит на хост, который узлом не был и не стал
		flyHTTPSRoute(e.gw, "shop", "lab", 0, stale),
		// этот автомат не трогает: у слушателя только control plane
		flyTCPRoute(e.gw, clusterListenerID("lab"), "lab", 6443, stale),
		// человеческие цели — остаются
		flyHTTPSRoute(e.gw, "other", "", 8080, stale),
		// и ещё один устаревший — после упавшего применения до него дело не дойдёт
		flyTCPRoute(e.gw, "game", "lab", 30567, FlyTarget{ID: "g", Label: "hv2", HostID: e.hv[1], Kind: "host", Port: 30567}),
	)
	lg := &testFlyLog{}
	// Шлюза в работе нет: применение падает, раскладка обязана быть записана.
	if err := e.s.flyClusterReconcile(ctx, lg, "admin", "lab"); err == nil {
		t.Fatal("применение без шлюза прошло")
	}
	if !slices.Contains(lg.keys, "fly.pipelineUnchanged") {
		t.Errorf("совпавший маршрут перекладывался: %v", lg.keys)
	}
	if !slices.Contains(lg.keys, "fly.pipelineTargets") {
		t.Errorf("устаревший маршрут не переложен: %v", lg.keys)
	}
	// Цель есть у каждого узла кластера — иначе трафик клиентов молча
	// упирается в машину, которой в списке нет.
	r := e.route(t, "shop")
	hosts, err := e.s.db.ClusterHosts(ctx, e.cl)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Targets) != len(hosts)+1 {
		t.Fatalf("пул %q: %+v", "shop", r.Targets)
	}
	for _, h := range hosts {
		// Машина принимает трафик у своего хоста: цель смотрит на него.
		tg := FlyTarget{HostID: h.ID, Kind: "host", Port: 8080}
		if h.ParentID != 0 {
			tg.HostID, tg.Kind, tg.Ref = h.ParentID, "vm", h.Name
		}
		got, ok := hasTarget(r.Targets, tg)
		if !ok {
			t.Errorf("узла %s в пуле нет", h.Name)
			continue
		}
		if got.Drain {
			t.Errorf("узел %s выведен", h.Name)
		}
	}
	if got, ok := hasTarget(r.Targets, stale); !ok || !got.Drain {
		t.Errorf("прежняя цель не выведена: %+v", r.Targets)
	}
	// Слушатель и человеческий маршрут — словом не тронуты.
	api := e.route(t, clusterListenerID("lab"))
	if len(api.Targets) != 1 || api.Targets[0].ID != "old" || api.Targets[0].Drain {
		t.Errorf("слушатель общего адреса тронут: %+v", api.Targets)
	}
	if got := e.route(t, "game").Targets; len(got) != 1 || got[0].Drain {
		t.Errorf("маршрут после упавшего применения переложен: %+v", got)
	}
	if got := e.route(t, "other").Targets; len(got) != 1 || got[0].HostID != e.hv[1] {
		t.Errorf("человеческий пул тронут: %+v", got)
	}
}

// Совпавший пул — ни записи, ни применения; выведенную вручную цель
// расхождением не считаем, но настоящая сверка вывод снимает (у автомата
// узел либо в работе, либо его нет).
func TestFlyClusterReconcileUnchanged(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	want, why := e.s.clusterFlyTargets(ctx, flyHTTPSRoute(e.gw, "shop", "lab", 0))
	if why != flyClusterOK {
		t.Fatal(why)
	}
	e.save(t, flyHTTPSRoute(e.gw, "shop", "lab", 0, want...))
	rev := e.revision()
	lg := &testFlyLog{}
	if err := e.s.flyClusterReconcile(ctx, lg, "admin", "lab"); err != nil {
		t.Fatalf("совпавший пул потянул применение: %v", err)
	}
	if e.revision() != rev {
		t.Errorf("карта переписана без изменения: %d → %d", rev, e.revision())
	}
	if !slices.Contains(lg.keys, "fly.pipelineUnchanged") {
		t.Errorf("журнал: %v", lg.keys)
	}

	// Ручной вывод того же узла — не расхождение: наблюдение не должно
	// будить задание каждую минуту на машину, которую держат выключенной.
	drained := want
	drained[0].Drain = true
	if _, err := e.s.flySave(ctx, FlyDoc{Gateways: e.s.flyDoc(ctx).Gateways,
		Routes: []FlyRoute{flyHTTPSRoute(e.gw, "shop", "lab", 0, drained...)}}, "admin", "drain"); err != nil {
		t.Fatal(err)
	}
	if list := e.s.flyClusterDrift(ctx, e.s.flyDoc(ctx)); len(list) != 0 {
		t.Errorf("вывод посчитан расхождением: %v", list)
	}
	_ = e.s.flyClusterReconcile(ctx, &testFlyLog{}, "admin", "lab")
	if r := e.route(t, "shop"); slices.ContainsFunc(r.Targets, func(tg FlyTarget) bool { return tg.Drain }) {
		t.Errorf("сверка вывод не сняла: %+v", r.Targets)
	}
}

// О том, чего автомат судить не умеет, — строчка в журнал, а не упавшее
// задание создания кластера: карта Traffic Fly не должна рвать сценарий.
func TestFlyClusterReconcileSaysWhy(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	e.setIngress(t, "lab", 0)
	e.cluster(t, "empty")
	other := FlyTarget{ID: "h", Label: "hv1", HostID: e.host, Kind: "host", Port: 8080}
	e.save(t,
		flyHTTPSRoute(e.gw, "shop", "lab", 0, other),
		flyHTTPSRoute(e.gw, "ghost", "ghost", 0, other),
		// У кластера без узлов пул не пустой, а чужой: пустой шлюз не
		// принимает конфигурацию маршрута, поэтому автомат и не пишет его.
		flyTCPRoute(e.gw, "empty", "empty", 30000, other),
	)
	for _, c := range []struct{ cluster, key string }{
		{"lab", "fly.clusterNoIngress"},
		{"ghost", "fly.clusterNoCluster"},
		{"empty", "fly.clusterNoNodes"},
	} {
		lg := &testFlyLog{}
		if err := e.s.flyClusterReconcile(ctx, lg, "admin", c.cluster); err != nil {
			t.Fatalf("%s: %v", c.cluster, err)
		}
		if !slices.Contains(lg.keys, c.key) {
			t.Errorf("%s: журнала %q нет — %v", c.cluster, c.key, lg.keys)
		}
		// Маршрут привязан, но переложен не был — «на карте нет» тут
		// было бы неправдой: правдой является «сверять нечего».
		if !slices.Contains(lg.keys, "fly.clusterNothing") {
			t.Errorf("%s: нет строки «нечего сверять» — %v", c.cluster, lg.keys)
		}
		if slices.Contains(lg.keys, "fly.clusterNone") {
			t.Errorf("%s: маршруту на карте есть, а журнал говорит, что нет — %v", c.cluster, lg.keys)
		}
	}
	// Названный порт спасает и без ingress'а: вести есть куда.
	lg := &testFlyLog{}
	if err := e.s.flyClusterReconcile(ctx, lg, "admin", "lab"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.flySave(ctx, FlyDoc{Gateways: e.s.flyDoc(ctx).Gateways,
		Routes: []FlyRoute{flyHTTPSRoute(e.gw, "shop", "lab", 30080, other)}}, "admin", "port"); err != nil {
		t.Fatal(err)
	}
	lg = &testFlyLog{}
	// Перекладка есть — применение без шлюза падает, и это видно журналу.
	if err := e.s.flyClusterReconcile(ctx, lg, "admin", "lab"); err == nil {
		t.Fatal("применение без шлюза прошло")
	}
	if slices.Contains(lg.keys, "fly.clusterNoIngress") || !slices.Contains(lg.keys, "fly.pipelineTargets") {
		t.Errorf("названный порт не обошёл ingress: %v", lg.keys)
	}
	// Кластеру на карте нечего сказать, только когда маршрутов его там
	// правда нет.
	e.save(t)
	lg = &testFlyLog{}
	if err := e.s.flyClusterReconcile(ctx, lg, "admin", "lab"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(lg.keys, "fly.clusterNone") || slices.Contains(lg.keys, "fly.clusterNothing") {
		t.Errorf("пустая карта: %v", lg.keys)
	}
}

// Расхождение — по узлам кластера: имя одно на все его маршруты, и
// автомат будит задание только когда состав тронули.
func TestFlyClusterDrift(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	want, _ := e.s.clusterFlyTargets(ctx, flyHTTPSRoute(e.gw, "shop", "lab", 0))
	tcpWant, _ := e.s.clusterFlyTargets(ctx, flyTCPRoute(e.gw, "game", "lab", 30567))
	e.save(t,
		flyHTTPSRoute(e.gw, "shop", "lab", 0, want...),
		flyTCPRoute(e.gw, "game", "lab", 30567, tcpWant...),
		flyHTTPSRoute(e.gw, "other", "", 8080, FlyTarget{ID: "h", Label: "hv2", HostID: e.hv[1], Kind: "host", Port: 8080}),
		flyTCPRoute(e.gw, clusterListenerID("lab"), "lab", 6443, want[:1]...),
	)
	if list := e.s.flyClusterDrift(ctx, e.s.flyDoc(ctx)); len(list) != 0 {
		t.Fatalf("расхождение там, где всё на месте: %v", list)
	}
	// Узел прибыл — мимо задания хаба.
	e.machine(t, "lab-w-2", e.hv[0], RoleWorker)
	list := e.s.flyClusterDrift(ctx, e.s.flyDoc(ctx))
	if !slices.Equal(list, []string{"lab"}) {
		t.Fatalf("узел прибыл, расхождения нет: %v", list)
	}
	// Узел ушёл — тоже расхождение.
	e.leave(t, e.vm)
	if list = e.s.flyClusterDrift(ctx, e.s.flyDoc(ctx)); !slices.Equal(list, []string{"lab"}) {
		t.Fatalf("узел ушёл: %v", list)
	}
	stale := FlyTarget{ID: "h", Label: "hv2", HostID: e.hv[1], Kind: "host", Port: 8080}
	// Наблюдение само карту не перекладывает — только будит задание.
	e.save(t, flyHTTPSRoute(e.gw, "shop", "lab", 0, stale))
	if list = e.s.flyClusterDrift(ctx, e.s.flyDoc(ctx)); !slices.Equal(list, []string{"lab"}) {
		t.Errorf("устаревший пул не замечен: %v", list)
	}
	if got := e.route(t, "shop").Targets; !flyPoolSame(got, []FlyTarget{stale}) {
		t.Errorf("наблюдение тронуло карту: %+v", got)
	}
	// О том, чего автомат судить не умеет (ingress не задан), молчит:
	// об этом скажет задание по кнопке, будить его каждую минуту незачем.
	e.setIngress(t, "lab", 0)
	e.save(t, flyHTTPSRoute(e.gw, "shop", "lab", 0, stale))
	if list = e.s.flyClusterDrift(ctx, e.s.flyDoc(ctx)); len(list) != 0 {
		t.Errorf("без ingress'а задание разбужено: %v", list)
	}
	// Слушатель общего адреса — вне автомата даже с устаревшим пулом:
	// control plane у него считает свой помощник.
	e.save(t, flyTCPRoute(e.gw, clusterListenerID("lab"), "lab", 6443, stale))
	if list = e.s.flyClusterDrift(ctx, e.s.flyDoc(ctx)); len(list) != 0 {
		t.Errorf("слушатель разбудил задание: %v", list)
	}
	// Названный порт ingress'а не ждёт: вести есть куда, и видно, куда.
	e.save(t, flyHTTPSRoute(e.gw, "shop", "lab", 30080, stale))
	if list = e.s.flyClusterDrift(ctx, e.s.flyDoc(ctx)); !slices.Equal(list, []string{"lab"}) {
		t.Errorf("названный порт расхождение потерял: %v", list)
	}
}

// Карточке кластера и заданию без имени нужно, сколько маршрутов ведёт в
// кластер и какие кластеры вообще привязаны.
func TestFlyClusterCountsAndNames(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	other := FlyTarget{ID: "h", Label: "hv1", HostID: e.host, Kind: "host", Port: 8080}
	e.save(t,
		flyHTTPSRoute(e.gw, "shop", "lab", 0, other),
		flyTCPRoute(e.gw, "game", "lab", 30567, other),
		flyTCPRoute(e.gw, clusterListenerID("lab"), "lab", 6443, other),
		flyHTTPSRoute(e.gw, "other", "", 8080, other),
	)
	counts := e.s.flyClusterCounts(ctx)
	if counts["lab"] != 2 {
		t.Errorf("маршрутов кластера: %v", counts)
	}
	if _, ok := counts[""]; ok {
		t.Error("непривязанные маршруты посчитаны")
	}
	names := e.s.flyClusterNames(ctx)
	if !slices.Equal(names, []string{"lab"}) {
		t.Errorf("привязанные кластеры: %v", names)
	}
	// Список кластеров несёт это же число — по нему форма показывает кнопку.
	req := httptest.NewRequest(http.MethodGet, "/hub/clusters", nil).
		WithContext(auth.WithUser(ctx, store.User{Username: "admin", Role: store.RoleAdmin}))
	rec := httptest.NewRecorder()
	e.s.handleClusterList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body)
	}
	var rows []struct {
		Name      string `json:"name"`
		FlyRoutes int    `json:"fly_routes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "lab" || rows[0].FlyRoutes != 2 {
		t.Errorf("строки списка: %+v", rows)
	}
}

// Кластер удаляют: его маршруты больше некуда вести — убираем их из карты,
// слушатель снимает свой помощник, непривязанные остаются.
func TestFlyClusterDropRoutes(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	other := FlyTarget{ID: "h", Label: "hv1", HostID: e.host, Kind: "host", Port: 8080}
	e.save(t,
		flyHTTPSRoute(e.gw, "shop", "lab", 0, other),
		flyTCPRoute(e.gw, "game", "lab", 30567, other),
		flyTCPRoute(e.gw, clusterListenerID("lab"), "lab", 6443, other),
		flyHTTPSRoute(e.gw, "other", "", 8080, other),
	)
	lg := &testFlyLog{}
	// Применение без шлюза падает — карта к тому моменту уже без маршрутов.
	if err := e.s.flyClusterDropRoutes(ctx, lg, "admin", "lab"); err == nil {
		t.Fatal("применение без шлюза прошло")
	}
	d := e.s.flyDoc(ctx)
	ids := []string{}
	for _, r := range d.Routes {
		ids = append(ids, r.ID)
	}
	if slices.Contains(ids, "shop") || slices.Contains(ids, "game") {
		t.Errorf("маршруты кластера остались в карте: %v", ids)
	}
	if !slices.Contains(ids, "other") || !slices.Contains(ids, clusterListenerID("lab")) {
		t.Errorf("лишнее убрано: %v", ids)
	}
	if !slices.Contains(lg.keys, "fly.clusterDropped") {
		t.Errorf("журнала о снятии нет: %v", lg.keys)
	}
	// Нечего снимать — ни новой версии, ни применения.
	rev := d.Revision
	if err := e.s.flyClusterDropRoutes(ctx, &testFlyLog{}, "admin", "lab"); err != nil {
		t.Errorf("повторное снятие: %v", err)
	}
	if e.revision() != rev {
		t.Errorf("пустое снятие переписало карту: %d → %d", rev, e.revision())
	}
}

// Маршрут, привязанный к кластеру, ведёт не одна машина: удаление узла не
// должно срывать имя клиента — пул перекладывает автомат.
func TestReleaseVMFlyKeepsClusterRoute(t *testing.T) {
	e := flyClusterEnvNew(t)
	ctx := context.Background()
	vm := FlyTarget{ID: "v", Label: "lab-w-1", HostID: e.host, Kind: "vm", Ref: "lab-w-1", Port: 8080}
	e.save(t,
		flyHTTPSRoute(e.gw, "t7-1", "", 30080, vm),
		flyHTTPSRoute(e.gw, "shop", "lab", 0, vm),
	)
	host, err := e.s.db.HostByID(ctx, e.vm)
	if err != nil {
		t.Fatal(err)
	}
	e.s.releaseVMFly(ctx, host)
	ids := map[string]bool{}
	for _, r := range e.s.flyDoc(ctx).Routes {
		ids[r.ID] = true
	}
	if ids["t7-1"] {
		t.Error("маршрут токена на удалённую машину остался")
	}
	if !ids["shop"] {
		t.Error("привязанный маршрут удалён вместе с машиной")
	}
}

// Кнопка сверки: POST /hub/fly/cluster — задание, его кластеры и отказ там,
// где сверять нечем или уже сверяют.
func TestHandleFlyCluster(t *testing.T) {
	e := flyClusterEnvNew(t)
	s := e.s
	ctx := context.Background()
	s.jobs.Register(KindFlyCluster, NewFlyClusterRunner(s))
	t.Cleanup(func() { flyClusterBusy.Delete("lab"); flyClusterLast.Delete("lab") })
	post := func(t *testing.T, user store.User, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/hub/fly/cluster", bytes.NewBufferString(body)).
			WithContext(auth.WithUser(ctx, user))
		rec := httptest.NewRecorder()
		s.handleFlyCluster(rec, req)
		return rec
	}
	admin := store.User{Username: "admin", Role: store.RoleAdmin}
	// Без привязанных маршрутов работы нет — и задания заводить незачем.
	if rec := post(t, admin, `{}`); rec.Code != http.StatusOK || rec.Body.String() != "{\"job_id\":0}\n" {
		t.Fatalf("пустая карта: код %d, %s", rec.Code, rec.Body)
	}
	other := FlyTarget{ID: "h", Label: "hv1", HostID: e.host, Kind: "host", Port: 8080}
	e.save(t, flyHTTPSRoute(e.gw, "shop", "lab", 0, other))
	if rec := post(t, store.User{Username: "u", Role: store.RoleViewer}, `{"cluster":"lab"}`); rec.Code != http.StatusForbidden {
		t.Errorf("не администратор: код %d", rec.Code)
	}
	if rec := post(t, admin, `{"cluster":"ghost"}`); rec.Code != http.StatusNotFound {
		t.Errorf("чужой кластер: код %d, %s", rec.Code, rec.Body)
	}
	flyClusterBusy.Store("lab", time.Now())
	if rec := post(t, admin, `{"cluster":"lab"}`); rec.Code != http.StatusConflict {
		t.Errorf("уже сверяют: код %d, %s", rec.Code, rec.Body)
	}
	flyClusterBusy.Delete("lab")
	// Имя не названо — все кластеры с привязанными маршрутами.
	rec := post(t, admin, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Job int64 `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Job == 0 {
		t.Fatalf("задание не завелось: %s", rec.Body)
	}
	waitFlyClusterDone(t, "lab")
	// Задание дошло до перекладки: пул обновлён (применение без шлюза —
	// падение задания, а не раскладка).
	r := e.route(t, "shop")
	if len(r.Targets) != 2 {
		t.Errorf("задание не переложило цели: %+v", r.Targets)
	}
	if j, err := s.db.JobByID(ctx, out.Job); err != nil || j.Kind != KindFlyCluster {
		t.Errorf("задание %d: %+v %v", out.Job, j, err)
	}
}

// Замок наблюдения: одно расхождение — одно задание, и неудачная попытка
// не плодит их раз в минуту. Кнопка администратора под cooldown не
// попадает, «в работе» держит и её.
func TestStartFlyClusterCooldown(t *testing.T) {
	s, _, _ := listenerFixture(t, "192.0.2.41")
	ctx := context.Background()
	const c = "cd-test"
	t.Cleanup(func() { flyClusterBusy.Delete(c); flyClusterLast.Delete(c) })
	s.jobs.Register(KindFlyCluster, NewFlyClusterRunner(s))
	if id, ok := s.startFlyCluster(ctx, []string{c}, "admin", true); !ok || id == 0 {
		t.Fatal("первое наблюдение не разбудило задание")
	}
	waitFlyClusterDone(t, c)
	if _, ok := s.startFlyCluster(ctx, []string{c}, "admin", true); ok {
		t.Error("наблюдение разбужено сразу после попытки")
	}
	if _, ok := s.startFlyCluster(ctx, []string{c}, "admin", false); !ok {
		t.Error("кнопка заблокирована cooldown'ом наблюдения")
	}
	waitFlyClusterDone(t, c)
	flyClusterBusy.Store(c, time.Now())
	if _, ok := s.startFlyCluster(ctx, []string{c}, "admin", false); ok {
		t.Error("второе задание в работе")
	}
	// Отметка «в работе» от умершего вместе с хабом задания не должна
	// запирать кластер навечно.
	flyClusterBusy.Store(c, time.Now().Add(-2*flyClusterStale))
	if _, ok := s.startFlyCluster(ctx, []string{c}, "admin", false); !ok {
		t.Error("устаревшая отметка «в работе» заперла кластер")
	}
	waitFlyClusterDone(t, c)
	// Пустого имени задание не просит.
	if _, ok := s.startFlyCluster(ctx, []string{""}, "admin", false); ok {
		t.Error("задание на пустой список")
	}
}

// waitFlyClusterDone — задание fly.cluster по кластеру закончилось: отметка
// «в работе» снята, «последний раз» появилась.
func waitFlyClusterDone(t *testing.T, c string) {
	t.Helper()
	for range 1000 {
		if _, busy := flyClusterBusy.Load(c); !busy {
			if _, last := flyClusterLast.Load(c); last {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("задание fly.cluster по кластеру %q не закончилось", c)
}
