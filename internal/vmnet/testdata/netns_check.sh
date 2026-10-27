#!/bin/bash
# Проверка правил nkt_isolation на живом трафике: «хост» — своё сетевое
# пространство (unshare -rn --mount), «машина» токена — в мосту virbr5,
# «локальная сеть» — за veth. Каждая строка: что вышло и что ждём.
# Запуск — из TestIsolationTraffic при NKT_NETNS=1.
set -e
R=$1
mount -t tmpfs none /run; mkdir -p /run/netns
ip link set lo up
ip link add virbr5 type bridge && ip addr add 192.168.5.1/24 dev virbr5 && ip link set virbr5 up
ip link add lan0 type veth peer name lan1 && ip addr add 10.9.0.1/24 dev lan0 && ip link set lan0 up
echo 1 > /proc/sys/net/ipv4/ip_forward
ip netns add vm && ip link add vmh type veth peer name vmg && ip link set vmg netns vm
ip link set vmh master virbr5 && ip link set vmh up
ip netns exec vm sh -c 'ip link set lo up; ip addr add 192.168.5.10/24 dev vmg; ip link set vmg up; ip route add default via 192.168.5.1'
# Машина другого токена — в своём мосту virbr6.
ip link add virbr6 type bridge && ip addr add 192.168.6.1/24 dev virbr6 && ip link set virbr6 up
ip netns add vm2 && ip link add vm2h type veth peer name vm2g && ip link set vm2g netns vm2
ip link set vm2h master virbr6 && ip link set vm2h up
ip netns exec vm2 sh -c 'ip link set lo up; ip addr add 192.168.6.11/24 dev vm2g; ip link set vm2g up; ip route add default via 192.168.6.1'
# «Интернет» — за veth wan (TEST-NET-2), слушает 80 и 25.
ip link add wan0 type veth peer name wan1 && ip addr add 198.51.100.1/24 dev wan0 && ip link set wan0 up
ip netns add wan && ip link set wan1 netns wan
ip netns exec wan sh -c 'ip link set lo up; ip addr add 198.51.100.2/24 dev wan1; ip link set wan1 up; ip route add 192.168.0.0/16 via 198.51.100.1'
ip netns add lan && ip link set lan1 netns lan
ip netns exec lan sh -c 'ip link set lo up; ip addr add 10.9.0.2/24 dev lan1; ip link set lan1 up; ip route add default via 10.9.0.1'
L='import socket,sys
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); s.bind(("0.0.0.0",int(sys.argv[1]))); s.listen(50)
while True: s.accept()[0].close()'
P='import socket,sys
try: socket.create_connection((sys.argv[1],int(sys.argv[2])),timeout=1.5); print("open")
except Exception: print("closed")'
timeout 30 python3 -c "$L" 22 & timeout 30 python3 -c "$L" 53 &
ip netns exec lan timeout 30 python3 -c "$L" 80 &
ip netns exec vm timeout 30 python3 -c "$L" 8080 &
ip netns exec wan timeout 30 python3 -c "$L" 80 &
ip netns exec wan timeout 30 python3 -c "$L" 25 &
sleep 0.7
nft -f "$R"
pr() { ip netns exec "$1" python3 -c "$P" "$2" "$3"; }
echo "vm -> host ssh 192.168.5.1:22   : $(pr vm 192.168.5.1 22) want closed"
echo "vm -> host dns 192.168.5.1:53   : $(pr vm 192.168.5.1 53) want open"
echo "vm -> host lan ip 10.9.0.1:22   : $(pr vm 10.9.0.1 22) want closed"
echo "vm -> lan 10.9.0.2:80           : $(pr vm 10.9.0.2 80) want closed"
echo "lan -> vm 192.168.5.10:8080     : $(pr lan 192.168.5.10 8080) want closed"
echo "vm -> internet :80             : $(pr vm 198.51.100.2 80) want open"
echo "vm -> internet :25 (no mail)    : $(pr vm 198.51.100.2 25) want closed"
echo "vm2 -> internet :25 (mail ok)   : $(pr vm2 198.51.100.2 25) want open"
echo "host -> vm 192.168.5.10:8080    : $(python3 -c "$P" 192.168.5.10 8080) want open"
# Проброс порта хоста (DNAT, как пул портов и Traffic Fly): снаружи —
# проходит; из сети токена через тот же порт хоста — нет.
nft -f - <<'NFT'
table ip pf {
	chain pre {
		type nat hook prerouting priority dstnat;
		tcp dport 2222 dnat to 192.168.5.10:8080
	}
}
NFT
echo "lan -> host:2222 (dnat to vm)   : $(pr lan 10.9.0.1 2222) want open"
echo "vm2 (other token) -> host:2222  : $(pr vm2 10.9.0.1 2222) want closed"
echo "vm2 (other token) -> vm 8080    : $(pr vm2 192.168.5.10 8080) want closed"
nft delete table inet nkt_isolation
echo "без правил vm -> lan 10.9.0.2:80 : $(pr vm 10.9.0.2 80) want open"
echo "без правил vm -> host 22         : $(pr vm 192.168.5.1 22) want open"
echo "без правил lan -> vm 8080        : $(pr lan 192.168.5.10 8080) want open"
kill $(jobs -p) 2>/dev/null || true
