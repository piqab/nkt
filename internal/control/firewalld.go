package control

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/store"
)

// FirewalldManager changes firewall rules through firewall-cmd — the second
// backend alongside FirewallManager's ufw, coexisting rather than replacing
// it: which one (if either) a given host actually runs is discovered at
// scan time (parse.Firewall), not decided here.
type FirewalldManager struct {
	cfg *config.Config
	c   collect.Collector
	db  *store.DB
}

// NewFirewalldManager builds the firewalld control plane.
func NewFirewalldManager(cfg *config.Config, c collect.Collector, db *store.DB) *FirewalldManager {
	return &FirewalldManager{cfg: cfg, c: c, db: db}
}

var firewalldNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// FirewalldPortSpec describes a port or named service to add/remove in one
// zone. Exactly one of (Port, Protocol) or Service must be set — firewalld
// treats a port and a service as alternative, not composable, targets of
// the same --add-port/--add-service flag pair.
//
// Runtime and Permanent are independent choices, not a single "how" — this
// is firewalld's defining difference from ufw's one flat ruleset (see
// parseFirewalldZones's doc comment). Applying to both is the common case
// and mirrors how ufw's AddRule always does the equivalent of "now and
// persisted" in one step, but the two are genuinely separable here: an
// operator might deliberately want a temporary runtime-only rule that a
// reload would drop, or pre-stage a permanent one without touching traffic
// until the next planned reload.
type FirewalldPortSpec struct {
	Zone      string `json:"zone"`
	Port      int    `json:"port,omitempty"`
	Protocol  string `json:"protocol,omitempty"` // tcp | udp
	Service   string `json:"service,omitempty"`
	Permanent bool   `json:"permanent"`
	Runtime   bool   `json:"runtime"`
}

// Validate rejects anything that would be unsafe or meaningless to pass to
// firewall-cmd.
func (s FirewalldPortSpec) Validate() error {
	if !firewalldNameRe.MatchString(s.Zone) {
		return fmt.Errorf("недопустимое имя зоны: %q", s.Zone)
	}
	if s.Service != "" {
		if s.Port != 0 || s.Protocol != "" {
			return fmt.Errorf("укажите либо service, либо port+protocol, не оба сразу")
		}
		if !firewalldNameRe.MatchString(s.Service) {
			return fmt.Errorf("недопустимое имя сервиса: %q", s.Service)
		}
	} else {
		if s.Port < 1 || s.Port > 65535 {
			return fmt.Errorf("порт вне диапазона 1..65535: %d", s.Port)
		}
		switch s.Protocol {
		case "tcp", "udp":
		default:
			return fmt.Errorf("протокол должен быть tcp или udp, получено %q", s.Protocol)
		}
	}
	if !s.Permanent && !s.Runtime {
		return fmt.Errorf("нужно выбрать хотя бы одно: применить сейчас или сохранить постоянно")
	}
	return nil
}

// target is the audit-log object name for this spec — "zone:port/proto" or
// "zone:service".
func (s FirewalldPortSpec) target() string {
	if s.Service != "" {
		return s.Zone + ":" + s.Service
	}
	return fmt.Sprintf("%s:%d/%s", s.Zone, s.Port, s.Protocol)
}

// argv builds one firewall-cmd invocation — verb is "add" or "remove",
// permanent picks which of the two independent stores this particular call
// targets (a full apply-both request makes two separate calls, one per
// store, since firewall-cmd itself has no single flag that means both).
func (s FirewalldPortSpec) argv(verb string, permanent bool) []string {
	var args []string
	if permanent {
		args = append(args, "--permanent")
	}
	args = append(args, "--zone="+s.Zone)
	if s.Service != "" {
		args = append(args, fmt.Sprintf("--%s-service=%s", verb, s.Service))
	} else {
		args = append(args, fmt.Sprintf("--%s-port=%d/%s", verb, s.Port, s.Protocol))
	}
	return args
}

// apply runs verb ("add"/"remove") against every store the spec asks for,
// stopping at the first failure (a permanent-store failure after a runtime
// one already succeeded still leaves the runtime change in effect and
// reports the error — nothing here can roll back a firewall-cmd call that
// already took effect, only report honestly that only one side landed).
func (f *FirewalldManager) apply(ctx context.Context, user, auditAction string, spec FirewalldPortSpec, verb string) (collect.CommandResult, error) {
	if err := spec.Validate(); err != nil {
		return collect.CommandResult{}, err
	}

	var (
		res     collect.CommandResult
		err     error
		argvs   [][]string
		outputs []string
	)
	run := func(permanent bool) {
		args := spec.argv(verb, permanent)
		argvs = append(argvs, append([]string{"firewall-cmd"}, args...))
		res, err = f.c.Run(ctx, "firewall-cmd", args...)
		outputs = append(outputs, strings.TrimSpace(res.Output()))
		if err == nil && !res.OK() {
			err = fmt.Errorf("firewall-cmd вернул код %d: %s", res.ExitCode, strings.TrimSpace(res.Output()))
		}
	}

	if spec.Runtime {
		run(false)
	}
	if err == nil && spec.Permanent {
		run(true)
	}

	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	f.db.Audit(ctx, user, auditAction, spec.target(), outcome,
		map[string]any{"argv": argvs, "output": strings.Join(outputs, "\n")})
	return res, err
}

// AddRule adds a port or service to a zone, in whichever of the runtime/
// permanent stores spec asks for.
func (f *FirewalldManager) AddRule(ctx context.Context, user string, spec FirewalldPortSpec) (collect.CommandResult, error) {
	return f.apply(ctx, user, "firewall.add", spec, "add")
}

// DeleteRule removes a port or service from a zone. Unlike ufw's
// DeleteRule, there is no index to verify against first: firewall-cmd's own
// --remove-port/--remove-service target the exact same (zone, port/service)
// pair AddRule used to create it, so there's nothing that could shift
// underneath a stale page the way ufw's numbered rules can.
func (f *FirewalldManager) DeleteRule(ctx context.Context, user string, spec FirewalldPortSpec) (collect.CommandResult, error) {
	return f.apply(ctx, user, "firewall.delete", spec, "remove")
}

// Reload re-applies firewalld's permanent configuration, replacing whatever
// runtime-only state was in effect — the same "make permanent state live"
// step ufw's Reload performs, though the two mean something different:
// ufw has no runtime/permanent split at all, so its reload is closer to
// "restart the daemon with its current rules" than to this.
func (f *FirewalldManager) Reload(ctx context.Context, user string) (collect.CommandResult, error) {
	res, err := f.c.Run(ctx, "firewall-cmd", "--reload")
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	f.db.Audit(ctx, user, "firewall.reload", "firewalld", outcome, strings.TrimSpace(res.Output()))
	if err != nil {
		return res, err
	}
	if !res.OK() {
		return res, fmt.Errorf("firewall-cmd --reload: код %d: %s", res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return res, nil
}
