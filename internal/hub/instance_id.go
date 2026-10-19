package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/piqab/nkt/internal/api"
)

// hubInstanceKey — идентификатор этого экземпляра хаба (kv). Не
// экспортируется (store.ExportedSettingKeys его не знает): у хаба,
// поднятого из экспорта, — свой, иначе хост не отличил бы два хаба.
const hubInstanceKey = "hub.instance_id"

// instanceID — идентификатор хаба для заголовка api.HubIDHeader.
func (m *Manager) instanceID(ctx context.Context) string {
	m.instanceOnce.Do(func() {
		if v, ok, err := m.db.KVGet(ctx, hubInstanceKey); err == nil && ok && len(v) >= 16 {
			m.instance = v
			return
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		m.instance = hex.EncodeToString(b)
		_ = m.db.KVSet(ctx, hubInstanceKey, m.instance)
	})
	return m.instance
}

// otherHubAgo — сколько секунд назад к хосту обращался другой хаб (-1 —
// не обращался за окно хоста).
func otherHubAgo(seen []api.HubSeen, mine string) int64 {
	best := int64(-1)
	for _, h := range seen {
		if h.ID != mine && (best < 0 || h.AgoS < best) {
			best = h.AgoS
		}
	}
	return best
}
