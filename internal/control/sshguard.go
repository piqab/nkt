package control

import (
	"bufio"
	"context"
	"github.com/piqab/nkt/internal/msgs"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/config"
)

// Правка sshd_config — единственная правка конфигурации, которая способна
// отрезать доступ к хосту насовсем: если демон после неё не поднимется,
// чинить будет уже нечем. Поэтому здесь две отдельные вещи.
//
// Первая — резервный канал: есть ли к хосту путь, не зависящий от sshd.
// Через хаб такого пути нет по построению — управляющий канал сам идёт по
// SSH-туннелю, и упавший sshd уносит с собой и его; а вот собственный
// веб-интерфейс nkt на хосте от sshd не зависит вовсе.
//
// Вторая — проверка живьём: до правки и после перезагрузки демона
// открывается новое TCP-соединение с портом sshd и читается его баннер.
// Именно новое соединение: уже установленная сессия переживает и падение
// демона, так что «у меня всё ещё работает терминал» ничего не доказывает.
// Если до правки соединение устанавливалось, а после перестало — правка
// откатывается автоматически.

// sshProbeTimeout ограничивает попытку соединения. Порог низкий намеренно:
// проверка идёт по локальной петле, и «долго» здесь означает не медленную
// сеть, а демона, который не отвечает.
const sshProbeTimeout = 3 * time.Second

// SSHProbe — результат попытки установить с sshd НОВОЕ соединение.
type SSHProbe struct {
	OK   bool `json:"ok"`
	Port int  `json:"port"`
	// Banner — первая строка, которую шлёт sshd ("SSH-2.0-OpenSSH_9.6").
	// Она и доказывает, что ответил именно sshd, а не что-то другое,
	// занявшее порт.
	Banner    string `json:"banner,omitempty"`
	Error     string `json:"error,omitempty"`
	Simulated bool   `json:"simulated,omitempty"`
}

// SSHReserve описывает, есть ли к хосту путь в обход sshd.
type SSHReserve struct {
	OK bool `json:"ok"`
	// ViaHubTunnel — этот запрос пришёл через SSH-туннель хаба, то есть
	// сам управляющий канал зависит от sshd.
	ViaHubTunnel bool   `json:"via_hub_tunnel"`
	NktAddr      string `json:"nkt_addr"`
	Detail       string `json:"detail"`
}

// sshdPort читает Port из основного sshd_config. Демон допускает несколько
// Port-директив; берётся первая — этого достаточно, чтобы проверить, что
// демон вообще принимает соединения.
func sshdPort(cfg *config.Config, read func(string) ([]byte, error)) int {
	raw, err := read(strings.TrimSuffix(cfg.SSHRoot, "/") + "/sshd_config")
	if err != nil {
		return 22
	}
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Port") {
			continue
		}
		if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return 22
}

// ProbeSSHD открывает новое соединение с локальным sshd и читает баннер.
func (m *ConfigManager) ProbeSSHD(ctx context.Context) SSHProbe {
	port := sshdPort(m.cfg, m.c.ReadFile)
	probe := SSHProbe{Port: port}
	if m.cfg.Mode == config.ModeFixtures {
		// В демо-режиме никакого sshd нет и трогать чужой порт нельзя.
		probe.OK, probe.Simulated, probe.Banner = true, true, "SSH-2.0-simulated"
		return probe
	}

	return probeSSHPort(ctx, port)
}

// probeSSHPort — сама проверка, отделённая от чтения конфигурации, чтобы
// её можно было прогнать против поддельного слушателя в тесте.
func probeSSHPort(ctx context.Context, port int) SSHProbe {
	probe := SSHProbe{Port: port}
	dialer := net.Dialer{Timeout: sshProbeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(sshProbeTimeout))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		probe.Error = msgs.Tc(ctx, "control.connectionOpenedButBannerArrived", err)
		return probe
	}
	probe.Banner = strings.TrimSpace(line)
	if !strings.HasPrefix(probe.Banner, "SSH-") {
		probe.Error = msgs.Tc(ctx, "control.portHeldSomethingOtherThan", port, probe.Banner)
		return probe
	}
	probe.OK = true
	return probe
}

// SSHReserveChannel отвечает на вопрос «если sshd не поднимется, останется
// ли способ сюда попасть».
func (m *ConfigManager) SSHReserveChannel(ctx context.Context, viaHubTunnel bool) SSHReserve {
	res := SSHReserve{ViaHubTunnel: viaHubTunnel, NktAddr: m.cfg.Addr}
	if !viaHubTunnel {
		res.OK = true
		res.Detail = msgs.Tc(ctx, "control.sshReserveDirect")
		return res
	}
	// Через туннель управляющий канал сам держится на sshd. Спасает только
	// то, что веб-интерфейс nkt слушает адрес, доступный снаружи.
	if listensPublicly(m.cfg.Addr) {
		res.OK = true
		res.Detail = msgs.Tc(ctx, "control.sshReservePublic", m.cfg.Addr)
		return res
	}
	res.Detail = msgs.Tc(ctx, "control.sshReserveNone", m.cfg.Addr)
	return res
}

// listensPublicly сообщает, доступен ли адрес прослушивания снаружи хоста.
// Петлевой адрес доступен только тому, кто уже внутри — то есть тому, кто
// зашёл по SSH, которого в этот момент и не будет.
func listensPublicly(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		// ":8077" — все интерфейсы.
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Имя, а не адрес — разрешать его здесь незачем: раз это не
		// петлевой литерал, считаем адрес внешним.
		return !strings.EqualFold(host, "localhost")
	}
	return !ip.IsLoopback()
}
