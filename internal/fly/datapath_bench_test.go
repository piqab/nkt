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

// Поток через ядро — путь маршрута по порту (DNAT шлюза → туннель → DNAT
// хоста → «контейнер»), в пространствах имён на veth, iperf (2). Шифрование
// WireGuard не входит: туннель здесь — veth. Только по запросу:
// NKT_FLY_BENCH=1 go test ./internal/fly -run DataPathThroughput -v
func TestDataPathThroughput(t *testing.T) {
	if os.Getenv("NKT_FLY_BENCH") == "" {
		t.Skip("NKT_FLY_BENCH=1 — замер потока")
	}
	for _, bin := range []string{"unshare", "nsenter", "ip", "nft", "iperf"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("нет " + bin)
		}
	}
	dir := t.TempDir()
	gw := GatewayConfig{Iface: "nkt-fly", Address: "10.77.1.1/24", ListenPort: 51820,
		Peers: []Peer{{Name: "host", PublicKey: keyA, Address: "10.77.1.2"}},
		Routes: []Route{{ID: "svc", Kind: KindTCP, Port: 5001, Algorithm: AlgoSourceHash, Check: Check{Kind: CheckNone},
			Targets: []Target{{ID: "t", Host: "10.77.1.2", Port: 40001}}}}}
	host := HostIface{Name: "nkt-fly1", Address: "10.77.1.2/24", Gateway: Peer{PublicKey: keyC, Address: "10.77.1.1"},
		Endpoint: "203.0.113.1:51820", Mark: HostMarkBase + 1, Table: HostTableBase + 1,
		Services: []Service{{Port: 40001, Proto: "tcp", Addr: "172.30.0.5", ToPort: 5001}}}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gwNFT := write("gw.nft", GatewayNFT(gw, nil))
	hostNFT := write("host.nft", HostNFT(host))
	script := write("net.sh", `set -e
PIDS=""
trap 'kill $PIDS 2>/dev/null || true' EXIT
ns() { unshare -n sleep 60 >/dev/null 2>&1 & echo $!; }
nx() { p=$1; shift; nsenter --net=/proc/$p/ns/net "$@"; }
CL=$(ns); HO=$(ns); CT=$(ns); PIDS="$CL $HO $CT"; sleep 0.2
ip link set lo up
ip link add pub0 type veth peer name c0; ip link set c0 netns $CL
ip addr add 203.0.113.1/24 dev pub0; ip link set pub0 up
ip link add nkt-fly type veth peer name nkt-fly1; ip link set nkt-fly1 netns $HO
ip addr add 10.77.1.1/24 dev nkt-fly; ip link set nkt-fly up
echo 1 > /proc/sys/net/ipv4/ip_forward
nft -f GW_NFT
nx $CL ip link set lo up; nx $CL ip addr add 203.0.113.50/24 dev c0; nx $CL ip link set c0 up
nx $HO ip link set lo up
nx $HO ip addr add 10.77.1.2/24 dev nkt-fly1; nx $HO ip link set nkt-fly1 up
nx $HO ip link add wan0 type dummy; nx $HO ip addr add 192.0.2.2/24 dev wan0; nx $HO ip link set wan0 up
nx $HO ip route add default via 192.0.2.1 dev wan0
nx $HO sh -c 'echo 1 > /proc/sys/net/ipv4/ip_forward'
nx $HO nft -f HOST_NFT
nx $HO ip rule add fwmark 0x464c0001 table 7701 priority 7701
nx $HO ip route replace default via 10.77.1.1 dev nkt-fly1 onlink table 7701
nx $HO ip link add h-ct type veth peer name ct0; nx $HO ip link set ct0 netns $CT
nx $HO ip addr add 172.30.0.1/24 dev h-ct; nx $HO ip link set h-ct up
nx $CT ip link set lo up; nx $CT ip addr add 172.30.0.5/24 dev ct0; nx $CT ip link set ct0 up; nx $CT ip route add default via 172.30.0.1
nx $CT iperf -s -p 5001 >/dev/null 2>&1 & PIDS="$PIDS $!"
sleep 0.5
echo "== direct (без Traffic Fly, тот же veth-путь до контейнера с хоста)"
nx $HO iperf -c 172.30.0.5 -p 5001 -t 4 -f m | tail -1
echo "== through Traffic Fly (DNAT шлюза + туннель + DNAT хоста)"
nx $CL iperf -c 203.0.113.1 -p 5001 -t 4 -f m | tail -1
echo "== through Traffic Fly, 4 потока"
nx $CL iperf -c 203.0.113.1 -p 5001 -t 4 -P 4 -f m | tail -1
`)
	b, _ := os.ReadFile(script)
	s := strings.NewReplacer("GW_NFT", gwNFT, "HOST_NFT", hostNFT).Replace(string(b))
	_ = os.WriteFile(script, []byte(s), 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "unshare", "-rn", "bash", script)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "Operation not permitted") {
		t.Skipf("нет пространств имён: %s", out)
	}
	t.Logf("\n%s", out)
	if err != nil {
		t.Fatal(err)
	}
}
