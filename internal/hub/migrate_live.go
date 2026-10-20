package hub

import (
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/jobs"
)

// Ход идущих переносов для карты хостов: шаг, доля скопированного, цели
// Traffic Fly, конвейер и сайт. Только в памяти хаба: по шагу карта
// рисует перенос вживую (призрак плитки на новом хосте, бегущие данные,
// переключение линий маршрутов и конвейера). Журнал задания — текст на
// языке автора, по нему карта не разбирает.

// Шаги переноса для карты.
const (
	liveStagePlan     = "plan"
	liveStagePack     = "pack"
	liveStageCopy     = "copy"
	liveStageUnpack   = "unpack"
	liveStageFlyAdd   = "fly-add"
	liveStageFlyWait  = "fly-wait"
	liveStageSwitched = "fly-switched"
	liveStagePipeline = "pipeline"
	liveStageSite     = "site"
	liveStageDone     = "done"
	liveStageFailed   = "failed"
)

// migrateLiveKeep — сколько законченный перенос ещё отдаётся карте:
// успевает дорисовать последний шаг, пока перечитывает хосты.
const migrateLiveKeep = 20 * time.Second

// migrateLiveFly — цель маршрута в переносе.
type migrateLiveFly struct {
	Route string `json:"route"`
	Ref   string `json:"ref"`
	// Old / New — цели на старом и новом хосте (New — после добавления).
	Old string `json:"old"`
	New string `json:"new,omitempty"`
	// WasActive — старая была рабочей (её трафик переходит на новую).
	WasActive bool `json:"was_active"`
	// Alive — новая ответила шлюзу.
	Alive bool `json:"alive,omitempty"`
}

// migrateLive — ход одного переноса.
type migrateLive struct {
	Job        int64    `json:"job"`
	From       int64    `json:"from"`
	To         int64    `json:"to"`
	Project    string   `json:"project,omitempty"`
	Containers []string `json:"containers,omitempty"`
	Stage      string   `json:"stage"`
	// Percent — доля переданного (шаг copy).
	Percent int              `json:"percent"`
	Fly     []migrateLiveFly `json:"fly,omitempty"`
	// Pipeline — конвейер стека, который переключается на новый хост.
	Pipeline     int64    `json:"pipeline,omitempty"`
	PipelineName string   `json:"pipeline_name,omitempty"`
	Site         []string `json:"site,omitempty"`
	// SiteState — "" (не трогали), "setup", "ok", "failed".
	SiteState string `json:"site_state,omitempty"`
	Started   string `json:"started"`
	// ended — когда закончился (для migrateLiveKeep).
	ended time.Time
}

var migrateLiveMu sync.Mutex
var migrateLiveMap = map[int64]*migrateLive{}

// liveBegin — перенос начался (задание jc).
func liveBegin(jc *jobs.Context, from, to int64) {
	if jc == nil {
		return
	}
	migrateLiveMu.Lock()
	defer migrateLiveMu.Unlock()
	migrateLiveMap[jc.Job.ID] = &migrateLive{Job: jc.Job.ID, From: from, To: to, Stage: liveStagePlan, Started: time.Now().UTC().Format(time.RFC3339)}
}

// liveSet — правка хода переноса задания jc (нет такого — ничего).
func liveSet(jc *jobs.Context, f func(*migrateLive)) {
	if jc == nil {
		return
	}
	migrateLiveMu.Lock()
	defer migrateLiveMu.Unlock()
	if l := migrateLiveMap[jc.Job.ID]; l != nil {
		f(l)
	}
}

// liveStage — шаг переноса.
func liveStage(jc *jobs.Context, stage string) { liveSet(jc, func(l *migrateLive) { l.Stage = stage }) }

// liveEnd — перенос закончился: удачно — done, иначе failed.
func liveEnd(jc *jobs.Context, err error) {
	liveSet(jc, func(l *migrateLive) {
		l.Stage = liveStageDone
		if err != nil {
			l.Stage = liveStageFailed
		}
		l.ended = time.Now()
	})
}

// liveList — идущие и только что законченные переносы.
func liveList(allow func(int64) bool) []migrateLive {
	migrateLiveMu.Lock()
	defer migrateLiveMu.Unlock()
	out := []migrateLive{}
	for id, l := range migrateLiveMap {
		if !l.ended.IsZero() && time.Since(l.ended) > migrateLiveKeep {
			delete(migrateLiveMap, id)
			continue
		}
		if allow != nil && (!allow(l.From) || !allow(l.To)) {
			continue
		}
		c := *l
		c.Containers = slices.Clone(l.Containers)
		c.Fly = slices.Clone(l.Fly)
		c.Site = slices.Clone(l.Site)
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b migrateLive) int { return int(a.Job - b.Job) })
	return out
}

// handleMigrateLive — GET /hub/migrate/live: ход переносов для карты.
func (s *Server) handleMigrateLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"live": liveList(s.scopeFilter(r.Context()))})
}
