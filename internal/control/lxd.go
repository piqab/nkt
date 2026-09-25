package control

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"regexp"
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
		return msgs.Errorf("control.invalidActionInstance", action)
	}
	if name == "" || strings.ContainsAny(name, "/?&# ") {
		return msgs.Errorf("control.invalidInstanceName", name)
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
		return msgs.Errorf("control.lxcCode", action, name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}

// CreateInstance launches a new instance from an image — `lxc launch` does
// image-fetch, create and start in one step, so unlike Podman there is no
// separate create-then-start round trip needed here.
func (m *LXDManager) CreateInstance(ctx context.Context, user, image, name string, vm ...bool) error {
	if strings.TrimSpace(image) == "" {
		return msgs.Errorf("control.specifyImage")
	}
	if name == "" || strings.ContainsAny(name, "/?&# ") {
		return msgs.Errorf("control.invalidInstanceName", name)
	}

	args := []string{"launch", image, name}
	// Образ виртуальной машины запускается с --vm: без флага lxc ищет
	// образ контейнера с тем же алиасом.
	if len(vm) > 0 && vm[0] {
		args = append(args, "--vm")
	}
	res, err := m.c.Run(ctx, "lxc", args...)
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
		return msgs.Errorf("control.lxcLaunchCode", image, name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}

// DeleteInstance removes an instance. force also stops a running one first —
// the same graceful-vs-forced distinction the rest of this application makes
// explicit rather than silently escalating.
func (m *LXDManager) DeleteInstance(ctx context.Context, user, name string, force bool) error {
	if name == "" || strings.ContainsAny(name, "/?&#") {
		return msgs.Errorf("control.invalidInstanceName", name)
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
		return msgs.Errorf("control.lxcDeleteCode", name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}

// SetAutostart включает или выключает запуск инстанса вместе с хостом
// (boot.autostart).
func (m *LXDManager) SetAutostart(ctx context.Context, user, name string, on bool) error {
	if name == "" || strings.ContainsAny(name, "/?&# ") {
		return msgs.Errorf("control.invalidInstanceName", name)
	}
	v := "false"
	if on {
		v = "true"
	}
	res, err := m.c.Run(ctx, "lxc", "config", "set", name, "boot.autostart", v)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "lxd.autostart", name, outcome, map[string]any{"on": on})
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("lxc config set: %s", strings.TrimSpace(res.Output()))
	}
	return nil
}

// lxdImageRefRe — ссылка на образ для lxc launch: «images:debian/12»,
// «ubuntu:24.04», алиас или отпечаток локального образа.
var lxdLaunchImageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,200}$`)
var lxdInstanceNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,62}$`)

// LXDLaunchArgs — проверенные аргументы lxc launch для задания создания.
func LXDLaunchArgs(image, name string, vm bool) ([]string, error) {
	if !lxdLaunchImageRe.MatchString(strings.TrimSpace(image)) {
		return nil, msgs.Errorf("control.specifyImage")
	}
	if !lxdInstanceNameRe.MatchString(name) {
		return nil, msgs.Errorf("control.invalidInstanceName", name)
	}
	args := []string{"launch", strings.TrimSpace(image), name}
	if vm {
		args = append(args, "--vm")
	}
	return args, nil
}
