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

// PodmanResult is everything the Podman parser produces.
type PodmanResult struct {
	Status     model.SourceStatus
	Containers []model.PodmanContainer
}

// podmanContainer is libpod's native (unversioned "/libpod/...") container
// list shape — distinct from Docker's engine API shape, so it gets its own
// type rather than reusing engineContainer from docker.go.
type podmanContainer struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"` // unlike Docker, not prefixed with "/"
	Image   string   `json:"Image"`
	State   string   `json:"State"`
	Status  string   `json:"Status"`
	Created int64    `json:"Created"`
	Pod     string   `json:"Pod"`
	PodName string   `json:"PodName"`
	Ports   []struct {
		HostIP        string `json:"host_ip"`
		ContainerPort int    `json:"container_port"`
		HostPort      int    `json:"host_port"`
		Protocol      string `json:"protocol"`
	} `json:"Ports"`
	Networks []string `json:"Networks"`
}

// Podman lists every container the local Podman engine knows about. Unlike
// Docker there is no compose-file reconciliation here — Podman has no
// first-class declared-vs-running duality in this application (Podman
// Quadlet systemd units are a future extension, not v1).
func Podman(ctx context.Context, c collect.Collector) PodmanResult {
	started := time.Now()
	res := PodmanResult{Status: model.SourceStatus{Name: model.ServicePodman}}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()
	// A host with Podman installed but zero containers running would
	// otherwise leave this nil, which encoding/json marshals as `null` and
	// crashes the Podman page's .map over it.
	res.Containers = []model.PodmanContainer{}

	if raw, code, err := c.PodmanAPI(ctx, "GET", "/libpod/version", nil); err == nil && code == 200 {
		var v struct {
			Version string `json:"Version"`
		}
		if json.Unmarshal(raw, &v) == nil && v.Version != "" {
			res.Status.Version = "Podman " + v.Version
		}
	}

	raw, code, err := c.PodmanAPI(ctx, "GET", "/libpod/containers/json?all=true", nil)
	if err != nil {
		res.Status.Warnings = append(res.Status.Warnings, err.Error())
		res.Status.Error = "podman недоступен: " + err.Error()
		return res
	}
	if code != 200 {
		msg := fmt.Sprintf("podman: список контейнеров вернул HTTP %d", code)
		res.Status.Warnings = append(res.Status.Warnings, msg)
		res.Status.Error = msg
		return res
	}
	res.Status.Available = true

	var list []podmanContainer
	if err := json.Unmarshal(raw, &list); err != nil {
		res.Status.Warnings = append(res.Status.Warnings, fmt.Sprintf("podman: разбор списка контейнеров: %v", err))
		return res
	}

	for _, e := range list {
		ct := model.PodmanContainer{
			ID:      e.ID,
			Name:    strings.TrimPrefix(firstOr(e.Names, ""), "/"),
			Image:   e.Image,
			State:   e.State,
			Status:  e.Status,
			Created: e.Created,
			Pod:     e.PodName,
		}
		for _, p := range e.Ports {
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			ct.Ports = append(ct.Ports, model.PortMapping{
				HostIP: p.HostIP, HostPort: p.HostPort, ContainerPort: p.ContainerPort, Protocol: proto,
			})
		}
		for _, name := range e.Networks {
			ct.Networks = append(ct.Networks, model.ContainerNetwork{Name: name})
		}
		res.Containers = append(res.Containers, ct)
	}
	sort.Slice(res.Containers, func(i, j int) bool { return res.Containers[i].Name < res.Containers[j].Name })
	return res
}
