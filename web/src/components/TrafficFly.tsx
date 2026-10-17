import { useMemo, useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select, Space, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, DiffView, Loading, Modal } from './ui'
import { DataTable } from './DataTable'
import { HelpButton } from './Docs'
import { formatBytes } from './charts'
import { unifiedDiff } from './textDiff'
import { InstallModal } from './EdgeCard'
import { osLabel } from './OsIcon'
import type { HubHost, Me } from '../types'

// Traffic Fly: проброс трафика из интернета через VPS-шлюз к сервисам на
// хостах — с настоящим IP клиента и балансировкой. Карта, маршруты (по
// имени — 80/443, по порту — TCP/UDP), пулы целей, история версий,
// сухой прогон и применение.

type Kind = 'https' | 'http' | 'tcp' | 'udp' | 'tcpudp'
type Algo = 'leastconn' | 'roundrobin' | 'source'
type TargetKind = 'container' | 'vm' | 'lxd' | 'host' | 'addr'

interface FlyCheck {
  kind: 'tcp' | 'http' | 'none'
  path?: string
  expect?: number
  port?: number
  interval_s?: number
}

interface FlyTarget {
  id: string
  label?: string
  host_id: number
  kind: TargetKind
  ref?: string
  port: number
  weight?: number
  drain?: boolean
}

interface FlyRoute {
  id: string
  label?: string
  gateway: number
  kind: Kind
  names?: string[]
  port: number
  proxy_protocol?: boolean
  algorithm: Algo
  check: FlyCheck
  targets: FlyTarget[]
}

interface FlyGatewayDoc {
  edge_id: number
  index: number
  listen_port: number
}

interface FlyDoc {
  revision: number
  gateways: FlyGatewayDoc[]
  routes: FlyRoute[]
}

interface FlyGatewayInfo {
  edge_id: number
  name: string
  address: string
  connected: boolean
  host_id?: number
}

interface TargetStats {
  id: string
  alive: boolean
  checked: boolean
  drain?: boolean
  active: number
  total: number
  last_error?: string
  latency_ms?: number
}

interface RouteStats {
  id: string
  active: number
  total: number
  failed: number
  bytes_in: number
  bytes_out: number
  clients: number
  top?: { ip: string; conns: number }[]
  targets?: TargetStats[]
}

interface PeerStats {
  public_key: string
  address?: string
  handshake: number
}

interface GatewayStatus {
  edge_id: number
  connected: boolean
  status?: { revision: number; now: number; routes: RouteStats[]; peers?: PeerStats[]; errors?: string[] }
  error?: string
}

const KINDS: Kind[] = ['https', 'http', 'tcp', 'udp', 'tcpudp']
const TARGET_KINDS: TargetKind[] = ['container', 'vm', 'lxd', 'host', 'addr']
// Список IP клиентов маршрута — до стольких; больше — только число.
const MAX_IPS_SHOWN = 30

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

function named(k: Kind) {
  return k === 'https' || k === 'http'
}

function routeTitle(r: FlyRoute): string {
  if (named(r.kind)) return `${(r.names ?? []).join(', ')}${r.port === (r.kind === 'https' ? 443 : 80) ? '' : `:${r.port}`}`
  return `${r.kind === 'tcpudp' ? 'tcp+udp' : r.kind} :${r.port}`
}

function routeText(r: FlyRoute | null): string {
  return r ? JSON.stringify(r, null, 2) + '\n' : ''
}

/** Состояние маршрута по сводке: живых целей из всех. Сводки нет (шлюз
 * не ответил, конфигурация ещё не применена) или цели ещё ни разу не
 * проверены — это «неизвестно», а не «мертвы». */
function poolState(r: FlyRoute, st?: RouteStats): { live: number; total: number; color: string; known: boolean } {
  const total = r.targets.filter((t) => !t.drain).length
  const checked = (st?.targets ?? []).filter((t) => t.checked && !t.drain)
  if (!st?.targets || (checked.length === 0 && total > 0)) return { live: 0, total, color: 'var(--text-muted)', known: false }
  const live = checked.filter((t) => t.alive).length
  return { live, total, color: live === 0 ? 'var(--status-critical)' : live < total ? 'var(--status-warning)' : 'var(--status-good)', known: true }
}

/** Подпись пула: «живы N из M» или «нет данных». */
function poolText(t: (k: string, o?: Record<string, unknown>) => string, ps: ReturnType<typeof poolState>): string {
  return ps.known ? t('fly.poolLive', { live: ps.live, total: ps.total }) : t('fly.poolUnknown', { total: ps.total })
}

