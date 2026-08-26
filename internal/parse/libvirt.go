package parse

import (
	"context"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// LibvirtQEMUDir is where libvirt stores the persistent XML definition of
// every domain `virsh define`s — a fixed convention of libvirt itself, not
// something this application configures. It doubles as the sandboxed root
// the config editor accepts for creating/editing a VM (see
// control.ConfigManager.serviceForPath).
const LibvirtQEMUDir = "/etc/libvirt/qemu"

// LibvirtResult is everything the libvirt/QEMU parser produces.
type LibvirtResult struct {
	Status model.SourceStatus
	VMs    []model.VirtualMachine
	Files  []model.ManagedFile
}

// domainXML is the subset of `virsh dumpxml`'s output this application
// needs — a real domain definition carries far more (boot order, graphics,
// CPU topology, channels...), none of which the dashboard shows.
type domainXML struct {
	Name   string `xml:"name"`
	UUID   string `xml:"uuid"`
	Memory struct {
		Unit  string `xml:"unit,attr"`
		Value int64  `xml:",chardata"`
	} `xml:"memory"`
	VCPU struct {
		Value int `xml:",chardata"`
	} `xml:"vcpu"`
	Devices struct {
		Disks []struct {
			Device string `xml:"device,attr"`
			Source struct {
				File string `xml:"file,attr"`
				Dev  string `xml:"dev,attr"`
			} `xml:"source"`
			Target struct {
				Bus string `xml:"bus,attr"`
			} `xml:"target"`
		} `xml:"disk"`
		Interfaces []struct {
			MAC struct {
				Address string `xml:"address,attr"`
			} `xml:"mac"`
			Source struct {
				Bridge  string `xml:"bridge,attr"`
				Network string `xml:"network,attr"`
			} `xml:"source"`
			Model struct {
				Type string `xml:"type,attr"`
			} `xml:"model"`
		} `xml:"interface"`
	} `xml:"devices"`
}

// Libvirt lists every domain (VM) libvirt knows about — running or only
// defined — via `virsh`, the same shell-out integration this application
// already uses for nginx/haproxy/certbot rather than a dedicated client
// library: a libvirt Go binding needs cgo against libvirt's C library,
// which this project's cross-platform dev workflow (Windows dev machine,
// fixtures mode) cannot assume is present.
func Libvirt(ctx context.Context, c collect.Collector, uri string) LibvirtResult {
	started := time.Now()
	res := LibvirtResult{Status: model.SourceStatus{Name: model.ServiceLibvirt}}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()
	// A host without libvirt (or with it installed but zero domains defined)
	// would otherwise leave these nil, which encoding/json marshals as
	// `null` and crashes the VMs page's .map over them.
	res.VMs = []model.VirtualMachine{}
	res.Files = []model.ManagedFile{}

	out, err := c.Run(ctx, "virsh", "-c", uri, "list", "--all", "--name")
	if err != nil {
		res.Status.Warnings = append(res.Status.Warnings, err.Error())
		res.Status.WarningRefs = append(res.Status.WarningRefs, model.TextRef{})
		res.Status.Error = "libvirt недоступен: " + err.Error()
		res.Status.ErrorKey = "parse.libvirtUnavailable"
		res.Status.ErrorArgs = []any{err.Error()}
		return res
	}
	if !out.OK() {
		msg := fmt.Sprintf("libvirt: virsh list вернул код %d: %s", out.ExitCode, strings.TrimSpace(out.Output()))
		res.Status.Warnings = append(res.Status.Warnings, msg)
		ref := model.TextRef{Key: "parse.libvirtListFailed", Args: []any{out.ExitCode, strings.TrimSpace(out.Output())}}
		res.Status.WarningRefs = append(res.Status.WarningRefs, ref)
		res.Status.Error = msg
		res.Status.ErrorKey = ref.Key
		res.Status.ErrorArgs = ref.Args
		return res
	}
	res.Status.Available = true

	for _, name := range strings.Split(out.Stdout, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		vm, warn, warnRef := readDomain(ctx, c, uri, name)
		if warn != "" {
			res.Status.Warnings = append(res.Status.Warnings, warn)
			res.Status.WarningRefs = append(res.Status.WarningRefs, warnRef)
		}
		res.VMs = append(res.VMs, vm)
		if vm.Persistent {
			// A transient domain (started via `virsh create`, never
			// `define`d) has no XML file on disk to list — only what
			// dominfo already reported in memory.
			path := LibvirtQEMUDir + "/" + name + ".xml"
			res.Files = append(res.Files, describeFile(c, path, model.ServiceLibvirt, true))
		}
	}
	return res
}

func readDomain(ctx context.Context, c collect.Collector, uri, name string) (model.VirtualMachine, string, model.TextRef) {
	vm := model.VirtualMachine{Name: name}

	if info, err := c.Run(ctx, "virsh", "-c", uri, "dominfo", name); err == nil && info.OK() {
		applyDominfo(&vm, info.Stdout)
	} else {
		return vm, fmt.Sprintf("libvirt: dominfo %s недоступен", name),
			model.TextRef{Key: "parse.libvirtDominfoUnavailable", Args: []any{name}}
	}

	xmlOut, err := c.Run(ctx, "virsh", "-c", uri, "dumpxml", name)
	if err != nil || !xmlOut.OK() {
		return vm, fmt.Sprintf("libvirt: dumpxml %s недоступен", name),
			model.TextRef{Key: "parse.libvirtDumpxmlUnavailable", Args: []any{name}}
	}
	var dom domainXML
	if err := xml.Unmarshal([]byte(xmlOut.Stdout), &dom); err != nil {
		return vm, fmt.Sprintf("libvirt: разбор XML домена %s: %v", name, err),
			model.TextRef{Key: "parse.libvirtXMLParseFailed", Args: []any{name, err}}
	}
	if dom.UUID != "" {
		vm.UUID = dom.UUID
	}
	vm.MemoryKB = toKB(dom.Memory.Value, dom.Memory.Unit)
	for _, d := range dom.Devices.Disks {
		src := d.Source.File
		if src == "" {
			src = d.Source.Dev
		}
		vm.Disks = append(vm.Disks, model.VMDisk{Device: d.Device, Source: src, Bus: d.Target.Bus})
	}
	for _, i := range dom.Devices.Interfaces {
		src := i.Source.Bridge
		if src == "" {
			src = i.Source.Network
		}
		vm.Networks = append(vm.Networks, model.VMNetIface{
			Source: src, MAC: i.MAC.Address, Model: i.Model.Type,
		})
	}
	return vm, "", model.TextRef{}
}

// applyDominfo fills state/CPU/memory/persistent/autostart from `virsh
// dominfo`'s plain "Key:   value" lines.
func applyDominfo(vm *model.VirtualMachine, text string) {
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "State":
			vm.State = value
		case "CPU(s)":
			vm.VCPUs, _ = strconv.Atoi(value)
		case "Max memory":
			fields := strings.Fields(value)
			if len(fields) == 2 {
				n, _ := strconv.ParseInt(fields[0], 10, 64)
				vm.MemoryKB = toKB(n, fields[1])
			}
		case "Persistent":
			vm.Persistent = value == "yes"
		case "Autostart":
			vm.Autostart = value == "enable"
		}
	}
}

// toKB normalises libvirt's memory units (KiB/MiB/GiB, occasionally bare
// "bytes") to KiB — dominfo and dumpxml do not always agree on which unit
// they report in.
func toKB(value int64, unit string) int64 {
	switch strings.ToLower(unit) {
	case "mib":
		return value * 1024
	case "gib":
		return value * 1024 * 1024
	case "bytes", "b":
		return value / 1024
	default: // KiB, or unspecified — dumpxml defaults to KiB
		return value
	}
}
