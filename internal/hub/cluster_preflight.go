package hub

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmimage"
)

// Сухой прогон кластера: всё, что можно проверить и подготовить до
// создания машин, ничего не меняя на хосте. Те же проверки — первым
// шагом настоящего создания: провалиться до первой машины лучше, чем
// посреди третьей.

type preflightCheck struct {
	ok      bool
	warn    bool
	message string
}

type hostPreflight struct {
	KVM             bool     `json:"kvm"`
	LibvirtdActive  bool     `json:"libvirtd_active"`
	ImagesFreeBytes int64    `json:"images_free_bytes"`
	MemTotalBytes   int64    `json:"mem_total_bytes"`
	CPUCores        int      `json:"cpu_cores"`
	ListeningPorts  []int    `json:"listening_ports"`
	MissingPackages []string `json:"missing_packages"`
	ImagesDownload  []string `json:"images_downloaded"`
	Bridges         []string `json:"bridges"`
	WireGuard       bool     `json:"wireguard"`
	Networks        []struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	} `json:"networks"`
}

// runPreflight проходит проверки по каждому хосту размещения, пишет
// чек-лист в журнал и возвращает, сколько провалов. prepare — заодно
// скачать образы и пакеты в кэш.
func (r *ClusterRunner) runPreflight(ctx context.Context, jc *jobs.Context, spec ClusterSpec, prepare bool) (failed int, err error) {
	lang := jc.Lang()
	var checks []preflightCheck
	// Строка чек-листа пишется сразу — под заголовком своего хоста.
	add := func(ok, warn bool, key string, args ...any) {
		c := preflightCheck{ok: ok, warn: warn, message: msgs.T(lang, key, args...)}
		checks = append(checks, c)
		mark := "✓"
		switch {
		case !c.ok:
			mark = "✗"
		case c.warn:
			mark = "!"
		}
		jc.Logf("  %s %s", mark, c.message)
	}
	hostByID := map[int64]store.Host{}
	hostName := func(id int64) string {
		if h, ok := hostByID[id]; ok {
			return h.Name
		}
		if h, err := r.m.db.HostByID(ctx, id); err == nil {
			hostByID[id] = h
			return h.Name
		}
		return fmt.Sprintf("host-%d", id)
	}
	nodes := spec.nodes(hostName)
	cpHost := hostName(spec.HostID)

	// План: что будет создано.
	jc.Log("hub.preflightPlan", len(nodes), spec.Flavor, cpHost)
	for _, n := range nodes {
		if n.Kind == KindHost {
			jc.Logf("      %s: %s (сам хост)", n.Name, n.Role)
			continue
		}
		net := n.Network
		if n.Bridge != "" {
			net = "bridge " + n.Bridge
		}
		jc.Logf("      %s: %s на %s, %d CPU, %d MB, %d GB, %s %s", n.Name, n.Role, hostName(n.HostID), n.VCPUs, n.MemMB, n.DiskGB, n.ImageID, net)
	}
	if spec.NetworkMode != "" && spec.NetworkMode != NetworkNAT {
		jc.Log("hub.preflightPlanNetwork", spec.NetworkMode)
	}
	if spec.NetworkMode == NetworkWireGuard {
		jc.Log("hub.preflightPlanMesh", wgPort)
	}
	if spec.Expose {
		jc.Log("hub.preflightPlanExpose", hostByID[spec.HostID].Addr, exposeSummary(spec))
	}
	if spec.CNI == "cilium" {
		jc.Log("hub.preflightPlanCilium", map[bool]string{true: "kube-proxy replacement", false: "kube-proxy"}[spec.KubeProxyReplacement])
	}

	// Имя свободно.
	if list, err := r.m.db.ListClusters(ctx); err == nil {
		for _, c := range list {
			if c.Name == spec.Name {
				add(false, false, "hub.preflightNameTaken", spec.Name)
			}
		}
	}
	if hosts, err := r.m.db.ListHosts(ctx); err == nil {
		for _, h := range hosts {
			for _, n := range nodes {
				if n.Kind == KindVM && h.Name == n.Name {
					add(false, false, "hub.preflightHostTaken", n.Name)
				}
			}
		}
	}

	// По хостам.
	seen := map[int64]bool{}
	for _, n := range nodes {
		if seen[n.HostID] {
			continue
		}
		seen[n.HostID] = true
		host, err := r.m.db.HostByID(ctx, n.HostID)
		if err != nil {
			add(false, false, "hub.preflightHostAPI", err)
			continue
		}
		hostByID[host.ID] = host
		jc.Log("hub.preflightHostHeader", host.Name)
		if host.Status != store.HostStatusOnline {
			add(false, false, "hub.preflightHostOffline", host.Name)
			continue
		}
		if host.NktVersion != "" && host.NktVersion != r.m.Version() {
			add(true, true, "hub.preflightHostVersion", host.NktVersion, r.m.Version())
		}
		var mine []clusterNode
		vms := false
		for _, m := range nodes {
			if m.HostID == n.HostID {
				mine = append(mine, m)
				if m.Kind == KindVM {
					vms = true
				}
			}
		}
		// Узел — сам хост: Kubernetes там ещё не должен стоять.
		for _, m := range mine {
			if m.Kind != KindHost {
				continue
			}
			var st struct {
				Status k8s.Status `json:"status"`
			}
			if _, err := r.m.HostAPI(ctx, host.ID, "GET", "/api/k8s", nil, &st); err != nil {
				add(false, false, "hub.preflightHostAPI", err)
			} else if st.Status.Installed {
				add(false, false, "hub.preflightHostHasK8s", host.Name, st.Status.Flavor, st.Status.Role)
			} else {
				add(true, false, "hub.preflightHostNodeOK", host.Name)
			}
		}
		wg := spec.NetworkMode == NetworkWireGuard
		if !vms && !wg {
			continue
		}
		var pf hostPreflight
		if _, err := r.m.HostAPI(ctx, host.ID, "GET", "/api/vm/preflight", nil, &pf); err != nil {
			add(false, false, "hub.preflightHostAPI", err)
			continue
		}
		if wg {
			// Туннель: адрес хоста — конечная точка для соседей,
			// wireguard-tools ставится при создании, если нет.
			if host.Addr == "" {
				add(false, false, "hub.preflightHostNoAddr", host.Name)
			} else {
				add(true, false, "hub.preflightWGEndpoint", host.Addr, wgPort)
			}
			add(true, !pf.WireGuard, map[bool]string{true: "hub.preflightWGOK", false: "hub.preflightWGMissing"}[pf.WireGuard])
			if prepare && !pf.WireGuard {
				jc.Log("hub.preflightPrepPackages", "wireguard-tools")
				if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/system/apt/download", map[string]any{"packages": []string{"wireguard-tools"}}, nil); err != nil {
					jc.Log("hub.preflightPrepFailed", err)
				} else {
					jc.Log("hub.preflightPrepPackagesDone")
				}
			}
		}
		if !vms {
			continue
		}
		add(pf.KVM, false, map[bool]string{true: "hub.preflightKVMOK", false: "hub.preflightKVMMissing"}[pf.KVM])
		add(pf.LibvirtdActive, false, map[bool]string{true: "hub.preflightLibvirtOK", false: "hub.preflightLibvirtDown"}[pf.LibvirtdActive])
		if len(pf.MissingPackages) > 0 {
			add(true, true, "hub.preflightTools", strings.Join(pf.MissingPackages, ", "))
		} else {
			add(true, false, "hub.preflightToolsOK")
		}
		var needDisk, needMem int64
		needCPU := 0
		images := map[string]bool{}
		bridges := map[string]bool{}
		nets := map[string]bool{}
		for _, m := range mine {
			if m.Kind != KindVM {
				continue
			}
			needDisk += int64(m.DiskGB)
			needMem += int64(m.MemMB)
			needCPU += m.VCPUs
			images[m.ImageID] = true
			if m.Bridge != "" {
				bridges[m.Bridge] = true
			} else if m.Network != "" {
				nets[m.Network] = true
			} else if len(pf.Networks) > 0 {
				nets[pf.Networks[0].Name] = true
			}
		}
		if pf.ImagesFreeBytes > 0 {
			freeGB := pf.ImagesFreeBytes >> 30
			add(freeGB >= needDisk, false, "hub.preflightDisk", freeGB, needDisk)
		}
		if pf.MemTotalBytes > 0 {
			totalMB := pf.MemTotalBytes >> 20
			add(true, needMem > totalMB*3/4, "hub.preflightMemory", totalMB, needMem)
		}
		if pf.CPUCores > 0 {
			add(true, needCPU > pf.CPUCores*2, "hub.preflightCPU", pf.CPUCores, needCPU)
		}
		for netName := range nets {
			found, active := false, false
			for _, nn := range pf.Networks {
				if nn.Name == netName {
					found, active = true, nn.Active
				}
			}
			switch {
			case !found:
				add(true, true, "hub.preflightNetMissing", netName)
			case !active:
				add(true, true, "hub.preflightNetInactive", netName)
			default:
				add(true, false, "hub.preflightNetOK", netName)
			}
		}
		for br := range bridges {
			found := false
			for _, b := range pf.Bridges {
				if b == br {
					found = true
				}
			}
			add(found, false, map[bool]string{true: "hub.preflightBridgeOK", false: "hub.preflightBridgeMissing"}[found], br)
		}
		if spec.Expose && host.ID == spec.HostID {
			for _, rule := range spec.exposeRules() {
				for _, l := range pf.ListeningPorts {
					if l == rule["host_port"] {
						add(false, false, "hub.preflightPortBusy", l)
					}
				}
			}
		}
		urls := []string{"https://get.k3s.io"}
		if spec.Flavor == k8s.FlavorKubeadm {
			urls = []string{"https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key", "https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml"}
		}
		if spec.CNI == "cilium" {
			urls = append(urls, "https://github.com/cilium/cilium-cli/releases/latest/download/cilium-linux-amd64.tar.gz.sha256sum")
		}
		var toDownload []string
		for id := range images {
			img, known := vmimage.ByID(id)
			downloaded := false
			for _, d := range pf.ImagesDownload {
				if d == id {
					downloaded = true
				}
			}
			switch {
			case downloaded:
				add(true, false, "hub.preflightImageOK", id)
			case known:
				add(true, true, "hub.preflightImageMissing", id)
				urls = append(urls, img.URL)
				toDownload = append(toDownload, id)
			default:
				add(false, false, "hub.preflightImageUnknown", id)
			}
		}
		for _, u := range urls {
			var res struct {
				OK     bool   `json:"ok"`
				Status int    `json:"status"`
				Error  string `json:"error"`
				MS     int64  `json:"ms"`
			}
			if _, err := r.m.HostAPI(ctx, host.ID, "GET", "/api/net/check?url="+url.QueryEscape(u), nil, &res); err != nil {
				add(false, false, "hub.preflightNetCheck", u, err)
				continue
			}
			if res.OK {
				add(true, false, "hub.preflightNetCheckOK", u, res.MS)
			} else {
				add(false, false, "hub.preflightNetCheck", u, fmt.Sprintf("%s %d", res.Error, res.Status))
			}
		}
		// Подготовка этого хоста — если проверки пока без провалов.
		if prepare && countFailed(checks) == 0 {
			for _, id := range toDownload {
				jc.Log("hub.preflightPrepImage", id)
				var started struct {
					JobID int64 `json:"job_id"`
				}
				if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/vm/images/download", map[string]any{"image_id": id}, &started); err != nil {
					jc.Log("hub.preflightPrepFailed", err)
				} else {
					g := &GroupApplyRunner{m: r.m}
					if err := g.waitHostJob(ctx, jc, host, started.JobID); err != nil {
						jc.Log("hub.preflightPrepFailed", err)
					}
				}
			}
			if len(pf.MissingPackages) > 0 {
				jc.Log("hub.preflightPrepPackages", strings.Join(pf.MissingPackages, ", "))
				if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/system/apt/download", map[string]any{"packages": pf.MissingPackages}, nil); err != nil {
					jc.Log("hub.preflightPrepFailed", err)
				} else {
					jc.Log("hub.preflightPrepPackagesDone")
				}
			}
		}
	}
	// Хосты туннеля видят друг друга? ICMP могут и резать, поэтому
	// только предупреждение.
	if spec.NetworkMode == NetworkWireGuard {
		var ids []int64
		for id := range seen {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) > 1 {
			jc.Log("hub.preflightPeersHeader")
		}
		for _, a := range ids {
			for _, b := range ids {
				if a == b || hostByID[b].Addr == "" || hostByID[a].Status != store.HostStatusOnline {
					continue
				}
				ok, detail := r.hostPing(ctx, a, hostByID[b].Addr)
				add(true, !ok, map[bool]string{true: "hub.preflightPeerPingOK", false: "hub.preflightPeerPing"}[ok], hostByID[a].Name, hostByID[b].Name, detail)
			}
		}
	}
	return r.finishPreflight(jc, checks), nil
}

func countFailed(checks []preflightCheck) int {
	n := 0
	for _, c := range checks {
		if !c.ok {
			n++
		}
	}
	return n
}

// finishPreflight печатает итог.
func (r *ClusterRunner) finishPreflight(jc *jobs.Context, checks []preflightCheck) int {
	failed := countFailed(checks)
	if failed == 0 {
		jc.Log("hub.preflightOK")
	} else {
		jc.Log("hub.preflightFailed", failed)
	}
	return failed
}
