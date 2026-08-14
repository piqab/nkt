import { Table, Tag, type TableColumnsType } from 'antd'
import { useApi } from '../api'
import type { NetworkInterface } from '../types'
import { Card, ErrorNote, Loading, formatBytesShort } from '../components/ui'

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
    title: 'Состояние',
    key: 'up',
    render: (_, i) => (
      <>
        <Tag color={i.up ? 'success' : 'default'}>{i.up ? 'up' : 'down'}</Tag>
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
