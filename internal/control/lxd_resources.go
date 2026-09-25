package control

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

func localFingerprint(remote, fp string) string {
	if remote == "local" {
		return fp
	}
	return ""
}

// LXDNetwork — сеть LXD (lxc network list).
type LXDNetwork struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Managed     bool     `json:"managed"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	IPv4        string   `json:"ipv4"`
	IPv6        string   `json:"ipv6"`
	NAT         bool     `json:"nat"`
	UsedBy      []string `json:"used_by"`
}

// LXDStoragePool — пул хранения (lxc storage list).
type LXDStoragePool struct {
	Name        string   `json:"name"`
	Driver      string   `json:"driver"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Size        string   `json:"size"`
	UsedBy      []string `json:"used_by"`
}

// lxdUsedBy — «/1.0/instances/c1?project=x» → «instance c1».
func lxdUsedBy(list []string) []string {
	out := make([]string, 0, len(list))
	for _, u := range list {
		u = strings.TrimPrefix(u, "/1.0/")
		if i := strings.IndexByte(u, '?'); i >= 0 {
			u = u[:i]
		}
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

func (m *LXDManager) listJSON(ctx context.Context, what string, v any) error {
	res, err := m.c.Run(ctx, "lxc", what, "list", "--format", "json")
	if err != nil {
		return fmt.Errorf("lxc %s list: %w", what, err)
	}
	if !res.OK() {
		return msgs.Errorf("control.lxcCode", what, "list", res.ExitCode, strings.TrimSpace(res.Output()))
	}
	if err := json.Unmarshal([]byte(res.Stdout), v); err != nil {
		return fmt.Errorf("lxc %s list: %w", what, err)
	}
	return nil
}

// ListNetworks — сети LXD; сначала управляемые (их можно удалять).
func (m *LXDManager) ListNetworks(ctx context.Context) ([]LXDNetwork, error) {
	var raw []struct {
		Name        string            `json:"name"`
		Type        string            `json:"type"`
		Managed     bool              `json:"managed"`
		Status      string            `json:"status"`
		Description string            `json:"description"`
		Config      map[string]string `json:"config"`
		UsedBy      []string          `json:"used_by"`
	}
	if err := m.listJSON(ctx, "network", &raw); err != nil {
		return nil, err
	}
	out := make([]LXDNetwork, 0, len(raw))
	for _, n := range raw {
		out = append(out, LXDNetwork{
			Name: n.Name, Type: n.Type, Managed: n.Managed, Status: n.Status, Description: n.Description,
			IPv4: n.Config["ipv4.address"], IPv6: n.Config["ipv6.address"], NAT: n.Config["ipv4.nat"] == "true",
			UsedBy: lxdUsedBy(n.UsedBy),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Managed != out[j].Managed {
			return out[i].Managed
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ListStoragePools — пулы хранения, только чтение.
func (m *LXDManager) ListStoragePools(ctx context.Context) ([]LXDStoragePool, error) {
	var raw []struct {
		Name        string            `json:"name"`
		Driver      string            `json:"driver"`
		Status      string            `json:"status"`
		Description string            `json:"description"`
		Config      map[string]string `json:"config"`
		UsedBy      []string          `json:"used_by"`
	}
	if err := m.listJSON(ctx, "storage", &raw); err != nil {
		return nil, err
	}
	out := make([]LXDStoragePool, 0, len(raw))
	for _, p := range raw {
		out = append(out, LXDStoragePool{
			Name: p.Name, Driver: p.Driver, Status: p.Status, Description: p.Description,
			Source: p.Config["source"], Size: p.Config["size"], UsedBy: lxdUsedBy(p.UsedBy),
		})
	}
	return out, nil
}

var (
	lxdNetNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,14}$`)
	// «auto», «none» или CIDR адреса моста.
	lxdNetAddrRe = regexp.MustCompile(`^(auto|none|[0-9a-fA-F.:]+/\d{1,3})$`)
	lxdFPRe      = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
)

// CreateNetwork — мост LXD: `lxc network create NAME ipv4.address=… ipv6.address=…`.
func (m *LXDManager) CreateNetwork(ctx context.Context, user, name, ipv4, ipv6 string) error {
	if !lxdNetNameRe.MatchString(name) {
		return msgs.Errorf("control.lxdBadNetwork", name)
	}
	if ipv4 == "" {
		ipv4 = "auto"
	}
	if ipv6 == "" {
		ipv6 = "none"
	}
	if !lxdNetAddrRe.MatchString(ipv4) || !lxdNetAddrRe.MatchString(ipv6) {
		return msgs.Errorf("control.lxdBadNetworkAddr", ipv4+" / "+ipv6)
	}
	return m.runAudited(ctx, user, "lxd.network.create", name, "network", "create", name, "ipv4.address="+ipv4, "ipv6.address="+ipv6)
}

// DeleteNetwork — `lxc network delete NAME` (LXD сам откажет, если сеть занята).
func (m *LXDManager) DeleteNetwork(ctx context.Context, user, name string) error {
	if !lxdNetNameRe.MatchString(name) {
		return msgs.Errorf("control.lxdBadNetwork", name)
	}
	return m.runAudited(ctx, user, "lxd.network.delete", name, "network", "delete", name)
}

// DeleteImage — `lxc image delete FINGERPRINT` локального образа.
func (m *LXDManager) DeleteImage(ctx context.Context, user, fp string) error {
	if !lxdFPRe.MatchString(fp) {
		return msgs.Errorf("control.lxdBadImage", fp)
	}
	return m.runAudited(ctx, user, "lxd.image.delete", fp, "image", "delete", fp)
}

// ValidImageRef — «images:debian/12», «ubuntu:24.04»: что можно скачать.
var lxdImageRefRe = regexp.MustCompile(`^(images|ubuntu):[A-Za-z0-9][A-Za-z0-9._/-]{0,80}$`)

func ValidLXDImageRef(ref string) bool { return lxdImageRefRe.MatchString(ref) }

func (m *LXDManager) runAudited(ctx context.Context, user, action, target string, args ...string) error {
	res, err := m.c.Run(ctx, "lxc", args...)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, action, target, outcome, map[string]any{
		"exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("lxc %s: %w", strings.Join(args[:2], " "), err)
	}
	if !res.OK() {
		return msgs.Errorf("control.lxcCode", strings.Join(args[:2], " "), target, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}
