package hub

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

// ИБП: переход на батарею, «садится», возврат сети, потеря связи — по
// событию на переход; первое наблюдение и старый хост (без ups) молчат.
func TestUPSEvents(t *testing.T) {
	m, id := eventTestManager(t)
	ctx := context.Background()
	online := []model.UPS{{Name: "ups", Status: "OL", ChargePct: 100, HasCharge: true}}
	set := func(list []model.UPS, low []string) {
		m.overviewMu.Lock()
		m.overview[id] = hostOverview{reachable: true, ups: upsSnapshot(list, low)}
		m.overviewMu.Unlock()
	}
	kinds := func() []string {
		events, _, _ := m.Events(ctx, 20)
		var out []string
		for i := len(events) - 1; i >= 0; i-- {
			out = append(out, events[i].Kind)
		}
		return out
	}

	m.noteUPS(ctx, id, online, nil) // первое наблюдение
	set(online, nil)
	m.noteUPS(ctx, id, nil, nil) // старый хост
	if k := kinds(); len(k) != 0 {
		t.Fatalf("лишние события: %v", k)
	}

	battery := []model.UPS{{Name: "ups", Status: "OB DISCHRG", OnBattery: true, ChargePct: 80, HasCharge: true, RuntimeS: 900, HasRuntime: true}}
	m.noteUPS(ctx, id, battery, nil)
	set(battery, nil)
	m.noteUPS(ctx, id, battery, nil) // без перемен — тишина
	low := []model.UPS{{Name: "ups", Status: "OB DISCHRG", OnBattery: true, ChargePct: 20, HasCharge: true, RuntimeS: 200, HasRuntime: true}}
	m.noteUPS(ctx, id, low, []string{"ups"})
	set(low, []string{"ups"})
	m.noteUPS(ctx, id, online, nil)
	set(online, nil)
	m.noteUPS(ctx, id, []model.UPS{{Name: "ups", Error: "Driver not connected"}}, nil)

	want := []string{store.EventPowerLost, store.EventBatteryLow, store.EventPowerRestored, store.EventUPSLost}
	got := kinds()
	if len(got) != len(want) {
		t.Fatalf("события %v, ждали %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("события %v, ждали %v", got, want)
		}
	}
	if s := m.EventSettings(ctx); !s.Notify[store.EventPowerLost] || !s.Notify[store.EventBatteryLow] {
		t.Errorf("по умолчанию не будят: %v", s.Notify)
	}
}
