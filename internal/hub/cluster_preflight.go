package hub

import (
	"context"
	"fmt"
	"net/url"
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
	Networks        []struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	} `json:"networks"`
}

// runPreflight проходит проверки, пишет чек-лист в журнал и возвращает,
// сколько провалов. prepare — заодно скачать образ и пакеты в кэш.
func (r *ClusterRunner) runPreflight(ctx context.Context, jc *jobs.Context, host store.Host, spec ClusterSpec, prepare bool) (failed int, err error) {
	lang := jc.Lang()
	var checks []preflightCheck
	add := func(ok, warn bool, key string, args ...any) {
		checks = append(checks, preflightCheck{ok: ok, warn: warn, message: msgs.T(lang, key, args...)})
	}

	// План: что будет создано.
	cps, workers := spec.nodeNames()
	jc.Log("hub.preflightPlan", len(cps)+len(workers), spec.Flavor, host.Name)
	for _, n := range cps {
		jc.Logf("      %s: control-plane, %d CPU, %d MB, %d GB, %s", n, spec.CPVCPUs, spec.CPMemoryMB, spec.CPDiskGB, spec.ImageID)
	}
	for _, n := range workers {
		jc.Logf("      %s: worker, %d CPU, %d MB, %d GB, %s", n, spec.WVCPUs, spec.WMemoryMB, spec.WDiskGB, spec.ImageID)
	}
	if spec.Expose {
		jc.Log("hub.preflightPlanExpose", host.Addr, exposeSummary(spec))
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
			for _, n := range append(append([]string{}, cps...), workers...) {
				if h.Name == n {
					add(false, false, "hub.preflightHostTaken", n)
				}
			}
		}
	}

	// Хост: версия, KVM, libvirt, инструменты, ресурсы, сеть, образ.
	if host.NktVersion != "" && host.NktVersion != r.m.Version() {
		add(true, true, "hub.preflightHostVersion", host.NktVersion, r.m.Version())
	}
	var pf hostPreflight
	if _, err := r.m.HostAPI(ctx, host.ID, "GET", "/api/vm/preflight", nil, &pf); err != nil {
		add(false, false, "hub.preflightHostAPI", err)
		return r.finishPreflight(jc, checks), nil
	}
	add(pf.KVM, false, map[bool]string{true: "hub.preflightKVMOK", false: "hub.preflightKVMMissing"}[pf.KVM])
	add(pf.LibvirtdActive, false, map[bool]string{true: "hub.preflightLibvirtOK", false: "hub.preflightLibvirtDown"}[pf.LibvirtdActive])
	if len(pf.MissingPackages) > 0 {
		add(true, true, "hub.preflightTools", strings.Join(pf.MissingPackages, ", "))
	} else {
		add(true, false, "hub.preflightToolsOK")
	}
	needDisk := int64(len(cps))*int64(spec.CPDiskGB) + int64(len(workers))*int64(spec.WDiskGB)
	if pf.ImagesFreeBytes > 0 {
		freeGB := pf.ImagesFreeBytes >> 30
		add(freeGB >= needDisk, false, "hub.preflightDisk", freeGB, needDisk)
	}
	needMem := int64(len(cps))*int64(spec.CPMemoryMB) + int64(len(workers))*int64(spec.WMemoryMB)
	if pf.MemTotalBytes > 0 {
		totalMB := pf.MemTotalBytes >> 20
		add(true, needMem > totalMB*3/4, "hub.preflightMemory", totalMB, needMem)
	}
	needCPU := len(cps)*spec.CPVCPUs + len(workers)*spec.WVCPUs
	if pf.CPUCores > 0 {
		add(true, needCPU > pf.CPUCores*2, "hub.preflightCPU", pf.CPUCores, needCPU)
	}
	netName := spec.Network
	if netName == "" && len(pf.Networks) > 0 {
		netName = pf.Networks[0].Name
	}
	if netName != "" {
		found, active := false, false
		for _, n := range pf.Networks {
			if n.Name == netName {
				found, active = true, n.Active
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
	if spec.Expose {
		for _, r := range spec.exposeRules() {
			for _, l := range pf.ListeningPorts {
				if l == r["host_port"] {
					add(false, false, "hub.preflightPortBusy", l)
				}
			}
		}
	}
	img, imgKnown := vmimage.ByID(spec.ImageID)
	downloaded := false
	for _, id := range pf.ImagesDownload {
		if id == spec.ImageID {
			downloaded = true
		}
	}
	switch {
	case downloaded:
		add(true, false, "hub.preflightImageOK", spec.ImageID)
	case imgKnown:
		add(true, true, "hub.preflightImageMissing", spec.ImageID)
	default:
		add(false, false, "hub.preflightImageUnknown", spec.ImageID)
	}

	// Интернет с хоста: установщик кластера и образ.
	urls := []string{"https://get.k3s.io"}
	if spec.Flavor == k8s.FlavorKubeadm {
		urls = []string{"https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key", "https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml"}
	}
	if imgKnown && !downloaded {
		urls = append(urls, img.URL)
	}
	if spec.CNI == "cilium" {
		urls = append(urls, "https://github.com/cilium/cilium-cli/releases/latest/download/cilium-linux-amd64.tar.gz.sha256sum")
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

	failed = r.finishPreflight(jc, checks)

	// Подготовка: кэши, не система.
	if prepare && failed == 0 {
		if imgKnown && !downloaded {
			jc.Log("hub.preflightPrepImage", spec.ImageID)
			var started struct {
				JobID int64 `json:"job_id"`
			}
			if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/vm/images/download", map[string]any{"image_id": spec.ImageID}, &started); err != nil {
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
	return failed, nil
}

// finishPreflight печатает чек-лист и итог.
func (r *ClusterRunner) finishPreflight(jc *jobs.Context, checks []preflightCheck) int {
	failed := 0
	for _, c := range checks {
		mark := "✓"
		switch {
		case !c.ok:
			mark = "✗"
			failed++
		case c.warn:
			mark = "!"
		}
		jc.Logf("  %s %s", mark, c.message)
	}
	if failed == 0 {
		jc.Log("hub.preflightOK")
	} else {
		jc.Log("hub.preflightFailed", failed)
	}
	return failed
}