export function TrafficFly({ me, onOpenJob }: { me: Me; onOpenJob: (id: number) => void }) {
  const { t } = useTranslation()
  const admin = me.is_admin
  const fly = useApi<{ doc: FlyDoc; text: string; gateways: FlyGatewayInfo[]; applied: number }>('/hub/fly')
  const status = useApi<{ gateways: GatewayStatus[]; hosts: Record<string, string>; now: number }>('/hub/fly/status', 5_000)
  const [edit, setEdit] = useState<FlyRoute | 'new' | null>(null)
  const [openRoute, setOpenRoute] = useState<string | null>(null)
  const [history, setHistory] = useState(false)
  const [addGateway, setAddGateway] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const doc = fly.data?.doc
  const gateways = fly.data?.gateways ?? []
  const hostNames = status.data?.hosts ?? {}
  const routeStats = useMemo(() => {
    const m = new Map<string, RouteStats>()
    for (const g of status.data?.gateways ?? []) for (const r of g.status?.routes ?? []) m.set(r.id, r)
    return m
  }, [status.data])

  async function apply(dryRun: boolean) {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>('/hub/fly/apply', { method: 'POST', body: { dry_run: dryRun } })
      onOpenJob(res.job_id)
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }

  async function saveDoc(routes: FlyRoute[], note: string) {
    if (!doc) return
    await api('/hub/fly', { method: 'PUT', body: { gateways: doc.gateways, routes, note, base: doc.revision } })
    await fly.reload()
  }

  async function removeRoute(r: FlyRoute) {
    if (!doc) return
    const text = unifiedDiff(routeText(r), '', t('tokens.saved'), t('tokens.draft'))
    setDiffAsk({ title: t('fly.deleteTitle', { route: routeTitle(r) }), diff: text, run: (note) => saveDoc(doc.routes.filter((x) => x.id !== r.id), note) })
  }
  const [diffAsk, setDiffAsk] = useState<{ title: string; diff: string; run: (note: string) => Promise<void> } | null>(null)

  const pending = doc && fly.data && doc.revision !== fly.data.applied

  return (
    <div className="col" style={{ gap: '1rem' }}>
      <Card
        title={
          <span>
            Traffic Fly <HelpButton docKey="deploy:fly" isHub admin={admin} compact />
          </span>
        }
        subtitle={t('fly.subtitle')}
        actions={
          <Space wrap>
            {doc && (
              <span className="small muted">
                {t('fly.revision', { n: doc.revision })}{' '}
                {pending ? <Tag color="warning">{fly.data?.applied ? t('fly.notApplied', { n: fly.data.applied }) : t('fly.neverApplied')}</Tag> : doc.revision > 0 && <Tag color="success">{t('fly.applied')}</Tag>}
              </span>
            )}
            <Button size="small" onClick={() => setHistory(true)} disabled={!doc}>
              {t('fly.history')}
            </Button>
            {admin && (
              <>
                <Button size="small" loading={busy} disabled={!doc || doc.routes.length === 0} onClick={() => void apply(true)}>
                  {t('fly.dryRun')}
                </Button>
                <Button size="small" type="primary" loading={busy} disabled={!doc} onClick={() => void apply(false)}>
                  {t('fly.apply')}
                </Button>
              </>
            )}
          </Space>
        }
      >
        {error && <Banner kind="error">{error}</Banner>}
        {fly.error && <Banner kind="error">{String(fly.error)}</Banner>}
        {!doc ? (
          <Loading />
        ) : doc.routes.length === 0 ? (
          <p className="small muted">{gateways.length === 0 ? t('fly.emptyNoGateway') : t('fly.empty')}</p>
        ) : (
          <FlyMap doc={doc} gateways={gateways} statuses={status.data?.gateways ?? []} hostNames={hostNames} onRoute={setOpenRoute} />
        )}
      </Card>

      <Card
        title={t('fly.routes')}
        actions={
          admin && (
            <Button size="small" type="primary" disabled={gateways.length === 0} onClick={() => setEdit('new')}>
              {t('fly.newRoute')}
            </Button>
          )
        }
      >
        <DataTable<FlyRoute>
          dataSource={doc?.routes ?? []}
          rowKey="id"
          size="small"
          locale={{ emptyText: t('fly.noRoutes') }}
          columns={[
            {
              title: t('fly.colRoute'),
              key: 'route',
              render: (_, r) => (
                <Button type="link" size="small" style={{ padding: 0 }} onClick={() => setOpenRoute(r.id)}>
                  <span className="mono">{routeTitle(r)}</span>
                </Button>
              ),
            },
            { title: t('fly.colKind'), key: 'kind', render: (_, r) => <Tag>{t(`fly.kind.${r.kind}`)}</Tag> },
            { title: t('fly.colGateway'), key: 'gw', render: (_, r) => gateways.find((g) => g.edge_id === r.gateway)?.name ?? `#${r.gateway}` },
            {
              title: t('fly.colPool'),
              key: 'pool',
              render: (_, r) => {
                const ps = poolState(r, routeStats.get(r.id))
                return (
                  <span style={{ color: ps.color }}>
                    {poolText(t, ps)} · {t(`fly.algo.${r.algorithm}`)}
                  </span>
                )
              },
            },
            {
              title: t('fly.colConns'),
              key: 'conns',
              render: (_, r) => {
                const st = routeStats.get(r.id)
                return st ? (
                  <span className="small">
                    {st.active} · {t('fly.clientsN', { n: st.clients })} · {formatBytes(st.bytes_in + st.bytes_out)}
                  </span>
                ) : (
                  '—'
                )
              },
            },
            ...(admin
              ? [
                  {
                    title: '',
                    key: 'actions',
                    align: 'right' as const,
                    render: (_: unknown, r: FlyRoute) => (
                      <Space size={4}>
                        <Button size="small" onClick={() => setEdit(r)}>
                          {t('fly.edit')}
                        </Button>
                        <Button size="small" danger onClick={() => void removeRoute(r)}>
                          {t('fly.delete')}
                        </Button>
                      </Space>
                    ),
                  },
                ]
              : []),
          ]}
        />
      </Card>

      <Card
        title={t('fly.gateways')}
        subtitle={t('fly.gatewaysHint')}
        actions={
          admin && (
            <Button size="small" onClick={() => setAddGateway(true)}>
              {t('fly.addGateway')}
            </Button>
          )
        }
      >
        {gateways.length === 0 ? (
          <p className="small muted">{t('fly.noGateways')}</p>
        ) : (
          <div className="col" style={{ gap: '0.4rem' }}>
            {gateways.map((g) => {
              const st = status.data?.gateways.find((x) => x.edge_id === g.edge_id)
              const peers = st?.status?.peers ?? []
              const up = peers.filter((p) => p.handshake > 0 && (st?.status?.now ?? 0) - p.handshake < 180).length
              return (
                <div key={g.edge_id} className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
                  <strong className="mono">{g.name}</strong>
                  {g.connected ? <Tag color="success">{t('fly.gwConnected')}</Tag> : <Tag color="error">{t('fly.gwOffline')}</Tag>}
                  {st?.status && <span className="small muted">{t('fly.tunnelsUp', { up, total: peers.length })}</span>}
                  {(st?.status?.errors ?? []).map((e) => (
                    <Tag key={e} color="error">
                      {e}
                    </Tag>
                  ))}
                  {st?.error && <Tag color="error">{st.error}</Tag>}
                </div>
              )
            })}
          </div>
        )}
      </Card>

      {openRoute && doc && (
        <RouteDetails
          route={doc.routes.find((r) => r.id === openRoute)!}
          stats={routeStats.get(openRoute)}
          hostNames={hostNames}
          admin={admin}
          onClose={() => setOpenRoute(null)}
        />
      )}
      {edit && doc && (
        <RouteEditor
          route={edit === 'new' ? null : edit}
          doc={doc}
          gateways={gateways}
          onClose={() => setEdit(null)}
          onAsk={(route) => {
            const before = edit === 'new' ? null : edit
            const next = before ? doc.routes.map((r) => (r.id === before.id ? route : r)) : [...doc.routes, route]
            setDiffAsk({
              title: before ? t('fly.editTitle', { route: routeTitle(before) }) : t('fly.newRoute'),
              diff: unifiedDiff(routeText(before), routeText(route), t('tokens.saved'), t('tokens.draft')),
              run: async (note) => {
                await saveDoc(next, note)
                setEdit(null)
              },
            })
          }}
        />
      )}
      {diffAsk && <DiffConfirm {...diffAsk} onClose={() => setDiffAsk(null)} />}
      {history && <HistoryModal admin={admin} onClose={() => setHistory(false)} onRestored={() => void fly.reload()} />}
      {addGateway && (
        <InstallModal
          presetRoles={['fly']}
          onClose={() => setAddGateway(false)}
          onStarted={(id) => {
            setAddGateway(false)
            onOpenJob(id)
          }}
        />
      )}
    </div>
  )
}

