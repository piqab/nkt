package hub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// hostCall — запрос к API хоста: управляемого — по SSH-туннелю, машины
// самого хаба (localHostID) — через встроенный API от имени user.
func (s *Server) hostCall(ctx context.Context, user string, hostID int64, method, path string, in, out any) (int, error) {
	if hostID == localHostID {
		return s.localAPI(ctx, user, method, path, in, out)
	}
	return s.hub.HostAPI(ctx, hostID, method, path, in, out)
}

// waitHostJobVia ждёт задание на хосте, пересказывая его журнал в своё
// (как GroupApplyRunner.waitHostJob, но и для машины хаба).
func (s *Server) waitHostJobVia(ctx context.Context, jc *jobs.Context, user string, hostID, jobID int64) error {
	deadline := time.Now().Add(hostJobTimeout)
	var after int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.jobHostDidFinishWithin", hostJobTimeout)
		}
		var res struct {
			Job   store.Job          `json:"job"`
			Lines []store.JobLogLine `json:"lines"`
		}
		path := fmt.Sprintf("/api/jobs/%d/log?after=%d", jobID, after)
		if _, err := s.hostCall(ctx, user, hostID, "GET", path, nil, &res); err != nil {
			jc.Log("hub.connectionLostRetrying", err)
			if !sleepCtx(ctx, hostJobPoll) {
				return ctx.Err()
			}
			continue
		}
		for _, line := range res.Lines {
			after = line.Seq
			jc.Logf("      %s", line.Text)
		}
		switch res.Job.Status {
		case store.JobSucceeded:
			return nil
		case store.JobFailed, store.JobCanceled, store.JobInterrupted:
			return msgs.Errorf("hub.jobHost", res.Job.Status, res.Job.Error)
		}
		if !sleepCtx(ctx, hostJobPoll) {
			return ctx.Err()
		}
	}
}

// targetHost — хост цели выкладки или сайта.
type targetHost struct {
	ID   int64
	Name string
	Addr string
}

// resolveHosts — хосты по именам и группе (машина хаба — «localhost»);
// только работающие. Неизвестное имя — ошибка: выкладывать «куда-то не
// туда» молча нельзя.
func (s *Server) resolveHosts(ctx context.Context, names []string, group string) ([]targetHost, error) {
	hosts, err := s.db.ListHosts(ctx)
	if err != nil {
		return nil, err
	}
	var out []targetHost
	seen := map[int64]bool{}
	add := func(t targetHost) {
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	for _, n := range names {
		if n == "localhost" && s.local != nil {
			add(targetHost{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"})
			continue
		}
		found := false
		for _, h := range hosts {
			// Без учёта регистра и пробелов по краям: «Web-1» в описании —
			// тот же хост, что web-1 на хабе.
			if sameHostName(h.Name, n) {
				found = true
				if h.Status != store.HostStatusOnline {
					return nil, msgs.Errorf("hub.hostReadyYetStatus", h.Name, h.Status)
				}
				add(targetHost{ID: h.ID, Name: h.Name, Addr: h.Addr})
			}
		}
		if !found {
			if like := similarHosts(n, hosts); len(like) > 0 {
				return nil, msgs.Errorf("deploy.hostUnknownLike", n, strings.Join(like, ", "))
			}
			return nil, msgs.Errorf("deploy.hostUnknown", n)
		}
	}
	if group != "" {
		if s.local != nil && s.hub.LocalHostGroup(ctx) == group {
			add(targetHost{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"})
		}
		for _, h := range hosts {
			if h.Group == group && h.Status == store.HostStatusOnline {
				add(targetHost{ID: h.ID, Name: h.Name, Addr: h.Addr})
			}
		}
	}
	if len(out) == 0 {
		return nil, msgs.Errorf("deploy.noHosts")
	}
	return out, nil
}

// similarHosts — похожие имена хостов хаба (общая часть имени), не больше
// пяти; нет похожих — все, если их немного.
func similarHosts(name string, hosts []store.Host) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	var out []string
	for _, h := range hosts {
		hn := strings.ToLower(h.Name)
		if n != "" && (strings.Contains(hn, n) || strings.Contains(n, hn) || (len(n) >= 3 && len(hn) >= 3 && hn[:3] == n[:3])) {
			out = append(out, h.Name)
		}
	}
	if len(out) == 0 && len(hosts) <= 8 {
		for _, h := range hosts {
			out = append(out, h.Name)
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}
