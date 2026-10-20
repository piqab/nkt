package hub

import (
	"errors"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Ход переноса для карты: шаги, доля, цели, видимость по токену и уборка
// законченного.
func TestMigrateLive(t *testing.T) {
	jc := &jobs.Context{Job: store.Job{ID: 9001}}
	liveBegin(jc, 1, 2)
	liveSet(jc, func(l *migrateLive) { l.Project, l.Containers = "garage", []string{"garage-1"} })
	liveStage(jc, liveStageCopy)
	liveSet(jc, func(l *migrateLive) { l.Percent = 40 })
	find := func(allow func(int64) bool) *migrateLive {
		for _, l := range liveList(allow) {
			if l.Job == 9001 {
				return &l
			}
		}
		return nil
	}
	l := find(nil)
	if l == nil || l.Stage != liveStageCopy || l.Percent != 40 || l.From != 1 || l.To != 2 || l.Project != "garage" {
		t.Fatalf("live: %+v", l)
	}
	if find(func(id int64) bool { return id == 1 }) != nil {
		t.Fatal("токен без нового хоста видит перенос")
	}
	// Чужое задание без liveBegin — ничего не заводится.
	liveStage(&jobs.Context{Job: store.Job{ID: 9002}}, liveStageCopy)
	for _, x := range liveList(nil) {
		if x.Job == 9002 {
			t.Fatal("ход без начала")
		}
	}
	liveEnd(jc, errors.New("x"))
	if l := find(nil); l == nil || l.Stage != liveStageFailed {
		t.Fatalf("после ошибки: %+v", l)
	}
	migrateLiveMu.Lock()
	migrateLiveMap[9001].ended = time.Now().Add(-migrateLiveKeep - time.Second)
	migrateLiveMu.Unlock()
	if find(nil) != nil {
		t.Fatal("законченный давно — всё ещё в списке")
	}
}
