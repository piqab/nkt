package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// Слушатель общего адреса: маршрут Traffic Fly ставится заданием кластера
// и решается по адресу — слушать имеет смысл там, куда указывает endpoint.
// Пустой белый список при этом отказ, а не «все».

// listenerFixture — хаб с одним шлюзом fly; адреса хостов задаются, потому
// что от них зависит белый список.
func listenerFixture(t *testing.T, hostAddrs ...string) (*Server, int64, []int64) {
	t.Helper()
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	ctx := context.Background()
	gw, err := s.putEdge(ctx, EdgeSettings{Enabled: true, Address: "203.0.113.10:8444", Roles: []string{EdgeRoleFly}})
	if err != nil {
		t.Fatal(err)
	}
	var hosts []int64
	for i, a := range hostAddrs {
		id, err := db.CreateHost(ctx, fmt.Sprintf("hv%d", i+1), a, 22, "root", store.HostAuthKey, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		hosts = append(hosts, id)
	}
	return s, gw.ID, hosts
}

// listenerNodes — узлы кластера «lab» на перечисленных хостах: kind —
// «vm» (машина на хосте) или «host» (сам хост), role — роль узла.
func listenerNodes(t *testing.T, s *Server, hosts []int64, kinds, roles []string) int64 {
	t.Helper()
	ctx := context.Background()
	clID, err := s.db.CreateCluster(ctx, store.Cluster{Name: "lab", HostID: hosts[0], Flavor: "kubeadm", Topology: TopologyCP3})
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range hosts {
		if i >= len(kinds) {
			break
		}
		if kinds[i] == "vm" {
			// Машина: запись хоста своя, живёт она на родителе.
			vm, err := s.db.CreateHost(ctx, fmt.Sprintf("lab-node-%d", i+1), fmt.Sprintf("10.0.0.%d", 11+i), 22, "root", store.HostAuthKey, []byte("x"))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.db.SetHostParent(ctx, vm, h); err != nil {
				t.Fatal(err)
			}
			if err := s.db.SetHostCluster(ctx, vm, clID, roles[i]); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := s.db.SetHostCluster(ctx, h, clID, roles[i]); err != nil {
			t.Fatal(err)
		}
	}
	return clID
}

// listenerJob — шаг слушателя внутри настоящего задания: журналу нужен
// контекст задания, а не пустой.
type listenerJob struct {
	fn   func(context.Context, *jobs.Context) error
	done chan error
}

func (j *listenerJob) Run(ctx context.Context, jc *jobs.Context) error {
	err := j.fn(ctx, jc)
	j.done <- err
	return err
}

// runInJob — выполняет fn заданием и возвращает его ошибку и ключи строк
// журнала: по ним и проверяют, что_step сказал человеку.
func runInJob(t *testing.T, s *Server, fn func(context.Context, *jobs.Context) error) (error, []string) {
	t.Helper()
	job := &listenerJob{fn: fn, done: make(chan error, 1)}
	const kind = "test.clusterListener"
	s.jobs.Register(kind, job)
	id, err := s.jobs.Start(context.Background(), jobs.Spec{Kind: kind, Title: "t",
		Queue: fmt.Sprintf("test:%d", time.Now().UnixNano()), Author: "admin", Steps: 1})
	if err != nil {
		t.Fatal(err)
	}
	var runErr error
	select {
	case runErr = <-job.done:
	case <-time.After(60 * time.Second):
		t.Fatal("задание не выполнилось")
	}
	lines, _ := s.db.JobLog(context.Background(), id, 0, 1000)
	keys := make([]string, 0, len(lines))
	for _, l := range lines {
		keys = append(keys, l.Key)
	}
	return runErr, keys
}

// connectFly — шлюз «на связи»: сеанса нет, и применение конфигурации
// споткнётся, но сама раскладка к тому моменту уже записана.
func connectFly(s *Server, edgeID int64) {
	s.edge = &edgeHub{clients: map[int64]*edgeClient{edgeID: {connected: true}}}
}

// withDNS — зона имён на время теста: живойresolver на машине разработчика
// отвечает то три адреса, то таймаутом, и проверка «имя смотрит не на шлюз»
// превратилась бы в лотерею.
func withDNS(t *testing.T, zone map[string][]string) {
	t.Helper()
	old := lookupHost
	lookupHost = func(ctx context.Context, host string) ([]string, error) {
		ips, ok := zone[host]
		if !ok {
			return nil, fmt.Errorf("lookup %s: no such host", host)
		}
		return ips, nil
	}
	t.Cleanup(func() { lookupHost = old })
}

func TestClusterListenerIDAndNodes(t *testing.T) {
	if got := clusterListenerID("Lab-1"); got != "api-lab-1" {
		t.Errorf("ID маршрута: %q", got)
	}
	// Имя кластера ограничено hostname-формой, но регистр в него мог
	// прийти из сценария — Traffic Fly таких ID не принимает.
	if !flyIDRe.MatchString(clusterListenerID("Lab-1")) {
		t.Error("ID маршрута не проходит проверку Traffic Fly")
	}
	nodes := listenerNodesFromHosts([]store.Host{
		{Name: "lab-cp-1", ID: 7, ParentID: 3, K8sRole: RoleControlPlane},
		{Name: "hv2", ID: 2, K8sRole: RoleControlPlane},
		{Name: "lab-w-1", ID: 8, ParentID: 3, K8sRole: RoleWorker},
	}, ClusterSpec{})
	want := []listenerNode{
		{Name: "lab-cp-1", HostID: 3, VM: true, CP: true, Self: 7},
		{Name: "hv2", HostID: 2, CP: true, Self: 2},
		{Name: "lab-w-1", HostID: 3, VM: true, Self: 8},
	}
	if !slices.Equal(nodes, want) {
		t.Errorf("узлы: %+v, надо %+v", nodes, want)
	}
	// macvtap: машина остаётся машиной, но режим меняет форму её цели.
	mac := listenerNodesFromHosts([]store.Host{{Name: "lab-cp-1", ID: 7, ParentID: 3, K8sRole: RoleControlPlane}},
		ClusterSpec{NetworkMode: NetworkMacvtap})
	if len(mac) != 1 || !mac[0].Macvtap || !mac[0].hostTarget() {
		t.Errorf("macvtap-машина: %+v", mac)
	}
	// Мост и NAT остаются целью «машина»: хост размещения её адрес знает.
	for _, mode := range []string{NetworkBridge, NetworkNAT, ""} {
		n := listenerNodesFromHosts([]store.Host{{Name: "lab-cp-1", ID: 7, ParentID: 3, K8sRole: RoleControlPlane}},
			ClusterSpec{NetworkMode: mode})[0]
		if n.Macvtap || n.hostTarget() {
			t.Errorf("режим %s: машина стала целью «сам хост» (%+v)", mode, n)
		}
	}
	fromSpec := listenerNodesFromSpec([]clusterNode{
		{Name: "lab-cp-1", HostID: 3, Role: RoleControlPlane, Kind: KindVM},
		{Name: "hv2", HostID: 2, Role: RoleWorker, Kind: KindHost},
	}, ClusterSpec{})
	if !slices.Equal(fromSpec, []listenerNode{{Name: "lab-cp-1", HostID: 3, VM: true, CP: true}, {Name: "hv2", HostID: 2, Self: 2}}) {
		t.Errorf("узлы из размещения: %+v", fromSpec)
	}
	// Сухой прогон: машины ещё нет, и её записи нет — форму цели хаб
	// предсказывает по режиму, а разложит её после создания.
	pre := listenerNodesFromSpec([]clusterNode{{Name: "lab-cp-1", HostID: 3, Role: RoleControlPlane, Kind: KindVM}},
		ClusterSpec{NetworkMode: NetworkMacvtap})
	if !pre[0].Macvtap || pre[0].hostTarget() {
		t.Errorf("macvtap в сухом прогоне: %+v", pre[0])
	}
}

// Белый список строит шлюз, поэтому частный адрес хоста размещения в него
// класть бессмысленно: соединения придут с наружного адреса NAT.
func TestListenerPublicAddrs(t *testing.T) {
	for _, a := range []string{"10.0.0.5", "192.168.1.1", "127.0.0.1", "169.254.9.9", "", "::1", "2001:4860:4860::8888"} {
		if got, ok := listenerPublicAddrs(context.Background(), a); ok || len(got) > 0 {
			t.Errorf("адрес %q принят как наружный: %v", a, got)
		}
	}
	got, ok := listenerPublicAddrs(context.Background(), "8.8.8.8")
	if !ok || !slices.Equal(got, []string{"8.8.8.8"}) {
		t.Errorf("публичный адрес: %v %v", got, ok)
	}
	withDNS(t, map[string][]string{
		"hv.example":   {"10.0.0.5", "8.8.4.4", "::1"},
		"loop.example": {"127.0.0.1"},
	})
	// Имя хоста резолвится, но в список попадают только видимые извне IPv4.
	if got, ok := listenerPublicAddrs(context.Background(), "hv.example"); !ok || !slices.Equal(got, []string{"8.8.4.4"}) {
		t.Errorf("имя хоста: %v %v", got, ok)
	}
	if got, ok := listenerPublicAddrs(context.Background(), "loop.example"); ok || len(got) > 0 {
		t.Errorf("loopback прошёл в наружные: %v", got)
	}
	if _, ok := listenerPublicAddrs(context.Background(), "none.example"); ok {
		t.Error("нерезолвленное имя принято как наружный адрес")
	}
}

// Поля слушателя — форма: адреса понимает белый список Traffic Fly, выбор
// шлюза без общего адреса означал бы, что поле заполнили, а слушать нечего.
func TestNormalizeListenerFields(t *testing.T) {
	base := func() ClusterSpec {
		return ClusterSpec{Name: "lab", Flavor: "kubeadm", Topology: TopologyCP1, Workers: 1,
			ImageID: "ubuntu-24.04", Endpoint: "k8s.example.com"}
	}
	spec := base()
	spec.AllowExtra = []string{" 8.8.8.8 ", "8.8.8.8", " 198.51.100.0/24 "}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spec.AllowExtra, []string{"8.8.8.8", "198.51.100.0/24"}) {
		t.Errorf("список «кому ещё»: %v", spec.AllowExtra)
	}
	// Имя вместо адреса: белый список понимает только IPv4 и сети, и
	// молча бросать ограничение нельзя — вход был бы не тот, что просили.
	spec = base()
	spec.AllowExtra = []string{"office.example.com"}
	if err := spec.Validate(); err == nil {
		t.Error("имя в белом списке пропущено")
	}
	// Шлюз или «кому ещё» без общего адреса — слушать нечего.
	spec = ClusterSpec{Name: "lab", Flavor: "kubeadm", Topology: TopologyCP1, Workers: 1, ImageID: "ubuntu-24.04", Gateway: 4}
	if err := spec.Validate(); err == nil {
		t.Error("шлюз без endpoint прошёл")
	}
	spec = ClusterSpec{Name: "lab", Flavor: "kubeadm", Topology: TopologyCP1, Workers: 1, ImageID: "ubuntu-24.04", AllowExtra: []string{"8.8.8.8"}}
	if err := spec.Validate(); err == nil {
		t.Error("«кому ещё» без endpoint прошло")
	}
	// Потолок списка — общий у хаба и у движка.
	spec = base()
	for i := range fly.MaxAllow + 1 {
		spec.AllowExtra = append(spec.AllowExtra, fmt.Sprintf("198.51.100.%d", i%254+1))
	}
	if err := spec.Validate(); err == nil {
		t.Error("список длиннее потолка принят")
	}
}

// Форма маршрута: порт из endpoint'а (не всегда 6443), цели — только
// control plane, белый список — адреса хостов размещения плюс «кому ещё».
func TestClusterListenerRouteShape(t *testing.T) {
	s, gwEdge, _ := listenerFixture(t, "10.0.0.5", "8.8.8.8")
	ctx := context.Background()
	r := NewClusterRunner(s)
	nodes := []listenerNode{
		{Name: "lab-cp-1", HostID: 1, VM: true, CP: true},
		{Name: "hv2", HostID: 2, CP: true},
		{Name: "lab-w-1", HostID: 1, VM: true},
	}
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "k8s.example.com:7443", AllowExtra: []string{"198.51.100.9"}}
	route, allow, unknown, err := r.clusterListenerRoute(ctx, spec, flyGatewayJSON{EdgeID: gwEdge, Address: "203.0.113.10"}, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if route.ID != "api-lab" || route.Gateway != gwEdge || route.Kind != fly.KindTCP || route.Port != 7443 {
		t.Errorf("маршрут: %+v", route)
	}
	if route.Algorithm != fly.AlgoSourceHash || route.Check.Kind != fly.CheckTCP {
		t.Errorf("алгоритм/проверка: %+v", route)
	}
	if !slices.Contains(allow, "8.8.8.8") || !slices.Contains(allow, "198.51.100.9") {
		t.Errorf("белый список: %v", allow)
	}
	for _, a := range allow {
		if strings.HasPrefix(a, "10.") {
			t.Errorf("частный адрес в списке: %v", allow)
		}
	}
	// Хоста с нерезолвленным адресом в списке нет, но он помечен: его
	// узлы окажутся заперты.
	if !slices.Contains(unknown, "hv1") || len(unknown) != 1 {
		t.Errorf("неизвестные хосты: %v", unknown)
	}
	wantTargets := []FlyTarget{
		{ID: "lab-cp-1", HostID: 1, Kind: "vm", Ref: "lab-cp-1", Port: k8sAPIPort},
		{ID: "hv2", HostID: 2, Kind: "host", Port: k8sAPIPort},
	}
	if !slices.Equal(route.Targets, wantTargets) {
		t.Errorf("цели: %+v, надо %+v", route.Targets, wantTargets)
	}
	// Маршрут принимает сам движок — его хаб отдаёт в flySave.
	if _, err := s.flySave(ctx, FlyDoc{Routes: []FlyRoute{route}}, "admin", "проба"); err != nil {
		t.Errorf("движок отверг маршрут слушателя: %v", err)
	}
	other := route
	other.Port = 6443
	if clusterListenerSame(route, other) {
		t.Error("разный порт не замечен")
	}
	other = route
	other.Allow = append(slices.Clone(route.Allow), "198.51.100.10")
	if clusterListenerSame(route, other) {
		t.Error("разный белый список не замечен")
	}
	other = route
	other.Targets = route.Targets[:1]
	if clusterListenerSame(route, other) {
		t.Error("пропавшая цель не замечена")
	}
	// Человеческие поля не считаются изменением: хаб их не перетирает.
	other = route
	other.Label = "наш API"
	other.MaxConns = 100
	if !clusterListenerSame(route, other) {
		t.Error("метка и лимиты посчитаны изменением")
	}
	other = route
	other.Clients = []int64{42}
	if clusterListenerSame(route, other) {
		t.Error("клиенты внутреннего пути не сравниваются")
	}
}

// Внутренний путь к слушателю: узлы с kubelet, до которых шлюз держит
// туннель (машины в macvtap и узлы-хосты), становятся чистыми клиентами
// маршрута; LB-машины и VM без своего туннеля (NAT/мост) — нет.
func TestClusterListenerRouteInternal(t *testing.T) {
	s, gwEdge, _ := listenerFixture(t, "10.0.0.5", "8.8.8.8")
	ctx := context.Background()
	r := NewClusterRunner(s)
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10:6443", NetworkMode: NetworkMacvtap, EndpointInternal: true}
	nodes := []listenerNode{
		{Name: "lab-cp-1", HostID: 1, VM: true, CP: true, Macvtap: true, Self: 11},
		{Name: "lab-w-1", HostID: 1, VM: true, Macvtap: true, Self: 12},
		{Name: "lab-w-2", HostID: 2, VM: true, Self: 13}, // NAT/bridge VM: no tunnel of its own
		{Name: "hv2", HostID: 2, Self: 2},                // worker as a host node
		{Name: "lab-lb-1", HostID: 1, VM: true, LB: true, Macvtap: true, Self: 14},
	}
	gw := flyGatewayJSON{EdgeID: gwEdge, Address: "203.0.113.10", Plan: "10.77.1.1"}
	route, allow, _, err := r.clusterListenerRoute(ctx, spec, gw, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{12, 2}; !slices.Equal(route.Clients, want) {
		t.Errorf("клиенты: %+v, надо %+v", route.Clients, want)
	}
	if !route.Internal {
		t.Error("внутренний путь не помечен на маршруте")
	}
	// Узлы приходят с план-адресов: подсеть плана шлюза в белом списке
	// уже на построении маршрута — её видят и сухой прогон, и префлайт.
	if !slices.Contains(allow, "10.77.1.0/24") || !slices.Contains(route.Allow, "10.77.1.0/24") {
		t.Errorf("белый список внутреннего маршрута: %v", allow)
	}
	spec.EndpointInternal = false
	plain, _, _, err := r.clusterListenerRoute(ctx, spec, gw, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Clients) != 0 {
		t.Errorf("клиенты без внутреннего пути: %+v", plain.Clients)
	}
	if plain.Internal {
		t.Error("внутренний маршрут без внутреннего пути")
	}
}

// Решение о слушателе: адрес указывает на шлюз — ставим; указывает куда-то
// ещё — не трогаем чужую точку входа; названный в кластере шлюз — указание
// ставить именно там.
func TestClusterListenerDecide(t *testing.T) {
	s, gwEdge, _ := listenerFixture(t, "8.8.8.8")
	ctx := context.Background()
	r := NewClusterRunner(s)
	gwSpec := func(endpoint string, gateway int64) ClusterSpec {
		return ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: endpoint, Gateway: gateway}
	}
	// Адрес совпадает со шлюзом.
	if d := r.clusterListenerDecide(ctx, gwSpec("203.0.113.10", 0)); !d.need || d.gw.EdgeID != gwEdge || d.mismatch != "" {
		t.Errorf("адрес шлюза: %+v", d)
	}
	// Порт дела не меняет.
	if d := r.clusterListenerDecide(ctx, gwSpec("203.0.113.10:7443", 0)); !d.need || d.mismatch != "" {
		t.Errorf("адрес шлюза с портом: %+v", d)
	}
	// Другой адрес — слушатель не нужен, и задание не падает.
	d := r.clusterListenerDecide(ctx, gwSpec("203.0.113.99", 0))
	if d.need || d.key != "hub.clusterListenerSkipElsewhere" || d.mismatch != "203.0.113.99" {
		t.Errorf("чужой адрес: %+v", d)
	}
	// Тот же чужой адрес, но шлюз назван руками: ставим по указанию.
	if d := r.clusterListenerDecide(ctx, gwSpec("203.0.113.99", gwEdge)); !d.need || d.mismatch != "203.0.113.99" {
		t.Errorf("шлюз назван: %+v", d)
	}
	// Шлюз назван, но его нет: цели нет вовсе — об этом скажет сухой прогон.
	if d := r.clusterListenerDecide(ctx, gwSpec("203.0.113.10", 9999)); !d.need || d.gw.EdgeID != 0 {
		t.Errorf("несуществующий шлюз: %+v", d)
	}
	// Шлюзов нет вовсе — тот же пропуск, а не отказ.
	m, db := newTestManager(t)
	plain := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	if d := NewClusterRunner(plain).clusterListenerDecide(ctx, gwSpec("k8s.example.com", 0)); d.need || d.key != "hub.clusterListenerSkipNoGateway" {
		t.Errorf("без шлюза: %+v", d)
	}
	// Шлюз один — выбор делать нечем, он и есть адрес кластера.
	second, err := plain.putEdge(ctx, EdgeSettings{Enabled: true, Address: "203.0.113.20:8444", Roles: []string{EdgeRoleFly}})
	if err != nil {
		t.Fatal(err)
	}
	if d := NewClusterRunner(plain).clusterListenerDecide(ctx, gwSpec("203.0.113.20", 0)); !d.need || d.gw.EdgeID != second.ID {
		t.Errorf("единственный шлюз: %+v", d)
	}
	// Их два, и адрес называет сам себя: указывать шлюз нечем — выбор
	// сделан самим адресом.
	third, err := plain.putEdge(ctx, EdgeSettings{Enabled: true, Address: "203.0.113.30:8444", Roles: []string{EdgeRoleFly}})
	if err != nil {
		t.Fatal(err)
	}
	if d := NewClusterRunner(plain).clusterListenerDecide(ctx, gwSpec("203.0.113.20", 0)); !d.need || d.gw.EdgeID != second.ID {
		t.Errorf("два шлюза, адрес называет сам себя: %+v", d)
	}
	// Ни на кого из двух адрес не указывает — не угадываем: молча выбранный
	// шлюз означал бы адрес кластера не там, где его ждут.
	if d := NewClusterRunner(plain).clusterListenerDecide(ctx, gwSpec("203.0.113.99", 0)); d.need || d.key != "hub.clusterListenerSkipNoGateway" {
		t.Errorf("два шлюза, адрес никуда: %+v", d)
	}
	// …а названный из двух — указание даже без совпадения адреса.
	if d := NewClusterRunner(plain).clusterListenerDecide(ctx, gwSpec("203.0.113.99", third.ID)); !d.need || d.gw.EdgeID != third.ID || d.mismatch == "" {
		t.Errorf("два шлюза, выбор сделан: %+v", d)
	}
	// DNS-имя, которое смотрит на шлюз, — это его адрес; имя, которое
	// смотрит куда-то ещё, — чужая точка входа.
	withDNS(t, map[string][]string{
		"ep.example":        {"203.0.113.10"},
		"both.example":      {"198.51.100.5", "203.0.113.10"},
		"elsewhere.example": {"198.51.100.5"},
		"v6.example":        {"2001:db8::5"},
	})
	for _, name := range []string{"ep.example", "both.example"} {
		if d := r.clusterListenerDecide(ctx, gwSpec(name+":6443", 0)); !d.need || d.gw.EdgeID != gwEdge || d.mismatch != "" {
			t.Errorf("имя %s на шлюз: %+v", name, d)
		}
	}
	if d := r.clusterListenerDecide(ctx, gwSpec("elsewhere.example", 0)); d.need || d.key != "hub.clusterListenerSkipElsewhere" || d.mismatch != "198.51.100.5" {
		t.Errorf("имя на чужой адрес: %+v", d)
	}
	// Имя без IPv4: слушателю ставить некуда — движок понимает только IPv4.
	if d := r.clusterListenerDecide(ctx, gwSpec("v6.example", 0)); d.need || d.key != "hub.clusterListenerSkipElsewhere" {
		t.Errorf("имя без IPv4: %+v", d)
	}
	// Нерезолвленное имя без названного шлюза — не отказ: адрес может
	// обслуживать кто-то вне хаба.
	if d := r.clusterListenerDecide(ctx, gwSpec("nope.invalid:6443", 0)); d.need || d.key != "hub.clusterListenerSkipElsewhere" {
		t.Errorf("имя никуда: %+v", d)
	}
	// …а названный шлюз — указание ставить и на имя, которое пока никуда
	// не смотрит: за DNS отвечает человек.
	if d := r.clusterListenerDecide(ctx, gwSpec("nope.invalid", gwEdge)); !d.need || d.mismatch == "" {
		t.Errorf("названный шлюз с именем никуда: %+v", d)
	}
}

// Шаг слушателя: не нужен — одна строка в журнале; нужен — маршрут в
// документе; уже такой — не переписываем; пустой список — отказ.
func TestClusterListenerStep(t *testing.T) {
	ctx := context.Background()
	s, gwEdge, hosts := listenerFixture(t, "8.8.8.8", "9.9.9.9")
	r := NewClusterRunner(s)
	clID := listenerNodes(t, s, hosts, []string{"vm", "host"}, []string{RoleControlPlane, RoleControlPlane})
	cl, err := s.db.ClusterByID(ctx, clID)
	if err != nil {
		t.Fatal(err)
	}
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10:7443", AllowExtra: []string{"198.51.100.9"}}
	step := func() func(context.Context, *jobs.Context) error {
		return func(c context.Context, jc *jobs.Context) error {
			_, err := r.clusterListenerStep(c, jc, cl, spec)
			return err
		}
	}
	// Шлюз не связан — внятный отказ до того, как что-то записано.
	runErr, _ := runInJob(t, s, step())
	if runErr == nil || !strings.Contains(runErr.Error(), "203.0.113.10") {
		t.Errorf("несвязанный шлюз: %v", runErr)
	}
	if s.flyDoc(ctx).Revision != 0 {
		t.Error("до связи шлюза что-то записано")
	}
	// Шлюз назван, но его нет на хабе: не «не связан», а именно нет —
	// хост могли убрать, а кластер остался с его id.
	missErr, _ := runInJob(t, s, func(c context.Context, jc *jobs.Context) error {
		_, err := r.clusterListenerStep(c, jc, cl, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10", Gateway: 9999})
		return err
	})
	if missErr == nil || !strings.Contains(missErr.Error(), "9999") {
		t.Errorf("несуществующий названный шлюз: %v", missErr)
	}
	// Связан — маршрут ложится; применить его без шлюза в сети нечем,
	// и это ошибка применения, а не раскладки.
	connectFly(s, gwEdge)
	runErr, keys := runInJob(t, s, step())
	if runErr != nil && strings.Contains(runErr.Error(), "api-lab") {
		t.Errorf("раскладка не собралась: %v", runErr)
	}
	doc := s.flyDoc(ctx)
	i := slices.IndexFunc(doc.Routes, func(x FlyRoute) bool { return x.ID == "api-lab" })
	if i < 0 {
		t.Fatalf("маршрута нет в документе: %+v (%v)", doc.Routes, runErr)
	}
	if doc.Routes[i].Port != 7443 || len(doc.Routes[i].Targets) != 2 {
		t.Errorf("маршрут: %+v", doc.Routes[i])
	}
	if !slices.Contains(keys, "hub.clusterListenerApplied") {
		t.Errorf("журнал без строки о слушателе: %v", keys)
	}
	// Тот же маршрут第二次 — ничего не переписываем: задание могло
	// продолжиться после перезапуска хаба.
	rev := s.flyDoc(ctx).Revision
	runErr, keys = runInJob(t, s, step())
	if runErr != nil {
		t.Fatalf("второй проход: %v", runErr)
	}
	if !slices.Contains(keys, "hub.clusterListenerUnchanged") {
		t.Errorf("повтор не помечен: %v", keys)
	}
	if s.flyDoc(ctx).Revision != rev {
		t.Error("повтор записал новую версию")
	}

	// Пустой белый список — отказ, а не открытый порт: хост размещения за
	// NAT и ничего сверх него.
	s2, gw2, hosts2 := listenerFixture(t, "10.0.0.5")
	r2 := NewClusterRunner(s2)
	cl2 := listenerNodes(t, s2, hosts2, []string{"vm"}, []string{RoleControlPlane})
	rec2, err := s2.db.ClusterByID(ctx, cl2)
	if err != nil {
		t.Fatal(err)
	}
	connectFly(s2, gw2)
	runErr, _ = runInJob(t, s2, func(c context.Context, jc *jobs.Context) error {
		_, err := r2.clusterListenerStep(c, jc, rec2, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10"})
		return err
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "api-lab") {
		t.Errorf("пустой белый список принят: %v", runErr)
	}
	if s2.flyDoc(ctx).Revision != 0 {
		t.Error("при отказе маршрут всё равно записан")
	}

	// Узел один, но worker: целей нет, слушать нечего.
	s3, gw3, hosts3 := listenerFixture(t, "8.8.8.8")
	r3 := NewClusterRunner(s3)
	cl3 := listenerNodes(t, s3, hosts3, []string{"vm"}, []string{RoleWorker})
	rec3, err := s3.db.ClusterByID(ctx, cl3)
	if err != nil {
		t.Fatal(err)
	}
	connectFly(s3, gw3)
	runErr, _ = runInJob(t, s3, func(c context.Context, jc *jobs.Context) error {
		_, err := r3.clusterListenerStep(c, jc, rec3, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10"})
		return err
	})
	if runErr == nil {
		t.Error("слушатель без целей прошёл")
	}

	// Адрес обслуживает не хаб: шаг existence-ит и задание продолжается.
	s4, _, hosts4 := listenerFixture(t, "8.8.8.8")
	r4 := NewClusterRunner(s4)
	cl4 := listenerNodes(t, s4, hosts4, []string{"vm"}, []string{RoleControlPlane})
	rec4, err := s4.db.ClusterByID(ctx, cl4)
	if err != nil {
		t.Fatal(err)
	}
	var needed bool
	runErr, keys = runInJob(t, s4, func(c context.Context, jc *jobs.Context) error {
		var e error
		needed, e = r4.clusterListenerStep(c, jc, rec4, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "vip.example:6443"})
		return e
	})
	if runErr != nil {
		t.Errorf("чужой адрес прервал задание: %v", runErr)
	}
	if needed {
		t.Error("слушатель на чужом адресе посчитан поставленным")
	}
	if !slices.Contains(keys, "hub.clusterListenerSkipElsewhere") {
		t.Errorf("журнал не говорит, почему слушателя нет: %v", keys)
	}
	if s4.flyDoc(ctx).Revision != 0 {
		t.Error("на чужом адресе маршрут записан")
	}
}

// Шаг слушателя в macvtap: цели строятся «сам хост» по записям самих машин,
// и журнал называет и форму, и цену белого списка. Повтор шага при этом
// остаётся «уже разложен»: форма не меняется от прохода к проходу, и хаб не
// перетирает ручной выбор человека видом «машина».
func TestClusterListenerStepMacvtap(t *testing.T) {
	s, gwEdge, hosts := listenerFixture(t, "8.8.8.8", "9.9.9.9")
	ctx := context.Background()
	r := NewClusterRunner(s)
	clID := listenerNodes(t, s, hosts, []string{"vm", "host"}, []string{RoleControlPlane, RoleControlPlane})
	cl, err := s.db.ClusterByID(ctx, clID)
	if err != nil {
		t.Fatal(err)
	}
	connectFly(s, gwEdge)
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10", NetworkMode: NetworkMacvtap}
	step := func(c context.Context, jc *jobs.Context) error {
		_, e := r.clusterListenerStep(c, jc, cl, spec)
		return e
	}
	runErr, keys := runInJob(t, s, step)
	if runErr != nil && strings.Contains(runErr.Error(), "api-lab") {
		t.Errorf("раскладка не собралась: %v", runErr)
	}
	for _, k := range []string{"hub.clusterListenerHostForm", "hub.clusterListenerAllowMacvtap", "hub.clusterListenerApplied"} {
		if !slices.Contains(keys, k) {
			t.Errorf("журнал не говорит %s: %v", k, keys)
		}
	}
	doc := s.flyDoc(ctx)
	i := slices.IndexFunc(doc.Routes, func(x FlyRoute) bool { return x.ID == "api-lab" })
	if i < 0 {
		t.Fatalf("маршрута нет: %+v", doc.Routes)
	}
	for _, tgt := range doc.Routes[i].Targets {
		if tgt.Kind != "host" || tgt.Ref != "" {
			t.Errorf("цель %s: вид %q, ссылка %q — надо «сам хост»", tgt.ID, tgt.Kind, tgt.Ref)
		}
	}
	if len(doc.Routes[i].Targets) != 2 {
		t.Errorf("целей %d, надо 2", len(doc.Routes[i].Targets))
	}
	_, keys = runInJob(t, s, step)
	if !slices.Contains(keys, "hub.clusterListenerUnchanged") {
		t.Errorf("повтор не помечен: %v", keys)
	}
}

// Форма цели слушателя решает режим сети, а не то, машина это или хост: до
// машины в macvtap хост размещения по сети не дойдёт, и адресом цели может
// быть только адрес узла в туннеле шлюза.
func TestClusterListenerTargetForm(t *testing.T) {
	s, gwEdge, hosts := listenerFixture(t, "8.8.8.8", "9.9.9.9")
	ctx := context.Background()
	r := NewClusterRunner(s)
	gw := flyGatewayJSON{EdgeID: gwEdge, Address: "203.0.113.10"}
	// Запись машины узла: своя (hosts[1]), живёт на родителе (hosts[0]).
	records := []store.Host{{Name: "lab-cp-1", ID: hosts[1], ParentID: hosts[0], K8sRole: RoleControlPlane}}
	targets := func(mode string) []FlyTarget {
		t.Helper()
		spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10", NetworkMode: mode}
		route, _, _, err := r.clusterListenerRoute(ctx, spec, gw, listenerNodesFromHosts(records, spec))
		if err != nil {
			t.Fatalf("режим %q: %v", mode, err)
		}
		return route.Targets
	}
	// NAT, мост и wireguard: адрес машины ищет хост размещения, и цель —
	// «машина» (аренда, ARP, гостевой агент — на его стороне).
	vmWant := []FlyTarget{{ID: "lab-cp-1", HostID: hosts[0], Kind: "vm", Ref: "lab-cp-1", Port: k8sAPIPort}}
	for _, mode := range []string{"", NetworkNAT, NetworkBridge, NetworkWireGuard} {
		if got := targets(mode); !slices.Equal(got, vmWant) {
			t.Errorf("режим %q: цели %+v, надо %+v", mode, got, vmWant)
		}
	}
	// macvtap: до машины хост по сети не дойдёт, поэтому цель — «сам хост»,
	// то есть запись самой машины и её адрес в туннеле шлюза.
	hostWant := []FlyTarget{{ID: "lab-cp-1", HostID: hosts[1], Kind: "host", Port: k8sAPIPort}}
	macTargets := targets(NetworkMacvtap)
	if !slices.Equal(macTargets, hostWant) {
		t.Errorf("macvtap: цели %+v, надо %+v", macTargets, hostWant)
	}
	// Узел-хост остаётся «сам хост» и в macvtap: своим адресом владеет сам.
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10", NetworkMode: NetworkMacvtap}
	hostRoute, _, _, err := r.clusterListenerRoute(ctx, spec, gw,
		listenerNodesFromHosts([]store.Host{{Name: "hv1", ID: hosts[0], K8sRole: RoleControlPlane}}, spec))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hostRoute.Targets, []FlyTarget{{ID: "hv1", HostID: hosts[0], Kind: "host", Port: k8sAPIPort}}) {
		t.Errorf("узел-хост: %+v", hostRoute.Targets)
	}
	// Машин ещё нет (сухой прогон): форму хаб предсказывает по режиму, но
	// цели разложит после создания — записей машин он ещё не знает.
	pre := listenerNodesFromSpec([]clusterNode{{Name: "lab-cp-1", HostID: hosts[0], Role: RoleControlPlane, Kind: KindVM}}, spec)
	preRoute, _, _, err := r.clusterListenerRoute(ctx, spec, gw, pre)
	if err != nil {
		t.Fatal(err)
	}
	if len(preRoute.Targets) != 1 || preRoute.Targets[0].Kind != "vm" {
		t.Errorf("сухой прогон macvtap: %+v", preRoute.Targets)
	}
	// Смена формы — это расхождение: старый маршрут надо разложить заново,
	// иначе задание скажет «слушатель уже такой» про пул из машин.
	same := func(a, b []FlyTarget) bool {
		return clusterListenerSame(FlyRoute{Targets: a}, FlyRoute{Targets: b})
	}
	if same(targets(NetworkBridge), macTargets) {
		t.Error("смена формы цели не замечена")
	}
	if same(hostRoute.Targets, macTargets) {
		t.Error("узел-хост и машина в macvtap с одним именем — одна и та же цель")
	}
	// Человеческий вывод цели хаб не снимает молча: с ним маршрут остаётся
	// тем же, что разложен, и задание его не переписывает.
	drained := FlyRoute{Targets: []FlyTarget{{ID: "lab-cp-1", HostID: hosts[1], Kind: "host", Port: k8sAPIPort, Drain: true}}}
	kept := listenerKeepDrain(drained, FlyRoute{Targets: macTargets})
	if !kept.Targets[0].Drain || !clusterListenerSame(drained, kept) {
		t.Errorf("вывод цели не сохранён: %+v", kept.Targets)
	}
	// Новая цель (узел добавился) выводом не помечается: вывод — решение
	// про конкретную цель, а не про маршрут.
	kept.Targets = append(slices.Clone(kept.Targets), FlyTarget{ID: "lab-cp-2", HostID: hosts[1], Kind: "host", Port: k8sAPIPort})
	two := listenerKeepDrain(drained, kept)
	if !two.Targets[0].Drain || two.Targets[1].Drain {
		t.Errorf("вывод уехал на новую цель: %+v", two.Targets)
	}
}

// Причины отказа слушателя: «общий адрес не отвечает» без них — отказ,
// который оператор разбирает сам. Живость шлюз проверяет со своего адреса,
// поэтому белый список для неё невидим: нулевой трафик — это и есть ответ
// «цель жива, а войти нельзя».
func TestClusterListenerReport(t *testing.T) {
	s, gwEdge, hosts := listenerFixture(t, "8.8.8.8")
	ctx := context.Background()
	r := NewClusterRunner(s)
	ids := []string{"lab-cp-1", "lab-cp-2"}
	route := FlyRoute{ID: "api-lab", Gateway: gwEdge, Kind: fly.KindTCP, Port: 6443, Algorithm: fly.AlgoSourceHash,
		Check: fly.Check{Kind: fly.CheckTCP}, Allow: []string{"8.8.8.8", "198.51.100.9"},
		Targets: []FlyTarget{{ID: ids[0], HostID: hosts[0], Kind: "vm", Ref: ids[0], Port: k8sAPIPort},
			{ID: ids[1], HostID: hosts[0], Kind: "vm", Ref: ids[1], Port: k8sAPIPort}}}
	if _, err := s.flySave(ctx, FlyDoc{Routes: []FlyRoute{route}}, "admin", "проба"); err != nil {
		t.Fatal(err)
	}
	status := func(targets []fly.TargetStats, traffic fly.RouteStats) []flyGatewayStatusJSON {
		rs := fly.RouteStats{ID: "api-lab", Targets: targets}
		rs.Active, rs.Clients, rs.BytesIn = traffic.Active, traffic.Clients, traffic.BytesIn
		return []flyGatewayStatusJSON{{EdgeID: gwEdge, Connected: true, Status: &fly.GatewayStatus{Routes: []fly.RouteStats{rs}}}}
	}
	alive := fly.TargetStats{ID: ids[0], Alive: true, Checked: true, LatencyMS: 37}
	down := fly.TargetStats{ID: ids[1], Checked: true, LastError: "dial tcp 10.77.1.3:40001: i/o timeout"}
	refused := []fly.TargetStats{down, {ID: ids[0], Checked: true, LastError: "connection refused"}}
	report := func(st []flyGatewayStatusJSON, want int, has []string, nothas ...string) {
		t.Helper()
		var got int
		runErr, logKeys := runInJob(t, s, func(c context.Context, jc *jobs.Context) error {
			got = r.clusterListenerReport(c, jc, st, "api-lab", ids)
			return nil
		})
		if runErr != nil {
			t.Fatal(runErr)
		}
		if got != want {
			t.Errorf("служит %d целей, надо %d (%v)", got, want, logKeys)
		}
		for _, k := range has {
			if !slices.Contains(logKeys, k) {
				t.Errorf("журнал не говорит %s: %v", k, logKeys)
			}
		}
		for _, k := range nothas {
			if slices.Contains(logKeys, k) {
				t.Errorf("журнал говорит лишнее (%s): %v", k, logKeys)
			}
		}
	}
	dead := []string{"hub.clusterListenerTargetDead"}
	// Одна цель служит, другая молчит: отказывать нечем, но и пул не тот.
	report(status([]fly.TargetStats{alive, down}, fly.RouteStats{Active: 1, Clients: 1}), 1,
		[]string{"hub.clusterListenerTargetDead", "hub.clusterListenerPartial"})
	// Ни одна не служит и через слушатель не прошло ни одного соединения —
	// это белый список, и хаб называет его прямо.
	report(status(refused, fly.RouteStats{}), 0,
		[]string{"hub.clusterListenerTargetDead", "hub.clusterListenerNoTraffic"})
	// Соединения шли — про белый список молчим: он ни при чём.
	report(status(refused, fly.RouteStats{BytesIn: 1 << 20}), 0, dead, "hub.clusterListenerNoTraffic")
	// Выведенную человеком цель не считаем мёртвой: шаг уже сказал про неё,
	// и ждать её живости задание не будет.
	report(status([]fly.TargetStats{alive, {ID: ids[1], Checked: true, Alive: true, Drain: true}},
		fly.RouteStats{Active: 1}), 1, nil, "hub.clusterListenerTargetDead", "hub.clusterListenerTargetMissing")
	// Цели нет в сводке шлюза: применение не дошло — хаб не выдумывает, что
	// она молчит.
	report(status([]fly.TargetStats{alive}, fly.RouteStats{Active: 1}), 1, []string{"hub.clusterListenerTargetMissing"})
	// Маршрута в сводке нет вовсе.
	missing := []string{"hub.clusterListenerRouteMissing"}
	report([]flyGatewayStatusJSON{{EdgeID: gwEdge, Connected: true, Status: &fly.GatewayStatus{
		Routes: []fly.RouteStats{{ID: "ssh"}}}}}, 0, missing)
	// Шлюз не связан: причина — в его ответе, а не в цели.
	report([]flyGatewayStatusJSON{{EdgeID: gwEdge, Connected: false, Error: "edge offline"}}, 0, missing)
}

// Чьей живости ждать задание: по применённому маршруту, а не по
// размещению, — состав целей могла поменять и карта Traffic Fly, и
// разбираться должно с тем, что разложено.
func TestClusterListenerTargets(t *testing.T) {
	s, gwEdge, hosts := listenerFixture(t, "8.8.8.8")
	ctx := context.Background()
	r := NewClusterRunner(s)
	if got := r.clusterListenerTargets(ctx, "api-lab"); got != nil {
		t.Errorf("маршрута нет, а цели нашлись: %v", got)
	}
	route := FlyRoute{ID: "api-lab", Gateway: gwEdge, Kind: fly.KindTCP, Port: 6443, Algorithm: fly.AlgoSourceHash,
		Check: fly.Check{Kind: fly.CheckTCP}, Allow: []string{"8.8.8.8"},
		Targets: []FlyTarget{{ID: "lab-cp-1", HostID: hosts[0], Kind: "host", Port: k8sAPIPort},
			{ID: "lab-cp-2", HostID: hosts[0], Kind: "host", Port: k8sAPIPort, Drain: true}}}
	if _, err := s.flySave(ctx, FlyDoc{Routes: []FlyRoute{route}}, "admin", "проба"); err != nil {
		t.Fatal(err)
	}
	if got := r.clusterListenerTargets(ctx, "api-lab"); !slices.Equal(got, []string{"lab-cp-1", "lab-cp-2"}) {
		t.Errorf("цели: %v", got)
	}
	ap, ok := r.appliedListener(ctx, "api-lab")
	if !ok || !slices.Equal(ap.Allow, []string{"8.8.8.8"}) {
		t.Errorf("белый список применённого маршрута: %+v (%v)", ap.Allow, ok)
	}
}

// Снятие слушателя вместе с кластером: маршрут уходит из документа, чужие
// остаются.
func TestClusterListenerRemoveRoute(t *testing.T) {
	s, gwEdge, _ := listenerFixture(t, "8.8.8.8")
	ctx := context.Background()
	other := FlyRoute{ID: "ssh", Gateway: gwEdge, Kind: fly.KindTCP, Port: 2222, Algorithm: fly.AlgoRoundRobin,
		Check: fly.Check{Kind: fly.CheckNone}, Targets: []FlyTarget{{ID: "s", HostID: 1, Kind: "host", Port: 22}}}
	api := FlyRoute{ID: "api-lab", Gateway: gwEdge, Kind: fly.KindTCP, Port: 6443, Algorithm: fly.AlgoSourceHash,
		Check: fly.Check{Kind: fly.CheckTCP}, Allow: []string{"8.8.8.8"},
		Targets: []FlyTarget{{ID: "lab-cp-1", HostID: 1, Kind: "vm", Ref: "lab-cp-1", Port: k8sAPIPort}}}
	if _, err := s.flySave(ctx, FlyDoc{Routes: []FlyRoute{other, api}}, "admin", "до удаления"); err != nil {
		t.Fatal(err)
	}
	connectFly(s, gwEdge)
	runInJob(t, s, func(c context.Context, jc *jobs.Context) error {
		return s.clusterListenerRemoveRoute(c, jc, "admin", "api-lab", "кластер удалён")
	})
	doc := s.flyDoc(ctx)
	if slices.ContainsFunc(doc.Routes, func(x FlyRoute) bool { return x.ID == "api-lab" }) {
		t.Errorf("слушатель остался после удаления: %+v", doc.Routes)
	}
	if !slices.ContainsFunc(doc.Routes, func(x FlyRoute) bool { return x.ID == "ssh" }) {
		t.Error("чужой маршрут удалён вместе со слушателем")
	}
	// Маршрута нет — нечего снимать и нечего записывать.
	rev := doc.Revision
	runErr, _ := runInJob(t, s, func(c context.Context, jc *jobs.Context) error {
		return s.clusterListenerRemoveRoute(c, jc, "admin", "api-net", "нет такого")
	})
	if runErr != nil {
		t.Errorf("снятие несуществующего слушателя: %v", runErr)
	}
	if s.flyDoc(ctx).Revision != rev {
		t.Error("пустое снятие записало версию")
	}
}

// Сухой прогон: те же проверки, что делает шаг, только ничего не
// записывается. DNS-имя, которое смотрит не на шлюз, — «!», а не провал.
func TestClusterListenerPreflight(t *testing.T) {
	type check struct {
		ok, warn bool
		key      string
	}
	collect := func(s *Server, spec ClusterSpec, nodes []clusterNode) []check {
		t.Helper()
		var out []check
		NewClusterRunner(s).listenerPreflight(context.Background(), spec, nodes, func(ok, warn bool, key string, args ...any) {
			out = append(out, check{ok, warn, key})
		})
		return out
	}
	has := func(list []check, key string) (check, bool) {
		for _, c := range list {
			if c.key == key {
				return c, true
			}
		}
		return check{}, false
	}
	one := []clusterNode{{Name: "lab-cp-1", HostID: 1, Role: RoleControlPlane, Kind: KindVM}}

	// Шлюза нет — предупреждение, и только: общим адресом может управлять
	// кто-то вне хаба.
	m, db := newTestManager(t)
	plain := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	list := collect(plain, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10"}, one)
	if c, ok := has(list, "hub.clusterListenerSkipNoGateway"); !ok || !c.warn || !c.ok {
		t.Errorf("шлюза нет: %+v", list)
	}
	if len(list) != 1 {
		t.Errorf("без шлюза проверяется что-то ещё: %+v", list)
	}

	s, gwEdge, hosts := listenerFixture(t, "8.8.8.8")
	listenerNodes(t, s, hosts, []string{"vm"}, []string{RoleControlPlane})
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10", AllowExtra: []string{"198.51.100.9"}}
	// Шлюз есть, но не связан — провал: адрес кластера не заработает.
	if c, ok := has(collect(s, spec, one), "hub.clusterListenerOffline"); !ok || c.ok {
		t.Errorf("несвязанный шлюз: %+v", c)
	}
	connectFly(s, gwEdge)
	list = collect(s, spec, one)
	for key, want := range map[string]bool{"hub.preflightListenerGateway": true, "hub.preflightListenerTargets": true,
		"hub.preflightListenerAllow": true, "hub.preflightListenerFree": true} {
		c, ok := has(list, key)
		if !ok || !c.ok || c.warn {
			t.Errorf("%s: %+v (%v)", key, c, want)
		}
	}
	// Порт уже занят чужим маршрутом того же шлюза — видно заранее.
	if _, err := s.flySave(context.Background(), FlyDoc{Routes: []FlyRoute{{ID: "busy", Gateway: gwEdge, Kind: fly.KindTCP, Port: 6443,
		Algorithm: fly.AlgoSourceHash, Check: fly.Check{Kind: fly.CheckTCP}, Allow: []string{"8.8.8.8"},
		Targets: []FlyTarget{{ID: "b", HostID: 1, Kind: "host", Port: 80}}}}}, "admin", "занято"); err != nil {
		t.Fatal(err)
	}
	if c, ok := has(collect(s, spec, one), "hub.preflightListenerFree"); !ok || c.ok {
		t.Errorf("спорящий порт не замечен: %+v", c)
	}

	// Хост размещения за NAT: его узлы окажутся заперты («!»), а пустой
	// список — провал.
	s2, gw2, hosts2 := listenerFixture(t, "10.0.0.5")
	listenerNodes(t, s2, hosts2, []string{"vm"}, []string{RoleControlPlane})
	connectFly(s2, gw2)
	list = collect(s2, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10"}, one)
	if c, ok := has(list, "hub.clusterListenerPrivate"); !ok || !c.warn {
		t.Errorf("частный адрес хоста не помечен: %+v", list)
	}
	if c, ok := has(list, "hub.preflightListenerAllow"); !ok || c.ok {
		t.Errorf("пустой белый список прошёл: %+v", list)
	}

	// macvtap: целей «машина» не будет — хост размещения до машины не
	// дойдёт; хаб говорит об этом заранее, как и про белый список, которому
	// адресов хостов размещения на такой сети обычно не хватает.
	s5, gw5, _ := listenerFixture(t, "8.8.8.8")
	connectFly(s5, gw5)
	mac := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10", NetworkMode: NetworkMacvtap}
	list = collect(s5, mac, one)
	if c, ok := has(list, "hub.preflightListenerHostForm"); !ok || c.warn || !c.ok {
		t.Errorf("форма цели macvtap в сухом прогоне: %+v (%v)", c, list)
	}
	if c, ok := has(list, "hub.clusterListenerAllowMacvtap"); !ok || !c.warn {
		t.Errorf("про egress сайта не предупреждено: %+v", list)
	}
	if _, ok := has(list, "hub.preflightListenerFree"); !ok {
		t.Errorf("с остальными проверками macvtap не ругается: %+v", list)
	}

	// Имя, которое смотрит не на шлюз, — «!»: слушателя не будет, и это
	// надо показать, а не молчать.
	withDNS(t, map[string][]string{"vip.example": {"198.51.100.77"}})
	s3, gw3, _ := listenerFixture(t, "8.8.8.8")
	connectFly(s3, gw3)
	list = collect(s3, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "vip.example"}, one)
	if c, ok := has(list, "hub.clusterListenerSkipElsewhere"); !ok || !c.warn || !c.ok {
		t.Errorf("имя на чужой адрес: %+v", list)
	}
	if len(list) != 1 {
		t.Errorf("на чужом адресе проверяется что-то ещё: %+v", list)
	}
	// Шлюз назван руками — слушатель ставится, а что видно по адресу,
	// остаётся предупреждением рядом с остальными проверками.
	list = collect(s3, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "vip.example", Gateway: gw3}, one)
	if c, ok := has(list, "hub.preflightListenerMismatch"); !ok || !c.warn || !c.ok {
		t.Errorf("несовпадение с названным шлюзом: %+v", list)
	}
	if c, ok := has(list, "hub.preflightListenerGateway"); !ok || !c.ok {
		t.Errorf("названный шлюз: %+v", list)
	}
	// Названного шлюза нет — провал, а не «!»: человек указал явно.
	if c, ok := has(collect(s3, ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "vip.example", Gateway: 9999}, one), "hub.clusterListenerGatewayNotFound"); !ok || c.ok {
		t.Errorf("несуществующий названный шлюз: %+v", list)
	}
}

// Поля слушателя приходят из строки сценария: шлюз — по имени или адресу.
func TestClusterListenerScriptArgs(t *testing.T) {
	s, gwEdge, _ := listenerFixture(t, "8.8.8.8")
	ctx := context.Background()
	r := NewScriptRunner(s)
	host := store.Host{ID: 1, Name: "hv1"}
	// Список «кому ещё» — одно слово через запятую: повторить аргумент
	// сценарий не даёт, а пробел разбил бы его на два слова.
	sc, issues := script.Parse("on hv1 k8s create lab flavor kubeadm nodes 3+1 endpoint k8s.example.com gateway 203.0.113.10 allow 8.8.8.8,198.51.100.0/24", nil)
	if len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	spec, err := r.clusterSpecFromStep(ctx, nil, host, sc.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if spec.Gateway != gwEdge {
		t.Errorf("шлюз: %d, надо %d", spec.Gateway, gwEdge)
	}
	if !slices.Equal(spec.AllowExtra, []string{"8.8.8.8", "198.51.100.0/24"}) {
		t.Errorf("список: %v", spec.AllowExtra)
	}
	// Неизвестный шлюз — отказ, а не молчаливый выбор.
	sc, issues = script.Parse("on hv1 k8s create lab flavor kubeadm nodes 3+1 endpoint k8s.example.com gateway nosuch", nil)
	if len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	if _, err := r.clusterSpecFromStep(ctx, nil, host, sc.Steps[0]); err == nil {
		t.Error("неизвестный шлюз принят")
	}
	// Непонятный адрес в «кому ещё» — отказ на разборе, а не в задании.
	sc, issues = script.Parse("on hv1 k8s create lab flavor kubeadm nodes 3+1 endpoint k8s.example.com allow office.example", nil)
	if len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	if _, err := r.clusterSpecFromStep(ctx, nil, host, sc.Steps[0]); err == nil {
		t.Error("имя в белом списке принято")
	}
}

// Форма кластера спрашивает шлюзы отдельно: весь документ Traffic Fly с
// чужими маршрутами ей не нужен.
func TestClusterGatewaysHandler(t *testing.T) {
	s, gwEdge, _ := listenerFixture(t, "8.8.8.8")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hub/cluster/gateways", nil)
	s.handleClusterGateways(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Gateways []flyGatewayJSON `json:"gateways"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Gateways) != 1 || out.Gateways[0].EdgeID != gwEdge || out.Gateways[0].Address != "203.0.113.10" {
		t.Fatalf("шлюзы: %s", rec.Body.String())
	}
	if out.Gateways[0].Connected {
		t.Error("несвязанный шлюз вышел связанным")
	}
	connectFly(s, gwEdge)
	rec = httptest.NewRecorder()
	s.handleClusterGateways(rec, httptest.NewRequest(http.MethodGet, "/hub/cluster/gateways", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Gateways) != 1 || !out.Gateways[0].Connected {
		t.Errorf("связанный шлюз: %s", rec.Body.String())
	}
}
