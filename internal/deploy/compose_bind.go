package deploy

import (
	"net/netip"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Адрес публикации портов стека (compose.bind). Порт, опубликованный
// docker без адреса («8080:80»), слушает на всех адресах — и docker
// открывает его своими правилами iptables в обход ufw и firewalld. Хаб в
// копии compose-файла, которая едет на хост, ставит таким публикациям
// адрес bind (по умолчанию 127.0.0.1): наружу стек — через прокси сайта
// или явно (bind: 0.0.0.0, адрес в ports). bind_force — адрес bind и у
// публикаций, где адрес уже указан.

// DefaultBind — адрес публикаций по умолчанию.
const DefaultBind = "127.0.0.1"

// BindAddr — адрес публикаций стека.
func (c ComposeSpec) BindAddr() string {
	if c.Bind == "" {
		return DefaultBind
	}
	return c.Bind
}

func validateBind(bind string) error {
	if bind == "" {
		return nil
	}
	if _, err := netip.ParseAddr(bind); err != nil || strings.Contains(bind, "%") {
		return msgs.Errorf("deploy.specBad", "compose.bind", bind)
	}
	return nil
}

// PortChange — что стало с публикацией порта.
type PortChange struct {
	Service string `json:"service"`
	From    string `json:"from"`
	// To — новая запись; пусто — оставлена как есть (Kept) или не
	// разобрана (Skipped: переменная окружения в записи).
	To      string `json:"to,omitempty"`
	Kept    bool   `json:"kept,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

// formatAddr — адрес для записи порта: IPv6 — в скобках.
func formatAddr(a netip.Addr) string {
	if a.Is6() && !a.Is4In6() {
		return "[" + a.String() + "]"
	}
	return a.String()
}

// splitPortAddr — адрес в короткой записи порта (если есть) и остаток
// «[порт хоста:]порт контейнера[/протокол]».
func splitPortAddr(p string) (addr, rest string, ok bool) {
	if strings.HasPrefix(p, "[") {
		i := strings.Index(p, "]:")
		if i < 0 {
			return "", "", false
		}
		return p[1:i], p[i+2:], true
	}
	body := p
	if i := strings.IndexByte(body, '/'); i >= 0 {
		body = body[:i]
	}
	switch strings.Count(body, ":") {
	case 0, 1:
		return "", p, true
	case 2:
		i := strings.IndexByte(p, ':')
		return p[:i], p[i+1:], true
	}
	return "", "", false
}

// bindShort — короткая запись порта с адресом bind.
func bindShort(p string, bind netip.Addr, force bool) (string, bool, bool) {
	if strings.Contains(p, "${") || strings.Contains(p, "$") {
		return p, false, false // переменная — не разобрать надёжно
	}
	addr, rest, ok := splitPortAddr(p)
	if !ok {
		return p, false, false
	}
	if addr != "" && !force {
		return p, false, true
	}
	if !strings.Contains(strings.SplitN(rest, "/", 2)[0], ":") {
		// Только порт контейнера: порт хоста — любой свободный.
		rest = ":" + rest
	}
	out := formatAddr(bind) + ":" + rest
	return out, out != p, true
}

// BindPorts ставит адрес bind публикациям портов всех сервисов (короткой
// и длинной записи). Адрес «на всех» (0.0.0.0, ::) без force ничего не
// меняет — так и так все адреса.
func BindPorts(text, bind string, force bool) (string, []PortChange, error) {
	addr, err := netip.ParseAddr(bind)
	if err != nil {
		return "", nil, msgs.Errorf("deploy.specBad", "compose.bind", bind)
	}
	if addr.IsUnspecified() && !force {
		return text, nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return "", nil, msgs.Errorf("deploy.composeYAML", err.Error())
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return text, nil, nil
	}
	services := mapValue(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return text, nil, nil
	}
	var changes []PortChange
	touched := false
	for i := 0; i+1 < len(services.Content); i += 2 {
		name := services.Content[i].Value
		svc := services.Content[i+1]
		if svc.Kind != yaml.MappingNode {
			continue
		}
		ports := mapValue(svc, "ports")
		if ports == nil || ports.Kind != yaml.SequenceNode {
			continue
		}
		for _, p := range ports.Content {
			switch p.Kind {
			case yaml.ScalarNode:
				out, changed, ok := bindShort(p.Value, addr, force)
				ch := PortChange{Service: name, From: p.Value}
				switch {
				case !ok:
					ch.Skipped = true
				case changed:
					ch.To = out
					p.Value, p.Tag, p.Style = out, "!!str", yaml.DoubleQuotedStyle
					touched = true
				default:
					ch.Kept = true
				}
				changes = append(changes, ch)
			case yaml.MappingNode:
				target := mapValue(p, "target")
				from := "target " + scalarOr(target, "?")
				if pub := mapValue(p, "published"); pub != nil {
					from = pub.Value + ":" + scalarOr(target, "?")
				}
				ch := PortChange{Service: name, From: from}
				hostIP := mapValue(p, "host_ip")
				switch {
				case hostIP != nil && !force:
					ch.From = hostIP.Value + ":" + from
					ch.Kept = true
				case hostIP != nil:
					ch.From = hostIP.Value + ":" + from
					hostIP.Value, hostIP.Tag = addr.String(), "!!str"
					ch.To = addr.String() + ":" + from
					touched = true
				default:
					p.Content = append(p.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "host_ip"},
						&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: addr.String()})
					ch.To = addr.String() + ":" + from
					touched = true
				}
				changes = append(changes, ch)
			}
		}
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Service < changes[j].Service })
	if !touched {
		return text, changes, nil
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", nil, err
	}
	return string(out), changes, nil
}

func scalarOr(n *yaml.Node, def string) string {
	if n == nil || n.Value == "" {
		return def
	}
	return n.Value
}
