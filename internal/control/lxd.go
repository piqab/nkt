package control

import (
	"context"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/store"
)

// LXDManager performs actions against the local LXD daemon via its `lxc`
// client CLI — see parse.LXD for why the CLI rather than LXD's REST API.
type LXDManager struct {
	c  collect.Collector
	db *store.DB
}

// NewLXDManager builds the LXD control plane.
func NewLXDManager(c collect.Collector, db *store.DB) *LXDManager {
	return &LXDManager{c: c, db: db}
}

// InstanceAction starts, stops, restarts or pauses an LXD instance.
func (m *LXDManager) InstanceAction(ctx context.Context, user, name, action string) error {
	switch action {
	case "start", "stop", "restart", "pause":
	default:
		return fmt.Errorf("недопустимое действие для инстанса: %q", action)
	}
	if name == "" || strings.ContainsAny(name, "/?&# ") {
		return fmt.Errorf("недопустимое имя инстанса: %q", name)
	}

	res, err := m.c.Run(ctx, "lxc", action, name)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "lxd."+action, name, outcome, map[string]any{
		"exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("lxc %s %s: %w", action, name, err)
	}
	if !res.OK() {
		return fmt.Errorf("lxc %s %s: код %d: %s", action, name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}

// CreateInstance launches a new instance from an image — `lxc launch` does
// image-fetch, create and start in one step, so unlike Podman there is no
// separate create-then-start round trip needed here.
func (m *LXDManager) CreateInstance(ctx context.Context, user, image, name string) error {
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("укажите образ")
	}
	if name == "" || strings.ContainsAny(name, "/?&# ") {
		return fmt.Errorf("недопустимое имя инстанса: %q", name)
	}

	res, err := m.c.Run(ctx, "lxc", "launch", image, name)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "lxd.create", name, outcome, map[string]any{
		"image": image, "exit_code": res.ExitCode,
		"output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("lxc launch %s %s: %w", image, name, err)
	}
	if !res.OK() {
		return fmt.Errorf("lxc launch %s %s: код %d: %s", image, name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}

// DeleteInstance removes an instance. force also stops a running one first —
// the same graceful-vs-forced distinction the rest of this application makes
// explicit rather than silently escalating.
func (m *LXDManager) DeleteInstance(ctx context.Context, user, name string, force bool) error {
	if name == "" || strings.ContainsAny(name, "/?&#") {
		return fmt.Errorf("недопустимое имя инстанса: %q", name)
	}

	args := []string{"delete", name}
	if force {
		args = append(args, "--force")
	}
	res, err := m.c.Run(ctx, "lxc", args...)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "lxd.delete", name, outcome, map[string]any{
		"force": force, "exit_code": res.ExitCode,
		"output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("lxc delete %s: %w", name, err)
	}
	if !res.OK() {
		return fmt.Errorf("lxc delete %s: код %d: %s", name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}
