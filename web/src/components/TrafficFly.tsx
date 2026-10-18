import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select, Space, Switch, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { DeleteOutlined } from '@ant-design/icons'
import { api, useApi } from '../api'
import { Banner, Card, DiffView, Loading, Modal } from './ui'
import { DataTable } from './DataTable'
import { HelpButton } from './Docs'
import { formatBytes } from './charts'
import { unifiedDiff } from './textDiff'
import { InstallModal } from './EdgeCard'
import { osLabel } from './OsIcon'
import { confirmAction } from './confirm'
import { Sensitive, blurText, usePrivacy } from '../privacy'
import { activeCount, applyOps, draftMarks, opBlocked, opEta, opKeys, pushOp, type FlyOp, type OpMark } from './flyOps'
import type { HubHost, Job, Me } from '../types'

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
  // Правка карты: черновик операций (одна версия и одно задание по
  // «Применить изменения») или «применять сразу» (задание на действие).
  // Задания в работе — спиннером с отсчётом на затронутых блоках.
  const [mapEdit, setMapEdit] = useState(false)
  const [applyNow, setApplyNow] = useState(false)
  const [draft, setDraft] = useState<FlyOp[]>([])
  const [running, setRunning] = useState<RunningJob[]>([])
  const [failed, setFailed] = useState<number[]>([])
  const [now, setNow] = useState(() => Date.now())
  const [targetMenu, setTargetMenu] = useState<{ route: FlyRoute; target: FlyTarget } | null>(null)
  const [picker, setPicker] = useState<FlyRoute | null>(null)
  const [portAsk, setPortAsk] = useState<PortAsk | null>(null)
  const [moveAsk, setMoveAsk] = useState<{ route: FlyRoute; target: FlyTarget } | null>(null)
  const [draftAsk, setDraftAsk] = useState(false)
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

  const alive = (route: string, target: string) => {
    const ts = routeStats.get(route)?.targets?.find((x) => x.id === target)
    return !!ts && ts.checked && ts.alive
  }
  // Карта показывает, что будет: сохранённое, задания в очереди, черновик.
  const queued = useMemo(() => running.flatMap((j) => j.ops), [running])
  const base = useMemo(() => (doc ? applyOps(doc.routes, queued) : []), [doc, queued])
  const preview = useMemo(() => applyOps(base, draft), [base, draft])
  const changes = useMemo(() => draftMarks(base, preview), [base, preview])
  const busyKeys = useMemo(() => {
    const m = new Map<string, number>()
    for (const j of running) {
      const left = Math.max(0, Math.round(j.eta - (now - j.started) / 1000))
      for (const k of j.ops.flatMap(opKeys)) m.set(k, Math.max(left, m.get(k) ?? 0))
    }
    return m
  }, [running, now])

  // Задания с карты: раз в секунду — отсчёт, раз в две — состояние;
  // закончилось — документ перечитывается, неудачное — в баннер.
  const runningRef = useRef(running)
  runningRef.current = running
  const hasRunning = running.length > 0
  useEffect(() => {
    if (!hasRunning) return
    let tick = 0
    const iv = setInterval(() => {
      setNow(Date.now())
      if (tick++ % 2) return
      void (async () => {
        const done: RunningJob[] = []
        for (const j of runningRef.current) {
          const job = await api<Job>(`/hosts/local/jobs/${j.id}`).catch(() => null)
          if (job && job.status !== 'queued' && job.status !== 'running') done.push({ ...j, status: job.status })
        }
        if (done.length === 0) return
        await Promise.all([fly.reload(), status.reload()])
        setRunning((rs) => rs.filter((r) => !done.some((d) => d.id === r.id)))
        const bad = done.filter((d) => d.status !== 'succeeded').map((d) => d.id)
        if (bad.length) setFailed((f) => [...f, ...bad])
      })()
    }, 1000)
    return () => clearInterval(iv)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasRunning])

  async function startOps(ops: FlyOp[], note: string) {
    const res = await api<{ job_id: number }>('/hub/fly/ops', { method: 'POST', body: { ops, note } })
    setRunning((rs) => [...rs, { id: res.job_id, ops, started: Date.now(), eta: opEta(ops), status: 'queued' }])
    setNow(Date.now())
  }

  // Колесо в режиме «применять сразу»: шаги веса копятся в черновике и
  // уходят одним заданием, когда колесо остановилось.
  const wheelTimer = useRef<number | undefined>(undefined)
  const draftRef = useRef(draft)
  draftRef.current = draft

  /** Действие на карте: в черновик или сразу заданием. Нельзя — объяснение. */
  async function dispatch(op: FlyOp, opts: { wheel?: boolean } = {}) {
    const why = opBlocked(preview, op, alive)
    if (why) {
      setError(t(why))
      return
    }
    setError(null)
    if (!applyNow || opts.wheel) {
      setDraft((d) => pushOp(d, op))
      if (applyNow) {
        window.clearTimeout(wheelTimer.current)
        wheelTimer.current = window.setTimeout(() => {
          const ops = draftRef.current
          setDraft([])
          if (ops.length) startOps(ops, '').catch((err) => setError(errText(err)))
        }, 900)
      }
      return
    }
    try {
      await startOps([op], '')
    } catch (err) {
      setError(errText(err))
    }
  }

  /** Контейнер из панели — в маршрут резервом; порт — объявленный
   * контейнером или первой цели маршрута, неизвестен — спросить. */
  function addItem(r: FlyRoute, it: DropItem) {
    const range = !!r.port_to && r.port_to > r.port
    const port = range ? r.port : it.ports?.[0] || r.targets[0]?.port || 0
    const op: FlyOp = { op: 'add', route: r.id, host_id: it.host_id, kind: it.kind, ref: it.ref, port }
    if (port) void dispatch(op)
    else setPortAsk({ title: t('fly.portAskTitle', { name: it.ref }), port: 0, onOk: (p) => void dispatch({ ...op, port: p }) })
  }

  const targetName = (tg: FlyTarget) => `${hostNames[String(tg.host_id)] ?? `#${tg.host_id}`} › ${tg.label || tg.ref || t(`fly.targetKind.${tg.kind}`)}:${tg.port}`

  async function switchHere(r: FlyRoute, tg: FlyTarget) {
    const from = r.targets.filter((x) => !x.drain && x.id !== tg.id).map(targetName).join(', ') || '—'
    const ok = await confirmAction(t('fly.switchConfirm', { from, to: targetName(tg) }), { title: t('fly.switchConfirmTitle'), okText: t('fly.switchOk'), danger: false })
    if (ok) void dispatch({ op: 'switch', route: r.id, target: tg.id })
  }

  /** Сбросить черновик — с вопросом, если в нём что-то есть. */
  async function discardDraft(): Promise<boolean> {
    if (draft.length === 0) return true
    const ok = await confirmAction(t('fly.draftDiscardText', { n: draft.length }), {
      title: t('fly.draftDiscardTitle'),
      okText: t('fly.draftDiscardOk'),
      cancelText: t('fly.draftKeep'),
    })
    if (ok) setDraft([])
    return ok
  }

  async function leaveMapEdit() {
    if (await discardDraft()) setMapEdit(false)
  }

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
              <Tooltip title={t('fly.mapEditHint')}>
                <Button size="small" type={mapEdit ? 'primary' : 'default'} onClick={() => (mapEdit ? void leaveMapEdit() : setMapEdit(true))}>
                  {t('fly.mapEdit')}
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
          <div className="col" style={{ gap: '0.5rem' }}>
            {(mapEdit || running.length > 0 || failed.length > 0) && (
              <div className="row" style={{ gap: '0.6rem', alignItems: 'center', flexWrap: 'wrap' }}>
                {mapEdit && (
                  <Tooltip title={draft.length ? t('fly.applyNowDraft') : t('fly.applyNowHint')}>
                    <span className="row small" style={{ gap: '0.3rem', alignItems: 'center' }}>
                      <Switch size="small" checked={applyNow} disabled={draft.length > 0} onChange={setApplyNow} />
                      {t('fly.applyNow')}
                    </span>
                  </Tooltip>
                )}
                {draft.length > 0 && (
                  <>
                    <Button size="small" type="primary" onClick={() => setDraftAsk(true)}>
                      {t('fly.draftApply', { n: draft.length })}
                    </Button>
                    <Button size="small" onClick={() => setDraft(draft.slice(0, -1))}>
                      {t('fly.draftUndo')}
                    </Button>
                    <Button size="small" danger onClick={() => void discardDraft()}>
                      {t('fly.draftDiscard', { n: draft.length })}
                    </Button>
                  </>
                )}
                {running.map((j) => {
                  const left = Math.max(0, Math.round(j.eta - (now - j.started) / 1000))
                  return (
                    <Tag key={j.id} color="processing" style={{ cursor: 'pointer' }} onClick={() => onOpenJob(j.id)}>
                      ⟳ {t('fly.jobOps', { n: j.ops.length })} · {left > 0 ? `~${left} ${t('fly.secShort')}` : '…'}
                    </Tag>
                  )
                })}
                {failed.map((id) => (
                  <Tag key={id} color="error" closable onClose={() => setFailed((f) => f.filter((x) => x !== id))} style={{ cursor: 'pointer' }} onClick={() => onOpenJob(id)}>
                    {t('fly.jobFailed', { id })}
                  </Tag>
                ))}
              </div>
            )}
            <div className="row" style={{ gap: '0.8rem', alignItems: 'flex-start', flexWrap: 'wrap' }}>
              <div style={{ flex: '1 1 600px', minWidth: 0 }}>
                <FlyMap
                  doc={{ ...doc, routes: preview }}
                  gateways={gateways}
                  statuses={status.data?.gateways ?? []}
                  tunnels={status.data?.tunnels ?? []}
                  hostNames={hostNames}
                  onRoute={setOpenRoute}
                  editing={mapEdit}
                  marks={changes.marks}
                  ghosts={changes.removed}
                  busy={busyKeys}
                  onDropRoute={addItem}
                  onTarget={(r, tg) => setTargetMenu({ route: r, target: tg })}
                  onMoveDrop={(r, tg, dest) => {
                    if (dest === 'trash') void dispatch({ op: 'remove', route: r.id, target: tg.id })
                    else if (dest.startsWith('route:') && dest.slice(6) !== r.id) void dispatch({ op: 'move', route: r.id, target: tg.id, to: dest.slice(6) })
                  }}
                  onWeight={(r, tg, delta) => {
                    const w = Math.min(100, Math.max(1, (tg.weight ?? 1) + delta))
                    if (w !== (tg.weight ?? 1)) void dispatch({ op: 'edit', route: r.id, target: tg.id, weight: w }, { wheel: true })
                  }}
                />
              </div>
              {mapEdit && (
                <div style={{ flex: '0 0 280px', minWidth: 240 }}>
                  <strong className="small">{t('fly.paletteTitle')}</strong>
                  <FlyPalette hosts={Object.entries(hostNames).map(([id, name]) => ({ id: Number(id), name }))} />
                </div>
              )}
            </div>
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
                          <Sensitive>{n}</Sensitive>
                        </span>
                      ) : (
                        <a key={n} className="mono" href={siteURL(r, n)} target="_blank" rel="noopener noreferrer">
                          <Sensitive>{n}</Sensitive> ↗
                        </a>
                      ),
                    )
                  ) : (
                    <span className="mono">{blurText(routeTitle(r))}</span>
                  )}
                  <Button size="small" type="link" style={{ padding: 0 }} onClick={() => setOpenRoute(r.id)}>
                    {t('fly.details')}
                  </Button>
                </span>
              ),
            },
            { title: t('fly.colKind'), key: 'kind', render: (_, r) => <Tag>{t(`fly.kind.${r.kind}`)}</Tag> },
            { title: t('fly.colGateway'), key: 'gw', render: (_, r) => <Sensitive>{gateways.find((g) => g.edge_id === r.gateway)?.name ?? `#${r.gateway}`}</Sensitive> },
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
                    <strong className="mono">
                      <Sensitive>{g.name}</Sensitive>
                    </strong>
                    {g.connected ? <Tag color="success">{t('fly.gwConnected')}</Tag> : <Tag color="error">{t('fly.gwOffline')}</Tag>}
                    {st?.status && (
                      <Tooltip title={t('fly.gwVersionHint')}>
                        <Tag>{st.status.version ? `nkt-edge ${st.status.version}` : t('fly.gwVersionOld')}</Tag>
                      </Tooltip>
                    )}
                    {tunnels.length > 0 && <span className="small muted">{t('fly.tunnelsUp', { up, total: tunnels.length })}</span>}
                    {(st?.status?.errors ?? []).map((e) => (
                      <Tag key={e} color="error">
                        {blurText(e)}
                      </Tag>
                    ))}
                    {st?.error && <Tag color="error">{blurText(st.error)}</Tag>}
                  </div>
                  {/* Неживые туннели — с причиной: где теряются пакеты. */}
                  {tunnels
                    .filter((x) => x.state !== 'up')
                    .map((x) => (
                      <div key={x.host_id} className="small" style={{ paddingLeft: '1rem' }}>
                        <span className="mono">
                          <Sensitive>{hostNames[String(x.host_id)] ?? `#${x.host_id}`}</Sensitive>
                        </span>
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

      {targetMenu &&
        (() => {
          const { route: r, target: tg } = targetMenu
          const close = () => setTargetMenu(null)
          const lastActive = !tg.drain && activeCount(r) <= 1
          const canSwitch = !!tg.drain && alive(r.id, tg.id)
          return (
            <Modal title={targetName(tg)} onClose={close} width={420}>
              <div className="col" style={{ gap: '0.4rem' }}>
                <Tooltip title={canSwitch ? undefined : tg.drain ? t('fly.opNotAliveUI') : t('fly.switchAlready')}>
                  <Button
                    type="primary"
                    disabled={!canSwitch}
                    onClick={() => {
                      close()
                      void switchHere(r, tg)
                    }}
                  >
                    {t('fly.switchHere')}
                  </Button>
                </Tooltip>
                {tg.drain ? (
                  <Button
                    onClick={() => {
                      close()
                      void dispatch({ op: 'active', route: r.id, target: tg.id })
                    }}
                  >
                    {t('fly.toActive')}
                  </Button>
                ) : (
                  <Tooltip title={lastActive ? t('fly.opLastActiveUI') : undefined}>
                    <Button
                      disabled={lastActive}
                      onClick={() => {
                        close()
                        void dispatch({ op: 'reserve', route: r.id, target: tg.id })
                      }}
                    >
                      {t('fly.toReserve')}
                    </Button>
                  </Tooltip>
                )}
                <Button
                  onClick={() => {
                    close()
                    const range = !!r.port_to && r.port_to > r.port
                    setPortAsk({
                      title: t('fly.editTargetTitle', { name: targetName(tg) }),
                      port: tg.port,
                      portLocked: range,
                      weight: tg.weight ?? 1,
                      onOk: (port, weight) => void dispatch({ op: 'edit', route: r.id, target: tg.id, port: range ? undefined : port, weight }),
                    })
                  }}
                >
                  {t('fly.editTarget')}
                </Button>
                <Tooltip title={lastActive ? t('fly.opLastActiveUI') : undefined}>
                  <Button
                    disabled={lastActive || preview.length < 2}
                    onClick={() => {
                      close()
                      setMoveAsk(targetMenu)
                    }}
                  >
                    {t('fly.moveTo')}
                  </Button>
                </Tooltip>
                <Tooltip title={tg.drain ? undefined : t('fly.opRemoveActiveUI')}>
                  <Button
                    danger
                    disabled={!tg.drain}
                    onClick={() => {
                      close()
                      void dispatch({ op: 'remove', route: r.id, target: tg.id })
                    }}
                  >
                    {t('fly.removeFromPool')}
                  </Button>
                </Tooltip>
                <Button
                  onClick={() => {
                    close()
                    setPicker(r)
                  }}
                >
                  {t('fly.addReserve')}
                </Button>
                <Button
                  type="link"
                  onClick={() => {
                    close()
                    setOpenRoute(r.id)
                  }}
                >
                  {t('fly.details')}
                </Button>
              </div>
            </Modal>
          )
        })()}
      {picker && (
        <Modal title={t('fly.addReserveTitle', { route: routeTitle(picker) })} onClose={() => setPicker(null)} width={480}>
          <FlyPalette
            hosts={Object.entries(hostNames).map(([id, name]) => ({ id: Number(id), name }))}
            onPick={(it) => {
              setPicker(null)
              addItem(picker, it)
            }}
          />
        </Modal>
      )}
      {portAsk && <PortWeightModal ask={portAsk} onClose={() => setPortAsk(null)} />}
      {moveAsk && (
        <MoveModal
          title={t('fly.moveTitle', { name: targetName(moveAsk.target) })}
          routes={preview.filter((r) => r.id !== moveAsk.route.id)}
          onOk={(to) => void dispatch({ op: 'move', route: moveAsk.route.id, target: moveAsk.target.id, to })}
          onClose={() => setMoveAsk(null)}
        />
      )}
      {draftAsk && (
        <DiffConfirm
          title={t('fly.draftApplyTitle', { n: draft.length })}
          diff={unifiedDiff(
            base
              .filter((r) => preview.some((p) => p.id === r.id && JSON.stringify(p) !== JSON.stringify(r)))
              .map(routeText)
              .join(''),
            preview
              .filter((p) => base.some((r) => r.id === p.id && JSON.stringify(p) !== JSON.stringify(r)))
              .map(routeText)
              .join(''),
            t('tokens.saved'),
            t('tokens.draft'),
          )}
          okText={t('fly.draftApplyOk')}
          hint={t('fly.draftApplyHint')}
          run={async (note) => {
            await startOps(draft, note)
            setDraft([])
          }}
          onClose={() => setDraftAsk(false)}
        />
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

/** Задание с карты в работе: его операции видны на карте до конца. */
interface RunningJob {
  id: number
  ops: FlyOp[]
  started: number
  eta: number
  status: Job['status']
}

/** Спросить порт (и вес) цели. */
interface PortAsk {
  title: string
  port: number
  portLocked?: boolean
  weight?: number
  onOk: (port: number, weight?: number) => void
}

/** Сократить по середине: видно и начало, и конец. */
function midEllipsis(s: string, max: number): string {
  if (s.length <= max) return s
  const head = Math.ceil((max - 1) / 2)
  const tail = Math.floor((max - 1) / 2)
  return s.slice(0, head) + '…' + s.slice(s.length - tail)
}

/** Куда брошено при перетаскивании цели: «route:<id>» или «trash». */
function dropAt(x: number, y: number): string | null {
  return document.elementFromPoint(x, y)?.closest('[data-fly-drop]')?.getAttribute('data-fly-drop') ?? null
}

/** Карта: шлюзы → маршруты → цели (хост — перед именем цели). Цвет —
 * состояние, толщина связи — соединения маршрута, резерв — пунктиром.
 * В режиме правки: контейнер из панели — на маршрут или его цель (в пул
 * резервом); цель тащится мышью или пальцем на другой маршрут (перенести)
 * или в корзину (убрать); колесо над целью — вес; щелчок — меню. */
function FlyMap({
  doc,
  gateways,
  statuses,
  tunnels,
  hostNames,
  onRoute,
  editing,
  marks,
  ghosts,
  busy,
  onDropRoute,
  onTarget,
  onMoveDrop,
  onWeight,
}: {
  doc: FlyDoc
  gateways: FlyGatewayInfo[]
  statuses: GatewayStatus[]
  tunnels: FlyTunnel[]
  hostNames: Record<string, string>
  onRoute: (id: string) => void
  editing?: boolean
  marks?: Map<string, OpMark>
  ghosts?: { route: string; target: FlyTarget }[]
  busy?: Map<string, number>
  onDropRoute?: (route: FlyRoute, item: DropItem) => void
  onTarget?: (route: FlyRoute, target: FlyTarget) => void
  onMoveDrop?: (route: FlyRoute, target: FlyTarget, dest: string) => void
  onWeight?: (route: FlyRoute, target: FlyTarget, delta: number) => void
}) {
  const { t } = useTranslation()
  const [over, setOver] = useState<string | null>(null)
  const [drag, setDrag] = useState<{ x: number; y: number; label: string } | null>(null)
  const [privacy] = usePrivacy()
  const ROW = 46
  // Столбцы равной ширины: цели не растягиваются, длинные имена
  // сокращаются по середине.
  const W = 900
  const X = [10, 315, 620]
  const BW = [270, 270, 270]
  const stats = new Map<string, RouteStats>()
  for (const g of statuses) for (const r of g.status?.routes ?? []) stats.set(r.id, r)
  const gwIDs = doc.gateways.map((g) => g.edge_id)
  // Убранные в черновике — бледными на прежнем месте.
  const targets: { route: FlyRoute; t: FlyTarget; ghost?: boolean }[] = []
  for (const r of doc.routes) {
    for (const tg of r.targets) targets.push({ route: r, t: tg })
    for (const g of ghosts ?? []) if (g.route === r.id) targets.push({ route: r, t: g.target, ghost: true })
  }
  const rows = Math.max(gwIDs.length, doc.routes.length, targets.length, 1)
  const H = rows * ROW + 10
  const y = (i: number, n: number) => 5 + ((rows - n) * ROW) / 2 + i * ROW + ROW / 2

  // Колбэки — через ссылку: обработчики окна вешаются один раз.
  const cb = useRef({ onTarget, onMoveDrop, onWeight })
  cb.current = { onTarget, onMoveDrop, onWeight }
  const targetsRef = useRef(targets)
  targetsRef.current = targets
  const press = useRef<{ i: number; sx: number; sy: number; started: boolean; label: string } | null>(null)
  const svgRef = useRef<SVGSVGElement>(null)

  useEffect(() => {
    if (!editing) return
    const move = (e: PointerEvent) => {
      const p = press.current
      if (!p) return
      if (!p.started && Math.hypot(e.clientX - p.sx, e.clientY - p.sy) < 6) return
      p.started = true
      setDrag({ x: e.clientX, y: e.clientY, label: p.label })
      setOver(dropAt(e.clientX, e.clientY))
    }
    const up = (e: PointerEvent) => {
      const p = press.current
      press.current = null
      if (!p) return
      const x = targetsRef.current[p.i]
      setDrag(null)
      setOver(null)
      if (!x) return
      if (!p.started) {
        cb.current.onTarget?.(x.route, x.t)
        return
      }
      const dest = dropAt(e.clientX, e.clientY)
      if (dest) cb.current.onMoveDrop?.(x.route, x.t, dest)
    }
    const cancel = () => {
      press.current = null
      setDrag(null)
      setOver(null)
    }
    // Колесо над целью — вес ±1 (не чаще раза в 120 мс: тачпад шлёт
    // десятки событий на жест).
    let last = 0
    const wheel = (e: WheelEvent) => {
      const el = (e.target as Element | null)?.closest('[data-fly-target]')
      if (!el) return
      e.preventDefault()
      if (e.timeStamp - last < 120 || e.deltaY === 0) return
      last = e.timeStamp
      const x = targetsRef.current[Number(el.getAttribute('data-fly-target'))]
      if (x && !x.ghost) cb.current.onWeight?.(x.route, x.t, e.deltaY < 0 ? 1 : -1)
    }
    const svg = svgRef.current
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', cancel)
    svg?.addEventListener('wheel', wheel, { passive: false })
    return () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', cancel)
      svg?.removeEventListener('wheel', wheel)
    }
  }, [editing])

  const readItem = (e: React.DragEvent): DropItem | null => {
    try {
      return JSON.parse(e.dataTransfer.getData(DND_TYPE)) as DropItem
    } catch {
      return null
    }
  }
  // Контейнер из панели (HTML-перетаскивание) — на маршрут или его цель.
  const paletteDrop = (key: string, r: FlyRoute) =>
    editing && onDropRoute
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
            if (it) onDropRoute(r, it)
          },
        }
      : {}

  const spinner = (col: number, left: number) => (
    <g>
      <text x={BW[col] - 26} y={21} fontSize={10} textAnchor="end" fill="var(--text-secondary)">
        {left > 0 ? `${left} ${t('fly.secShort')}` : '…'}
      </text>
      <g transform={`translate(${BW[col] - 13},17)`}>
        <circle r={7} fill="none" stroke="var(--border, #ccc)" strokeWidth={2} />
        <path d="M0,-7 A7,7 0 0,1 7,0" fill="none" stroke="var(--accent, #1677ff)" strokeWidth={2}>
          <animateTransform attributeName="transform" type="rotate" from="0" to="360" dur="1s" repeatCount="indefinite" />
        </path>
      </g>
    </g>
  )

  const node = (
    col: number,
    i: number,
    n: number,
    label: string,
    sub: string,
    color: string,
    key: string,
    opts: {
      onClick?: () => void
      full?: string
      drop?: string
      route?: FlyRoute
      dashed?: boolean
      ghost?: boolean
      mark?: OpMark
      busy?: number
      index?: number
      /** Подпись (и вторая строка) — чувствительные: имена, адреса. */
      secret?: boolean
      secretSub?: boolean
    } = {},
  ) => {
    const cy = y(i, n)
    const right = opts.busy !== undefined ? 56 : opts.mark ? 70 : 0
    // Сколько знаков помещается: ~7 px на знак при 12 px.
    const max = Math.floor((BW[col] - 34 - right) / 7)
    const hot = !!opts.drop && over === opts.drop
    const draggable = editing && opts.index !== undefined && !opts.ghost && opts.busy === undefined
    return (
      <g
        key={key}
        transform={`translate(${X[col]},${cy - 17})`}
        style={{ cursor: draggable ? 'grab' : opts.onClick ? 'pointer' : 'default', opacity: opts.ghost ? 0.4 : 1 }}
        onClick={draggable ? undefined : opts.onClick}
        onPointerDown={
          draggable
            ? (e) => {
                if (e.button !== 0) return
                // Без этого браузер начинает выделять подписи, а потом сам
                // тащит выделенный текст и обрывает перетаскивание
                // (pointercancel). Захват — чтобы движения шли сюда, даже
                // когда указатель вышел за карту.
                e.preventDefault()
                try {
                  e.currentTarget.setPointerCapture(e.pointerId)
                } catch {
                  // нет захвата — хватит обработчиков окна
                }
                press.current = { i: opts.index!, sx: e.clientX, sy: e.clientY, started: false, label }
              }
            : undefined
        }
        data-fly-drop={opts.drop}
        data-fly-target={editing && opts.index !== undefined && !opts.ghost ? opts.index : undefined}
        {...(opts.route ? paletteDrop(opts.drop ?? key, opts.route) : {})}
      >
        {!privacy && <title>{opts.full ?? label}</title>}
        <rect
          width={BW[col]}
          height={34}
          rx={7}
          fill={hot ? 'var(--surface-hover, #e8f0fe)' : 'var(--surface-raised)'}
          stroke={hot || opts.mark ? 'var(--accent, #1677ff)' : color}
          strokeWidth={hot ? 3 : 2}
          strokeDasharray={opts.dashed ? '6 4' : undefined}
        />
        <circle cx={12} cy={17} r={5} fill={color} />
        <text x={24} y={15} fontSize={12} fill="var(--text-primary)" textDecoration={opts.ghost ? 'line-through' : undefined} className={opts.secret ? 'sensitive' : undefined}>
          {midEllipsis(label, max)}
        </text>
        <text x={24} y={28} fontSize={10} fill="var(--text-secondary)" className={opts.secretSub ? 'sensitive' : undefined}>
          {midEllipsis(sub, Math.floor((BW[col] - 34 - right) / 6))}
        </text>
        {opts.busy !== undefined
          ? spinner(col, opts.busy)
          : opts.mark && (
              <text x={BW[col] - 8} y={21} fontSize={10} textAnchor="end" fill="var(--accent, #1677ff)">
                {t(`fly.mark.${opts.mark}`)}
              </text>
            )}
      </g>
    )
  }
  const link = (x1: number, y1: number, x2: number, y2: number, color: string, w: number, key: string, dashed = false) => (
    <path
      key={key}
      d={`M${x1},${y1} C${(x1 + x2) / 2},${y1} ${(x1 + x2) / 2},${y2} ${x2},${y2}`}
      fill="none"
      stroke={color}
      strokeWidth={w}
      opacity={dashed ? 0.35 : 0.75}
      strokeDasharray={dashed ? '5 4' : undefined}
    />
  )
  const tunnelOf = (r: FlyRoute, host: number) => tunnels.find((x) => x.edge_id === r.gateway && x.host_id === host)
  // Цвет — живость (и у резервной: видно, можно ли на неё переключить).
  const targetColor = (r: FlyRoute, tg: FlyTarget) => {
    const ts = stats.get(r.id)?.targets?.find((s) => s.id === tg.id)
    const tn = tunnelOf(r, tg.host_id)
    if (tn && tn.state !== 'up' && tn.state !== 'not_applied' && tn.state !== 'unknown') return 'var(--status-critical)'
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
    if (!x.ghost) lines.push(link(X[1] + BW[1], y(ri, doc.routes.length), X[2], y(ti, targets.length), targetColor(x.route, x.t), 1.5, `r-t-${x.route.id}-${x.t.id}`, !!x.t.drain))
  })
  return (
    <div>
      <div style={{ overflowX: 'auto' }}>
        <svg
          ref={svgRef}
          viewBox={`0 0 ${W} ${H}`}
          style={{ width: '100%', minWidth: 640, maxWidth: 1100, height: 'auto',
            // В правке: без выделения подписей и без прокрутки касанием по
            // карте — иначе браузер забирает жест себе (pointercancel).
            // touch-action на элементах внутри SVG Chrome не учитывает,
            // поэтому — на всю карту.
            userSelect: editing ? 'none' : undefined,
            WebkitUserSelect: editing ? 'none' : undefined,
            touchAction: editing ? 'none' : undefined,
          }}
          role="img"
          aria-label={t('fly.map')}
          onDragStart={editing ? (e) => e.preventDefault() : undefined}
          onMouseDown={editing ? (e) => (e.target as Element).closest('[data-fly-target]') && e.preventDefault() : undefined}
        >
          {lines}
          {gwIDs.map((id, i) => {
            const g = gateways.find((x) => x.edge_id === id)
            const st = statuses.find((s) => s.edge_id === id)
            const wg = `WireGuard ${doc.gateways[i].listen_port}/udp`
            return node(0, i, gwIDs.length, g?.name ?? `#${id}`, g && g.address !== g.name ? `${g.address} · ${wg}` : wg, st?.connected ? 'var(--status-good)' : 'var(--status-critical)', `gw-${id}`, {
              secret: true,
              secretSub: !!g && g.address !== g.name,
            })
          })}
          {doc.routes.map((r, i) => {
            const st = stats.get(r.id)
            const ps = poolState(r, st)
            return node(1, i, doc.routes.length, routeTitle(r), `${poolText(t, ps)} · ${st?.active ?? 0} ${t('fly.connsShort')}`, ps.color, `r-${r.id}`, {
              onClick: () => onRoute(r.id),
              secret: named(r.kind),
              drop: editing ? `route:${r.id}` : undefined,
              route: r,
              busy: busy?.get(`r:${r.id}`),
            })
          })}
          {targets.map((x, i) => {
            const ts = stats.get(x.route.id)?.targets?.find((s) => s.id === x.t.id)
            const tn = tunnelOf(x.route, x.t.host_id)
            const host = hostNames[String(x.t.host_id)] ?? `#${x.t.host_id}`
            const name = x.t.label || (x.t.kind === 'host' ? t('fly.targetKind.host') : x.t.ref || '')
            const full = `${host} › ${name}:${x.t.port}`
            const bad = tn && tn.state !== 'up' ? t(`fly.tunnelShort.${tn.state}`) : ''
            const weight = (x.t.weight ?? 1) !== 1 ? ` · ${t('fly.weightShort', { n: x.t.weight })}` : ''
            const sub = `${t(`fly.targetKind.${x.t.kind}`)}${x.t.drain ? ` · ${t('fly.drainShort')}` : ''}${weight}${ts ? ` · ${ts.active} ${t('fly.connsShort')}` : ''}${bad ? ` · ${bad}` : ''}`
            const key = `${x.route.id}/${x.t.id}`
            return node(2, i, targets.length, full, sub, targetColor(x.route, x.t), `t-${key}${x.ghost ? '-ghost' : ''}`, {
              full,
              secret: true,
              onClick: () => onRoute(x.route.id),
              drop: editing && !x.ghost ? `route:${x.route.id}` : undefined,
              route: x.ghost ? undefined : x.route,
              dashed: !!x.t.drain,
              ghost: x.ghost,
              mark: x.ghost ? 'removed' : marks?.get(key),
              busy: x.ghost ? undefined : busy?.get(`t:${key}`),
              index: i,
            })
          })}
        </svg>
      </div>
      {editing && (
        <div className="row" style={{ justifyContent: 'flex-end', marginTop: 6 }}>
          <div
            data-fly-drop="trash"
            title={t('fly.trashHint')}
            className="row"
            style={{
              gap: 6,
              alignItems: 'center',
              padding: '6px 14px',
              borderRadius: 8,
              fontSize: 12,
              border: `2px dashed ${over === 'trash' ? 'var(--status-critical)' : 'var(--text-muted)'}`,
              background: over === 'trash' ? 'var(--status-critical-bg, #fff1f0)' : 'var(--surface-raised)',
              color: over === 'trash' ? 'var(--status-critical)' : 'var(--text-secondary)',
              opacity: drag ? 1 : 0.75,
            }}
          >
            <DeleteOutlined /> {drag ? t('fly.trashHint') : t('fly.trash')}
          </div>
        </div>
      )}
      {drag && (
        <div
          style={{
            position: 'fixed',
            left: drag.x + 12,
            top: drag.y + 12,
            pointerEvents: 'none',
            zIndex: 2000,
            padding: '4px 10px',
            borderRadius: 7,
            fontSize: 12,
            border: '2px solid var(--accent, #1677ff)',
            background: 'var(--surface-raised)',
            boxShadow: '0 4px 12px rgba(0,0,0,0.2)',
            whiteSpace: 'nowrap',
          }}
        >
          <Sensitive>{midEllipsis(drag.label, 40)}</Sensitive>
        </div>
      )}
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
              {open === h.id ? '▾' : '▸'} <Sensitive>{h.name}</Sensitive>
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
  const [privacy] = usePrivacy()
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
            title={privacy ? undefined : `${x.name}${x.ip ? ` · ${x.ip}` : ''}${x.ports?.length ? ` · ${x.ports.join(', ')}` : ''}${x.state ? ` · ${x.state}` : ''}`}
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
                {blurText([x.ip, x.ports?.length ? x.ports.join(', ') : '', x.state && x.state !== 'running' ? x.state : ''].filter(Boolean).join(' · ') || t(`fly.targetKind.${kind}`))}
              </span>
            </span>
          </div>
        )
      })}
    </div>
  )
}

