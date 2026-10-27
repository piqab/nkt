package vmnet

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Сети машин токенов (1.17.3). Машины чужих клиентов живут каждая в сети
// своего токена: NAT наружу есть, а хост, его локальная сеть и сети
// других токенов — закрыты. Держит это таблица nftables nkt_isolation:
// отдельная от правил libvirt и firewalld, поэтому её не сбивают их
// перезагрузки, а она — их. Таблица собирается заново из списка сетей
// libvirt (по приставке имени), так что состояния, кроме самих сетей,
// хранить негде и незачем.

// IsolatedPrefix — приставка имени сети токена: «nkt-t<ID токена>».
const IsolatedPrefix = "nkt-t"

// IsolationTable — таблица nftables с правилами сетей токенов.
const IsolationTable = "nkt_isolation"

// IsolatedName — имя сети токена.
func IsolatedName(tokenID int64) string {
	return IsolatedPrefix + strconv.FormatInt(tokenID, 10)
}

// IsIsolated — сеть токена.
func IsIsolated(name string) bool {
	rest, ok := strings.CutPrefix(name, IsolatedPrefix)
	if !ok || rest == "" {
		return false
	}
	_, err := strconv.ParseInt(rest, 10, 64)
	return err == nil
}

// privateRanges — куда из сети токена нельзя открывать соединения:
// частные сети (локальная сеть хоста, другие сети libvirt), CGNAT,
// link-local (в том числе адрес метаданных облака), loopback, multicast.
var privateRanges = []string{
	"10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4",
}

// IsolationRules — сценарий nft для мостов сетей токенов: таблица
// заменяется целиком одной транзакцией. Без мостов — таблица удаляется.
//
//   - к хосту из сети токена — только DNS и DHCP (их раздаёт dnsmasq
//     libvirt) и ping; ответы на соединения хоста (вход хаба через хост)
//     проходят;
//   - наружу — да, в частные сети — нет, IPv6 — нет (в сети токена его
//     нет, и обходить им правила незачем);
//   - в сеть токена снаружи новые соединения не открываются, кроме
//     перенаправленных хостом (DNAT: порты из пула, Traffic Fly).
func IsolationRules(bridges []string, mail ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {}\ndelete table inet %s\n", IsolationTable, IsolationTable)
	if len(bridges) == 0 {
		return b.String()
	}
	quoted := make([]string, len(bridges))
	for i, br := range bridges {
		quoted[i] = strconv.Quote(br)
	}
	fmt.Fprintf(&b, "table inet %s {\n", IsolationTable)
	fmt.Fprintf(&b, "\tset private4 {\n\t\ttype ipv4_addr\n\t\tflags interval\n\t\telements = { %s }\n\t}\n", strings.Join(privateRanges, ", "))
	fmt.Fprintf(&b, "\tset isolated {\n\t\ttype ifname\n\t\telements = { %s }\n\t}\n", strings.Join(quoted, ", "))
	// Мосты, которым разрешена почта (порт 25); пустое множество — никому.
	b.WriteString("\tset mail_ok {\n\t\ttype ifname\n")
	if len(mail) > 0 {
		qm := make([]string, len(mail))
		for i, br := range mail {
			qm[i] = strconv.Quote(br)
		}
		fmt.Fprintf(&b, "\t\telements = { %s }\n", strings.Join(qm, ", "))
	}
	b.WriteString("\t}\n")
	b.WriteString("\tchain input {\n\t\ttype filter hook input priority filter - 10; policy accept;\n")
	b.WriteString("\t\tiifname @isolated ct state established,related accept\n")
	b.WriteString("\t\tiifname @isolated udp dport { 53, 67 } accept\n")
	b.WriteString("\t\tiifname @isolated tcp dport 53 accept\n")
	b.WriteString("\t\tiifname @isolated icmp type echo-request accept\n")
	b.WriteString("\t\tiifname @isolated drop\n\t}\n")
	b.WriteString("\tchain forward {\n\t\ttype filter hook forward priority filter - 10; policy accept;\n")
	// Машины одного токена в одной сети друг друга видят (мост с
	// br_netfilter проводит их через forward).
	for _, q := range quoted {
		fmt.Fprintf(&b, "\t\tiifname %s oifname %s accept\n", q, q)
	}
	b.WriteString("\t\tiifname @isolated meta nfproto ipv6 drop\n")
	b.WriteString("\t\tiifname @isolated ip daddr @private4 ct state new,invalid,untracked drop\n")
	// Почта наружу — только сетям с разрешением: рассылки шли бы с адреса
	// хоста.
	b.WriteString("\t\tiifname @isolated iifname != @mail_ok tcp dport 25 ct state new drop\n")
	// Внутрь — только то, что хост сам перенаправил (DNAT): порты из пула
	// и Traffic Fly. Из сети токена к чужой машине через порт хоста не
	// пройти — это отрезано правилом выше.
	b.WriteString("\t\toifname @isolated ct status dnat accept\n")
	b.WriteString("\t\toifname @isolated ct state new,invalid,untracked drop\n")
	b.WriteString("\t}\n}\n")
	return b.String()
}

// IsolatedBridges — мосты сетей токенов на хосте: сети libvirt и сети
// LXD (1.17.7; у LXD мост называется как сеть). Нет ни virsh, ни lxc —
// ошибка; нет одного из них — берётся другой.
func (m *Manager) IsolatedBridges(ctx context.Context) ([]string, error) {
	nets, err := m.isolatedNets(ctx)
	if err != nil {
		return nil, err
	}
	var bridges []string
	for _, br := range nets {
		bridges = append(bridges, br)
	}
	sort.Strings(bridges)
	return bridges, nil
}

