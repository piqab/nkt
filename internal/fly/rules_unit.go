package fly

// Пересылка пакетов и правила nft туннеля — не из PostUp wg-quick: на
// Ubuntu 26.04+ профиль AppArmor «wg-quick» запускает sysctl и nft в своих
// узких подпрофилях — sysctl может писать только src_valid_mark, nft не
// читает файлы вовсе («permission denied on key net.ipv4.ip_forward»,
// «Could not open file …nft: Permission denied» у root), и wg-quick
// снимает интерфейс. Эти шаги делает служба systemd рядом с туннелем: она
// запускается вслед за wg-quick@<iface> (и после перезагрузки) и под его
// профиль не попадает. В PostUp остаётся то, что профиль разрешает: ключ
// (wg), ip rule и ip route, iptables.

// RulesUnitPath — шаблон службы (один на хост или шлюз).
const RulesUnitPath = "/etc/systemd/system/nkt-fly-rules@.service"

// RulesUnitName — служба туннеля iface (уже проверенного ValidIface или
// "nkt-fly").
func RulesUnitName(iface string) string { return "nkt-fly-rules@" + iface + ".service" }

// RulesUnit — текст шаблона. Правила nft — из /etc/wireguard/<iface>.nft,
// если он есть (у хоста; шлюз грузит свои правила сам). Снимает таблицу
// PostDown wg-quick: удаление таблицы файла не читает и профилю не мешает.
const RulesUnit = `# nkt Traffic Fly — файл пишет nkt, правки руками затрутся.
# sysctl и nft туннеля %i: из PostUp wg-quick их не пускает AppArmor
# (Ubuntu 26.04+), поэтому — отдельной службой вслед за wg-quick@%i.
[Unit]
Description=nkt Traffic Fly: forwarding and nft rules for %i
After=wg-quick@%i.service
BindsTo=wg-quick@%i.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'sysctl -qw net.ipv4.ip_forward=1 net.ipv4.conf.%i.rp_filter=2'
ExecStart=/bin/sh -c 'if [ -f ` + WGDir + `/%i.nft ]; then nft -f ` + WGDir + `/%i.nft; fi'

[Install]
WantedBy=wg-quick@%i.service
`

// SysctlPath — постоянные настройки ядра для Traffic Fly: пересылка
// включена с загрузки, до туннеля.
const SysctlPath = "/etc/sysctl.d/90-nkt-fly.conf"

// SysctlConf — текст SysctlPath; у шлюза ещё счётчики байт соединений
// (сводка по маршрутам).
func SysctlConf(gateway bool) string {
	s := "# nkt Traffic Fly — файл пишет nkt, правки руками затрутся\nnet.ipv4.ip_forward = 1\n"
	if gateway {
		s += "net.netfilter.nf_conntrack_acct = 1\n"
	}
	return s
}
