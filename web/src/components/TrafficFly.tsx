import { useEffect, useMemo, useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Radio, Select, Space, Tag, Tooltip } from 'antd'
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
  /** Конец диапазона портов (TCP/UDP): порт сохраняется насквозь. */
  port_to?: number
  proxy_protocol?: boolean
  /** Сертификат на шлюзе: TLS завершает шлюз, цели — HTTP. */
  tls?: boolean
  /** Белый список адресов клиентов (IPv4, сети); пусто — все. */
  allow?: string[]
  /** Не больше одновременных / новых в секунду соединений с одного адреса. */
  max_conns?: number
  rate?: number
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
  denied?: number
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

/** Сертификат имени на шлюзе. */
interface CertStatus {
  name: string
  not_after?: number
  error?: string
  pending?: boolean
}

interface GatewayStatus {
  edge_id: number
  connected: boolean
  status?: { revision: number; version?: string; now: number; routes: RouteStats[]; peers?: PeerStats[]; errors?: string[]; certs?: CertStatus[] }
  error?: string
}

/** Туннель «шлюз — хост» и почему он не жив (считает хаб). */
interface FlyTunnel {
  edge_id: number
  host_id: number
  state: 'up' | 'not_applied' | 'gateway_missing' | 'host_down' | 'blocked' | 'keys' | 'stale' | 'no_handshake' | 'unknown'
  gateway_rx: number
  host_tx: number
  host_rx: number
}

const KINDS: Kind[] = ['https', 'http', 'tcp', 'udp', 'tcpudp']
// Адрес или сеть IPv4 белого списка (точную проверку делает шлюз).
const allowRe = /^(\d{1,3}\.){3}\d{1,3}(\/(\d|[12]\d|3[0-2]))?$/
const TARGET_KINDS: TargetKind[] = ['container', 'vm', 'lxd', 'host', 'addr']
// Список IP клиентов маршрута — до стольких; больше — только число.
const MAX_IPS_SHOWN = 30

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

function named(k: Kind) {
  return k === 'https' || k === 'http'
}

function routeTitle(r: FlyRoute): string {
  if (named(r.kind)) return `${(r.names ?? []).join(', ')}${r.port === (r.kind === 'https' ? 443 : 80) ? '' : `:${r.port}`}`
  return `${r.kind === 'tcpudp' ? 'tcp+udp' : r.kind} :${r.port}${r.port_to && r.port_to > r.port ? `-${r.port_to}` : ''}`
}

