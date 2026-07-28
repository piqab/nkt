package control

import (
	"context"
	"fmt"
	"strings"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/parse"
	"github.com/althq/netknownsthat/internal/store"
)

// ServiceManager performs systemd and docker actions.
type ServiceManager struct {
	cfg   *config.Config
	c     collect.Collector
	db    *store.DB
	specs map[string]parse.ServiceSpec
}

// NewServiceManager builds the service control plane.
func NewServiceManager(cfg *config.Config, c collect.Collector, db *store.DB) *ServiceManager {
	specs := map[string]parse.ServiceSpec{}
	for _, s := range parse.DefaultServiceSpecs() {
		specs[s.Name] = s
	}
	return &ServiceManager{cfg: cfg, c: c, db: db, specs: specs}
}

// Actions the API accepts. Anything else is rejected before touching the host,
// so a service name or action can never be spliced into a command.
var allowedActions = map[string]bool{
	"start": true, "stop": true, "restart": true, "reload": true,
}

// Action runs a systemd action against a known service.
func (s *ServiceManager) Action(ctx context.Context, user, service, action string) (collect.CommandResult, error) {
	spec, ok := s.specs[service]
	if !ok {
		return collect.CommandResult{}, fmt.Errorf("неизвестный сервис: %q", service)
	}
	if !allowedActions[action] {
		return collect.CommandResult{}, fmt.Errorf("недопустимое действие: %q", action)
	}
	if !contains(spec.Actions, action) {
		return collect.CommandResult{}, fmt.Errorf("действие %q не поддерживается для %s", action, service)
	}

	var res collect.CommandResult
	var err error
	if service == model.ServiceUFW && action == "reload" {
		res, err = s.c.Run(ctx, "ufw", "reload")
	} else {
		res, err = s.c.Run(ctx, "systemctl", action, spec.Unit)
	}

	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	s.db.Audit(ctx, user, "service."+action, service, outcome, map[string]any{
		"unit": spec.Unit, "exit_code": res.ExitCode,
		"output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return res, err
	}
	if !res.OK() {
		return res, fmt.Errorf("%s %s: код %d: %s", action, spec.Unit, res.ExitCode,
			strings.TrimSpace(res.Output()))
	}
	return res, nil
}

// Validate asks a service to check its own configuration. The second return
// value reports whether a validator was actually available: a missing binary is
// not the same as an invalid config, and must never trigger a rollback.
func (s *ServiceManager) Validate(ctx context.Context, service string, paths ...string) (collect.CommandResult, bool) {
	var (
		res collect.CommandResult
		err error
	)
	switch service {
	case model.ServiceNginx:
		res, err = s.c.Run(ctx, "nginx", "-t")
	case model.ServiceHAProxy:
		res, err = s.c.Run(ctx, "haproxy", "-c", "-f", s.cfg.HAProxyMainConf)
	case model.ServiceDocker:
		path := ""
		if len(paths) > 0 {
			path = paths[0]
		}
		if path == "" {
			return collect.CommandResult{}, false
		}
		res, err = s.c.Run(ctx, "docker", "compose", "-f", path, "config", "-q")
	default:
		return collect.CommandResult{}, false
	}
	if err != nil {
		return collect.CommandResult{}, false
	}
	if res.ExitCode == 127 {
		// The validator itself is missing; nothing was checked.
		return res, false
	}
	return res, true
}

// ValidateOnly runs a validation and records it, without changing anything.
func (s *ServiceManager) ValidateOnly(ctx context.Context, user, service string) (collect.CommandResult, bool, error) {
	res, ok := s.Validate(ctx, service)
	if !ok {
		return res, false, fmt.Errorf("для %s нет доступной проверки конфигурации", service)
	}
	outcome := "ok"
	if !res.OK() {
		outcome = "error"
	}
	s.db.Audit(ctx, user, "service.validate", service, outcome, strings.TrimSpace(res.Output()))
	return res, true, nil
}

// ContainerAction starts, stops or restarts a docker container.
func (s *ServiceManager) ContainerAction(ctx context.Context, user, name, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("недопустимое действие для контейнера: %q", action)
	}
	if name == "" || strings.ContainsAny(name, "/?&#") {
		return fmt.Errorf("недопустимое имя контейнера: %q", name)
	}

	_, code, err := s.c.DockerAPI(ctx, "POST", "/containers/"+name+"/"+action, nil)
	outcome := "ok"
	if err != nil || (code != 204 && code != 304) {
		outcome = "error"
	}
	s.db.Audit(ctx, user, "container."+action, name, outcome, map[string]any{"http_status": code})
	if err != nil {
		return fmt.Errorf("docker %s %s: %w", action, name, err)
	}
	if code != 204 && code != 304 {
		return fmt.Errorf("docker %s %s: HTTP %d", action, name, code)
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
