package hub

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// fakeHostJobs — API заданий хоста: POST с ?job=1 заводит задание, журнал
// отдаётся по частям, на третий опрос — итог.
type fakeHostJobs struct {
	mu       sync.Mutex
	noJob    bool   // старый хост: ?job=1 не знает
	outcome  string // succeeded | failed | never
	polls    int
	canceled bool
	paths    []string
}

func (f *fakeHostJobs) call(_ *Manager, _ context.Context, _ int64, method, path string, _, out any) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, method+" "+path)
	var reply any
	switch {
	case method == "POST" && strings.HasSuffix(path, "/cancel"):
		f.canceled = true
		return 200, nil
	case method == "POST":
		if f.noJob {
			reply = map[string]any{"status": "ok"}
		} else {
			reply = map[string]any{"job_id": 7}
		}
	default:
		f.polls++
		status := "running"
		if f.polls >= 3 && f.outcome != "never" {
			status = f.outcome
		}
		reply = map[string]any{
			"job":   store.Job{ID: 7, Status: status, Error: map[bool]string{true: "apt-get: exit 100"}[status == "failed"]},
			"lines": []store.JobLogLine{{Seq: int64(f.polls), Text: "строка " + string(rune('0'+f.polls))}},
		}
	}
	if out != nil {
		raw, _ := json.Marshal(reply)
		_ = json.Unmarshal(raw, out)
	}
	return 200, nil
}

func TestRunHostJob(t *testing.T) {
	oldPoll := hostJobPollEvery
	hostJobPollEvery = 10 * time.Millisecond
	t.Cleanup(func() { hostJobPollEvery = oldPoll; hostAPIForJobs = (*Manager).HostAPI })
	m, _ := newTestManager(t)

	run := func(f *fakeHostJobs, ctx context.Context) (error, []string) {
		hostAPIForJobs = f.call
		var lines []string
		err := m.runHostJob(ctx, 1, "/api/system/apt/download", map[string]any{"packages": []string{"wireguard-tools"}}, func(l string) { lines = append(lines, l) })
		return err, lines
	}

	ok := &fakeHostJobs{outcome: "succeeded"}
	if err, lines := run(ok, t.Context()); err != nil || len(lines) != 3 || ok.paths[0] != "POST /api/system/apt/download?job=1" {
		t.Errorf("успех: %v %v %v", err, lines, ok.paths)
	}
	bad := &fakeHostJobs{outcome: "failed"}
	if err, _ := run(bad, t.Context()); err == nil || !strings.Contains(err.Error(), "exit 100") {
		t.Errorf("ошибка задания хоста: %v", err)
	}
	old := &fakeHostJobs{noJob: true}
	if err, _ := run(old, t.Context()); err != nil || len(old.paths) != 1 {
		t.Errorf("старый хост: %v %v", err, old.paths)
	}
	hang := &fakeHostJobs{outcome: "never"}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err, _ := run(hang, ctx); err == nil || !hang.canceled {
		t.Errorf("отмена задания хаба должна отменить задание хоста: %v, отменено=%v", err, hang.canceled)
	}
}
