package hub

import (
	"context"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Общая галочка «Использовать кэш пакетов хаба» («О системе»). Выключена
// — хаб не держит проброс apt ни на один хост и не ставит его при
// установке; отметки «пакеты через хаб» у хостов остаются (их выбор) и
// снова работают после включения. По умолчанию выключена; у хаба, где
// кэшем уже пользуется хоть один хост, — включена (иначе после
// обновления apt у них перестал бы работать). Уходит в экспорт.

const aptCacheEnabledKey = "aptcache.enabled"

// KindAptCacheToggle — задание: снять или вернуть прокси apt на хостах с
// отметкой при выключении или включении кэша.
const KindAptCacheToggle = "aptcache.toggle"

// AptCacheEnabled — включён ли кэш пакетов хаба.
func (m *Manager) AptCacheEnabled(ctx context.Context) bool {
	if v, ok, err := m.db.KVGet(ctx, aptCacheEnabledKey); err == nil && ok && v != "" {
		return v == "1"
	}
	if hosts, err := m.db.ListHosts(ctx); err == nil {
		for _, h := range hosts {
			if h.AptViaHub {
				return true
			}
		}
	}
	return false
}

// AptCacheToggleParams — вход задания.
type AptCacheToggleParams struct {
	Enabled bool `json:"enabled"`
}

// AptCacheToggleRunner — исполнитель.
type AptCacheToggleRunner struct{ m *Manager }

// NewAptCacheToggleRunner строит исполнителя.
func NewAptCacheToggleRunner(m *Manager) *AptCacheToggleRunner { return &AptCacheToggleRunner{m: m} }

// Run — прокси apt на хостах с отметкой: убрать (выключен) или вернуть.
// Хост не на связи — пропуск с записью: при включении проброс поднимется
// сам, а снять настройку — повторить задание, когда он вернётся.
func (r *AptCacheToggleRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p AptCacheToggleParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	hosts, err := r.m.db.ListHosts(ctx)
	if err != nil {
		return err
	}
	var todo []store.Host
	for _, h := range hosts {
		if h.AptViaHub {
			todo = append(todo, h)
		}
	}
	if len(todo) == 0 {
		jc.Log("hub.aptToggleNone")
		return nil
	}
	failed := 0
	for i, h := range todo {
		jc.StepKey(i+1, len(todo), "hub.aptToggleHost", h.Name)
		if h.Status != store.HostStatusOnline {
			jc.Log("hub.aptToggleOffline", h.Name)
			failed++
			continue
		}
		if err := r.m.applyAptProxyHost(ctx, h.ID, p.Enabled); err != nil {
			jc.Log("hub.aptToggleFailed", h.Name, msgs.Localize(jc.Lang(), err))
			failed++
			continue
		}
		jc.Log("hub.aptToggleDone", h.Name)
	}
	if failed > 0 {
		return msgs.Errorf("hub.aptToggleSome", failed, len(todo))
	}
	return nil
}
