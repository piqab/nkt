package hub

import (
	"context"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

// События ИБП по опросу хоста: переход на батарею и обратно, батарея
// садится (пороги — хоста, он сам отдаёт ups_low), нет связи с ИБП.
// Сравнение — с прошлым опросом, как у перезагрузки: первое появление
// хоста событий не даёт.

// upsSeen — что хаб знал о ИБП хоста на прошлом опросе.
type upsSeen struct {
	onBattery bool
	low       bool
	lost      bool
}

func upsSnapshot(list []model.UPS, low []string) map[string]upsSeen {
	if list == nil {
		return nil
	}
	isLow := map[string]bool{}
	for _, n := range low {
		isLow[n] = true
	}
	out := make(map[string]upsSeen, len(list))
	for _, u := range list {
		out[u.Name] = upsSeen{onBattery: u.OnBattery, low: isLow[u.Name], lost: u.Error != ""}
	}
	return out
}

// noteUPS пишет события ИБП хоста. Хост старой версии (без ups в обзоре)
// молчит.
func (m *Manager) noteUPS(ctx context.Context, hostID int64, list []model.UPS, low []string) {
	if list == nil {
		return
	}
	m.overviewMu.Lock()
	prevOV, seen := m.overview[hostID]
	m.overviewMu.Unlock()
	if !seen || prevOV.ups == nil {
		return
	}
	var host *store.Host
	get := func() bool {
		if host == nil {
			h, err := m.db.HostByID(ctx, hostID)
			if err != nil {
				return false
			}
			host = &h
		}
		return true
	}
	now := upsSnapshot(list, low)
	for _, u := range list {
		cur, prev := now[u.Name], prevOV.ups[u.Name]
		link := "/hardware"
		switch {
		case cur.lost && !prev.lost:
			if get() {
				m.recordEventLink(ctx, *host, store.EventUPSLost, "", link, "hub.upsLost", u.Name, u.Error)
			}
		case cur.onBattery && !prev.onBattery:
			if get() {
				m.recordEventLink(ctx, *host, store.EventPowerLost, "", link, "hub.upsOnBattery", u.Name, upsCharge(u), upsRuntime(u))
			}
		case !cur.onBattery && prev.onBattery && !cur.lost:
			if get() {
				m.recordEventLink(ctx, *host, store.EventPowerRestored, "", link, "hub.upsOnline", u.Name, upsCharge(u))
			}
		}
		if cur.low && !prev.low && get() {
			m.recordEventLink(ctx, *host, store.EventBatteryLow, "critical", link, "hub.upsBatteryLow", u.Name, upsCharge(u), upsRuntime(u))
		}
	}
}

func upsCharge(u model.UPS) string {
	if !u.HasCharge {
		return "?"
	}
	return fmt.Sprintf("%.0f", u.ChargePct)
}

func upsRuntime(u model.UPS) string {
	if !u.HasRuntime {
		return "?"
	}
	return strings.TrimSpace(fmt.Sprintf("%d", u.RuntimeS/60))
}
