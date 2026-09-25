package parse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// guestCollector — поддельные virsh (гостевой агент) и lxc file pull.
type guestCollector struct {
	collect.Collector
	files map[string]string // "vm:path" / "lxd:path"
	reads int
	open  string
}

func (g *guestCollector) Run(_ context.Context, name string, args ...string) (collect.CommandResult, error) {
	if name == "lxc" && len(args) >= 3 && args[0] == "file" {
		inst, path, _ := strings.Cut(args[2], "/")
		if v, ok := g.files["lxd:"+inst+":/"+path]; ok {
			return collect.CommandResult{Stdout: v}, nil
		}
		return collect.CommandResult{ExitCode: 1, Stderr: "Error: not found"}, nil
	}
	var cmd struct {
		Execute   string         `json:"execute"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal([]byte(args[len(args)-1]), &cmd)
	vm := args[3]
	switch cmd.Execute {
	case "guest-file-open":
		if _, ok := g.files["vm:"+vm+":"+cmd.Arguments["path"].(string)]; !ok {
			return collect.CommandResult{ExitCode: 1, Stderr: "error: Failed to open file: No such file or directory"}, nil
		}
		g.reads = 0
		g.open = "vm:" + vm + ":" + cmd.Arguments["path"].(string)
		return collect.CommandResult{Stdout: `{"return":7}`}, nil
	case "guest-file-read":
		// Файл отдаётся двумя кусками — проверка склейки.
		data := g.files[g.open]
		half := len(data) / 2
		part, eof := data[:half], false
		if g.reads > 0 {
			part, eof = data[half:], true
		}
		g.reads++
		out, _ := json.Marshal(map[string]any{"return": map[string]any{"count": len(part), "buf-b64": base64.StdEncoding.EncodeToString([]byte(part)), "eof": eof}})
		return collect.CommandResult{Stdout: string(out)}, nil
	}
	return collect.CommandResult{Stdout: `{"return":{}}`}, nil
}

func TestInstanceManifests(t *testing.T) {
	c := &guestCollector{files: map[string]string{
		"vm:web:/var/lib/dpkg/status": "Package: bash\nStatus: install ok installed\nVersion: 5.2\n",
		"vm:web:/etc/os-release":      "ID=debian\n",
		"lxd:c1:/var/lib/dpkg/status": "Package: curl\n",
		"lxd:c1:/etc/os-release":      "ID=ubuntu\n",
	}}
	snap := &model.Snapshot{
		LXD: []model.LXDInstance{{Name: "c1", Status: "Stopped", Type: "container"}, {Name: "alpine", Status: "Running", Type: "container"}},
		VMs: []model.VirtualMachine{{Name: "web", State: "running"}, {Name: "win", State: "running"}, {Name: "off", State: "shut off"}},
	}
	list, warnings := InstanceManifests(context.Background(), c, snap)
	got := map[string]model.PackageManifest{}
	for _, m := range list {
		got[m.Target] = m.Manifest
	}
	if len(got) != 2 || !strings.Contains(got["VM web"].DpkgStatus, "Version: 5.2") || got["VM web"].OSRelease != "ID=debian\n" || got["LXD c1"].OSRelease != "ID=ubuntu\n" {
		t.Fatalf("%+v", got)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
}