/** Адрес сайта для имени маршрута по имени (порт — если нестандартный). */
function siteURL(r: FlyRoute, name: string): string {
  const scheme = r.kind === 'https' ? 'https' : 'http'
  const std = r.kind === 'https' ? 443 : 80
  return `${scheme}://${name}${r.port === std ? '' : `:${r.port}`}/`
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
  const status = useApi<{ gateways: GatewayStatus[]; hosts: Record<string, string>; tunnels?: FlyTunnel[]; revision?: number; applied?: number; now: number }>('/hub/fly/status', 5_000)
  const [edit, setEdit] = useState<FlyRoute | 'new' | null>(null)
  const [openRoute, setOpenRoute] = useState<string | null>(null)
  const [history, setHistory] = useState(false)
  const [addGateway, setAddGateway] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Перенаправление на карте: режим, окно переключения, меню цели и выбор
  // контейнера щелчком (на телефоне перетаскивать неудобно).
  const [redirect, setRedirect] = useState(false)
  const [switchAsk, setSwitchAsk] = useState<{ route: FlyRoute; after: FlyTarget[]; title: string } | null>(null)
  const [targetMenu, setTargetMenu] = useState<{ route: FlyRoute; target: FlyTarget } | null>(null)
  const [picker, setPicker] = useState<{ route: FlyRoute; target: FlyTarget } | null>(null)
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

  // Номер применённой версии — из сводки (опрос раз в 5 с): задание
  // «Применить» меняет его, а сам документ страница не перечитывает.
  const appliedRev = status.data?.applied ?? fly.data?.applied ?? 0
  const pending = doc && fly.data && doc.revision !== appliedRev

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
                {pending ? <Tag color="warning">{appliedRev ? t('fly.notApplied', { n: appliedRev }) : t('fly.neverApplied')}</Tag> : doc.revision > 0 && <Tag color="success">{t('fly.applied')}</Tag>}
              </span>
            )}
            <Button size="small" onClick={() => setHistory(true)} disabled={!doc}>
              {t('fly.history')}
            </Button>
            {admin && doc && doc.routes.length > 0 && (
              <Tooltip title={t('fly.redirectHint')}>
                <Button size="small" type={redirect ? 'primary' : 'default'} onClick={() => setRedirect(!redirect)}>
                  {t('fly.redirect')}
                </Button>
              </Tooltip>
            )}
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
          <div className="row" style={{ gap: '0.8rem', alignItems: 'flex-start', flexWrap: 'wrap' }}>
            <div style={{ flex: '1 1 600px', minWidth: 0 }}>
              <FlyMap
                doc={doc}
                gateways={gateways}
                statuses={status.data?.gateways ?? []}
                tunnels={status.data?.tunnels ?? []}
                hostNames={hostNames}
                onRoute={setOpenRoute}
                editing={redirect}
                onDropRoute={(r, it) => setSwitchAsk({ route: r, after: [...r.targets, newTargetFrom(r, it, undefined)], title: t('fly.switchAddTitle', { route: routeTitle(r) }) })}
                onDropTarget={(r, tg, it) =>
                  setSwitchAsk({ route: r, after: r.targets.map((x) => (x.id === tg.id ? newTargetFrom(r, it, x) : x)), title: t('fly.switchReplaceTitle', { route: routeTitle(r) }) })
                }
                onTarget={(r, tg) => setTargetMenu({ route: r, target: tg })}
              />
            </div>
            {redirect && (
              <div style={{ flex: '0 0 280px', minWidth: 240 }}>
                <strong className="small">{t('fly.paletteTitle')}</strong>
                <FlyPalette hosts={Object.entries(hostNames).map(([id, name]) => ({ id: Number(id), name }))} />
              </div>
            )}
          </div>
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
                <span className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
                  {/* Имена сайта — ссылками в новую вкладку; подробности —
                      отдельной кнопкой. */}
                  {named(r.kind) ? (
                    (r.names ?? []).map((n) =>
                      n.startsWith('*.') ? (
                        <span key={n} className="mono">
                          {n}
                        </span>
                      ) : (
                        <a key={n} className="mono" href={siteURL(r, n)} target="_blank" rel="noopener noreferrer">
                          {n} ↗
                        </a>
                      ),
                    )
                  ) : (
                    <span className="mono">{routeTitle(r)}</span>
                  )}
                  <Button size="small" type="link" style={{ padding: 0 }} onClick={() => setOpenRoute(r.id)}>
                    {t('fly.details')}
                  </Button>
                </span>
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
              const tunnels = (status.data?.tunnels ?? []).filter((x) => x.edge_id === g.edge_id)
              const up = tunnels.filter((x) => x.state === 'up').length
              return (
                <div key={g.edge_id} className="col" style={{ gap: '0.2rem' }}>
                  <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
                    <strong className="mono">{g.name}</strong>
                    {g.connected ? <Tag color="success">{t('fly.gwConnected')}</Tag> : <Tag color="error">{t('fly.gwOffline')}</Tag>}
                    {st?.status && (
                      <Tooltip title={t('fly.gwVersionHint')}>
                        <Tag>{st.status.version ? `nkt-edge ${st.status.version}` : t('fly.gwVersionOld')}</Tag>
                      </Tooltip>
                    )}
                    {tunnels.length > 0 && <span className="small muted">{t('fly.tunnelsUp', { up, total: tunnels.length })}</span>}
                    {(st?.status?.errors ?? []).map((e) => (
                      <Tag key={e} color="error">
                        {e}
                      </Tag>
                    ))}
                    {st?.error && <Tag color="error">{st.error}</Tag>}
                  </div>
                  {/* Неживые туннели — с причиной: где теряются пакеты. */}
                  {tunnels
                    .filter((x) => x.state !== 'up')
                    .map((x) => (
                      <div key={x.host_id} className="small" style={{ paddingLeft: '1rem' }}>
                        <span className="mono">{hostNames[String(x.host_id)] ?? `#${x.host_id}`}</span>
                        {': '}
                        <span style={{ color: x.state === 'not_applied' || x.state === 'unknown' ? 'var(--text-secondary)' : 'var(--status-critical)' }}>
                          {t(`fly.tunnel.${x.state}`, { port: doc?.gateways.find((d) => d.edge_id === g.edge_id)?.listen_port ?? 51820 })}
                        </span>
                        {(x.host_tx > 0 || x.gateway_rx > 0) && (
                          <span className="muted">
                            {' '}
                            ({t('fly.tunnelBytes', { tx: formatBytes(x.host_tx), rx: formatBytes(x.gateway_rx) })})
                          </span>
                        )}
                      </div>
                    ))}
                </div>
              )
            })}
          </div>
        )}
      </Card>

      {switchAsk && doc && (
        <FlySwitchModal
          route={switchAsk.route}
          after={switchAsk.after}
          title={switchAsk.title}
          base={doc.revision}
          onSave={(targets, note) => saveDoc(doc.routes.map((r) => (r.id === switchAsk.route.id ? { ...r, targets } : r)), note)}
          onSwitched={(id) => onOpenJob(id)}
          onClose={() => {
            setSwitchAsk(null)
            void fly.reload()
          }}
        />
      )}
      {targetMenu && (
        <Modal title={`${hostNames[String(targetMenu.target.host_id)] ?? ''} › ${targetMenu.target.ref || targetMenu.target.kind}:${targetMenu.target.port}`} onClose={() => setTargetMenu(null)} width={420}>
          <div className="col" style={{ gap: '0.4rem' }}>
            <Button
              onClick={() => {
                setPicker(targetMenu)
                setTargetMenu(null)
              }}
            >
              {t('fly.redirectTo')}
            </Button>
            <Button
              danger
              disabled={targetMenu.route.targets.length < 2}
              onClick={() => {
                const r = targetMenu.route
                setSwitchAsk({ route: r, after: r.targets.filter((x) => x.id !== targetMenu.target.id), title: t('fly.switchRemoveTitle', { route: routeTitle(r) }) })
                setTargetMenu(null)
              }}
            >
              {t('fly.removeFromPool')}
            </Button>
            <Button
              type="link"
              onClick={() => {
                setOpenRoute(targetMenu.route.id)
                setTargetMenu(null)
              }}
            >
              {t('fly.details')}
            </Button>
          </div>
        </Modal>
      )}
      {picker && (
        <Modal title={t('fly.redirectTo')} onClose={() => setPicker(null)} width={480}>
          <FlyPalette
            hosts={Object.entries(hostNames).map(([id, name]) => ({ id: Number(id), name }))}
            onPick={(it) => {
              const r = picker.route
              setSwitchAsk({ route: r, after: r.targets.map((x) => (x.id === picker.target.id ? newTargetFrom(r, it, x) : x)), title: t('fly.switchReplaceTitle', { route: routeTitle(r) }) })
              setPicker(null)
            }}
          />
        </Modal>
      )}
      {openRoute && doc && (
        <RouteDetails
          route={doc.routes.find((r) => r.id === openRoute)!}
          stats={routeStats.get(openRoute)}
          certs={status.data?.gateways.find((g) => g.edge_id === doc.routes.find((r) => r.id === openRoute)?.gateway)?.status?.certs}
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

/** Что можно бросить на карту: контейнер, машина или LXD на хосте. */
interface DropItem {
  host_id: number
  kind: TargetKind
  ref: string
  ports?: number[]
}

const DND_TYPE = 'application/x-nkt-fly-target'

/** Цель из брошенного контейнера: заменяя — порт прежней цели, добавляя
 * — порт, который объявляет контейнер, или порт первой цели; у диапазона
 * — порт маршрута. Идентификатор — новый, не занятый. */
function newTargetFrom(r: FlyRoute, it: DropItem, replace: FlyTarget | undefined): FlyTarget {
  const used = new Set(r.targets.map((x) => x.id))
  let id = `c${it.host_id}`
  for (let n = 2; used.has(id); n++) id = `c${it.host_id}-${n}`
  const port = r.port_to && r.port_to > r.port ? r.port : replace?.port || it.ports?.[0] || r.targets[0]?.port || 0
  return { id, host_id: it.host_id, kind: it.kind, ref: it.ref, port, weight: replace?.weight }
}

/** Сократить по середине: видно и начало, и конец. */
function midEllipsis(s: string, max: number): string {
  if (s.length <= max) return s
  const head = Math.ceil((max - 1) / 2)
  const tail = Math.floor((max - 1) / 2)
  return s.slice(0, head) + '…' + s.slice(s.length - tail)
}

/** Карта: шлюзы → маршруты → цели (хост — перед именем цели). Цвет —
 * состояние, толщина связи — соединения маршрута. В режиме правки на
 * маршрут и на цель можно бросить контейнер из панели: на маршрут —
 * добавить в пул, на цель — заменить её. */
function FlyMap({
  doc,
  gateways,
  statuses,
  tunnels,
  hostNames,
  onRoute,
  editing,
  onDropRoute,
  onDropTarget,
  onTarget,
}: {
  doc: FlyDoc
  gateways: FlyGatewayInfo[]
  statuses: GatewayStatus[]
  tunnels: FlyTunnel[]
  hostNames: Record<string, string>
  onRoute: (id: string) => void
  editing?: boolean
  onDropRoute?: (route: FlyRoute, item: DropItem) => void
  onDropTarget?: (route: FlyRoute, target: FlyTarget, item: DropItem) => void
  onTarget?: (route: FlyRoute, target: FlyTarget) => void
}) {
  const { t } = useTranslation()
  const [over, setOver] = useState<string | null>(null)
  const ROW = 46
  // Столбцы равной ширины: цели не растягиваются, длинные имена
  // сокращаются по середине.
  const W = 900
  const X = [10, 315, 620]
  const BW = [270, 270, 270]
  const stats = new Map<string, RouteStats>()
  for (const g of statuses) for (const r of g.status?.routes ?? []) stats.set(r.id, r)
  const gwIDs = doc.gateways.map((g) => g.edge_id)
  const targets: { route: FlyRoute; t: FlyTarget }[] = []
  for (const r of doc.routes) for (const tg of r.targets) targets.push({ route: r, t: tg })
  const rows = Math.max(gwIDs.length, doc.routes.length, targets.length, 1)
  const H = rows * ROW + 10
  const y = (i: number, n: number) => 5 + ((rows - n) * ROW) / 2 + i * ROW + ROW / 2
  const readItem = (e: React.DragEvent): DropItem | null => {
    try {
      return JSON.parse(e.dataTransfer.getData(DND_TYPE)) as DropItem
    } catch {
      return null
    }
  }
  const dropProps = (key: string, onDrop?: (item: DropItem) => void) =>
    editing && onDrop
      ? {
          onDragOver: (e: React.DragEvent) => {
            e.preventDefault()
            e.dataTransfer.dropEffect = 'copy'
            setOver(key)
          },
          onDragLeave: () => setOver((o) => (o === key ? null : o)),
          onDrop: (e: React.DragEvent) => {
            e.preventDefault()
            setOver(null)
            const it = readItem(e)
            if (it) onDrop(it)
          },
        }
      : {}

  const node = (
    col: number,
    i: number,
    n: number,
    label: string,
    sub: string,
    color: string,
    key: string,
    opts: { onClick?: () => void; full?: string; drop?: (item: DropItem) => void } = {},
  ) => {
    const cy = y(i, n)
    // Сколько знаков помещается: ~7 px на знак при 12 px.
    const max = Math.floor((BW[col] - 34) / 7)
    const hot = over === key
    return (
      <g
        key={key}
        transform={`translate(${X[col]},${cy - 17})`}
        style={{ cursor: opts.onClick ? 'pointer' : 'default' }}
        onClick={opts.onClick}
        {...dropProps(key, opts.drop)}
      >
        <title>{opts.full ?? label}</title>
        <rect width={BW[col]} height={34} rx={7} fill={hot ? 'var(--surface-hover, #e8f0fe)' : 'var(--surface-raised)'} stroke={hot ? 'var(--accent, #1677ff)' : color} strokeWidth={hot ? 3 : 2} strokeDasharray={editing && opts.drop ? '6 3' : undefined} />
        <circle cx={12} cy={17} r={5} fill={color} />
        <text x={24} y={15} fontSize={12} fill="var(--text-primary)">
          {midEllipsis(label, max)}
        </text>
        <text x={24} y={28} fontSize={10} fill="var(--text-secondary)">
          {midEllipsis(sub, Math.floor((BW[col] - 34) / 6))}
        </text>
      </g>
    )
  }
  const link = (x1: number, y1: number, x2: number, y2: number, color: string, w: number, key: string) => (
    <path key={key} d={`M${x1},${y1} C${(x1 + x2) / 2},${y1} ${(x1 + x2) / 2},${y2} ${x2},${y2}`} fill="none" stroke={color} strokeWidth={w} opacity={0.75} />
  )
  const tunnelOf = (r: FlyRoute, host: number) => tunnels.find((x) => x.edge_id === r.gateway && x.host_id === host)
  const targetColor = (r: FlyRoute, tg: FlyTarget) => {
    const ts = stats.get(r.id)?.targets?.find((s) => s.id === tg.id)
    const tn = tunnelOf(r, tg.host_id)
    if (tn && tn.state !== 'up' && tn.state !== 'not_applied' && tn.state !== 'unknown') return 'var(--status-critical)'
    if (tg.drain) return 'var(--status-warning)'
    return !ts || !ts.checked ? 'var(--text-muted)' : ts.alive ? 'var(--status-good)' : 'var(--status-critical)'
  }
  const lines: JSX.Element[] = []
  doc.routes.forEach((r, ri) => {
    const gi = gwIDs.indexOf(r.gateway)
    const st = stats.get(r.id)
    const ps = poolState(r, st)
    const w = Math.min(10, 1.5 + Math.log2(1 + (st?.active ?? 0)))
    if (gi >= 0) lines.push(link(X[0] + BW[0], y(gi, gwIDs.length), X[1], y(ri, doc.routes.length), ps.color, w, `g-r-${r.id}`))
  })
  targets.forEach((x, ti) => {
    const ri = doc.routes.indexOf(x.route)
    lines.push(link(X[1] + BW[1], y(ri, doc.routes.length), X[2], y(ti, targets.length), targetColor(x.route, x.t), 1.5, `r-t-${x.route.id}-${x.t.id}`))
  })
  return (
    <div style={{ overflowX: 'auto' }}>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', minWidth: 640, maxWidth: 1100, height: 'auto' }} role="img" aria-label={t('fly.map')}>
        {lines}
        {gwIDs.map((id, i) => {
          const g = gateways.find((x) => x.edge_id === id)
          const st = statuses.find((s) => s.edge_id === id)
          const wg = `WireGuard ${doc.gateways[i].listen_port}/udp`
          return node(0, i, gwIDs.length, g?.name ?? `#${id}`, g && g.address !== g.name ? `${g.address} · ${wg}` : wg, st?.connected ? 'var(--status-good)' : 'var(--status-critical)', `gw-${id}`)
        })}
        {doc.routes.map((r, i) => {
          const st = stats.get(r.id)
          const ps = poolState(r, st)
          return node(1, i, doc.routes.length, routeTitle(r), `${poolText(t, ps)} · ${st?.active ?? 0} ${t('fly.connsShort')}`, ps.color, `r-${r.id}`, {
            onClick: () => onRoute(r.id),
            drop: onDropRoute ? (it) => onDropRoute(r, it) : undefined,
          })
        })}
        {targets.map((x, i) => {
          const ts = stats.get(x.route.id)?.targets?.find((s) => s.id === x.t.id)
          const tn = tunnelOf(x.route, x.t.host_id)
          const host = hostNames[String(x.t.host_id)] ?? `#${x.t.host_id}`
          const name = x.t.label || (x.t.kind === 'host' ? t('fly.targetKind.host') : x.t.ref || '')
          const full = `${host} › ${name}:${x.t.port}`
          const bad = tn && tn.state !== 'up' ? t(`fly.tunnelShort.${tn.state}`) : ''
          const sub = `${t(`fly.targetKind.${x.t.kind}`)}${x.t.drain ? ` · ${t('fly.drainShort')}` : ''}${ts ? ` · ${ts.active} ${t('fly.connsShort')}` : ''}${bad ? ` · ${bad}` : ''}`
          return node(2, i, targets.length, full, sub, targetColor(x.route, x.t), `t-${x.route.id}-${x.t.id}`, {
            full,
            onClick: () => (editing && onTarget ? onTarget(x.route, x.t) : onRoute(x.route.id)),
            drop: onDropTarget ? (it) => onDropTarget(x.route, x.t, it) : undefined,
          })
        })}
      </svg>
    </div>
  )
}

/** Панель «Контейнеры и машины» режима перенаправления: по хостам, с
 * поиском; элементы перетаскиваются на карту или выбираются щелчком. */
function FlyPalette({ hosts, onPick }: { hosts: { id: number; name: string }[]; onPick?: (item: DropItem) => void }) {
  const { t } = useTranslation()
  const [kind, setKind] = useState<TargetKind>('container')
  const [q, setQ] = useState('')
  const [open, setOpen] = useState<number | null>(hosts[0]?.id ?? null)
  return (
    <div className="col" style={{ gap: '0.4rem', minWidth: 0 }}>
      <Space.Compact size="small">
        {(['container', 'vm', 'lxd'] as TargetKind[]).map((k) => (
          <Button key={k} size="small" type={kind === k ? 'primary' : 'default'} onClick={() => setKind(k)}>
            {t(`fly.targetKind.${k}`)}
          </Button>
        ))}
      </Space.Compact>
      <Input size="small" allowClear placeholder={t('fly.paletteSearch')} value={q} onChange={(e) => setQ(e.target.value)} />
      <div style={{ maxHeight: 460, overflowY: 'auto', paddingRight: 4 }}>
        {hosts.map((h) => (
          <div key={h.id}>
            <Button type="text" size="small" style={{ padding: 0, fontWeight: 600 }} onClick={() => setOpen(open === h.id ? null : h.id)}>
              {open === h.id ? '▾' : '▸'} {h.name}
            </Button>
            {open === h.id && <PaletteHost hostId={h.id} kind={kind} q={q} onPick={onPick} />}
          </div>
        ))}
      </div>
      {!onPick && <span className="small muted">{t('fly.paletteHint')}</span>}
    </div>
  )
}

function PaletteHost({ hostId, kind, q, onPick }: { hostId: number; kind: TargetKind; q: string; onPick?: (item: DropItem) => void }) {
  const { t } = useTranslation()
  const list = useTargets(hostId, kind)
  const items = list.items.filter((x) => x.kind === kind && (!q || x.name.toLowerCase().includes(q.toLowerCase())))
  if (list.loading) return <div className="small muted" style={{ paddingLeft: '1rem' }}>{t('fly.loadingTargets')}</div>
  if (items.length === 0) return <div className="small muted" style={{ paddingLeft: '1rem' }}>{t('fly.noTargets')}</div>
  return (
    <div className="col" style={{ gap: 6, padding: '4px 0 8px' }}>
      {items.map((x) => {
        const item: DropItem = { host_id: hostId, kind, ref: x.name, ports: x.ports }
        return (
          // Карточка — как блок карты: рамка и точка по состоянию, имя,
          // ниже — адрес, порты и состояние.
          <div
            key={x.name}
            draggable={!onPick}
            onDragStart={(e) => {
              e.dataTransfer.setData(DND_TYPE, JSON.stringify(item))
              e.dataTransfer.effectAllowed = 'copy'
            }}
            onClick={onPick ? () => onPick(item) : undefined}
            title={`${x.name}${x.ip ? ` · ${x.ip}` : ''}${x.ports?.length ? ` · ${x.ports.join(', ')}` : ''}${x.state ? ` · ${x.state}` : ''}`}
            style={{
              cursor: onPick ? 'pointer' : 'grab',
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              height: 34,
              padding: '0 10px',
              borderRadius: 7,
              border: `2px solid ${x.state === 'running' ? 'var(--status-good)' : 'var(--text-muted)'}`,
              background: 'var(--surface-raised)',
              userSelect: 'none',
            }}
          >
            <span style={{ width: 10, height: 10, borderRadius: '50%', flex: 'none', background: x.state === 'running' ? 'var(--status-good)' : 'var(--text-muted)' }} />
            <span style={{ minWidth: 0, lineHeight: 1.15 }}>
              <span style={{ display: 'block', fontSize: 12, color: 'var(--text-primary)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{midEllipsis(x.name, 34)}</span>
              <span style={{ display: 'block', fontSize: 10, color: 'var(--text-secondary)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {[x.ip, x.ports?.length ? x.ports.join(', ') : '', x.state && x.state !== 'running' ? x.state : ''].filter(Boolean).join(' · ') || t(`fly.targetKind.${kind}`)}
              </span>
            </span>
          </div>
        )
      })}
    </div>
  )
}

/** Переключение целей маршрута: что изменится (дифф), порт новой цели,
 * плавно (по умолчанию) или сразу; сохранить или сохранить и переключить. */
function FlySwitchModal({
  route,
  after,
  title,
  base,
  onSave,
  onSwitched,
  onClose,
}: {
  route: FlyRoute
  after: FlyTarget[]
  title: string
  base: number
  onSave: (targets: FlyTarget[], note: string) => Promise<void>
  onSwitched: (jobId: number) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const isRange = !!route.port_to && route.port_to > route.port
  const [targets, setTargets] = useState(after)
  const [smooth, setSmooth] = useState(!isRange)
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const added = targets.filter((x) => !route.targets.some((o) => o.id === x.id && o.ref === x.ref && o.host_id === x.host_id))
  const diff = unifiedDiff(routeText(route), routeText({ ...route, targets }), t('tokens.saved'), t('tokens.draft'))
  const run = async (doSwitch: boolean) => {
    setBusy(true)
    setError(null)
    try {
      if (doSwitch) {
        const res = await api<{ job_id: number }>(`/hub/fly/routes/${route.id}/switch`, { method: 'POST', body: { targets, smooth, note, base } })
        onSwitched(res.job_id)
      } else {
        await onSave(targets, note)
      }
      onClose()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={title} onClose={onClose} width={860}>
      <div className="col" style={{ gap: '0.5rem' }}>
        {added.map((x) => (
          <label key={x.id} className="small">
            {t('fly.switchPort', { name: x.ref || x.kind })}
            <InputNumber
              min={1}
              max={65535}
              value={x.port || null}
              disabled={isRange}
              onChange={(v) => setTargets(targets.map((y) => (y.id === x.id ? { ...y, port: v ?? 0 } : y)))}
            />
          </label>
        ))}
        <Radio.Group value={smooth ? 'smooth' : 'now'} onChange={(e) => setSmooth(e.target.value === 'smooth')}>
          <Radio value="smooth" disabled={isRange}>
            {t('fly.switchSmooth')}
          </Radio>
          <Radio value="now">{t('fly.switchNow')}</Radio>
        </Radio.Group>
        <span className="small muted">{t(isRange ? 'fly.switchRangeHint' : smooth ? 'fly.switchSmoothHint' : 'fly.switchNowHint')}</span>
        <DiffView text={diff} />
        <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('fly.notePlaceholder')} />
        {error && <Banner kind="error">{error}</Banner>}
        <Space wrap>
          <Button type="primary" loading={busy} disabled={targets.some((x) => !x.port)} onClick={() => void run(true)}>
            {t('fly.switchApply')}
          </Button>
          <Button loading={busy} disabled={targets.some((x) => !x.port)} onClick={() => void run(false)}>
            {t('fly.switchSaveOnly')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </Space>
      </div>
    </Modal>
  )
}

/** Подробности маршрута: цели и их проверки, клиенты (IP — администратору
 * и только пока их немного; иначе — число). */
function RouteDetails({
  route,
  stats,
  certs,
  hostNames,
  admin,
  onClose,
}: {
  route: FlyRoute
  stats?: RouteStats
  certs?: CertStatus[]
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
          {route.tls && <Tag color="green">{t('fly.tlsTag')}</Tag>}
          {(route.allow ?? []).length > 0 && (
            <Tooltip title={(route.allow ?? []).join(', ')}>
              <Tag color="purple">{t('fly.allowTag', { n: route.allow?.length })}</Tag>
            </Tooltip>
          )}
          {route.max_conns ? <Tag>{t('fly.maxConnsTag', { n: route.max_conns })}</Tag> : null}
          {route.rate ? <Tag>{t('fly.rateTag', { n: route.rate })}</Tag> : null}
          {stats?.denied ? <span className="small muted">{t('fly.deniedN', { n: stats.denied })}</span> : null}
          {stats && (
            <span className="small">
              {t('fly.statsLine', { active: stats.active, total: stats.total, failed: stats.failed, bytes: formatBytes(stats.bytes_in + stats.bytes_out) })}
            </span>
          )}
        </Space>
        {route.tls && (
          <div className="small">
            {route.names?.map((n) => {
              const c = certs?.find((x) => x.name === n)
              return (
                <div key={n}>
                  <span className="mono">{n}</span>
                  {': '}
                  {!c || c.pending ? (
                    <span className="muted">{t('fly.certPending')}</span>
                  ) : c.error ? (
                    <span style={{ color: 'var(--status-critical)' }}>{t('fly.certError', { error: c.error })}</span>
                  ) : (
                    <span style={{ color: 'var(--status-good)' }}>{t('fly.certUntil', { date: new Date((c.not_after ?? 0) * 1000).toLocaleDateString() })}</span>
                  )}
                </div>
              )
            })}
          </div>
        )}
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
  const valid = /^[a-z0-9][a-z0-9-]{0,40}$/.test(r.id) && !idTaken && (r.allow ?? []).every((a) => allowRe.test(a)) && !(r.tls && (r.names ?? []).some((n) => n.startsWith('*.'))) && r.port > 0 && r.targets.length > 0 && (!isNamed || (r.names ?? []).length > 0) && r.targets.every((x) => x.port > 0 && x.host_id !== undefined && (x.kind === 'host' || !!x.ref))
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
                  tls: k === 'https' ? r.tls : false,
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
            <InputNumber
              min={1}
              max={65535}
              value={r.port || null}
              onChange={(v) => set({ port: v ?? 0, targets: r.port_to ? r.targets.map((x) => ({ ...x, port: v ?? 0 })) : r.targets })}
            />
          </label>
          {!isNamed && (
            <Tooltip title={t('fly.portToHint')}>
              <label>
                {t('fly.fieldPortTo')}
                <InputNumber
                  min={1}
                  max={65535}
                  value={r.port_to || null}
                  placeholder="—"
                  onChange={(v) =>
                    set({
                      port_to: v && v > r.port ? v : undefined,
                      // Диапазон сохраняет порты: у целей — тот же порт.
                      targets: v && v > r.port ? r.targets.map((x) => ({ ...x, port: r.port })) : r.targets,
                    })
                  }
                />
              </label>
            </Tooltip>
          )}
        </div>
        {!isNamed && r.port_to && r.port_to > r.port ? <span className="small muted">{t('fly.rangeHint')}</span> : null}
        {isNamed && (
          <>
            <label>
              {t('fly.fieldNames')}
              <Select mode="tags" value={r.names} onChange={(v: string[]) => set({ names: v.map((x) => x.trim().toLowerCase()).filter(Boolean) })} tokenSeparators={[',', ' ']} placeholder="app.example.com, *.app.example.com" />
            </label>
            {r.kind === 'https' && (
              <>
                <Checkbox checked={!!r.tls} onChange={(e) => set({ tls: e.target.checked, proxy_protocol: e.target.checked ? false : r.proxy_protocol })}>
                  {t('fly.tlsGateway')}
                </Checkbox>
                <span className="small muted">{t(r.tls ? 'fly.tlsGatewayHint' : 'fly.tlsPassHint')}</span>
                {r.tls && (r.names ?? []).some((n) => n.startsWith('*.')) && <Banner kind="warn">{t('fly.tlsNoWildcard')}</Banner>}
              </>
            )}
            {!r.tls && (
              <Checkbox checked={r.proxy_protocol} onChange={(e) => set({ proxy_protocol: e.target.checked })}>
                {t('fly.proxyProtocol')}
              </Checkbox>
            )}
          </>
        )}
        {!isNamed && <span className="small muted">{t('fly.portHint')}</span>}
        <label>
          {t('fly.fieldAllow')}
          <Select
            mode="tags"
            value={r.allow ?? []}
            onChange={(v: string[]) => set({ allow: v.map((x) => x.trim()).filter(Boolean) })}
            tokenSeparators={[',', ' ', ';']}
            placeholder="203.0.113.7, 198.51.100.0/24"
            status={(r.allow ?? []).some((a) => !allowRe.test(a)) ? 'error' : undefined}
          />
          <span className="small muted">{t('fly.allowHint')}</span>
        </label>
        <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap' }}>
          <Tooltip title={t('fly.limitsHint')}>
            <label>
              {t('fly.fieldMaxConns')}
              <InputNumber min={1} max={100000} value={r.max_conns || null} placeholder="—" onChange={(v) => set({ max_conns: v || undefined })} />
            </label>
          </Tooltip>
          <Tooltip title={t('fly.limitsHint')}>
            <label>
              {t('fly.fieldRate')}
              <InputNumber min={1} max={100000} value={r.rate || null} placeholder="—" onChange={(v) => set({ rate: v || undefined })} />
            </label>
          </Tooltip>
        </div>
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

type TargetItem = { kind: string; name: string; ip?: string; state?: string; ports?: number[] }

// Списки целей хоста по виду — одна загрузка на хост и вид для всех строк
// редактора; хаб сам держит их 30 с, здесь — столько же.
const targetLists = new Map<string, { at: number; p: Promise<TargetItem[]> }>()

function loadTargets(hostId: number, kind: TargetKind, refresh = false): Promise<TargetItem[]> {
  const key = `${hostId}/${kind}`
  const hit = targetLists.get(key)
  if (hit && !refresh && Date.now() - hit.at < 30_000) return hit.p
  const p = api<{ targets: TargetItem[] }>(`/hub/fly/targets?host_id=${hostId}&kind=${kind}${refresh ? '&refresh=1' : ''}`).then((r) => r.targets)
  p.catch(() => targetLists.delete(key))
  targetLists.set(key, { at: Date.now(), p })
  return p
}

function useTargets(hostId: number, kind: TargetKind) {
  const listable = kind === 'container' || kind === 'vm' || kind === 'lxd'
  const [items, setItems] = useState<TargetItem[]>([])
  const [loading, setLoading] = useState(false)
  const [tick, setTick] = useState(0)
  useEffect(() => {
    if (!listable || hostId < -1) return
    let alive = true
    setLoading(true)
    loadTargets(hostId, kind, tick > 0)
      .then((v) => alive && setItems(v))
      .catch(() => alive && setItems([]))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [hostId, kind, listable, tick])
  return { items, loading, refresh: () => setTick((n) => n + 1) }
}

/** Цель пула: хост, что на нём (выбор из контейнеров, машин, LXD), порт. */
function TargetRow({ target, hosts, onChange, onRemove }: { target: FlyTarget; hosts: HubHost[]; onChange: (t: FlyTarget) => void; onRemove: () => void }) {
  const { t } = useTranslation()
  const list = useTargets(target.host_id, target.kind)
  const items = list.items.filter((x) => x.kind === target.kind)
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
          placeholder={list.loading ? t('fly.loadingTargets') : t('fly.pickRef')}
          notFoundContent={list.loading ? t('fly.loadingTargets') : t('fly.noTargets')}
          style={{ width: 240 }}
        />
      ) : null}
      {target.kind !== 'host' && target.kind !== 'addr' && (
        <Tooltip title={t('fly.refreshTargets')}>
          <Button size="small" loading={list.loading} onClick={list.refresh} aria-label={t('fly.refreshTargets')}>
            ↻
          </Button>
        </Tooltip>
      )}
      <InputNumber min={1} max={65535} value={target.port || null} onChange={(v) => onChange({ ...target, port: v ?? 0 })} placeholder={t('fly.fieldPort')} />
      <Tooltip title={t('fly.weightHint')}>
        <InputNumber min={1} max={100} value={target.weight || 1} onChange={(v) => onChange({ ...target, weight: v ?? 1 })} addonBefore={t('fly.colWeight')} style={{ width: 130 }} />
      </Tooltip>
      <Tooltip title={t('fly.drainHint')}>
        <Checkbox checked={!!target.drain} onChange={(e) => onChange({ ...target, drain: e.target.checked })}>
          {t('fly.drainLabel')}
        </Checkbox>
      </Tooltip>
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
