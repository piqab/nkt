package vmcreate

import (
	"context"
	"fmt"
	"html"
	"net"
	"regexp"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// macRe — MAC интерфейса домена: уходит в описание брони DHCP.
var macRe = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)

// FixAddress закрепляет за машиной её текущий адрес в сети libvirt:
// бронь DHCP по MAC (virsh net-update … ip-dhcp-host), сразу и в
// постоянном описании сети. Нужно пробросам портов и Traffic Fly: адрес
// цели не должен меняться после перезагрузки. Уже закреплённый — тот же.
func (r *CreateRunner) FixAddress(ctx context.Context, name string) (string, error) {
	report := r.AddressReport(ctx, name)
	var network, mac string
	for _, i := range report.Ifaces {
		if i.Type == "network" && macRe.MatchString(i.MAC) {
			network, mac = i.Source, strings.ToLower(i.MAC)
			break
		}
	}
	if network == "" {
		return "", msgs.Errorf("vmcreate.fixAddressNoNetwork", name)
	}
	ip := ""
	for _, a := range report.Addresses {
		if strings.EqualFold(a.MAC, mac) || a.MAC == "" {
			ip = a.Addr
			break
		}
	}
	if net.ParseIP(ip).To4() == nil {
		return "", msgs.Errorf("vmcreate.fixAddressNoAddress", name)
	}
	entry := fmt.Sprintf("<host mac='%s' name='%s' ip='%s'/>", mac, html.EscapeString(name), ip)
	for _, verb := range []string{"add-last", "modify"} {
		res, err := r.run(ctx, "virsh", "net-update", network, verb, "ip-dhcp-host", entry, "--live", "--config")
		if err != nil {
			return "", err
		}
		if res.ExitCode == 0 {
			return ip, nil
		}
		// Бронь по этому MAC уже есть — заменяем её (add-last отказывает).
		if verb == "add-last" && strings.Contains(strings.ToLower(res.Output()), "exist") {
			continue
		}
		return "", fmt.Errorf("virsh net-update: %s", commandError(res.Stderr, res.Stdout))
	}
	return ip, nil
}
