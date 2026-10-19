package api

import (
	"net/http"
	"regexp"
	"sort"
	"time"
)

// HubIDHeader — идентификатор экземпляра хаба в его запросах к хосту. По
// нему хост видит, что к нему ходят два хаба сразу (тестовый хаб с
// импортом рабочего): оба управляли бы одними стеками, маршрутами и
// обновлениями, мешая друг другу. Хост помнит хабы за hubsSeenWindow и
// отдаёт их в /api/overview — хаб помечает хост.
const HubIDHeader = "X-NKT-Hub"

const (
	hubsSeenWindow = 15 * time.Minute
	hubsSeenMax    = 16
)

var hubIDRe = regexp.MustCompile(`^[0-9a-f]{16,64}$`)

// noteHub — запомнить хаб запроса (идентификатор только из 16–64
// шестнадцатеричных знаков: чужое значение в память не попадает).
func (s *Server) noteHub(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get(HubIDHeader); hubIDRe.MatchString(id) {
			s.hubsMu.Lock()
			if s.hubsSeen == nil {
				s.hubsSeen = map[string]time.Time{}
			}
			now := time.Now()
			for k, t := range s.hubsSeen {
				if now.Sub(t) > hubsSeenWindow {
					delete(s.hubsSeen, k)
				}
			}
			if _, ok := s.hubsSeen[id]; ok || len(s.hubsSeen) < hubsSeenMax {
				s.hubsSeen[id] = now
			}
			s.hubsMu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

// HubSeen — хаб, обращавшийся к хосту, и сколько секунд назад.
type HubSeen struct {
	ID   string `json:"id"`
	AgoS int64  `json:"ago_s"`
}

func (s *Server) hubsSeenList() []HubSeen {
	s.hubsMu.Lock()
	defer s.hubsMu.Unlock()
	out := []HubSeen{}
	now := time.Now()
	for id, t := range s.hubsSeen {
		if ago := now.Sub(t); ago <= hubsSeenWindow {
			out = append(out, HubSeen{ID: id, AgoS: int64(ago.Seconds())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgoS < out[j].AgoS })
	return out
}