/** Карта: шлюзы → маршруты → хосты → цели. Цвет — состояние, толщина
 * связи — соединения маршрута. Щелчок по маршруту — подробности. */
function FlyMap({
  doc,
  gateways,
  statuses,
  hostNames,
  onRoute,
}: {
  doc: FlyDoc
  gateways: FlyGatewayInfo[]
  statuses: GatewayStatus[]
  hostNames: Record<string, string>
  onRoute: (id: string) => void
}) {
  const { t } = useTranslation()
  const ROW = 46
  const W = 1100
  const X = [10, 280, 580, 840]
  const BW = [220, 250, 210, 250]
  const stats = new Map<string, RouteStats>()
  for (const g of statuses) for (const r of g.status?.routes ?? []) stats.set(r.id, r)
  const gwIDs = doc.gateways.map((g) => g.edge_id)
  const hostIDs: number[] = []
  const targets: { route: FlyRoute; t: FlyTarget }[] = []
  for (const r of doc.routes)
    for (const tg of r.targets) {
      if (!hostIDs.includes(tg.host_id)) hostIDs.push(tg.host_id)
      targets.push({ route: r, t: tg })
    }
  const rows = Math.max(gwIDs.length, doc.routes.length, hostIDs.length, targets.length, 1)
  const H = rows * ROW + 10
  const y = (i: number, n: number) => 5 + ((rows - n) * ROW) / 2 + i * ROW + ROW / 2
  const tunnelUp = (gw: number, host: number): boolean | undefined => {
    const st = statuses.find((s) => s.edge_id === gw)?.status
    const g = doc.gateways.find((x) => x.edge_id === gw)
    if (!st || !g) return undefined
    // Адрес хоста в туннеле знает хаб; здесь — по любому пиру с этим
    // хостом: достаточно, что хоть один туннель шлюза жив к нему.
    const peers = st.peers ?? []
    if (peers.length === 0) return false
    const addrs = new Set(peers.filter((p) => p.handshake > 0 && st.now - p.handshake < 180).map((p) => p.address))
    return addrs.size > 0 && host >= 0 ? true : addrs.size > 0
  }
  const node = (col: number, i: number, n: number, label: string, sub: string, color: string, onClick?: () => void, key?: string) => {
    const cy = y(i, n)
    return (
      <g key={key ?? `${col}-${i}`} transform={`translate(${X[col]},${cy - 17})`} style={{ cursor: onClick ? 'pointer' : 'default' }} onClick={onClick}>
        <rect width={BW[col]} height={34} rx={7} fill="var(--surface-raised)" stroke={color} strokeWidth={2} />
        <circle cx={12} cy={17} r={5} fill={color} />
        <text x={24} y={15} fontSize={12} fill="var(--text-primary)">
          {label.length > 30 ? label.slice(0, 29) + '…' : label}
        </text>
        <text x={24} y={28} fontSize={10} fill="var(--text-secondary)">
          {sub.length > 36 ? sub.slice(0, 35) + '…' : sub}
        </text>
      </g>
    )
  }
  const link = (x1: number, y1: number, x2: number, y2: number, color: string, w: number, key: string) => (
    <path key={key} d={`M${x1},${y1} C${(x1 + x2) / 2},${y1} ${(x1 + x2) / 2},${y2} ${x2},${y2}`} fill="none" stroke={color} strokeWidth={w} opacity={0.75} />
  )
  const lines: JSX.Element[] = []
  doc.routes.forEach((r, ri) => {
    const gi = gwIDs.indexOf(r.gateway)
    const st = stats.get(r.id)
    const ps = poolState(r, st)
    const w = Math.min(10, 1.5 + Math.log2(1 + (st?.active ?? 0)))
    if (gi >= 0) lines.push(link(X[0] + BW[0], y(gi, gwIDs.length), X[1], y(ri, doc.routes.length), ps.color, w, `g-r-${r.id}`))
    const hostsOfRoute = [...new Set(r.targets.map((x) => x.host_id))]
    for (const h of hostsOfRoute) {
      const hi = hostIDs.indexOf(h)
      const up = tunnelUp(r.gateway, h)
      lines.push(link(X[1] + BW[1], y(ri, doc.routes.length), X[2], y(hi, hostIDs.length), up === false ? 'var(--status-critical)' : ps.color, w, `r-h-${r.id}-${h}`))
    }
  })
  targets.forEach((x, ti) => {
    const hi = hostIDs.indexOf(x.t.host_id)
    const ts = stats.get(x.route.id)?.targets?.find((s) => s.id === x.t.id)
    const color = x.t.drain ? 'var(--status-warning)' : !ts || !ts.checked ? 'var(--text-muted)' : ts.alive ? 'var(--status-good)' : 'var(--status-critical)'
    lines.push(link(X[2] + BW[2], y(hi, hostIDs.length), X[3], y(ti, targets.length), color, 1.5, `h-t-${x.route.id}-${x.t.id}`))
  })
  return (
    <div style={{ overflowX: 'auto' }}>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', minWidth: 720, height: 'auto' }} role="img" aria-label={t('fly.map')}>
        {lines}
        {gwIDs.map((id, i) => {
          const g = gateways.find((x) => x.edge_id === id)
          const st = statuses.find((s) => s.edge_id === id)
          const wg = `WireGuard ${doc.gateways[i].listen_port}/udp`
          return node(0, i, gwIDs.length, g?.name ?? `#${id}`, g && g.address !== g.name ? `${g.address} · ${wg}` : wg, st?.connected ? 'var(--status-good)' : 'var(--status-critical)', undefined, `gw-${id}`)
        })}
        {doc.routes.map((r, i) => {
          const st = stats.get(r.id)
          const ps = poolState(r, st)
          return node(1, i, doc.routes.length, routeTitle(r), `${poolText(t, ps)} · ${st?.active ?? 0} ${t('fly.connsShort')}`, ps.color, () => onRoute(r.id), `r-${r.id}`)
        })}
        {hostIDs.map((h, i) => node(2, i, hostIDs.length, hostNames[String(h)] ?? `#${h}`, t('fly.host'), 'var(--seq-300, #4c8bf5)', undefined, `h-${h}`))}
        {targets.map((x, i) => {
          const ts = stats.get(x.route.id)?.targets?.find((s) => s.id === x.t.id)
          const color = x.t.drain ? 'var(--status-warning)' : !ts || !ts.checked ? 'var(--text-muted)' : ts.alive ? 'var(--status-good)' : 'var(--status-critical)'
          const label = x.t.label || (x.t.kind === 'host' ? t('fly.targetKind.host') : x.t.ref || '')
          return node(3, i, targets.length, `${label}:${x.t.port}`, `${t(`fly.targetKind.${x.t.kind}`)}${ts ? ` · ${ts.active} ${t('fly.connsShort')}` : ''}`, color, () => onRoute(x.route.id), `t-${x.route.id}-${x.t.id}`)
        })}
      </svg>
    </div>
  )
}

