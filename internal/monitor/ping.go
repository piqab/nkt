package monitor

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// PingRunner запускает ping. В юните nkt ping лишён file capabilities
// (NoNewPrivileges) и отвечает «Operation not permitted» — main подменяет
// его запуском вне песочницы (api.RunTooling), как у virsh и lxc.
var PingRunner = func(ctx context.Context, argv ...string) (output string, exitCode int, err error) {
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code, err = ee.ExitCode(), nil
	}
	return string(out), code, err
}

// probeICMP — один ping; «доступна» = ответ пришёл за таймаут проверки.
func (p *Prober) probeICMP(ctx context.Context, t store.Target) string {
	wait := int(p.cfg.ProbeTimeout / time.Second)
	if wait < 1 {
		wait = 1
	}
	ctx, cancel := context.WithTimeout(ctx, p.cfg.ProbeTimeout+2*time.Second)
	defer cancel()
	out, code, err := PingRunner(ctx, "ping", "-n", "-c", "1", "-W", strconv.Itoa(wait), t.Host)
	if err != nil {
		return shortenNetError(err)
	}
	if code != 0 {
		if line := lastLine(out); line != "" {
			return line
		}
		return "ping: no reply"
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 160 {
				l = l[:160]
			}
			return l
		}
	}
	return ""
}
