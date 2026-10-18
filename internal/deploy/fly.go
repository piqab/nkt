package deploy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/site"
)

// FlySpec — маршрут Traffic Fly стека: после успешной выкладки хаб ставит
// целями контейнеры сервиса на хостах выкладки. Маршрута нет — создаёт;
// есть — сверяет и меняет только цели (новые — после проверки, старые —
// выводом, без обрыва), а вид, имена и сертификат существующего маршрута
// не трогает. Ничего не изменилось — только проверяет.
type FlySpec struct {
	// Route — идентификатор маршрута в Traffic Fly.
	Route string `yaml:"route"`
	// Gateway — шлюз (имя хоста, адрес или имя edge); пусто — если шлюз
	// один.
	Gateway string `yaml:"gateway,omitempty"`
	// Kind — https (по умолчанию), http, tcp, udp, tcpudp.
	Kind  string   `yaml:"kind,omitempty"`
	Names []string `yaml:"names,omitempty"`
	// Port / PortTo — порт шлюза (у https/http по умолчанию 443/80) и
	// конец диапазона.
	Port   int `yaml:"port,omitempty"`
	PortTo int `yaml:"port_to,omitempty"`
	// TLS — «gateway»: сертификат на шлюзе, цели — HTTP.
	TLS           string `yaml:"tls,omitempty"`
	ProxyProtocol bool   `yaml:"proxy_protocol,omitempty"`
	// Service — сервис compose; TargetPort — его порт внутри контейнера.
	Service    string `yaml:"service"`
	TargetPort int    `yaml:"target_port"`
	// Check — tcp (по умолчанию; у udp — none), http, none.
	Check     string `yaml:"check,omitempty"`
	CheckPath string `yaml:"check_path,omitempty"`
	CheckPort int    `yaml:"check_port,omitempty"`
}

var (
	flyRouteRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	flyGwRe    = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,120}$`)
	flyPathRe  = regexp.MustCompile(`^/[A-Za-z0-9._~/%?&=-]{0,200}$`)
	flyNameRe  = regexp.MustCompile(`^(\*\.)?(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// Named — маршрут по имени (https/http).
func (f FlySpec) Named() bool { return f.Kind == "https" || f.Kind == "http" }

// normalize — умолчания: вид, порт, проверка.
func (f *FlySpec) normalize() {
	f.Kind = strings.ToLower(strings.TrimSpace(f.Kind))
	if f.Kind == "" {
		f.Kind = "https"
	}
	if f.Port == 0 {
		switch f.Kind {
		case "https":
			f.Port = 443
		case "http":
			f.Port = 80
		}
	}
	for i, n := range f.Names {
		f.Names[i] = strings.ToLower(strings.TrimSpace(n))
	}
	if f.Check == "" {
		f.Check = "tcp"
		if f.Kind == "udp" && f.CheckPort == 0 {
			f.Check = "none"
		}
	}
}

func (f *FlySpec) validate(i int) error {
	f.normalize()
	field := func(name string) string { return fmt.Sprintf("compose.fly[%d].%s", i, name) }
	if !flyRouteRe.MatchString(f.Route) {
		return msgs.Errorf("deploy.specBad", field("route"), f.Route)
	}
	if f.Gateway != "" && !flyGwRe.MatchString(f.Gateway) {
		return msgs.Errorf("deploy.specBad", field("gateway"), f.Gateway)
	}
	switch f.Kind {
	case "https", "http":
		if len(f.Names) == 0 {
			return msgs.Errorf("deploy.specNeeds", field("names"))
		}
		for _, n := range f.Names {
			if !flyNameRe.MatchString(n) || (f.TLS == "gateway" && strings.HasPrefix(n, "*.")) {
				return msgs.Errorf("deploy.specBad", field("names"), n)
			}
		}
		if f.PortTo != 0 {
			return msgs.Errorf("deploy.specBad", field("port_to"), fmt.Sprint(f.PortTo))
		}
	case "tcp", "udp", "tcpudp":
		if len(f.Names) > 0 || f.TLS != "" || f.ProxyProtocol {
			return msgs.Errorf("deploy.specBad", field("kind"), f.Kind)
		}
		if f.Port == 0 {
			return msgs.Errorf("deploy.specNeeds", field("port"))
		}
	default:
		return msgs.Errorf("deploy.specBad", field("kind"), f.Kind)
	}
	if f.Port < 1 || f.Port > 65535 || f.PortTo != 0 && (f.PortTo <= f.Port || f.PortTo > 65535) {
		return msgs.Errorf("deploy.specBad", field("port"), fmt.Sprint(f.Port))
	}
	if f.TLS != "" && (f.TLS != "gateway" || f.Kind != "https" || f.ProxyProtocol) {
		return msgs.Errorf("deploy.specBad", field("tls"), f.TLS)
	}
	if !site.ValidName(f.Service) {
		return msgs.Errorf("deploy.specBad", field("service"), f.Service)
	}
	if f.TargetPort < 1 || f.TargetPort > 65535 || f.PortTo != 0 && f.TargetPort != f.Port {
		return msgs.Errorf("deploy.specBad", field("target_port"), fmt.Sprint(f.TargetPort))
	}
	switch f.Check {
	case "tcp", "none":
	case "http":
		if f.CheckPath == "" {
			f.CheckPath = "/"
		}
		if !flyPathRe.MatchString(f.CheckPath) {
			return msgs.Errorf("deploy.specBad", field("check_path"), f.CheckPath)
		}
	default:
		return msgs.Errorf("deploy.specBad", field("check"), f.Check)
	}
	if f.CheckPort < 0 || f.CheckPort > 65535 {
		return msgs.Errorf("deploy.specBad", field("check_port"), fmt.Sprint(f.CheckPort))
	}
	return nil
}

// validateFly — блоки fly: стека; маршрут — не дважды.
func validateFly(fs []FlySpec) error {
	seen := map[string]bool{}
	for i := range fs {
		if err := fs[i].validate(i); err != nil {
			return err
		}
		if seen[fs[i].Route] {
			return msgs.Errorf("deploy.specBad", fmt.Sprintf("compose.fly[%d].route", i), fs[i].Route)
		}
		seen[fs[i].Route] = true
	}
	return nil
}