/** Подробности маршрута: цели и их проверки, клиенты (IP — администратору
 * и только пока их немного; иначе — число). */
function RouteDetails({
  route,
  stats,
  hostNames,
  admin,
  onClose,
}: {
  route: FlyRoute
  stats?: RouteStats
  hostNames: Record<string, string>
  admin: boolean
  onClose: () => void
}) {
  const { t } = useTranslation()
  if (!route) return null
  const showIPs = admin && (stats?.top?.length ?? 0) > 0 && (stats?.clients ?? 0) <= MAX_IPS_SHOWN
  return (
    <Modal title={`Traffic Fly · ${routeTitle(route)}`} onClose={onClose} width={860}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <Space wrap>
          <Tag>{t(`fly.kind.${route.kind}`)}</Tag>
          <Tag>{t(`fly.algo.${route.algorithm}`)}</Tag>
          {route.proxy_protocol && <Tag color="blue">PROXY protocol</Tag>}
          {stats && (
            <span className="small">
              {t('fly.statsLine', { active: stats.active, total: stats.total, failed: stats.failed, bytes: formatBytes(stats.bytes_in + stats.bytes_out) })}
            </span>
          )}
        </Space>
        <DataTable<FlyTarget>
          dataSource={route.targets}
          rowKey="id"
          size="small"
          pagination={false}
          columns={[
            { title: t('fly.colTarget'), key: 't', render: (_, x) => <span className="mono">{`${x.label || x.ref || t('fly.targetKind.host')}:${x.port}`}</span> },
            { title: t('fly.colHost'), key: 'h', render: (_, x) => hostNames[String(x.host_id)] ?? `#${x.host_id}` },
            { title: t('fly.colKind'), key: 'k', render: (_, x) => t(`fly.targetKind.${x.kind}`) },
            {
              title: t('fly.colState'),
              key: 's',
              render: (_, x) => {
                const ts = stats?.targets?.find((s) => s.id === x.id)
                if (x.drain) return <Tag color="warning">{t('fly.drain')}</Tag>
                if (!ts) return '—'
                if (!ts.checked) return <Tag>{t('fly.checking')}</Tag>
                return (
                  <Tooltip title={ts.last_error}>
                    <Tag color={ts.alive ? 'success' : 'error'}>{ts.alive ? t('fly.alive') : t('fly.dead')}</Tag>
                  </Tooltip>
                )
              },
            },
            { title: t('fly.colConns'), key: 'c', render: (_, x) => stats?.targets?.find((s) => s.id === x.id)?.active ?? '—' },
            { title: t('fly.colLatency'), key: 'l', render: (_, x) => { const v = stats?.targets?.find((s) => s.id === x.id)?.latency_ms; return v ? `${v} ms` : '—' } },
            { title: t('fly.colWeight'), key: 'w', render: (_, x) => x.weight || 1 },
          ]}
        />
        <div className="small">
          <strong>{t('fly.clients')}</strong>{' '}
          {showIPs ? (
            <span className="mono">{stats!.top!.map((c) => `${c.ip}${c.conns > 1 ? ` ×${c.conns}` : ''}`).join(', ')}</span>
          ) : (
            <span>{stats?.clients ?? 0}</span>
          )}
          {admin && (stats?.clients ?? 0) > MAX_IPS_SHOWN && <span className="muted"> — {t('fly.tooManyIPs', { n: MAX_IPS_SHOWN })}</span>}
        </div>
      </div>
    </Modal>
  )
}

