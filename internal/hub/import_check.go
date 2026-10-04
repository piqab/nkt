package hub

import (
	"context"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// После импорта хаб сам смотрит, стоит ли nkt на перенесённых хостах:
// хост, где nkt нет (например, удалили перед переездом), становится «не
// установлен» — серый, с кнопкой установки, а не красным «недоступен».
// Хост, до которого не достучаться, и хост с nkt остаются как есть.

// importCheckParallel — сколько хостов проверяется сразу.
const importCheckParallel = 3

// CheckImportedHosts — проверка хостов с этими именами (в фоне).
func (m *Manager) CheckImportedHosts(names []string) {
	if len(names) == 0 {
		return
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		hosts, err := m.db.ListHosts(ctx)
		if err != nil {
			return
		}
		sem := make(chan struct{}, importCheckParallel)
		var wg sync.WaitGroup
		for _, h := range hosts {
			if !want[h.Name] || h.Status == store.HostStatusNew || h.Status == store.HostStatusInstalling {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(h store.Host) {
				defer wg.Done()
				defer func() { <-sem }()
				m.checkImportedHost(ctx, h)
			}(h)
		}
		wg.Wait()
	}()
}

func (m *Manager) checkImportedHost(ctx context.Context, h store.Host) {
	dialCtx, cancel := context.WithTimeout(ctx, sshDialTimeout)
	defer cancel()
	link, err := m.dialHost(dialCtx, h)
	if err != nil {
		return
	}
	defer link.Close()
	version, active, err := probeExistingInstall(link.client, remoteBinPath, "netknownsthat")
	if err != nil || version != "" || active == "active" {
		return
	}
	// nkt нет: установка с нуля, как у нового хоста.
	_ = m.db.SetHostVersion(ctx, h.ID, "")
	_ = m.db.SetHostStatus(ctx, h.ID, store.HostStatusNew, "")
	m.log.Info("imported host has no nkt", "host", h.Name)
}
