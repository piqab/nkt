package hub

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// Память применения Traffic Fly (KV hub.fly.applied) помнит хосты, на которые
// конфигурация уже легла, чтобы ушедшую с карты цель снять: пустая
// конфигурация, туннели погаснут. Хост при этом мог уйти из инвентаря —
// запись удаляется шестью разными путями, и ни один память не чистит.
// Попытка снять конфигурацию с несуществующего агента была отказом всего
// применения, а состояние пишется только в конце удачного: круг замыкался, и
// один удалённый хост выключал Traffic Fly целиком — вплоть до новых
// маршрутов, которым не везло идти после него.

func flyWriteApplied(t *testing.T, s *Server, a flyApplied) {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.KVSet(context.Background(), flyAppliedKey, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestFlyAppliedStateForgetsDeletedHosts(t *testing.T) {
	ctx := context.Background()
	s, _, parent := flyTestServer(t)

	vm, err := s.db.CreateHost(ctx, "node-vm", "192.0.2.6", 22, "root", store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	// Машина живёт внутри хоста: своя запись, свой id — в инвентаре она есть,
	// и забывать её применение не вправе.
	if err := s.db.SetHostParent(ctx, vm, parent); err != nil {
		t.Fatal(err)
	}
	dead, err := s.db.CreateHost(ctx, "old-node", "192.0.2.7", 22, "root", store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.DeleteHost(ctx, dead); err != nil {
		t.Fatal(err)
	}

	flyWriteApplied(t, s, flyApplied{
		Revision: 7,
		Hosts:    []int64{parent, vm, dead, localHostID},
		Gateways: []int64{11},
		HostPush: map[int64]flyPush{
			parent: {Hash: "a", Rev: 7},
			dead:   {Hash: "b", Rev: 7},
		},
	})

	a, gone := s.flyAppliedState(ctx)
	if len(gone) != 1 || gone[0] != dead {
		t.Fatalf("забытые хосты: %v, хотели %v", gone, []int64{dead})
	}
	for _, id := range []int64{parent, vm, localHostID} {
		if !slices.Contains(a.Hosts, id) {
			t.Errorf("хост %d пропал из памяти применения", id)
		}
	}
	if slices.Contains(a.Hosts, dead) {
		t.Error("удалённый хост остался в памяти применения — следующее снятие конфигурации снова откажет")
	}
	if _, ok := a.HostPush[dead]; ok {
		t.Error("отпечаток конфигурации удалённого хоста остался: применение сочтёт, что на нём всё лежит")
	}
	if a.Revision != 7 || len(a.Gateways) != 1 {
		t.Errorf("остальное состояние исказилось: ревизия %d, шлюзы %v", a.Revision, a.Gateways)
	}

	// Чтение ничего не переписывает: память чистит только удачное применение
	// (оно пишет состояние в конце), до него каждое чтение забывает заново —
	// и ни разу не из-за этого не отказывает.
	if _, again := s.flyAppliedState(ctx); len(again) != 1 || again[0] != dead {
		t.Errorf("повторное чтение: забытые %v, ждали ещё раз %v", again, []int64{dead})
	}
	var stored flyApplied
	raw, ok, err := s.db.KVGet(ctx, flyAppliedKey)
	if err != nil || !ok {
		t.Fatalf("память применения пропала: ok=%v err=%v", ok, err)
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(stored.Hosts, dead) {
		t.Error("чтение молча переписало состояние — писателем должно оставаться применение")
	}
}

func TestFlyAppliedStateWithoutMemory(t *testing.T) {
	s, _, _ := flyTestServer(t)
	a, gone := s.flyAppliedState(context.Background())
	if len(gone) != 0 || len(a.Hosts) != 0 || a.Revision != 0 {
		t.Fatalf("до первого применения: %+v, забытые %v", a, gone)
	}
	if s.flyAppliedRevision(context.Background()) != 0 {
		t.Error("ревизия до первого применения должна быть нулевой")
	}
}

func TestFlyHostLabelNamesUnknownHost(t *testing.T) {
	names := map[int64]string{5: "web-1", 6: ""}
	if got := flyHostLabel(names, 5); got != "web-1" {
		t.Errorf("имя хоста: %q", got)
	}
	// Имени нет — отказ не должен висеть на «Хост : …»: id остаётся.
	for _, id := range []int64{6, 7, localHostID} {
		if got := flyHostLabel(names, id); got == "" {
			t.Errorf("хост %d назван пустой строкой", id)
		}
	}
	if got := flyHostLabel(names, 7); got != "#7" {
		t.Errorf("удалённый хост: %q, ждали «#7»", got)
	}
	if got := flyHostLabels([]int64{7, 9}); got != "#7, #9" {
		t.Errorf("список забытых: %q", got)
	}
}

// Внутренний маршрут на старом шлюзе — тихая ловушка: поле internal он
// игнорирует, рисует маршрут внешним (после раннего return туннельного
// трафика), и узлы получают RST от самого шлюза при зелёной сводке.
// Применение обязано отклоняться внятной строкой.
func TestFlyCheckGatewayInternal(t *testing.T) {
	d := FlyDoc{Routes: []FlyRoute{
		{ID: "web", Gateway: 5, Kind: "tcp", Port: 443},
		{ID: "api-lab", Gateway: 5, Kind: "tcp", Port: 6443, Internal: true},
	}}
	g := FlyGateway{EdgeID: 5}
	if err := flyCheckGatewayInternal(d, g, "fly", "1.13.34"); err == nil {
		t.Error("старый шлюз прошёл с внутренним маршрутом")
	} else if !strings.Contains(err.Error(), "1.23.1") || !strings.Contains(err.Error(), "api-lab") {
		t.Errorf("в отказе нет нужной версии и маршрута: %v", err)
	}
	for _, ok := range []string{"1.23.1", "1.23.4-beta", "1.24.0", "dev"} {
		if err := flyCheckGatewayInternal(d, g, "fly", ok); err != nil {
			t.Errorf("версия %s отвергнута: %v", ok, err)
		}
	}
	// Другой шлюз и документы без внутренних маршрутов не затрагиваются.
	if err := flyCheckGatewayInternal(d, FlyGateway{EdgeID: 6}, "fly2", "1.0.0"); err != nil {
		t.Errorf("чужой шлюз: %v", err)
	}
	plain := FlyDoc{Routes: []FlyRoute{{ID: "web", Gateway: 5, Kind: "tcp", Port: 443}}}
	if err := flyCheckGatewayInternal(plain, g, "fly", ""); err != nil {
		t.Errorf("без внутренних маршрутов: %v", err)
	}
}
