package parse

import (
	"context"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/osinfo"
)

// ОС внутри контейнеров (значок перед именем): /etc/os-release
// запущенного контейнера — одно exec на контейнер, ответ помнится по ID
// (новый контейнер — новый ID, новое чтение). Внутри нет ни cat, ни файла
// (scratch, distroless) — Linux образа: на Linux-хосте других контейнеров
// не бывает.

var (
	ctOSMu    sync.Mutex
	ctOSCache = map[string]*model.OSInfo{} // engine + "/" + ID
)

// containerOSParallel — сколько exec идёт одновременно (первый скан хоста
// с десятками контейнеров).
const containerOSParallel = 4

// fillContainerOS — ОС каждого запущенного контейнера из списка ids
// (ID → куда записать); кэш забывает контейнеры, которых больше нет.
func fillContainerOS(ctx context.Context, c collect.Collector, engine string, targets map[string]**model.OSInfo) {
	ctOSMu.Lock()
	for k := range ctOSCache {
		if len(k) > len(engine) && k[:len(engine)+1] == engine+"/" {
			if _, ok := targets[k[len(engine)+1:]]; !ok {
				delete(ctOSCache, k)
			}
		}
	}
	var todo []string
	for id, dst := range targets {
		if o, ok := ctOSCache[engine+"/"+id]; ok {
			*dst = o
		} else {
			todo = append(todo, id)
		}
	}
	ctOSMu.Unlock()

	sem := make(chan struct{}, containerOSParallel)
	var wg sync.WaitGroup
	for _, id := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer func() { <-sem; wg.Done() }()
			o := readContainerOS(ctx, c, engine, id)
			ctOSMu.Lock()
			ctOSCache[engine+"/"+id] = o
			*targets[id] = o
			ctOSMu.Unlock()
		}(id)
	}
	wg.Wait()
}

// readContainerOS — os-release запущенного контейнера.
func readContainerOS(ctx context.Context, c collect.Collector, engine, id string) *model.OSInfo {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if res, err := c.Run(ctx, engine, "exec", id, "cat", "/etc/os-release"); err == nil && res.OK() {
		if o := osinfo.FromOSRelease(res.Stdout, osinfo.SourceOSRelease); o != nil {
			return o
		}
	}
	return &model.OSInfo{ID: "linux", Name: "Linux", Source: osinfo.SourceImage}
}