function emptyRoute(gateway: number): FlyRoute {
  return { id: '', gateway, kind: 'https', names: [], port: 443, proxy_protocol: true, algorithm: 'leastconn', check: { kind: 'tcp' }, targets: [] }
}

/** Редактор маршрута: вид, имя или порт, пул целей, проверка. Запись —
 * через окно с диффом. */
function RouteEditor({
  route,
  doc,
  gateways,
  onClose,
  onAsk,
}: {
  route: FlyRoute | null
  doc: FlyDoc
  gateways: FlyGatewayInfo[]
  onClose: () => void
  onAsk: (r: FlyRoute) => void
}) {
  const { t } = useTranslation()
  const [r, setR] = useState<FlyRoute>(() => (route ? (JSON.parse(JSON.stringify(route)) as FlyRoute) : emptyRoute(gateways[0]?.edge_id ?? 0)))
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const set = (p: Partial<FlyRoute>) => setR({ ...r, ...p })
  const isNamed = named(r.kind)
  const idTaken = !route && doc.routes.some((x) => x.id === r.id)
  const valid = /^[a-z0-9][a-z0-9-]{0,40}$/.test(r.id) && !idTaken && r.port > 0 && r.targets.length > 0 && (!isNamed || (r.names ?? []).length > 0) && r.targets.every((x) => x.port > 0 && x.host_id !== undefined && (x.kind === 'host' || !!x.ref))
  return (
    <Modal title={route ? t('fly.editTitle', { route: routeTitle(route) }) : t('fly.newRoute')} onClose={onClose} width={900}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap' }}>
          <label>
            {t('fly.fieldID')}
            <Input value={r.id} disabled={!!route} status={idTaken ? 'error' : undefined} onChange={(e) => set({ id: e.target.value.toLowerCase() })} placeholder="app" style={{ width: 160 }} />
          </label>
          <label>
            {t('fly.fieldLabel')}
            <Input value={r.label} onChange={(e) => set({ label: e.target.value })} style={{ width: 200 }} />
          </label>
          <label>
            {t('fly.colGateway')}
            <Select value={r.gateway} onChange={(v) => set({ gateway: v })} options={gateways.map((g) => ({ value: g.edge_id, label: g.name }))} style={{ width: 220 }} />
          </label>
          <label>
            {t('fly.colKind')}
            <Select
              value={r.kind}
              onChange={(k: Kind) =>
                set({
                  kind: k,
                  port: k === 'https' ? 443 : k === 'http' ? 80 : r.port === 443 || r.port === 80 ? 0 : r.port,
                  proxy_protocol: named(k) ? r.proxy_protocol : false,
                  names: named(k) ? r.names : [],
                  algorithm: !named(k) && r.algorithm === 'leastconn' ? 'source' : r.algorithm,
                  check: k === 'udp' && r.check.kind === 'tcp' && !r.check.port ? { kind: 'none' } : r.check,
                })
              }
              options={KINDS.map((k) => ({ value: k, label: t(`fly.kind.${k}`) }))}
              style={{ width: 200 }}
            />
          </label>
          <label>
            {t('fly.fieldPort')}
            <InputNumber min={1} max={65535} value={r.port || null} onChange={(v) => set({ port: v ?? 0 })} />
          </label>
        </div>
        {isNamed && (
          <>
            <label>
              {t('fly.fieldNames')}
              <Select mode="tags" value={r.names} onChange={(v: string[]) => set({ names: v.map((x) => x.trim().toLowerCase()).filter(Boolean) })} tokenSeparators={[',', ' ']} placeholder="app.example.com, *.app.example.com" />
            </label>
            <Checkbox checked={r.proxy_protocol} onChange={(e) => set({ proxy_protocol: e.target.checked })}>
              {t('fly.proxyProtocol')}
            </Checkbox>
          </>
        )}
        {!isNamed && <span className="small muted">{t('fly.portHint')}</span>}
        <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap' }}>
          <label>
            {t('fly.fieldAlgo')}
            <Select
              value={r.algorithm}
              onChange={(v: Algo) => set({ algorithm: v })}
              options={(isNamed ? (['leastconn', 'roundrobin', 'source'] as Algo[]) : (['source', 'roundrobin'] as Algo[])).map((a) => ({ value: a, label: t(`fly.algo.${a}`) }))}
              style={{ width: 240 }}
            />
          </label>
          <label>
            {t('fly.fieldCheck')}
            <Select
              value={r.check.kind}
              onChange={(v: FlyCheck['kind']) => set({ check: { ...r.check, kind: v } })}
              options={(['tcp', 'http', 'none'] as const).filter((k) => k !== 'http' || r.kind === 'http').map((k) => ({ value: k, label: t(`fly.check.${k}`) }))}
              style={{ width: 200 }}
            />
          </label>
          {r.check.kind === 'http' && (
            <>
              <label>
                {t('fly.checkPath')}
                <Input value={r.check.path ?? '/'} onChange={(e) => set({ check: { ...r.check, path: e.target.value } })} style={{ width: 160 }} />
              </label>
              <label>
                {t('fly.checkExpect')}
                <InputNumber min={100} max={599} value={r.check.expect || null} onChange={(v) => set({ check: { ...r.check, expect: v ?? 0 } })} />
              </label>
            </>
          )}
          {r.check.kind !== 'none' && (
            <label>
              {t('fly.checkPort')}
              <InputNumber min={1} max={65535} value={r.check.port || null} onChange={(v) => set({ check: { ...r.check, port: v ?? 0 } })} placeholder={t('fly.checkPortSame')} />
            </label>
          )}
        </div>
        <strong className="small">{t('fly.targets')}</strong>
        {r.targets.map((x, i) => (
          <TargetRow
            key={i}
            target={x}
            hosts={hosts.data ?? []}
            onChange={(nx) => set({ targets: r.targets.map((y, j) => (j === i ? nx : y)) })}
            onRemove={() => set({ targets: r.targets.filter((_, j) => j !== i) })}
          />
        ))}
        <div>
          <Button
            size="small"
            type="dashed"
            onClick={() => {
              let n = r.targets.length + 1
              while (r.targets.some((x) => x.id === `t${n}`)) n++
              set({ targets: [...r.targets, { id: `t${n}`, host_id: hosts.data?.[0]?.id ?? -1, kind: 'container', port: 0 }] })
            }}
          >
            {t('fly.addTarget')}
          </Button>
        </div>
        <Space>
          <Button type="primary" disabled={!valid} onClick={() => onAsk({ ...r, check: r.check.kind === 'http' ? { ...r.check, path: r.check.path || '/' } : r.check })}>
            {t('common.save')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </Space>
      </div>
    </Modal>
  )
}

/** Цель пула: хост, что на нём (выбор из контейнеров, машин, LXD), порт. */
function TargetRow({ target, hosts, onChange, onRemove }: { target: FlyTarget; hosts: HubHost[]; onChange: (t: FlyTarget) => void; onRemove: () => void }) {
  const { t } = useTranslation()
  const list = useApi<{ targets: { kind: string; name: string; ip?: string; state?: string; ports?: number[] }[] }>(
    target.kind !== 'host' && target.kind !== 'addr' ? `/hub/fly/targets?host_id=${target.host_id}` : null,
  )
  const items = (list.data?.targets ?? []).filter((x) => x.kind === target.kind)
  return (
    <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap', alignItems: 'center' }}>
      <Select
        value={target.host_id}
        onChange={(v: number) => onChange({ ...target, host_id: v, ref: '' })}
        options={hosts.map((h) => ({ value: h.id, label: osLabel(h.os_info, h.name), search: h.name }))}
        optionFilterProp="search"
        showSearch
        style={{ width: 200 }}
      />
      <Select value={target.kind} onChange={(v: TargetKind) => onChange({ ...target, kind: v, ref: '' })} options={TARGET_KINDS.map((k) => ({ value: k, label: t(`fly.targetKind.${k}`) }))} style={{ width: 150 }} />
      {target.kind === 'addr' ? (
        <Input value={target.ref} onChange={(e) => onChange({ ...target, ref: e.target.value.trim() })} placeholder="192.168.1.20" style={{ width: 170 }} />
      ) : target.kind !== 'host' ? (
        <Select
          value={target.ref || undefined}
          onChange={(v: string) => {
            const it = items.find((x) => x.name === v)
            onChange({ ...target, ref: v, port: target.port || it?.ports?.[0] || 0 })
          }}
          loading={list.loading}
          options={items.map((x) => ({ value: x.name, label: `${x.name}${x.ip ? ` · ${x.ip}` : ''}${x.state ? ` · ${x.state}` : ''}` }))}
          placeholder={t('fly.pickRef')}
          style={{ width: 240 }}
        />
      ) : null}
      <InputNumber min={1} max={65535} value={target.port || null} onChange={(v) => onChange({ ...target, port: v ?? 0 })} placeholder={t('fly.fieldPort')} />
      <Tooltip title={t('fly.weightHint')}>
        <InputNumber min={1} max={100} value={target.weight || 1} onChange={(v) => onChange({ ...target, weight: v ?? 1 })} addonBefore={t('fly.colWeight')} style={{ width: 130 }} />
      </Tooltip>
      <Checkbox checked={!!target.drain} onChange={(e) => onChange({ ...target, drain: e.target.checked })}>
        {t('fly.drain')}
      </Checkbox>
      <Button size="small" danger onClick={onRemove}>
        ×
      </Button>
    </div>
  )
}

/** Дифф перед записью и пометка к версии. */
function DiffConfirm({ title, diff, run, onClose }: { title: string; diff: string; run: (note: string) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation()
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  return (
    <Modal title={title} onClose={onClose} width={860}>
      <div className="col" style={{ gap: '0.5rem' }}>
        <DiffView text={diff} />
        <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('fly.notePlaceholder')} />
        <span className="small muted">{t('fly.saveHint')}</span>
        {error && <Banner kind="error">{error}</Banner>}
        <Space>
          <Button
            type="primary"
            loading={busy}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                await run(note)
                onClose()
              } catch (err) {
                setError(errText(err))
              } finally {
                setBusy(false)
              }
            }}
          >
            {t('common.save')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </Space>
      </div>
    </Modal>
  )
}

