import { useMemo } from 'react'
import { Table, Tag, Tooltip, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { useApi } from '../api'
import type { NetworkInterface } from '../types'
import { Card, ErrorNote, InfoHint, Loading, formatBytesShort } from '../components/ui'
import i18n from '../i18n'

/**
 * up alone can't tell a genuinely working interface apart from one that's
 * administratively enabled with nothing actually connected — lower_up
 * (kernel's own carrier/operational state) is what catches "cable fell
 * out" or "switch port disabled on the other end".
 */
function StateTag({ i }: { i: NetworkInterface }) {
  const { t } = useTranslation()
  if (!i.up) return <Tag>{t('interfaces.down')}</Tag>
  if (!i.lower_up) {
    return (
      <Tooltip title={t('interfaces.noLinkTooltip')}>
        <Tag color="warning">{t('interfaces.noLink')}</Tag>
      </Tooltip>
    )
  }
  return <Tag color="success">{t('interfaces.up')}</Tag>
}

/** A raw byte counter never shows this: an interface can carry plenty of
 * traffic and still be quietly losing packets to a bad cable, a
 * misconfigured MTU further down the path, or a saturated queue. Shown
 * only when something is actually non-zero — a column of "0 / 0" on every
 * healthy row would just be noise. */
function ErrorsCell({ i }: { i: NetworkInterface }) {
  const { t } = useTranslation()
  const rxErr = i.rx_errors ?? 0
  const rxDrop = i.rx_dropped ?? 0
  const txErr = i.tx_errors ?? 0
  const txDrop = i.tx_dropped ?? 0
  if (rxErr + rxDrop + txErr + txDrop === 0) return <span className="small muted">—</span>
  return (
    <div className="small">
      {(rxErr > 0 || rxDrop > 0) && (
        <div style={{ color: 'var(--status-warning)' }}>{t('interfaces.rxErrors', { errors: rxErr, dropped: rxDrop })}</div>
      )}
      {(txErr > 0 || txDrop > 0) && (
        <div style={{ color: 'var(--status-warning)' }}>{t('interfaces.txErrors', { errors: txErr, dropped: txDrop })}</div>
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
  const { t } = useTranslation()
  if (i.docker_network) {
    return (
      <>
        <Tag color="blue">docker: {i.docker_network}</Tag>
        <div className="small muted">
          {i.attached_containers
            ? t('interfaces.containersAttached', { count: i.attached_containers })
            : t('interfaces.noContainersAttached')}
        </div>
      </>
    )
  }
  const guess = guessInterfaceKind(i.name)
  if (!guess) return <span className="small muted">—</span>
  return (
    <Tooltip title={t('interfaces.guessTooltip')}>
      <Tag>{guess}</Tag>
    </Tooltip>
  )
}

// A plain function, not a component — reads the standalone i18n singleton
// directly (see ui.tsx's severityLabel for why that's still reactive
// enough).
function guessInterfaceKind(name: string): string | null {
  if (/^veth/.test(name)) return i18n.t('interfaces.kindVeth')
  if (/^wg/.test(name)) return i18n.t('interfaces.kindWireguard')
  if (/^(tun|tap)/.test(name)) return i18n.t('interfaces.kindTunnel')
  if (/^virbr/.test(name)) return i18n.t('interfaces.kindLibvirt')
  if (/^br-/.test(name)) return i18n.t('interfaces.kindBridge')
  return null
}

/** Real physical/loopback interfaces read top, virtual ones read below in
 * roughly the order traffic passes through them on its way to a
 * container: a VPN/tunnel terminates on the host itself, a bridge is the
 * virtual switch, veth is a single container's leg plugged into it. Plain
 * alphabetical order scattered these across the table (br-acme-backend
 * next to br-acme-monitoring, but eth0 stuck between them) with nothing
 * to indicate which rows are "the real network" versus internal plumbing. */
function interfaceRank(i: NetworkInterface): number {
  if (i.loopback) return 1
  if (/^veth/.test(i.name)) return 4
  if (/^wg/.test(i.name) || /^(tun|tap)/.test(i.name)) return 2
  if (i.docker_network || /^(br-|docker0|virbr)/.test(i.name)) return 3
  return 0 // physical / unclassified — the actual uplinks
}

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
  const { t } = useTranslation()
  const { data, error, loading } = useApi<{ interfaces: NetworkInterface[] }>('/interfaces', 60_000)
  const interfaces = useMemo(() => {
    const list = data?.interfaces ?? []
    return [...list].sort((a, b) => interfaceRank(a) - interfaceRank(b) || a.name.localeCompare(b.name))
  }, [data])

  const columns: TableColumnsType<NetworkInterface> = [
    {
      title: t('interfaces.colInterface'),
      key: 'name',
      render: (_, i) => (
        <>
          <strong>{i.name}</strong>
          {i.mac && <div className="small mono muted">{i.mac}</div>}
        </>
      ),
    },
    {
      title: t('interfaces.colOwner'),
      key: 'owner',
      render: (_, i) => <OwnerCell i={i} />,
    },
    {
      title: t('interfaces.colState'),
      key: 'up',
      render: (_, i) => (
        <>
          <StateTag i={i} />
          {i.loopback && <Tag>loopback</Tag>}
        </>
      ),
    },
    {
      title: t('interfaces.colAddresses'),
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
      title: t('interfaces.colRx'),
      key: 'rx',
      align: 'right',
      render: (_, i) => <span className="small">{formatBytesShort(i.rx_bytes)}</span>,
    },
    {
      title: t('interfaces.colTx'),
      key: 'tx',
      align: 'right',
      render: (_, i) => <span className="small">{formatBytesShort(i.tx_bytes)}</span>,
    },
    {
      title: t('interfaces.colErrors'),
      key: 'errors',
      render: (_, i) => <ErrorsCell i={i} />,
    },
  ]

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('interfaces.title')}
            <InfoHint>{t('interfaces.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={error} />

      <Card title={t('interfaces.cardTitle')} subtitle={t('interfaces.found', { count: interfaces.length })}>
        {loading && !data ? (
          <Loading what={t('interfaces.loading')} />
        ) : interfaces.length === 0 ? (
          <div className="chart-empty">{t('interfaces.none')}</div>
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
