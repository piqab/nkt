package vmnet

import (
	"context"
	"encoding/xml"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net"
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
		return nil, msgs.Errorf("vmnet.networkManagementUnavailableMode")
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
		return msgs.Errorf("vmnet.networkManagementUnavailableMode")
	}
	if err := m.checkSubnetFree(ctx, s); err != nil {
		return err
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

// checkSubnetFree отвергает создание сети с уже занятой подсетью.
//
// Force снимает только запрет на пересечение с сетью самого хоста: там
// бывают осознанные случаи (машины в той же сети, что хост). Пересечение
// двух сетей libvirt не снимается ничем — такая сеть не поднимется.
func (m *Manager) checkSubnetFree(ctx context.Context, s Spec) error {
	if s.Mode == ModeBridge || s.Subnet == "" {
		return nil
	}
	taken, _, err := m.Occupied(ctx)
	if err != nil {
		// Не смогли осмотреться — не мешаем создавать: отказ по
		// неизвестной причине хуже, чем пропущенная проверка.
		return nil
	}
	if s.Force {
		kept := taken[:0]
		for _, t := range taken {
			if !t.Host {
				kept = append(kept, t)
			}
		}
		taken = kept
	}
	return CheckSubnet(s.Subnet, taken)
}

// EnsureNAT заводит NAT-сеть с таким именем, если её ещё нет, и
// поднимает.
//
// Создаётся, а не отвергается: сеть «default» на минимальной установке
// libvirt отсутствует, и требовать от оператора сходить создать её
// руками ради того, что nkt умеет сам, — лишняя работа. Подсеть
// подбирается свободная: занятая чужой сетью не поднимется.
func (m *Manager) EnsureNAT(ctx context.Context, name string) (created bool, err error) {
	all, err := m.names(ctx, "--all")
	if err != nil {
		return false, err
	}
	if toSet(all)[name] {
		// Поднятую сеть не трогаем: «virsh net-start» на уже работающей
		// отвечает отказом, и он выглядел бы как невозможность создать
		// машину, хотя всё в порядке.
		active, err := m.names(ctx)
		if err != nil {
			return false, err
		}
		if toSet(active)[name] {
			return false, nil
		}
		return false, m.Start(ctx, name)
	}

	subnet, bridge, err := m.Suggest(ctx)
	if err != nil {
		return false, err
	}
	spec := Spec{Name: name, Mode: ModeNAT, Bridge: bridge, Subnet: subnet, DHCP: true, Autostart: true}
	if err := m.Create(ctx, spec); err != nil {
		return false, err
	}
	return true, nil
}

// freeSubnet подбирает подсеть и имя моста, не занятые ничем на хосте.
//
// Перебираются 192.168.<N>.0/24 начиная с libvirt'овской 122: занятая
// подсеть не поднимется, а совпадение с домашней сетью оператора сломает
// ему маршрутизацию. taken — всё занятое: и сети libvirt, и интерфейсы
// самого хоста.
func freeSubnet(existing []Network, taken []Occupied) (subnet, bridge string, err error) {
	bridges := map[string]bool{}
	for _, n := range existing {
		if n.Bridge != "" {
			bridges[n.Bridge] = true
		}
	}
	for i := 122; i < 255; i++ {
		candidate := fmt.Sprintf("192.168.%d.0/24", i)
		if CheckSubnet(candidate, taken) != nil {
			continue
		}
		for b := 0; b < 100; b++ {
			name := fmt.Sprintf("virbr%d", b)
			if !bridges[name] {
				return candidate, name, nil
			}
		}
	}
	return "", "", msgs.Errorf("vmnet.freeSubnetFoundSetNetwork")
}

// Occupied собирает всё занятое на хосте: подсети сетей libvirt и
// адреса его собственных интерфейсов.
func (m *Manager) Occupied(ctx context.Context) ([]Occupied, []Network, error) {
	nets, err := m.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	taken := NetworkRanges(ctx, nets)
	taken = append(taken, m.hostRanges(ctx)...)
	return taken, nets, nil
}

// Suggest подбирает свободную подсеть и мост — тем и заполняется форма
// создания сети, чтобы предложенное значение было заведомо свободным, а
// не просто правдоподобным.
func (m *Manager) Suggest(ctx context.Context) (subnet, bridge string, err error) {
	taken, nets, err := m.Occupied(ctx)
	if err != nil {
		return "", "", err
	}
	return freeSubnet(nets, taken)
}

// hostRanges — подсети интерфейсов самого хоста.
//
// Ошибка здесь не фатальна: без списка интерфейсов проверка просто
// становится менее строгой, а сети libvirt всё равно проверяются.
func (m *Manager) hostRanges(ctx context.Context) []Occupied {
	if m.run == nil {
		return nil
	}
	res, err := m.run(ctx, "ip", "-o", "-4", "addr", "show")
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	var out []Occupied
	for _, line := range strings.Split(res.Stdout, "\n") {
		// «2: eth0    inet 192.168.1.5/24 brd 192.168.1.255 scope global eth0»
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != "inet" {
			continue
		}
		iface := strings.TrimSuffix(fields[1], ":")
		if iface == "lo" {
			continue
		}
		ip, ipNet, err := net.ParseCIDR(fields[3])
		if err != nil || ip.To4() == nil || ip.IsLoopback() {
			continue
		}
		out = append(out, Occupied{
			CIDR:  ipNet.String(),
			Where: msgs.Tc(ctx, "vmnet.hostInterface", iface),
			Host:  true,
		})
	}
	return out
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
		return msgs.Errorf("vmnet.invalidNetworkName", name)
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
		return msgs.Errorf("vmnet.invalidNetworkName", name)
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
		return msgs.Errorf("vmnet.invalidNetworkName", name)
	}
	if m.run == nil {
		return msgs.Errorf("vmnet.networkManagementUnavailableMode")
	}
	res, err := m.run(ctx, "virsh", verb, name)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		out := meaningfulLines(res.Stderr, res.Stdout)
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
		return msgs.Errorf("vmnet.virshInstalledHostInstallLibvirt", err)
	}
	return err
}

// meaningfulLines собирает из вывода команды то, что стоит показать.
//
// Не первая строка: virsh пишет заголовок («Failed to start network
// iivirt») первым, а причину — следующей. Показывать только первую
// значит каждый раз выбрасывать ровно ту часть, ради которой сообщение
// и читают.
func meaningfulLines(streams ...string) string {
	var lines []string
	for _, s := range streams {
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "error: ")
			line = strings.TrimPrefix(line, "ошибка: ")
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	if len(lines) == 0 {
		return msgs.T(msgs.DefaultLang, "vmnet.commandFailed")
	}
	if len(lines) > 3 {
		lines = lines[:3]
	}
	return strings.Join(lines, "; ")
}

// firstMeaningful отдаёт первую строку из meaningfulLines — для мест,
// где нужен короткий заголовок.
func firstMeaningful(streams ...string) string {
	out := meaningfulLines(streams...)
	if head, _, ok := strings.Cut(out, "; "); ok {
		return head
	}
	return out
}
