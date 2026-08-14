import { Table, Tag, Tooltip, type TableColumnsType } from 'antd'
import { useApi } from '../api'
import type { NetworkInterface } from '../types'
import { Card, ErrorNote, Loading, formatBytesShort } from '../components/ui'

/**
 * up alone can't tell a genuinely working interface apart from one that's
 * administratively enabled with nothing actually connected — lower_up
 * (kernel's own carrier/operational state) is what catches "cable fell
 * out" or "switch port disabled on the other end".
 */
function StateTag({ i }: { i: NetworkInterface }) {
  if (!i.up) return <Tag>down</Tag>
  if (!i.lower_up) {
    return (
      <Tooltip title="Интерфейс включён администратором, но нет отклика от линк-партнёра — вынут кабель, отключён порт коммутатора, либо peer не поднят">
        <Tag color="warning">нет линка</Tag>
      </Tooltip>
    )
  }
  return <Tag color="success">up</Tag>
}

/** A raw byte counter never shows this: an interface can carry plenty of
 * traffic and still be quietly losing packets to a bad cable, a
 * misconfigured MTU further down the path, or a saturated queue. Shown
 * only when something is actually non-zero — a column of "0 / 0" on every
 * healthy row would just be noise. */
function ErrorsCell({ i }: { i: NetworkInterface }) {
  const rxErr = i.rx_errors ?? 0
  const rxDrop = i.rx_dropped ?? 0
  const txErr = i.tx_errors ?? 0
  const txDrop = i.tx_dropped ?? 0
  if (rxErr + rxDrop + txErr + txDrop === 0) return <span className="small muted">—</span>
  return (
    <div className="small">
      {(rxErr > 0 || rxDrop > 0) && (
        <div style={{ color: 'var(--status-warning)' }}>
          RX: {rxErr} ошибок, {rxDrop} потеряно
        </div>
      )}
      {(txErr > 0 || txDrop > 0) && (
        <div style={{ color: 'var(--status-warning)' }}>
          TX: {txErr} ошибок, {txDrop} потеряно
        </div>
      )}
    </div>
  )
}

/** Points-to-point links (a VPN tunnel) genuinely have a from/to; a bridge
 * doesn't — it's a virtual switch with several things plugged into it, so
 * the meaningful question is "what's attached", not "where does it go".
 * docker_network/attached_containers is real, server-resolved data (the
 * interface matched against docker's own network inspect output — see
 * attachInterfaceOwnership); everything else here is a client-side guess
 * from the interface's name alone, shown visibly less certain (outlined,
 * not filled) so the two are never confused for the same kind of claim. */
function OwnerCell({ i }: { i: NetworkInterface }) {
  if (i.docker_network) {
    return (
      <>
        <Tag color="blue">docker: {i.docker_network}</Tag>
        <div className="small muted">
          {i.attached_containers ? `контейнеров: ${i.attached_containers}` : 'ни одного контейнера не подключено'}
        </div>
      </>
    )
  }
  const guess = guessInterfaceKind(i.name)
  if (!guess) return <span className="small muted">—</span>
  return (
    <Tooltip title="По имени интерфейса — не проверено, в отличие от docker-меток выше">
      <Tag>{guess}</Tag>
    </Tooltip>
  )
}

function guessInterfaceKind(name: string): string | null {
  if (/^veth/.test(name)) return 'veth — контейнер/namespace'
  if (/^wg/.test(name)) return 'WireGuard (VPN)'
  if (/^(tun|tap)/.test(name)) return 'туннель (VPN)'
  if (/^virbr/.test(name)) return 'libvirt/QEMU'
  if (/^br-/.test(name)) return 'мост (bridge)'
  return null
}

const columns: TableColumnsType<NetworkInterface> = [
  {
    title: 'Интерфейс',
    key: 'name',
    render: (_, i) => (
      <>
        <strong>{i.name}</strong>
        {i.mac && <div className="small mono muted">{i.mac}</div>}
      </>
    ),
  },
  {
    title: 'Кому принадлежит',
    key: 'owner',
    render: (_, i) => <OwnerCell i={i} />,
  },
  {
    title: 'Состояние',
    key: 'up',
    render: (_, i) => (
      <>
        <StateTag i={i} />
        {i.loopback && <Tag>loopback</Tag>}
      </>
    ),
  },
  {
    title: 'Адреса',
    key: 'addresses',
    render: (_, i) =>
      i.addresses?.length ? (
        <div className="small mono">
          {i.addresses.map((a) => (
            <div key={a}>{a}</div>
          ))}
        </div>
      ) : (
        <span className="small muted">—</span>
      ),
  },
  { title: 'MTU', key: 'mtu', align: 'right', render: (_, i) => <span className="small">{i.mtu || '—'}</span> },
  {
    title: 'Принято',
    key: 'rx',
    align: 'right',
    render: (_, i) => <span className="small">{formatBytesShort(i.rx_bytes)}</span>,
  },
  {
    title: 'Передано',
    key: 'tx',
    align: 'right',
    render: (_, i) => <span className="small">{formatBytesShort(i.tx_bytes)}</span>,
  },
  {
    title: 'Ошибки/потери',
    key: 'errors',
    render: (_, i) => <ErrorsCell i={i} />,
  },
]

/**
 * Plain inventory of every network interface on the host — physical NICs,
 * bridges, VLANs, tunnels, loopback — from `ip addr` plus traffic counters
 * from /proc/net/dev. Deliberately does not attempt to say which interface
 * is "the public one": Listener.Public() (see «Разное», «Firewall» и
 * находки) already answers that precisely, per socket — guessing it again
 * here, at the interface level, would just be a second, coarser answer to
 * a question already answered correctly elsewhere.
 */
export default function Interfaces() {
  const { data, error, loading } = useApi<{ interfaces: NetworkInterface[] }>('/interfaces', 60_000)
  const interfaces = data?.interfaces ?? []

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Сетевые интерфейсы</h1>
          <p>
            Всё, что реально есть на хосте по данным <code className="mono">ip addr</code>: физические
            карты, мосты, VLAN, туннели, loopback. Кто из них публичный — не здесь: это точно
            определяется по каждому сокету отдельно на страницах «Разное», Firewall и в находках.
          </p>
        </div>
      </div>

      <ErrorNote error={error} />

      <Card title="Интерфейсы" subtitle={`найдено: ${interfaces.length}`}>
        {loading && !data ? (
          <Loading what="сетевые интерфейсы" />
        ) : interfaces.length === 0 ? (
          <div className="chart-empty">Интерфейсы не обнаружены.</div>
        ) : (
          <div className="table-wrap">
            <Table<NetworkInterface>
              dataSource={interfaces}
              columns={columns}
              rowKey="name"
              pagination={false}
              size="small"
            />
          </div>
        )}
      </Card>
    </>
  )
}
