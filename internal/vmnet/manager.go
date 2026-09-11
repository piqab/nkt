package vmnet

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Manager читает и правит сети libvirt через virsh.
type Manager struct {
	run Runner
	// tmpDir — куда класть описание сети перед net-define. Свой каталог
	// данных: /tmp у юнита свой (PrivateTmp), и файл оттуда virsh по ту
	// сторону песочницы не увидит.
	tmpDir string
}

// NewManager строит управление сетями.
func NewManager(run Runner, tmpDir string) *Manager {
	return &Manager{run: run, tmpDir: tmpDir}
}

// List отдаёт сети хоста вместе с их устройством.
//
// Имена берутся через --name, а не из таблицы: virsh переводит и
// заголовки, и состояния, и разбор по колонкам на русской машине даёт
// пустой список. Список имён — это просто строки, и он одинаков везде.
func (m *Manager) List(ctx context.Context) ([]Network, error) {
	if m.run == nil {
		return nil, fmt.Errorf("управление сетями недоступно в этом режиме")
	}
	all, err := m.names(ctx, "--all")
	if err != nil {
		return nil, err
	}
	active, _ := m.names(ctx)
	autostart, _ := m.names(ctx, "--autostart")

	activeSet := toSet(active)
	autoSet := toSet(autostart)

	nets := make([]Network, 0, len(all))
	for _, name := range all {
		n := Network{
			Name:      name,
			Active:    activeSet[name],
			Autostart: autoSet[name],
			// Непостоянную сеть virsh забудет при перезагрузке; здесь
			// все заводятся через net-define, то есть постоянными.
			Persistent: true,
		}
		// Устройство — из описания: это XML, он от языка не зависит.
		if dump, err := m.run(ctx, "virsh", "net-dumpxml", name); err == nil && dump.ExitCode == 0 {
			fillFromXML(&n, dump.Stdout)
		}
		nets = append(nets, n)
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].Name < nets[j].Name })
	return nets, nil
}

// names отдаёт имена сетей, подходящих под условия.
func (m *Manager) names(ctx context.Context, filters ...string) ([]string, error) {
	argv := append([]string{"virsh", "net-list", "--name"}, filters...)
	res, err := m.run(ctx, argv...)
	if err != nil {
		return nil, describeRunError(err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("virsh net-list: %s", firstMeaningful(res.Stderr, res.Stdout))
	}
	var out []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

// Exists отвечает, заведена ли сеть с таким именем.
func (m *Manager) Exists(ctx context.Context, name string) (bool, error) {
	all, err := m.names(ctx, "--all")
	if err != nil {
		return false, err
	}
	return toSet(all)[name], nil
}

func toSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// Create заводит сеть и, если просили, поднимает её.
func (m *Manager) Create(ctx context.Context, s Spec) error {
	doc, err := XML(s)
	if err != nil {
		return err
	}
	if m.run == nil {
		return fmt.Errorf("управление сетями недоступно в этом режиме")
	}
	if err := os.MkdirAll(m.tmpDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(m.tmpDir, "net-"+s.Name+".xml")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		return err
	}
	defer os.Remove(path)

	if res, err := m.run(ctx, "virsh", "net-define", path); err != nil {
		return err
	} else if res.ExitCode != 0 {
		return fmt.Errorf("virsh net-define: %s", firstMeaningful(res.Stderr, res.Stdout))
	}
	// Заведённая, но не поднятая сеть — ровно то состояние, из-за
	// которого машины и не стартуют, поэтому поднимаем сразу.
	if err := m.Start(ctx, s.Name); err != nil {
		return err
	}
	if s.Autostart {
		return m.SetAutostart(ctx, s.Name, true)
	}
	return nil
}

// EnsureNAT заводит NAT-сеть с таким именем, если её ещё нет, и
// поднимает.
//
// Создаётся, а не отвергается: сеть «default» на минимальной установке
// libvirt отсутствует, и требовать от оператора сходить создать её
// руками ради того, что nkt умеет сам, — лишняя работа. Подсеть
// подбирается свободная: занятая чужой сетью не поднимется.
func (m *Manager) EnsureNAT(ctx context.Context, name string) (created bool, err error) {
	exists, err := m.Exists(ctx, name)
	if err != nil {
		return false, err
	}
	if exists {
		return false, m.Start(ctx, name)
	}

	nets, err := m.List(ctx)
	if err != nil {
		return false, err
	}
	subnet, bridge, err := freeSubnet(nets)
	if err != nil {
		return false, err
	}
	spec := Spec{Name: name, Mode: ModeNAT, Bridge: bridge, Subnet: subnet, DHCP: true, Autostart: true}
	if err := m.Create(ctx, spec); err != nil {
		return false, err
	}
	return true, nil
}

// freeSubnet подбирает подсеть и имя моста, не занятые другими сетями.
//
// Перебираются 192.168.<N>.0/24 начиная с libvirt'овской 122: на чужую
// подсеть сеть просто не поднимется, а совпадение с домашней сетью
// оператора сломает ему маршрутизацию.
func freeSubnet(existing []Network) (subnet, bridge string, err error) {
	taken := map[string]bool{}
	bridges := map[string]bool{}
	for _, n := range existing {
		if n.Address != "" {
			// Адрес хоста в сети — это .1 её подсети.
			if idx := strings.LastIndex(n.Address, "."); idx > 0 {
				taken[n.Address[:idx]] = true
			}
		}
		if n.Bridge != "" {
			bridges[n.Bridge] = true
		}
	}
	for i := 122; i < 255; i++ {
		prefix := fmt.Sprintf("192.168.%d", i)
		if taken[prefix] {
			continue
		}
		for b := 0; b < 100; b++ {
			name := fmt.Sprintf("virbr%d", b)
			if !bridges[name] {
				return prefix + ".0/24", name, nil
			}
		}
	}
	return "", "", fmt.Errorf("не нашлось свободной подсети — задайте сеть вручную")
}

// Start поднимает сеть.
func (m *Manager) Start(ctx context.Context, name string) error {
	return m.simple(ctx, "net-start", name, "already active")
}

// Stop останавливает сеть. Машины, включённые в неё, теряют связь — об
// этом предупреждает интерфейс.
func (m *Manager) Stop(ctx context.Context, name string) error {
	return m.simple(ctx, "net-destroy", name, "not active")
}

// SetAutostart включает или выключает поднятие сети вместе с хостом.
func (m *Manager) SetAutostart(ctx context.Context, name string, on bool) error {
	argv := []string{"virsh", "net-autostart", name}
	if !on {
		argv = append(argv, "--disable")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("некорректное имя сети: %q", name)
	}
	res, err := m.run(ctx, argv...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("virsh net-autostart: %s", firstMeaningful(res.Stderr, res.Stdout))
	}
	return nil
}

// Delete убирает сеть целиком: сначала останавливает, потом забывает
// описание.
func (m *Manager) Delete(ctx context.Context, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("некорректное имя сети: %q", name)
	}
	// Остановка может отказать, если сеть и так не поднята, — это не
	// повод не удалять.
	_ = m.Stop(ctx, name)
	return m.simple(ctx, "net-undefine", name, "")
}

// simple выполняет команду virsh над сетью, прощая заранее известный
// безобидный отказ (сеть уже поднята, уже остановлена).
func (m *Manager) simple(ctx context.Context, verb, name, benign string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("некорректное имя сети: %q", name)
	}
	if m.run == nil {
		return fmt.Errorf("управление сетями недоступно в этом режиме")
	}
	res, err := m.run(ctx, "virsh", verb, name)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		out := firstMeaningful(res.Stderr, res.Stdout)
		if benign != "" && strings.Contains(strings.ToLower(out), benign) {
			return nil
		}
		return fmt.Errorf("virsh %s: %s", verb, out)
	}
	return nil
}