/** Порт и вес цели (или только порт — для новой). */
function PortWeightModal({ ask, onClose }: { ask: PortAsk; onClose: () => void }) {
  const { t } = useTranslation()
  const [port, setPort] = useState<number | null>(ask.port || null)
  const [weight, setWeight] = useState<number | null>(ask.weight ?? null)
  return (
    <Modal title={ask.title} onClose={onClose} width={420}>
      <div className="col" style={{ gap: '0.5rem' }}>
        <label className="small">
          {t('fly.targetPort')}{' '}
          <InputNumber min={1} max={65535} value={port} disabled={ask.portLocked} onChange={setPort} />
        </label>
        {ask.weight !== undefined && (
          <label className="small">
            {t('fly.targetWeight')} <InputNumber min={1} max={100} value={weight} onChange={setWeight} />
          </label>
        )}
        {ask.portLocked && <span className="small muted">{t('fly.switchRangeHint')}</span>}
        <Space>
          <Button
            type="primary"
            disabled={!port}
            onClick={() => {
              ask.onOk(port ?? 0, weight ?? undefined)
              onClose()
            }}
          >
            {t('fly.portOk')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </Space>
      </div>
    </Modal>
  )
}

/** Перенести цель в другой маршрут — встанет резервом. */
function MoveModal({ title, routes, onOk, onClose }: { title: string; routes: FlyRoute[]; onOk: (to: string) => void; onClose: () => void }) {
  const { t } = useTranslation()
  const [to, setTo] = useState<string | undefined>(routes[0]?.id)
  return (
    <Modal title={title} onClose={onClose} width={460}>
      <div className="col" style={{ gap: '0.5rem' }}>
        <Select value={to} onChange={setTo} options={routes.map((r) => ({ value: r.id, label: routeTitle(r) }))} />
        <span className="small muted">{t('fly.moveHint')}</span>
        <Space>
          <Button
            type="primary"
            disabled={!to}
            onClick={() => {
              if (to) onOk(to)
              onClose()
            }}
          >
            {t('fly.moveOk')}
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
                  <span className="mono">
                    <Sensitive>{n}</Sensitive>
                  </span>
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
            { title: t('fly.colHost'), key: 'h', render: (_, x) => <Sensitive>{hostNames[String(x.host_id)] ?? `#${x.host_id}`}</Sensitive> },
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
            <span className="mono sensitive">{stats!.top!.map((c) => `${c.ip}${c.conns > 1 ? ` ×${c.conns}` : ''}`).join(', ')}</span>
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
            <Select value={r.gateway} onChange={(v) => set({ gateway: v })} options={gateways.map((g) => ({ value: g.edge_id, label: <Sensitive>{g.name}</Sensitive> }))} style={{ width: 220 }} />
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
              <Select className="sensitive" mode="tags" value={r.names} onChange={(v: string[]) => set({ names: v.map((x) => x.trim().toLowerCase()).filter(Boolean) })} tokenSeparators={[',', ' ']} placeholder="app.example.com, *.app.example.com" />
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
            className="sensitive"
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
        options={hosts.map((h) => ({ value: h.id, label: <Sensitive>{osLabel(h.os_info, h.name)}</Sensitive>, search: h.name }))}
        optionFilterProp="search"
        showSearch
        style={{ width: 200 }}
      />
      <Select value={target.kind} onChange={(v: TargetKind) => onChange({ ...target, kind: v, ref: '' })} options={TARGET_KINDS.map((k) => ({ value: k, label: t(`fly.targetKind.${k}`) }))} style={{ width: 150 }} />
      {target.kind === 'addr' ? (
        <Input className="sensitive" value={target.ref} onChange={(e) => onChange({ ...target, ref: e.target.value.trim() })} placeholder="192.168.1.20" style={{ width: 170 }} />
      ) : target.kind !== 'host' ? (
        <Select
          value={target.ref || undefined}
          onChange={(v: string) => {
            const it = items.find((x) => x.name === v)
            onChange({ ...target, ref: v, port: target.port || it?.ports?.[0] || 0 })
          }}
          loading={list.loading}
          options={items.map((x) => ({ value: x.name, label: blurText(`${x.name}${x.ip ? ` · ${x.ip}` : ''}${x.state ? ` · ${x.state}` : ''}`) }))}
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
function DiffConfirm({ title, diff, run, okText, hint, onClose }: { title: string; diff: string; run: (note: string) => Promise<void>; okText?: string; hint?: string; onClose: () => void }) {
  const { t } = useTranslation()
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  return (
    <Modal title={title} onClose={onClose} width={860}>
      <div className="col" style={{ gap: '0.5rem' }}>
        <DiffView text={diff} />
        <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('fly.notePlaceholder')} />
        <span className="small muted">{hint ?? t('fly.saveHint')}</span>
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
            {okText ?? t('common.save')}
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