/** История версий: разница с предыдущей, возврат версии. */
function HistoryModal({ admin, onClose, onRestored }: { admin: boolean; onClose: () => void; onRestored: () => void }) {
  const { t } = useTranslation()
  const vs = useApi<{ versions: { n: number; ts: string; user: string; note?: string; text: string }[] }>('/hub/fly/versions')
  const [sel, setSel] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const list = vs.data?.versions ?? []
  const cur = list[sel]
  const prev = list[sel + 1]
  return (
    <Modal title={t('fly.historyTitle')} onClose={onClose} width={960}>
      {!vs.data ? (
        <Loading />
      ) : list.length === 0 ? (
        <p className="small muted">{t('fly.noHistory')}</p>
      ) : (
        <div className="row" style={{ gap: '1rem', alignItems: 'flex-start' }}>
          <div className="col" style={{ gap: '0.2rem', minWidth: 220 }}>
            {list.map((v, i) => (
              <Button key={v.n} size="small" type={i === sel ? 'primary' : 'text'} onClick={() => setSel(i)} style={{ textAlign: 'left', justifyContent: 'flex-start' }}>
                #{v.n} · {v.ts.slice(0, 16).replace('T', ' ')} · {v.user}
                {v.note ? ` · ${v.note}` : ''}
              </Button>
            ))}
          </div>
          <div className="col" style={{ gap: '0.5rem', flex: 1, minWidth: 0 }}>
            {error && <Banner kind="error">{error}</Banner>}
            {cur && <DiffView text={unifiedDiff(prev?.text ?? '', cur.text, prev ? `#${prev.n}` : '—', `#${cur.n}`)} />}
            {admin && cur && sel > 0 && (
              <div>
                <Button
                  size="small"
                  onClick={async () => {
                    try {
                      await api(`/hub/fly/versions/${cur.n}/restore`, { method: 'POST' })
                      onRestored()
                      onClose()
                    } catch (err) {
                      setError(errText(err))
                    }
                  }}
                >
                  {t('fly.restore', { n: cur.n })}
                </Button>
              </div>
            )}
          </div>
        </div>
      )}
    </Modal>
  )
}
