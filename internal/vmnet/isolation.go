package vmnet

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
//   - в сеть токена снаружи новые соединения не открываются (пробросы
//     портов — отдельными правилами, когда появятся).
func IsolationRules(bridges []string) string {
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
	b.WriteString("\t\toifname @isolated ct state new,invalid,untracked drop\n")
	b.WriteString("\t}\n}\n")
	return b.String()
}

// IsolatedBridges — мосты сетей токенов на хосте.
func (m *Manager) IsolatedBridges(ctx context.Context) ([]string, error) {
	all, err := m.names(ctx, "--all")
	if err != nil {
		return nil, err
	}
	var bridges []string
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
		bridges = append(bridges, n.Bridge)
	}
	sort.Strings(bridges)
	return bridges, nil
}

// ApplyIsolation ставит правила для всех сетей токенов хоста. nft нет —
// ошибка: машину токена без изоляции создавать нельзя.
func (m *Manager) ApplyIsolation(ctx context.Context) ([]string, error) {
	if m.run == nil {
		return nil, msgs.Errorf("vmnet.networkManagementUnavailableMode")
	}
	bridges, err := m.IsolatedBridges(ctx)
	if err != nil {
		return nil, err
	}
	if res, err := m.run(ctx, "sh", "-c", "command -v nft"); err != nil || res.ExitCode != 0 {
		return nil, msgs.Errorf("vmnet.isolationNoNft")
	}
	if err := os.MkdirAll(m.tmpDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(m.tmpDir, "nkt-isolation.nft")
	if err := os.WriteFile(path, []byte(IsolationRules(bridges)), 0o644); err != nil {
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
		all, err := m.names(ctx, "--all")
		if err != nil {
			continue
		}
		any := false
		for _, n := range all {
			any = any || IsIsolated(n)
		}
		if !any {
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
