package deploy

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Конфликт публикаций внутри стека: один и тот же порт хоста на
// пересекающихся адресах. 0.0.0.0:8080 и 127.0.0.1:8080 — конфликт:
// «все адреса» уже включают loopback, и второй bind упадёт на compose up
// («port is already allocated»). Проверка с хоста этого не видит — она
// сравнивает стек только с тем, что на хосте уже слушает.

// PortConflict — две публикации одного порта хоста.
type PortConflict struct {
	Port  int
	Proto string
	A, B  string // «сервис адрес:порт»
}

type portPub struct {
	service, addr, proto string
	port                 int
}

func (p portPub) String() string {
	a := p.addr
	if a == "" {
		a = "0.0.0.0"
	}
	return fmt.Sprintf("%s %s:%d", p.service, a, p.port)
}

func wildAddr(a string) bool {
	switch strings.Trim(a, "[]") {
	case "", "0.0.0.0", "::", "*":
		return true
	}
	return false
}

func sameAddr(a, b string) bool {
	pa, ea := netip.ParseAddr(strings.Trim(a, "[]"))
	pb, eb := netip.ParseAddr(strings.Trim(b, "[]"))
	if ea == nil && eb == nil {
		return pa.Unmap() == pb.Unmap()
	}
	return strings.EqualFold(strings.Trim(a, "[]"), strings.Trim(b, "[]"))
}

// PortConflicts — публикации стека, которые не поднимутся вместе.
// Публикации с переменной и диапазоны портов не разбираются — они не
// считаются ни конфликтом, ни его отсутствием (их называет журнал bind).
func PortConflicts(text string) []PortConflict {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(text), &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	services := mapValue(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil
	}
	var pubs []portPub
	for i := 0; i+1 < len(services.Content); i += 2 {
		name := services.Content[i].Value
		ports := mapValue(services.Content[i+1], "ports")
		if ports == nil || ports.Kind != yaml.SequenceNode {
			continue
		}
		for _, p := range ports.Content {
			switch p.Kind {
			case yaml.ScalarNode:
				if pub, ok := shortPub(name, p.Value); ok {
					pubs = append(pubs, pub)
				}
			case yaml.MappingNode:
				pubV := mapValue(p, "published")
				if pubV == nil {
					continue
				}
				port, err := strconv.Atoi(strings.TrimSpace(pubV.Value))
				if err != nil || port <= 0 {
					continue
				}
				proto := "tcp"
				if pr := mapValue(p, "protocol"); pr != nil && pr.Value != "" {
					proto = strings.ToLower(pr.Value)
				}
				addr := ""
				if ip := mapValue(p, "host_ip"); ip != nil {
					addr = ip.Value
				}
				pubs = append(pubs, portPub{service: name, addr: addr, proto: proto, port: port})
			}
		}
	}
	var out []PortConflict
	for i := 0; i < len(pubs); i++ {
		for j := i + 1; j < len(pubs); j++ {
			a, b := pubs[i], pubs[j]
			if a.port != b.port || a.proto != b.proto {
				continue
			}
			if wildAddr(a.addr) || wildAddr(b.addr) || sameAddr(a.addr, b.addr) {
				out = append(out, PortConflict{Port: a.port, Proto: a.proto, A: a.String(), B: b.String()})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// shortPub — короткая запись «[адрес:]порт_хоста:порт_контейнера[/протокол]»;
// без порта хоста (только порт контейнера) — любой свободный, не конфликт.
func shortPub(service, p string) (portPub, bool) {
	if strings.Contains(p, "$") {
		return portPub{}, false
	}
	addr, rest, ok := splitPortAddr(p)
	if !ok {
		return portPub{}, false
	}
	proto := "tcp"
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		proto = strings.ToLower(rest[i+1:])
		rest = rest[:i]
	}
	host, _, found := strings.Cut(rest, ":")
	if !found || host == "" || strings.Contains(host, "-") {
		return portPub{}, false
	}
	port, err := strconv.Atoi(host)
	if err != nil || port <= 0 {
		return portPub{}, false
	}
	return portPub{service: service, addr: addr, proto: proto, port: port}, true
}
