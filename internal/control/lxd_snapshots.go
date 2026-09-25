package control

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// LXDSnapshot — снимок инстанса LXD.
type LXDSnapshot struct {
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Stateful  bool       `json:"stateful"`
	// Size — байты на пуле хранения; -1, если драйвер не сообщает.
	Size int64 `json:"size"`
}

var lxdSnapshotNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

func validLXDInstance(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/?&# ")
}

// ListSnapshots — снимки инстанса, новые сверху. lxc query отдаёт JSON
// REST API LXD без разбора табличного вывода.
func (m *LXDManager) ListSnapshots(ctx context.Context, name string) ([]LXDSnapshot, error) {
	if !validLXDInstance(name) {
		return nil, msgs.Errorf("control.invalidInstanceName", name)
	}
	res, err := m.c.Run(ctx, "lxc", "query", "/1.0/instances/"+name+"/snapshots?recursion=1")
	if err != nil {
		return nil, fmt.Errorf("lxc query: %w", err)
	}
	if !res.OK() {
		return nil, msgs.Errorf("control.lxcCode", "query", name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return parseLXDSnapshots([]byte(res.Stdout))
}

func parseLXDSnapshots(data []byte) ([]LXDSnapshot, error) {
	var raw []struct {
		Name      string    `json:"name"`
		CreatedAt time.Time `json:"created_at"`
		ExpiresAt time.Time `json:"expires_at"`
		Stateful  bool      `json:"stateful"`
		Size      int64     `json:"size"`
	}
	if strings.TrimSpace(string(data)) == "" {
		return []LXDSnapshot{}, nil
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("lxc query snapshots: %w", err)
	}
	out := make([]LXDSnapshot, 0, len(raw))
	for _, s := range raw {
		// Старые LXD отдают «инстанс/снимок».
		n := s.Name
		if i := strings.LastIndexByte(n, '/'); i >= 0 {
			n = n[i+1:]
		}
		sn := LXDSnapshot{Name: n, CreatedAt: s.CreatedAt, Stateful: s.Stateful, Size: s.Size}
		// Нулевая дата LXD (0001-01-01) — «без срока».
		if s.ExpiresAt.Year() > 1 {
			e := s.ExpiresAt
			sn.ExpiresAt = &e
		}
		out = append(out, sn)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// SnapshotAction — create | restore | delete снимка snap инстанса name.
// stateful — со снимком памяти (только create, нужен CRIU/VM-агент).
func (m *LXDManager) SnapshotAction(ctx context.Context, user, name, snap, action string, stateful bool) error {
	if !validLXDInstance(name) {
		return msgs.Errorf("control.invalidInstanceName", name)
	}
	if !lxdSnapshotNameRe.MatchString(snap) {
		return msgs.Errorf("control.invalidSnapshotName", snap)
	}
	var args []string
	switch action {
	case "create":
		args = []string{"snapshot", name, snap}
		if stateful {
			args = append(args, "--stateful")
		}
	case "restore":
		args = []string{"restore", name, snap}
	case "delete":
		args = []string{"delete", name + "/" + snap}
	default:
		return msgs.Errorf("control.invalidActionInstance", action)
	}
	res, err := m.c.Run(ctx, "lxc", args...)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "lxd.snapshot."+action, name+"/"+snap, outcome, map[string]any{
		"exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("lxc %s: %w", args[0], err)
	}
	if !res.OK() {
		return msgs.Errorf("control.lxcCode", args[0], name+"/"+snap, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}