// netXML — то, что нужно из описания сети.
type netXML struct {
	Forward struct {
		Mode string `xml:"mode,attr"`
	} `xml:"forward"`
	Bridge struct {
		Name string `xml:"name,attr"`
	} `xml:"bridge"`
	IP struct {
		Address string `xml:"address,attr"`
		Netmask string `xml:"netmask,attr"`
		DHCP    struct {
			Range struct {
				Start string `xml:"start,attr"`
			} `xml:"range"`
		} `xml:"dhcp"`
	} `xml:"ip"`
}

// fillFromXML достаёт из описания сети то, что показывается в списке.
func fillFromXML(n *Network, doc string) {
	var parsed netXML
	if xml.Unmarshal([]byte(doc), &parsed) != nil {
		return
	}
	n.Mode = parsed.Forward.Mode
	n.Bridge = parsed.Bridge.Name
	n.Address = parsed.IP.Address
	n.Netmask = parsed.IP.Netmask
	n.DHCP = parsed.IP.DHCP.Range.Start != ""
}

// describeRunError переводит отказ запуска на человеческий.
//
// «exec: virsh: executable file not found in $PATH» — верно и
// бесполезно: оператору нужно знать не про PATH, а про то, что пакет не
// установлен и где нажать кнопку.
func describeRunError(err error) error {
	text := err.Error()
	if strings.Contains(text, "executable file not found") || strings.Contains(text, "no such file or directory") {
		return fmt.Errorf("на хосте нет virsh — установите пакет libvirt-clients "+
			"(в «Профилях» для этого есть кнопка «Установить недостающее»): %w", err)
	}
	return err
}

// firstMeaningful отдаёт первую непустую строку без повторяющегося
// «error:», которым virsh начинает каждую свою.
func firstMeaningful(streams ...string) string {
	for _, s := range streams {
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "error: ")
			line = strings.TrimPrefix(line, "ошибка: ")
			if line != "" {
				return line
			}
		}
	}
	return "команда завершилась с ошибкой"
}
