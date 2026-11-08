import type { NetworkInterface } from './types'

// Выбор устройства, в которое машину включают напрямую (macvtap).
//
// Настоящий ответ на вопрос «годится ли» даёт сам хост: internal/vmcreate
// спрашивает iproute2 в момент создания машины и отказывает, если устройство
// опущено, это loopback или уже macvlan/macvtap. Здесь то же правило, но по
// снимку интерфейсов — оно нужно, чтобы список был из чего выбирать ещё до
// отказа, и когда агент старше тех ответов.
const SERVICE = /^(lo$|veth|virbr|vnet|docker|br-|br[0-9]|nkt-|nktfly|nktwg|wg|tap|tun|macvlan|macvtap|flannel|cni|kube|dummy|vxlan|ipip|sit|gre)/

/** Служебное ли имя: хост отвечает за подъём и линк, а за то, что машина
 * окажется в сети, из которой ей не выйти. Поэтому фильтр один для обоих
 * источников списка — и для снимка интерфейсов, и для вердикта хоста. */
export function nicService(name: string): boolean {
  return SERVICE.test(name)
}

/** Годное ли устройство стоит предложить: поднято, на линке, не loopback и
 * не служебное (туннель, мост, veth контейнера — macvtap поверх них существует,
 * но машины никуда не выводит). */
export function nicSuitable(i: NetworkInterface): boolean {
  if (!i.up || !i.lower_up || i.loopback) return false
  return !nicService(i.name)
}

/** IPv4 устройства без маски: по нему видно, в какой сети окажется машина. */
export function nicV4(i: NetworkInterface): string {
  return (i.addresses ?? []).find((a) => !a.includes(':'))?.split('/')[0] ?? ''
}

/** Подпись устройства для списка: имя и адрес, — на хосте их бывает два и
 * больше, и «eth0» без адреса не объясняет, куда попадёт машина. */
export function nicLabel(name: string, addr?: string): string {
  return addr ? `${name} — ${addr}` : name
}
