package fly

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Путь пакета целиком, в пространствах имён (без прав и без последствий):
//
//	клиент 203.0.113.50 ──▶ шлюз 203.0.113.1:2222 (GatewayNFT: DNAT, без SNAT)
//	   ──nkt-fly / nkt-fly1──▶ хост 10.77.1.2:40001 (HostNFT: DNAT на «контейнер»)
//	   ──▶ контейнер 172.30.0.5:8080 — отвечает адресом, который видит.
//
// Туннель — пара veth с именами интерфейсов Traffic Fly (для DNAT, меток и
// маршрутизации ответов это то же, что WireGuard). Маршрут хоста по
// умолчанию ведёт в «интернет» хоста (wan0): ответ дойдёт до клиента,
// только если метка соединения вернёт его в туннель. Контейнер должен
// увидеть настоящий IP клиента — по TCP и по UDP.
func TestDataPathRealIP(t *testing.T) {
	for _, bin := range []string{"unshare", "nsenter", "ip", "nft", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("нет " + bin)
		}
	}
	dir := t.TempDir()
	gw := GatewayConfig{Iface: "nkt-fly", Address: "10.77.1.1/24", ListenPort: 51820,
		Peers: []Peer{{Name: "host", PublicKey: keyA, Address: "10.77.1.2"}},
		Routes: []Route{{ID: "svc", Kind: KindTCPUDP, Port: 2222, Algorithm: AlgoSourceHash, Check: Check{Kind: CheckNone},
			Targets: []Target{{ID: "t", Host: "10.77.1.2", Port: 40001}}},
			{ID: "local", Kind: KindTCP, Port: 3333, Algorithm: AlgoRoundRobin, Check: Check{Kind: CheckNone},
				Targets: []Target{{ID: "t", Host: "10.77.1.2", Port: 40002}}}}}
	host := HostIface{Name: "nkt-fly1", Address: "10.77.1.2/24", Gateway: Peer{PublicKey: keyC, Address: "10.77.1.1"},
		Endpoint: "203.0.113.1:51820", Mark: HostMarkBase + 1, Table: HostTableBase + 1,
		Services: []Service{{Port: 40001, Proto: "tcp", Addr: "172.30.0.5", ToPort: 8080}, {Port: 40001, Proto: "udp", Addr: "172.30.0.5", ToPort: 8080},
			// Цель — сам хост (его адрес в туннеле): ответы — через output.
			{Port: 40002, Proto: "tcp", Addr: "10.77.1.2", ToPort: 9090}}}
	if err := gw.Validate(); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gwNFT := write("gw.nft", GatewayNFT(gw, nil))
	hostNFT := write("host.nft", HostNFT(host))
	server := write("server.py", `import socket, threading
def tcp():
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); s.bind(("0.0.0.0", 8080)); s.listen()
    while True:
        c, a = s.accept(); c.sendall(("tcp " + a[0]).encode()); c.close()
def udp():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.bind(("0.0.0.0", 8080))
    while True:
        d, a = s.recvfrom(100); s.sendto(("udp " + a[0]).encode(), a)
threading.Thread(target=tcp, daemon=True).start(); udp()
`)
	client := write("client.py", `import socket
s = socket.create_connection(("203.0.113.1", 2222), 3); print(s.recv(100).decode())
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); u.settimeout(3); u.sendto(b"hi", ("203.0.113.1", 2222)); print(u.recvfrom(100)[0].decode())
h = socket.create_connection(("203.0.113.1", 3333), 3); print("host-" + h.recv(100).decode())
`)
	hostServer := write("hostserver.py", `import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); s.bind(("0.0.0.0", 9090)); s.listen()
while True:
    c, a = s.accept(); c.sendall(("tcp " + a[0]).encode()); c.close()
`)
	script := write("net.sh", `set -ex
PIDS=""
trap 'kill $PIDS 2>/dev/null || true' EXIT
ns() { unshare -n sleep 30 >/dev/null 2>&1 & echo $!; }
nx() { p=$1; shift; nsenter --net=/proc/$p/ns/net "$@"; }
CL=$(ns); HO=$(ns); CT=$(ns); PIDS="$CL $HO $CT"; sleep 0.2
# шлюз = это пространство имён
ip link set lo up
ip link add pub0 type veth peer name c0; ip link set c0 netns $CL
ip addr add 203.0.113.1/24 dev pub0; ip link set pub0 up
ip link add nkt-fly type veth peer name nkt-fly1; ip link set nkt-fly1 netns $HO
ip addr add 10.77.1.1/24 dev nkt-fly; ip link set nkt-fly up
echo 1 > /proc/sys/net/ipv4/ip_forward
nft -f GW_NFT
# клиент
nx $CL ip link set lo up; nx $CL ip addr add 203.0.113.50/24 dev c0; nx $CL ip link set c0 up
# хост: свой «интернет» wan0 — маршрут по умолчанию не через туннель
nx $HO ip link set lo up
nx $HO ip addr add 10.77.1.2/24 dev nkt-fly1; nx $HO ip link set nkt-fly1 up
nx $HO ip link add wan0 type dummy; nx $HO ip addr add 192.0.2.2/24 dev wan0; nx $HO ip link set wan0 up
nx $HO ip route add default via 192.0.2.1 dev wan0
nx $HO sh -c 'echo 1 > /proc/sys/net/ipv4/ip_forward'
nx $HO nft -f HOST_NFT
nx $HO ip rule add fwmark MARK table TABLE priority TABLE
nx $HO ip route replace default via 10.77.1.1 dev nkt-fly1 onlink table TABLE
# контейнер за хостом
ip link add h-ct type veth peer name ct0; ip link set h-ct netns $HO; ip link set ct0 netns $CT
nx $HO ip addr add 172.30.0.1/24 dev h-ct; nx $HO ip link set h-ct up
nx $CT ip link set lo up; nx $CT ip addr add 172.30.0.5/24 dev ct0; nx $CT ip link set ct0 up; nx $CT ip route add default via 172.30.0.1
nx $CT python3 SERVER >/dev/null 2>&1 & PIDS="$PIDS $!"
nx $HO python3 HOSTSERVER >/dev/null 2>&1 & PIDS="$PIDS $!"
sleep 0.5
nx $CL timeout 10 python3 CLIENT || { set +x; set +x; echo "== gw"; nft list ruleset | grep -E "dport|counter"; sysctl net.ipv4.conf.all.rp_filter net.ipv4.ip_forward; echo "== host"; nx $HO nft list ruleset | grep -E "counter|mark"; nx $HO sysctl net.ipv4.conf.all.rp_filter net.ipv4.conf.nkt-fly1.rp_filter net.ipv4.ip_forward; nx $HO ip rule; nx $HO ip route show table all | grep -v local; exit 1; }
`)
	b, _ := os.ReadFile(script)
	s := strings.NewReplacer("GW_NFT", gwNFT, "HOST_NFT", hostNFT, "SERVER", server, "CLIENT", client, "HOSTSERVER", hostServer,
		"MARK", "0x464c0001", "TABLE", "7701").Replace(string(b))
	if err := os.WriteFile(script, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "unshare", "-rn", "bash", script)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "Operation not permitted") {
		t.Skipf("нет пространств имён: %s", out)
	}
	got := string(out)
	if err != nil || !strings.Contains(got, "tcp 203.0.113.50") || !strings.Contains(got, "udp 203.0.113.50") || !strings.Contains(got, "host-tcp 203.0.113.50") {
		t.Fatalf("контейнер должен видеть настоящий IP клиента, а ответ — дойти: %v\n%s", err, got)
	}
}