// isolatedNets — сети токенов хоста: имя сети → мост.
func (m *Manager) isolatedNets(ctx context.Context) (map[string]string, error) {
	all, virshErr := m.names(ctx, "--all")
	lxd, lxdErr := m.lxdIsolated(ctx)
	if virshErr != nil && lxdErr != nil {
		return nil, virshErr
	}
	nets := map[string]string{}
	for _, br := range lxd {
		nets[br] = br
	}
	for _, name := range all {
		if !IsIsolated(name) {
			continue
		}
		dump, err := m.run(ctx, "virsh", "net-dumpxml", name)
		if err != nil || dump.ExitCode != 0 {
			return nil, msgs.Errorf("vmnet.isolationNetworkUnreadable", name)
		}
		var n Network
		fillFromXML(&n, dump.Stdout)
		if !bridgeRe.MatchString(n.Bridge) {
			return nil, msgs.Errorf("vmnet.isolationNetworkUnreadable", name)
		}
		nets[name] = n.Bridge
	}
	return nets, nil
}

// mailFile — сети токенов, которым разрешена почта (порт 25).
func (m *Manager) mailFile() string { return filepath.Join(m.tmpDir, "nkt-isolation-mail.json") }

func (m *Manager) mailAllowed() []string {
	var list []string
	if raw, err := os.ReadFile(m.mailFile()); err == nil {
		_ = json.Unmarshal(raw, &list)
	}
	return list
}

// SetMailAllowed разрешает или закрывает исходящий порт 25 сети токена и
// ставит правила заново.
func (m *Manager) SetMailAllowed(ctx context.Context, network string, allow bool) error {
	if !IsIsolated(network) {
		return msgs.Errorf("vmnet.notTokenNetwork", network)
	}
	list := m.mailAllowed()
	i := slices.Index(list, network)
	switch {
	case allow && i < 0:
		list = append(list, network)
	case !allow && i >= 0:
		list = slices.Delete(list, i, i+1)
	default:
		return nil
	}
	if err := os.MkdirAll(m.tmpDir, 0o755); err != nil {
		return err
	}
	raw, _ := json.Marshal(list)
	if err := os.WriteFile(m.mailFile(), raw, 0o600); err != nil {
		return err
	}
	_, err := m.ApplyIsolation(ctx)
	return err
}

// lxdIsolated — мосты сетей токенов LXD (lxc network list, CSV: имя,
// тип, …): только мосты с именем сети токена.
func (m *Manager) lxdIsolated(ctx context.Context) ([]string, error) {
	res, err := m.run(ctx, "lxc", "network", "list", "--format", "csv")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("lxc network list: %s", firstMeaningful(res.Stderr, res.Stdout))
	}
	var out []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		f := strings.Split(line, ",")
		if len(f) >= 2 && IsIsolated(f[0]) && f[1] == "bridge" && bridgeRe.MatchString(f[0]) {
			out = append(out, f[0])
		}
	}
	return out, nil
}

// ApplyIsolation ставит правила для всех сетей токенов хоста. nft нет —
// ошибка: машину токена без изоляции создавать нельзя.
func (m *Manager) ApplyIsolation(ctx context.Context) ([]string, error) {
	if m.run == nil {
		return nil, msgs.Errorf("vmnet.networkManagementUnavailableMode")
	}
	nets, err := m.isolatedNets(ctx)
	if err != nil {
		return nil, err
	}
	var bridges, mail []string
	allowed := m.mailAllowed()
	for name, br := range nets {
		bridges = append(bridges, br)
		if slices.Contains(allowed, name) {
			mail = append(mail, br)
		}
	}
	sort.Strings(bridges)
	sort.Strings(mail)
	if res, err := m.run(ctx, "sh", "-c", "command -v nft"); err != nil || res.ExitCode != 0 {
		return nil, msgs.Errorf("vmnet.isolationNoNft")
	}
	if err := os.MkdirAll(m.tmpDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(m.tmpDir, "nkt-isolation.nft")
	if err := os.WriteFile(path, []byte(IsolationRules(bridges, mail...)), 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(path)
	res, err := m.run(ctx, "nft", "-f", path)
	if err != nil {
		return nil, describeRunError(err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("nft -f: %s", firstMeaningful(res.Stderr, res.Stdout))
	}
	return bridges, nil
}

// isolationEvery — как часто сверять таблицу: её сносят «nft flush
// ruleset» и перезапуск службы nftables, а после перезагрузки хоста её нет
// вовсе.
const isolationEvery = 10 * time.Minute

// KeepIsolation держит правила сетей токенов: при запуске и раз в 10
// минут. Хост без libvirt или без сетей токенов — ничего не делает.
func KeepIsolation(ctx context.Context, m *Manager, log *slog.Logger) {
	wait := 20 * time.Second
	lastErr := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = isolationEvery
		if bridges, err := m.IsolatedBridges(ctx); err != nil || len(bridges) == 0 {
			continue
		}
		if _, err := m.ApplyIsolation(ctx); err != nil {
			if err.Error() != lastErr {
				log.Warn("token network isolation", "err", err)
			}
			lastErr = err.Error()
			continue
		}
		lastErr = ""
	}
}
