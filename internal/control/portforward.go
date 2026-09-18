package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/msgs"
)

// Проброс портов хоста в виртуальную машину: DNAT на порт хоста → адрес
// машины в сети libvirt. Так control plane кластера на виртуалке виден
// снаружи по адресу хоста. Правила живут в своих цепочках NKT-PF (nat и
// filter), чтобы применяться идемпотентно, и восстанавливаются при
// загрузке юнитом nkt-portforward.service из сгенерированного скрипта.

const (
	portForwardDir    = "/etc/netknownsthat/portforward"
	portForwardScript = "/etc/netknownsthat/portforward.sh"
	portForwardUnit   = "/etc/systemd/system/nkt-portforward.service"
)

// PortForward — один набор правил: имя (машина) → адрес и порты.
type PortForward struct {
	Name  string `json:"name"`
	IP    string `json:"ip"`
	Ports []int  `json:"ports"`
}

var pfNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// PortForwardManager хранит и применяет пробросы.
type PortForwardManager struct {
	c   collect.Collector
	run PrivilegedRunner
}

func NewPortForwardManager(c collect.Collector, run PrivilegedRunner) *PortForwardManager {
	return &PortForwardManager{c: c, run: run}
}

// List — сохранённые пробросы.
func (m *PortForwardManager) List() ([]PortForward, error) {
	files, err := m.c.Glob(path.Join(portForwardDir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := []PortForward{}
	for _, f := range files {
		raw, err := m.c.ReadFile(f)
		if err != nil {
			continue
		}
		var pf PortForward
		if json.Unmarshal(raw, &pf) == nil && pf.Name != "" {
			out = append(out, pf)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Set сохраняет проброс и применяет все правила заново.
func (m *PortForwardManager) Set(ctx context.Context, pf PortForward) error {
	if m.run == nil {
		return msgs.Errorf("control.installationUnavailableMode")
	}
	if !pfNameRe.MatchString(pf.Name) {
		return msgs.Errorf("control.portForwardBadName", pf.Name)
	}
	if ip := net.ParseIP(pf.IP); ip == nil || ip.To4() == nil {
		return msgs.Errorf("control.portForwardBadIP", pf.IP)
	}
	if len(pf.Ports) == 0 {
		return msgs.Errorf("control.portForwardNoPorts")
	}
	for _, p := range pf.Ports {
		if p < 1 || p > 65535 {
			return msgs.Errorf("control.portForwardBadPort", p)
		}
	}
	raw, _ := json.Marshal(pf)
	if err := m.writeFile(ctx, path.Join(portForwardDir, pf.Name+".json"), raw, "0644"); err != nil {
		return err
	}
	return m.apply(ctx)
}

// Remove убирает проброс и переприменяет остальные.
func (m *PortForwardManager) Remove(ctx context.Context, name string) error {
	if m.run == nil {
		return msgs.Errorf("control.installationUnavailableMode")
	}
	if !pfNameRe.MatchString(name) {
		return msgs.Errorf("control.portForwardBadName", name)
	}
	if out, err := m.run(ctx, "rm", "-f", path.Join(portForwardDir, name+".json")); err != nil || out.ExitCode != 0 {
		return msgs.Errorf("control.portForwardApply", "rm", err)
	}
	return m.apply(ctx)
}

// apply перегенерирует скрипт, юнит и выполняет скрипт.
func (m *PortForwardManager) apply(ctx context.Context) error {
	list, err := m.List()
	if err != nil {
		return err
	}
	script := Script(list)
	if err := m.writeFile(ctx, portForwardScript, []byte(script), "0755"); err != nil {
		return err
	}
	unit := "[Unit]\nDescription=nkt: port forwards into virtual machines\nAfter=network-online.target libvirtd.service\nWants=network-online.target\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=" + portForwardScript + "\n\n[Install]\nWantedBy=multi-user.target\n"
	if err := m.writeFile(ctx, portForwardUnit, []byte(unit), "0644"); err != nil {
		return err
	}
	cmd := "systemctl daemon-reload && systemctl enable nkt-portforward.service >/dev/null 2>&1; " + portForwardScript
	out, err := m.run(ctx, "sh", "-c", cmd)
	if err != nil {
		return msgs.Errorf("control.portForwardApply", "iptables", err)
	}
	if out.ExitCode != 0 {
		return msgs.Errorf("control.portForwardApply", "iptables", fmt.Errorf("%s", lastLineOf(out.Output())))
	}
	return nil
}

// writeFile кладёт файл вне песочницы: содержимое через base64, чтобы
// не зависеть от кавычек.
func (m *PortForwardManager) writeFile(ctx context.Context, p string, data []byte, mode string) error {
	enc := base64.StdEncoding.EncodeToString(data)
	cmd := fmt.Sprintf("install -d -m 755 %s && printf '%%s' %s | base64 -d > %s.tmp && chmod %s %s.tmp && mv -f %s.tmp %s",
		path.Dir(p), enc, p, mode, p, p, p)
	out, err := m.run(ctx, "sh", "-c", cmd)
	if err != nil {
		return msgs.Errorf("control.portForwardApply", p, err)
	}
	if out.ExitCode != 0 {
		return msgs.Errorf("control.portForwardApply", p, fmt.Errorf("%s", lastLineOf(out.Output())))
	}
	return nil
}

// Script — идемпотентный скрипт iptables для всех пробросов: свои
// цепочки очищаются и заполняются заново, вставка в PREROUTING/FORWARD
// идёт первой, чтобы обойти запреты libvirt и ufw для FORWARD.
func Script(list []PortForward) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# nkt: пробросы портов в виртуальные машины; генерируется автоматически.\n")
	b.WriteString("iptables -t nat -N NKT-PF 2>/dev/null || iptables -t nat -F NKT-PF\n")
	b.WriteString("iptables -t nat -C PREROUTING -j NKT-PF 2>/dev/null || iptables -t nat -I PREROUTING 1 -j NKT-PF\n")
	b.WriteString("iptables -N NKT-PF 2>/dev/null || iptables -F NKT-PF\n")
	b.WriteString("iptables -C FORWARD -j NKT-PF 2>/dev/null || iptables -I FORWARD 1 -j NKT-PF\n")
	b.WriteString("sysctl -qw net.ipv4.ip_forward=1\n")
	for _, pf := range list {
		for _, p := range pf.Ports {
			fmt.Fprintf(&b, "# %s\niptables -t nat -A NKT-PF -p tcp --dport %d -j DNAT --to-destination %s:%d\n", pf.Name, p, pf.IP, p)
			fmt.Fprintf(&b, "iptables -A NKT-PF -p tcp -d %s --dport %d -j ACCEPT\n", pf.IP, p)
		}
	}
	return b.String()
}

func lastLineOf(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
