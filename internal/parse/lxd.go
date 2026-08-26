package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// LXDResult is everything the LXD parser produces.
type LXDResult struct {
	Status    model.SourceStatus
	Instances []model.LXDInstance
}

// lxdInstance is the subset of `lxc list --format json`'s per-instance shape
// this application needs — the real output carries far more (config,
// devices, profiles, snapshots...), none of which the dashboard shows.
type lxdInstance struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	Type         string `json:"type"` // container | virtual-machine
	Architecture string `json:"architecture"`
	State        *struct {
		Network map[string]struct {
			Addresses []struct {
				Family  string `json:"family"`
				Address string `json:"address"`
			} `json:"addresses"`
		} `json:"network"`
	} `json:"state"`
}

// LXD lists every instance the local LXD daemon manages, via `lxc list
// --format json` — LXD's REST API requires TLS client-certificate auth even
// over the local unix socket in most installs, so the CLI (which already
// holds a trusted client cert under the invoking user) is the practical
// integration point, the same way this application already shells out to
// nginx/haproxy/certbot rather than talking to any of their APIs directly.
func LXD(ctx context.Context, c collect.Collector) LXDResult {
	started := time.Now()
	res := LXDResult{Status: model.SourceStatus{Name: model.ServiceLXD}}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()
	// A host without LXD (or with it installed but zero instances) would
	// otherwise leave this nil, which encoding/json marshals as `null` and
	// crashes the LXD page's .map over it.
	res.Instances = []model.LXDInstance{}

	out, err := c.Run(ctx, "lxc", "list", "--format", "json")
	if err != nil {
		res.Status.Warnings = append(res.Status.Warnings, err.Error())
		res.Status.WarningRefs = append(res.Status.WarningRefs, model.TextRef{})
		res.Status.Error = "lxd недоступен: " + err.Error()
		res.Status.ErrorKey = "parse.lxdUnavailable"
		res.Status.ErrorArgs = []any{err.Error()}
		return res
	}
	if !out.OK() {
		msg := fmt.Sprintf("lxd: lxc list вернул код %d: %s", out.ExitCode, strings.TrimSpace(out.Output()))
		res.Status.Warnings = append(res.Status.Warnings, msg)
		ref := model.TextRef{Key: "parse.lxdListFailed", Args: []any{out.ExitCode, strings.TrimSpace(out.Output())}}
		res.Status.WarningRefs = append(res.Status.WarningRefs, ref)
		res.Status.Error = msg
		res.Status.ErrorKey = ref.Key
		res.Status.ErrorArgs = ref.Args
		return res
	}
	res.Status.Available = true

	var list []lxdInstance
	if err := json.Unmarshal([]byte(out.Stdout), &list); err != nil {
		res.Status.Warnings = append(res.Status.Warnings, fmt.Sprintf("lxd: разбор списка: %v", err))
		res.Status.WarningRefs = append(res.Status.WarningRefs,
			model.TextRef{Key: "parse.lxdListParseFailed", Args: []any{err}})
		return res
	}

	for _, e := range list {
		inst := model.LXDInstance{
			// lxc reports capitalized statuses ("Running", "Stopped") — lower
			// them to match every other status string in this application
			// (docker/podman/systemd all use lowercase), so badges and
			// colouring work without a special case per source.
			Name: e.Name, Type: e.Type, Status: strings.ToLower(e.Status), Architecture: e.Architecture,
		}
		if e.State != nil {
			for _, iface := range e.State.Network {
				for _, addr := range iface.Addresses {
					if addr.Family == "inet" {
						inst.IPv4 = append(inst.IPv4, addr.Address)
					}
				}
			}
			sort.Strings(inst.IPv4)
		}
		res.Instances = append(res.Instances, inst)
	}
	sort.Slice(res.Instances, func(i, j int) bool { return res.Instances[i].Name < res.Instances[j].Name })
	return res
}
