package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
)

// Перезагрузка хоста из интерфейса (раздел «Системные настройки» и
// плашка «требуется перезагрузка»). Перед ней окно показывает, сколько
// работает сейчас и что само после перезагрузки не поднимется.

// rebootNoAuto — то, что работает, но само не запустится.
type rebootNoAuto struct {
	Kind   string `json:"kind"` // docker | podman | lxd | vm | service
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

// handleRebootPreview — GET /system/reboot/preview.
func (s *Server) handleRebootPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap := s.scanner.Latest()
	running := map[string]int{"docker": 0, "podman": 0, "lxd": 0, "vm": 0, "service": 0}
	noAuto := []rebootNoAuto{}
	required := false
	if snap != nil {
		required = snap.Packages.RebootRequired
		for _, c := range snap.Container {
			if c.State != "running" {
				continue
			}
			running["docker"]++
			if c.Restart != "always" && c.Restart != "unless-stopped" {
				policy := c.Restart
				if policy == "" {
					policy = "no"
				}
				noAuto = append(noAuto, rebootNoAuto{Kind: "docker", Name: c.Name, Reason: "restart: " + policy})
			}
		}
		policies := s.podmanRestartPolicies(ctx)
		for _, c := range snap.Podman {
			if c.State != "running" {
				continue
			}
			running["podman"]++
			if p := policies[c.Name]; p != "always" && p != "unless-stopped" {
				if p == "" {
					p = "no"
				}
				noAuto = append(noAuto, rebootNoAuto{Kind: "podman", Name: c.Name, Reason: "restart: " + p})
			}
		}
		for _, in := range snap.LXD {
			if !strings.EqualFold(in.Status, "running") {
				continue
			}
			running["lxd"]++
			if !in.Autostart {
				noAuto = append(noAuto, rebootNoAuto{Kind: "lxd", Name: in.Name, Reason: "boot.autostart: false"})
			}
		}
		for _, vm := range snap.VMs {
			if vm.State != "running" {
				continue
			}
			running["vm"]++
			if !vm.Autostart {
				noAuto = append(noAuto, rebootNoAuto{Kind: "vm", Name: vm.Name, Reason: "autostart: off"})
			}
		}
		for _, svc := range snap.Services {
			if svc.ActiveState != "active" {
				continue
			}
			running["service"]++
			// static/indirect/generated поднимают другие юниты — их не
			// перечисляем; выключенная работающая служба сама не встанет.
			if svc.Enabled == "disabled" || svc.Enabled == "masked" {
				noAuto = append(noAuto, rebootNoAuto{Kind: "service", Name: svc.Name, Reason: svc.Enabled})
			}
		}
	}
	sort.SliceStable(noAuto, func(i, j int) bool { return noAuto[i].Kind < noAuto[j].Kind })
	writeJSON(w, http.StatusOK, map[string]any{
		"reboot_required": required, "running": running, "no_autostart": noAuto,
		"simulated": s.cfg.IsFixtures(),
	})
}

// podmanRestartPolicies — политика перезапуска работающих контейнеров
// Podman (в снимке её нет): имя → no | always | unless-stopped | on-failure.
func (s *Server) podmanRestartPolicies(ctx context.Context) map[string]string {
	out := map[string]string{}
	c := s.scanner.Collector()
	res, err := c.Run(ctx, "podman", "ps", "--format", "{{.Names}}")
	if err != nil || !res.OK() {
		return out
	}
	names := strings.Fields(res.Stdout)
	if len(names) == 0 {
		return out
	}
	args := append([]string{"inspect", "--format", "{{.Name}} {{.HostConfig.RestartPolicy.Name}}"}, names...)
	res, err = c.Run(ctx, "podman", args...)
	if err != nil || !res.OK() {
		return out
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if f := strings.Fields(line); len(f) >= 1 {
			p := ""
			if len(f) > 1 {
				p = f[1]
			}
			out[strings.TrimPrefix(f[0], "/")] = p
		}
	}
	return out
}

// rebootDelay — сколько ждать перед перезагрузкой: ответ должен успеть
// дойти до браузера (и через хаб).
const rebootDelay = 5 * time.Second

// handleReboot — POST /system/reboot {confirm: true}.
func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !req.Confirm {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("system.rebootNotConfirmed"))
		return
	}
	user := auth.Username(r.Context())
	if s.cfg.IsFixtures() {
		s.db.Audit(r.Context(), user, "system.reboot", "", "ok", "simulated")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "simulated": true})
		return
	}
	s.db.Audit(r.Context(), user, "system.reboot", "", "ok", nil)
	// Запрос закончится раньше перезагрузки: контекст — от запроса, но
	// без его отмены.
	bg := context.WithoutCancel(r.Context())
	go func() {
		time.Sleep(rebootDelay)
		ctx, cancel := context.WithTimeout(bg, 30*time.Second)
		defer cancel()
		if res, err := RunUnrestricted(ctx, "systemctl", "reboot"); (err != nil || !res.OK()) && s.log != nil {
			s.log.Error("reboot failed", "err", err, "out", strings.TrimSpace(res.Output()))
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "delay_s": int(rebootDelay.Seconds())})
}
