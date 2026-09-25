package parse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// Манифесты пакетов внутри инстансов LXD и машин libvirt — те же три
// файла, что Manifest читает у хоста, только изнутри гостя:
//
//	LXD     — `lxc file pull NAME/path -` (у контейнера работает и
//	          остановленного; у VM LXD — через lxd-agent, только работающей);
//	libvirt — qemu-guest-agent (guest-file-open/read), только работающей
//	          машины с агентом.
//
// Гость без /var/lib/dpkg/status (не Debian/Ubuntu, Windows) пропускается
// молча, как и хост; недоступный агент — предупреждение в итоге скана.

const maxGuestFile = 32 << 20

// InstanceManifests собирает манифесты гостей по снимку инвентаря.
func InstanceManifests(ctx context.Context, c collect.Collector, snap *model.Snapshot) ([]model.InstanceManifest, []string) {
	var out []model.InstanceManifest
	var warnings []string
	if snap == nil {
		return out, warnings
	}
	for _, in := range snap.LXD {
		vm := in.Type == "virtual-machine"
		if vm && !strings.EqualFold(in.Status, "running") {
			continue
		}
		read := func(path string) ([]byte, error) { return lxdPull(ctx, c, in.Name, path) }
		m, err := guestManifest(read)
		if err != nil {
			if vm {
				warnings = append(warnings, msgs.Tc(ctx, "parse.guestManifestLXD", in.Name, err))
			}
			continue
		}
		if m.Available {
			out = append(out, model.InstanceManifest{Target: "LXD " + in.Name, Manifest: m})
		}
	}
	for _, vm := range snap.VMs {
		if vm.State != "running" {
			continue
		}
		read := func(path string) ([]byte, error) { return qgaReadFile(ctx, c, vm.Name, path) }
		m, err := guestManifest(read)
		if err != nil {
			warnings = append(warnings, msgs.Tc(ctx, "parse.guestManifestVM", vm.Name, err))
			continue
		}
		if m.Available {
			out = append(out, model.InstanceManifest{Target: "VM " + vm.Name, Manifest: m})
		}
	}
	return out, warnings
}

// errNoFile — файла в госте нет: не ошибка, гость просто не dpkg.
var errNoFile = fmt.Errorf("no such file")

// guestManifest — три файла через read; нет dpkg status — пустой манифест.
func guestManifest(read func(string) ([]byte, error)) (model.PackageManifest, error) {
	var m model.PackageManifest
	status, err := read("/var/lib/dpkg/status")
	if err == errNoFile {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	m.Available = true
	m.DpkgStatus = string(status)
	if b, err := read("/etc/os-release"); err == nil {
		m.OSRelease = string(b)
	}
	if b, err := read("/etc/debian_version"); err == nil {
		m.DebianVersion = string(b)
	}
	return m, nil
}

func lxdPull(ctx context.Context, c collect.Collector, name, path string) ([]byte, error) {
	res, err := c.Run(ctx, "lxc", "file", "pull", name+path, "-")
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		msg := strings.TrimSpace(res.Stderr + res.Stdout)
		if strings.Contains(strings.ToLower(msg), "not found") || strings.Contains(msg, "No such file") {
			return nil, errNoFile
		}
		return nil, fmt.Errorf("%s", firstLine(msg))
	}
	return []byte(res.Stdout), nil
}

// qgaCommand — одна команда гостевого агента через virsh.
func qgaCommand(ctx context.Context, c collect.Collector, name string, cmd any) (json.RawMessage, error) {
	body, _ := json.Marshal(cmd)
	res, err := c.Run(ctx, "virsh", "-c", "qemu:///system", "qemu-agent-command", name, "--timeout", "10", string(body))
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		msg := strings.TrimSpace(res.Stderr + res.Stdout)
		if strings.Contains(msg, "No such file") || strings.Contains(msg, "not found") || strings.Contains(msg, "cannot find") {
			return nil, errNoFile
		}
		return nil, fmt.Errorf("%s", firstLine(msg))
	}
	var out struct {
		Return json.RawMessage `json:"return"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		return nil, err
	}
	return out.Return, nil
}

// qgaReadFile читает файл гостя кусками по 4 МиБ (guest-file-read).
func qgaReadFile(ctx context.Context, c collect.Collector, name, path string) ([]byte, error) {
	raw, err := qgaCommand(ctx, c, name, map[string]any{"execute": "guest-file-open", "arguments": map[string]any{"path": path, "mode": "r"}})
	if err != nil {
		return nil, err
	}
	var handle int64
	if err := json.Unmarshal(raw, &handle); err != nil {
		return nil, err
	}
	defer func() {
		_, _ = qgaCommand(ctx, c, name, map[string]any{"execute": "guest-file-close", "arguments": map[string]any{"handle": handle}})
	}()
	var data []byte
	for len(data) <= maxGuestFile {
		raw, err := qgaCommand(ctx, c, name, map[string]any{"execute": "guest-file-read", "arguments": map[string]any{"handle": handle, "count": 4 << 20}})
		if err != nil {
			return nil, err
		}
		var chunk struct {
			Count int    `json:"count"`
			Buf   string `json:"buf-b64"`
			EOF   bool   `json:"eof"`
		}
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return nil, err
		}
		b, err := base64.StdEncoding.DecodeString(chunk.Buf)
		if err != nil {
			return nil, err
		}
		data = append(data, b...)
		if chunk.EOF || chunk.Count == 0 {
			return data, nil
		}
	}
	return nil, fmt.Errorf("%s: larger than %d bytes", path, maxGuestFile)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
