import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Checkbox, Input, Segmented, Space, Switch, Tag, Tooltip } from 'antd'
import { LoadingOutlined, ReloadOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { OverflowBar } from './OverflowBar'
import { api, useApi } from '../api'
import { Banner, Loading, Modal } from './ui'
import { confirmAction } from './confirm'
import { usePrivacy } from '../privacy'
import type { HubHost, OSInfo } from '../types'

// Карта хостов хаба: у каждого хоста — его контейнеры, машины и LXD
// плитками; слева — маршруты Traffic Fly и конвейеры выкладок, связи к
// своим плиткам — своим цветом (рабочая цель — сплошная, резерв —
// пунктир). Окно с масштабом колесом в точке курсора и сдвигом
// перетаскиванием; щелчок по плитке — в её раздел на хосте.

interface MapItem {
  kind: 'container' | 'vm' | 'lxd'
  name: string
  ip?: string
  state?: string
  ports?: number[]
  image?: string
  project?: string
  service?: string
  os_info?: OSInfo
  cpus?: number
  mem_bytes?: number
  lxd_type?: string
  /** Призрак: переезжает сюда (задание переноса), на хосте его ещё нет. */
  ghost?: number
}

interface MapTarget {
  host_id: number
  kind: string
  ref?: string
  port: number
  drain?: boolean
  alive: boolean
  checked: boolean
}

interface MapRoute {
  id: string
  kind: string
  names?: string[]
  port: number
  port_to?: number
  gateway: string
  targets: MapTarget[]
}

interface MapPipeline {
  id: number
  name: string
  project: string
  status?: string
  hosts: number[]
}

interface MapData {
  hosts: Record<string, { items: MapItem[] | null; error?: string }>
  routes: MapRoute[]
  pipelines: MapPipeline[]
}

// Цвета связей: у каждого маршрута и конвейера свой, по кругу.
const PALETTE = ['#1677ff', '#eb2f96', '#13a8a8', '#fa8c16', '#722ed1', '#52c41a', '#d4a106', '#2f54eb', '#f5222d', '#08979c']

const TILE_W = 210
const TILE_H = 46
const GAP = 8
const COLS = 3
const COLLAPSED = 12
/** Меток Traffic Fly на плитке, дальше — «+N». */
const MARKS_MAX = 3
const HEAD_H = 34
/** Подпись над рамкой стека. */
const STACK_H = 16
const BLOCK_W = COLS * TILE_W + (COLS + 1) * GAP
const LEFT_W = 250
const LEFT_X = 10
const HOSTS_X = LEFT_X + LEFT_W + 120
const NODE_H = 38
/** Хостов не больше — карта одним столбцом (при «Колонки: авто»). */
const ONE_COLUMN_MAX = 5

const store = {
  get<T>(k: string, def: T): T {
    try {
      const v = localStorage.getItem('nkt-hostsmap-' + k)
      return v === null ? def : (JSON.parse(v) as T)
    } catch {
      return def
    }
  },
  set(k: string, v: unknown) {
    try {
      localStorage.setItem('nkt-hostsmap-' + k, JSON.stringify(v))
    } catch {
      // без localStorage вид просто не запомнится
    }
  },
}

function midEllipsis(s: string, max: number): string {
  if (s.length <= max) return s
  const head = Math.ceil((max - 1) / 2)
  return s.slice(0, head) + '…' + s.slice(s.length - Math.floor((max - 1) / 2))
}

function stateColor(state?: string): string {
  const s = (state ?? '').toLowerCase()
  if (s === 'running') return 'var(--status-good)'
  if (s.startsWith('restart') || s === 'paused' || s === 'frozen') return 'var(--status-warning)'
  if (s === 'crashed' || s === 'dead' || s === 'error') return 'var(--status-critical)'
  return 'var(--text-muted)'
}

function routeTitle(r: MapRoute): string {
  if (r.kind === 'https' || r.kind === 'http') return (r.names ?? []).join(', ') || r.id
  return `${r.kind === 'tcpudp' ? 'tcp+udp' : r.kind} :${r.port}${r.port_to && r.port_to > r.port ? `-${r.port_to}` : ''}`
}

function itemPath(it: MapItem): string {
  const q = (p: Record<string, string>) => '?' + new URLSearchParams(p).toString()
  if (it.kind === 'vm') return '/containers' + q({ tab: 'vms', focus: it.name })
  if (it.kind === 'lxd') return '/containers' + q({ tab: 'lxd', focus: it.name })
  return '/containers' + q({ tab: 'docker', focus: it.name })
}

const tileKey = (hostId: number, kind: string, name: string) => `${hostId}/${kind}/${name}`

interface Link {
  from: string // route:<id> | pipe:<id>
  to: string // ключ плитки или host:<id>
  color: string
  dashed?: boolean
  dead?: boolean
}

export function HostsMap({
  hosts,
  onOpenHost,
  onOpenAt,
  onOpenJob,
  admin,
}: {
  hosts: HubHost[]
  onOpenHost: (h: HubHost) => void
  onOpenAt: (h: HubHost, path: string) => void
  /** Журнал задания (перенос, откат, уборка старого). */
  onOpenJob?: (id: number) => void
  admin?: boolean
}) {
  const { t } = useTranslation()
  const [privacy] = usePrivacy()
  const data = useApi<MapData>('/hub/map', 60_000)
  const [refreshing, setRefreshing] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [expandAll, setExpandAll] = useState<boolean>(() => store.get('expand', false))
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const [q, setQ] = useState('')
  const [onlyRunning, setOnlyRunning] = useState<boolean>(() => store.get('running', false))
  const [onlyLinked, setOnlyLinked] = useState<boolean>(() => store.get('linked', false))
  const [showFly, setShowFly] = useState<boolean>(() => store.get('fly', true))
  const [showDeploy, setShowDeploy] = useState<boolean>(() => store.get('deploy', true))
  const [cols, setCols] = useState<'auto' | '1' | '2'>(() => store.get('cols', 'auto'))
  useEffect(() => store.set('cols', cols), [cols])
  const [view, setView] = useState<{ x: number; y: number; k: number }>(() => store.get('view', { x: 0, y: 0, k: 1 }))
  const [hover, setHover] = useState<string | null>(null)
  const [pinned, setPinned] = useState<string | null>(null)
  // Перенос контейнера: режим (перетаскивание плитки Docker на другой
  // хост вместо сдвига карты), что тащат, куда, окно плана, меню
  // перенесённого.
  const [moveMode, setMoveMode] = useState(false)
  // Тащат плитку — подсвечивается весь её стек (project), он и переносится.
  const [tileDrag, setTileDrag] = useState<{ x: number; y: number; label: string; host: number; name: string; project?: string } | null>(null)
  const [overHost, setOverHost] = useState<number | null>(null)
  const [migrateAsk, setMigrateAsk] = useState<{ from: HubHost; to: HubHost; item: MapItem } | null>(null)
  // Как у Traffic Fly: план переносов копится и уходит «Применить
  // переносы (N)», или «Применять сразу» — перенос после окна плана сразу.
  const [applyNow, setApplyNow] = useState<boolean>(() => store.get('applyNow', false))
  useEffect(() => store.set('applyNow', applyNow), [applyNow])
  const [drafts, setDrafts] = useState<DraftMove[]>([])
  // Переносы в работе: спиннеры на плитках, по завершении — карта заново.
  const [moving, setMoving] = useState<MovingJob[]>([])
  const [movesFailed, setMovesFailed] = useState<number[]>([])
  const [settling, setSettling] = useState(false)
  const [tick, setTick] = useState(0)
  const [recMenu, setRecMenu] = useState<MigrationRecord | null>(null)
  const migrations = useApi<{ migrations: MigrationRecord[] }>('/hub/migrations', 60_000)
  const movingRef = useRef(moving)
  movingRef.current = moving
  const hasMoving = moving.length > 0
  // Ход переносов с хаба — и начатых не здесь: раз в секунду, пока что-то
  // переносится, иначе раз в 15 секунд (заметить чужой перенос).
  const [live, setLive] = useState<MigrateLive[]>([])
  const liveSeen = useRef(new Set<number>())
  const liveFast = useRef(false)
  liveFast.current = hasMoving || live.length > 0
  useEffect(() => {
    let stop = false
    let timer = 0
    const poll = async () => {
      const res = await api<{ live: MigrateLive[] }>('/hub/migrate/live').catch(() => null)
      if (stop) return
      if (res) {
        setLive(res.live)
        setMoving((ms) => {
          let out = ms.map((m) => {
            const l = res.live.find((x) => x.job === m.id)
            return l && m.names.length === 0 && (l.containers ?? []).length ? { ...m, names: l.containers!, title: l.project || m.title } : m
          })
          for (const l of res.live) {
            if (l.stage === 'done' || l.stage === 'failed' || liveSeen.current.has(l.job) || out.some((m) => m.id === l.job)) continue
            liveSeen.current.add(l.job)
            out = [...out, { id: l.job, from: l.from, to: l.to, names: l.containers ?? [], title: l.project || (l.containers ?? [])[0] || `#${l.job}`, started: Date.parse(l.started) || Date.now() }]
          }
          return out
        })
      }
      // Только что появился — уже часто (liveFast обновится лишь с отрисовкой).
      timer = window.setTimeout(() => void poll(), liveFast.current || (res?.live.length ?? 0) > 0 ? 1000 : 15000)
    }
    void poll()
    return () => {
      stop = true
      window.clearTimeout(timer)
    }
  }, [])
  const reduceMotion = useMemo(() => {
    try {
      return window.matchMedia('(prefers-reduced-motion: reduce)').matches
    } catch {
      return false
    }
  }, [])
  useEffect(() => {
    if (!hasMoving) return
    let n = 0
    const iv = window.setInterval(() => {
      setTick((x) => x + 1)
      if (n++ % 2) return
      void (async () => {
        const done: { id: number; ok: boolean }[] = []
        for (const m of movingRef.current) {
          const job = await api<{ status: string }>(`/hosts/local/jobs/${m.id}`).catch(() => null)
          if (job && job.status !== 'queued' && job.status !== 'running') done.push({ id: m.id, ok: job.status === 'succeeded' })
        }
        if (done.length === 0) return
        // Перенос закончился — списки хостов мимо кэша (пока карта
        // перечитывается — пелена «Обновляю карту…»), затем карта, учёт
        // переносов и маршруты Traffic Fly (они в ответе карты). Удачный
        // перенос, а контейнера на новом хосте ещё нет — ещё раз, до трёх.
        setSettling(true)
        try {
          const okMoves = movingRef.current.filter((m) => done.some((d) => d.id === m.id && d.ok))
          for (let i = 0; i < 3; i++) {
            const fresh = await api<MapData>('/hub/map?refresh=1').catch(() => null)
            // Свежо — когда на новом хосте контейнеры есть, а на старом
            // уже не работают.
            const arrived = okMoves.every((m) =>
              m.names.every(
                (n) =>
                  (fresh?.hosts[String(m.to)]?.items ?? []).some((it) => it.kind === 'container' && it.name === n) &&
                  !(fresh?.hosts[String(m.from)]?.items ?? []).some((it) => it.kind === 'container' && it.name === n && (it.state ?? '').toLowerCase() === 'running'),
              ),
            )
            if (!fresh || arrived) break
            await new Promise((r) => window.setTimeout(r, 2000))
          }
          await Promise.all([data.reload(), migrations.reload()])
        } finally {
          setSettling(false)
        }
        setMoving((l) => l.filter((m) => !done.some((d) => d.id === m.id)))
        const bad = done.filter((d) => !d.ok).map((d) => d.id)
        if (bad.length) setMovesFailed((f) => [...f, ...bad])
      })()
    }, 1000)
    return () => window.clearInterval(iv)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasMoving])
  void tick
  const startMove = async (d: DraftMove) => {
    const res = await api<{ job_id: number }>('/hub/migrate', { method: 'POST', body: { from: d.from.id, to: d.to.id, container: d.item.name, switch_fly: d.switchFly, clean_leftovers: d.cleanLeftovers, switch_pipeline: d.switchPipeline } })
    liveSeen.current.add(res.job_id)
    setMoving((l) => [...l, { id: res.job_id, from: d.from.id, to: d.to.id, names: d.names, title: d.item.name, started: Date.now() }])
    return res.job_id
  }
  const [applyErr, setApplyErr] = useState<string | null>(null)
  const applyDrafts = async () => {
    setApplyErr(null)
    const list = drafts
    setDrafts([])
    for (const d of list) {
      try {
        await startMove(d)
      } catch (e) {
        setApplyErr(e instanceof Error ? e.message : String(e))
      }
    }
  }
  const movingTile = (hostId: number, name: string) => moving.find((m) => m.from === hostId && m.names.includes(name))
  const draftTile = (hostId: number, name: string) => drafts.find((d) => d.from.id === hostId && d.names.includes(name))
  const tilePress = useRef<{ from: HubHost; item: MapItem; sx: number; sy: number; started: boolean } | null>(null)
  const hostsRef = useRef(hosts)
  hostsRef.current = hosts
  // «Применять сразу»: план проверяется молча, препятствий и
  // предупреждений нет — перенос сразу (по умолчанию: маршруты Traffic Fly
  // переключить, остатки старой копии убрать); есть — окно плана.
  const [planning, setPlanning] = useState<{ from: number; name: string; to: string } | null>(null)
  const askMove = async (ask: { from: HubHost; to: HubHost; item: MapItem }) => {
    if (!applyNow) {
      setMigrateAsk(ask)
      return
    }
    setPlanning({ from: ask.from.id, name: ask.item.name, to: ask.to.name })
    try {
      const pv = await api<MigratePreview>('/hub/migrate/preview', { method: 'POST', body: { from: ask.from.id, to: ask.to.id, container: ask.item.name }, timeoutMs: 120_000 })
      const problems = (pv.leftovers ? pv.leftovers.problems : pv.problems) ?? []
      // Конвейер, который перенос не переключит, — тоже повод показать окно.
      if (problems.length > 0 || (pv.warnings ?? []).length > 0 || (pv.pipeline && !pv.pipeline.can_switch)) {
        setMigrateAsk(ask)
        return
      }
      const id = await startMove({
        from: ask.from,
        to: ask.to,
        item: ask.item,
        names: (pv.plan.containers ?? []).map((c) => c.name),
        switchFly: (pv.fly ?? []).length > 0,
        cleanLeftovers: !!pv.leftovers,
        switchPipeline: !!pv.pipeline?.can_switch,
      })
      onOpenJob?.(id)
    } catch {
      // Не вышло проверить или запустить — окно плана покажет почему.
      setMigrateAsk(ask)
    } finally {
      setPlanning(null)
    }
  }
  const askRef = useRef(askMove)
  askRef.current = askMove
  const dataRef = useRef(data.data)
  dataRef.current = data.data
  useEffect(() => {
    if (!moveMode) return
    const hostAt = (x: number, y: number) => {
      const v = document.elementFromPoint(x, y)?.closest('[data-map-host]')?.getAttribute('data-map-host')
      return v ? Number(v) : null
    }
    const move = (e: PointerEvent) => {
      const p = tilePress.current
      if (!p) return
      if (!p.started && Math.hypot(e.clientX - p.sx, e.clientY - p.sy) < 6) return
      p.started = true
      const it = p.item
      const stackN = it.project ? (dataRef.current?.hosts[String(p.from.id)]?.items ?? []).filter((x) => x.kind === 'container' && x.project === it.project).length : 1
      setTileDrag({
        x: e.clientX,
        y: e.clientY,
        label: stackN >= 2 ? t('hostsMap.dragStack', { name: it.project, n: stackN }) : `docker · ${it.name}`,
        host: p.from.id,
        name: it.name,
        project: stackN >= 2 ? it.project : undefined,
      })
      const h = hostAt(e.clientX, e.clientY)
      setOverHost(h !== null && h !== p.from.id ? h : null)
    }
    const up = (e: PointerEvent) => {
      const p = tilePress.current
      tilePress.current = null
      setTileDrag(null)
      setOverHost(null)
      if (!p?.started) return
      const h = hostAt(e.clientX, e.clientY)
      const to = hostsRef.current.find((x) => x.id === h)
      if (to && to.id !== p.from.id && to.status === 'online') askRef.current({ from: p.from, to, item: p.item })
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    return () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
  }, [moveMode, t])
  // Перенесённое: «здесь остановлен, работает там» или «перенесён сюда».
  // Старая копия — только неработающий контейнер: тот же стек переносят
  // туда и обратно с теми же именами, и работающий не должен считаться
  // старым (иначе его не перетащить).
  // Решает последняя запись переноса этого контейнера с этим хостом, а
  // не состояние: список хоста после переноса может быть ещё старым
  // («работает»). Перенос обратно — новая запись в обратную сторону, и
  // вернувшийся контейнер снова обычный.
  const migratedHere = (hostId: number, name: string): { rec: MigrationRecord; role: 'from' | 'to' | 'second' } | null => {
    const last = [...(migrations.data?.migrations ?? [])]
      .filter((r) => r.containers.includes(name) && (r.from === hostId || r.to === hostId))
      .sort((a, b) => a.ts.localeCompare(b.ts))
      .pop()
    if (!last) return null
    // Откачен без нового хоста — на нём вторая копия, её надо остановить.
    if (last.state === 'rolled_back' && last.new_not_stopped && last.to === hostId) return { rec: last, role: 'second' }
    if (last.state !== 'moved') return null
    return { rec: last, role: last.from === hostId ? 'from' : 'to' }
  }
  const boxRef = useRef<HTMLDivElement | null>(null)
  const svgRef = useRef<SVGSVGElement>(null)
  // Окно карты появляется только после загрузки данных (до того —
  // «Загрузка…»): обработчик колеса вешается на него, когда оно есть.
  const [box, setBox] = useState<HTMLDivElement | null>(null)
  const boxCallback = (el: HTMLDivElement | null) => {
    boxRef.current = el
    setBox(el)
  }

  useEffect(() => store.set('expand', expandAll), [expandAll])
  useEffect(() => store.set('running', onlyRunning), [onlyRunning])
  useEffect(() => store.set('linked', onlyLinked), [onlyLinked])
  useEffect(() => store.set('fly', showFly), [showFly])
  useEffect(() => store.set('deploy', showDeploy), [showDeploy])
  useEffect(() => store.set('view', view), [view])

  async function refresh() {
    setRefreshing(true)
    try {
      await api('/hub/map?refresh=1')
      await data.reload()
    } finally {
      setRefreshing(false)
    }
  }

  // Связи: маршруты Traffic Fly к своим целям, конвейеры к контейнерам
  // своего стека на хостах выкладки.
  const { links, routeColor, pipeColor } = useMemo(() => {
    const links: Link[] = []
    const routeColor = new Map<string, string>()
    const pipeColor = new Map<number, string>()
    let ci = 0
    for (const r of data.data?.routes ?? []) {
      const color = PALETTE[ci++ % PALETTE.length]
      routeColor.set(r.id, color)
      for (const tg of r.targets) {
        const to = tg.kind === 'container' || tg.kind === 'vm' || tg.kind === 'lxd' ? tileKey(tg.host_id, tg.kind, tg.ref ?? '') : `host:${tg.host_id}`
        links.push({ from: `route:${r.id}`, to, color, dashed: !!tg.drain, dead: tg.checked && !tg.alive })
      }
    }
    for (const p of data.data?.pipelines ?? []) {
      const color = PALETTE[ci++ % PALETTE.length]
      pipeColor.set(p.id, color)
      for (const hid of p.hosts) {
        const items = data.data?.hosts[String(hid)]?.items ?? []
        const mine = items.filter((it) => it.kind === 'container' && it.project === p.project)
        if (mine.length === 0) links.push({ from: `pipe:${p.id}`, to: `host:${hid}`, color })
        for (const it of mine) links.push({ from: `pipe:${p.id}`, to: tileKey(hid, it.kind, it.name), color, dead: p.status === 'failed' })
      }
    }
    return { links, routeColor, pipeColor }
  }, [data.data])

  const routesById = useMemo(() => new Map((data.data?.routes ?? []).map((r) => [r.id, r])), [data.data])
  const visibleLinks = links.filter((l) => (l.from.startsWith('route:') ? showFly : showDeploy))
  const linkedTiles = new Set(visibleLinks.map((l) => l.to))

  // Раскладка: левый столбец узлов; хосты — блоками в два столбца (в
  // более короткий), плитки — сеткой по три.
  const layout = useMemo(() => {
    const left: { key: string; label: string; sub: string; color: string; y: number; bad?: boolean }[] = []
    let ly = 10
    if (showFly)
      for (const r of data.data?.routes ?? []) {
        left.push({ key: `route:${r.id}`, label: routeTitle(r), sub: `Traffic Fly · ${r.gateway || '—'}`, color: routeColor.get(r.id)!, y: ly })
        ly += NODE_H + GAP
      }
    if (showDeploy)
      for (const p of data.data?.pipelines ?? []) {
        left.push({ key: `pipe:${p.id}`, label: p.name, sub: `${t('hostsMap.stack')} ${p.project}${p.status ? ` · ${t(`hostsMap.deploy.${p.status}`, { defaultValue: p.status })}` : ''}`, color: pipeColor.get(p.id)!, y: ly, bad: p.status === 'failed' })
        ly += NODE_H + GAP
      }
    const ql = q.trim().toLowerCase()
    const colY = [10, 10]
    const blocks: {
      host: HubHost
      x: number
      y: number
      h: number
      tiles: { item: MapItem; key: string; x: number; y: number }[]
      stacks: { project: string; x: number; y: number; w: number; h: number }[]
      more: number
      hidden: MapItem[]
      moreAt?: { x: number; y: number }
      error?: string
      offline: boolean
    }[] = []
    const tilePos = new Map<string, { x: number; y: number }>()
    const hostPos = new Map<number, { x: number; y: number }>()
    // Сначала — какие хосты и плитки видны (по ним — число столбцов),
    // потом раскладка.
    const prepared: { h: HubHost; hd: MapData['hosts'][string] | undefined; offline: boolean; items: MapItem[]; gkey: (it: MapItem) => string }[] = []
    for (const h of hosts) {
      const hd = data.data?.hosts[String(h.id)]
      const offline = h.status !== 'online'
      // «Только запущенные» — и без хостов, которые не на связи.
      if (onlyRunning && (offline || hd?.error)) continue
      // Переезжающие сюда — призраками, пока их нет в списке хоста.
      const real = hd?.items ?? []
      const ghosts: MapItem[] = []
      for (const l of live)
        if (l.to === h.id && l.stage !== 'failed')
          for (const name of l.containers ?? [])
            if (!real.some((it) => it.kind === 'container' && it.name === name)) ghosts.push({ kind: 'container', name, project: l.project, ghost: l.job })
      let items = [...real, ...ghosts].filter((it) => {
        if (it.ghost) return true
        if (onlyRunning && (it.state ?? '').toLowerCase() !== 'running') return false
        if (onlyLinked && !linkedTiles.has(tileKey(h.id, it.kind, it.name))) return false
        if (ql && !`${it.name} ${it.image ?? ''} ${it.ip ?? ''} ${it.project ?? ''}`.toLowerCase().includes(ql) && !h.name.toLowerCase().includes(ql)) return false
        return true
      })
      if ((onlyLinked || ql) && items.length === 0 && !h.name.toLowerCase().includes(ql)) continue
      // Сначала связанные, потом работающие, потом по имени: свёрнутый
      // хост показывает самое важное.
      items = [...items].sort((a, b) => {
        const la = linkedTiles.has(tileKey(h.id, a.kind, a.name)) ? 0 : 1
        const lb = linkedTiles.has(tileKey(h.id, b.kind, b.name)) ? 0 : 1
        const ra = (a.state ?? '').toLowerCase() === 'running' ? 0 : 1
        const rb = (b.state ?? '').toLowerCase() === 'running' ? 0 : 1
        return la - lb || ra - rb || a.name.localeCompare(b.name)
      })
      // Стек (compose-проект от двух контейнеров) — подряд, на месте
      // первого своего контейнера.
      const projN = new Map<string, number>()
      for (const it of items) if (it.kind === 'container' && it.project) projN.set(it.project, (projN.get(it.project) ?? 0) + 1)
      const gkey = (it: MapItem) => (it.kind === 'container' && it.project && (projN.get(it.project) ?? 0) >= 2 ? `p:${it.project}` : `i:${it.kind}:${it.name}`)
      const first = new Map<string, number>()
      items.forEach((it, i) => {
        if (!first.has(gkey(it))) first.set(gkey(it), i)
      })
      items = items
        .map((it, i) => ({ it, i }))
        .sort((a, b) => first.get(gkey(a.it))! - first.get(gkey(b.it))! || a.i - b.i)
        .map((x) => x.it)
      prepared.push({ h, hd, offline, items, gkey })
    }
    // Хостов немного — одним столбцом: карта уже и крупнее.
    const ncols = cols === 'auto' ? (prepared.length <= ONE_COLUMN_MAX ? 1 : 2) : Number(cols)
    for (const { h, hd, offline, items, gkey } of prepared) {
      const open = expandAll || expanded.has(h.id)
      const shown = open || items.length <= COLLAPSED ? items : items.slice(0, COLLAPSED - 1)
      const more = items.length - shown.length
      const cells = shown.length + (more > 0 ? 1 : 0)
      const col = ncols === 1 || colY[0] <= colY[1] ? 0 : 1
      const bx = HOSTS_X + col * (BLOCK_W + 30)
      const by = colY[col]
      // Сетка по COLS; стек — с новой строки, под рамкой с подписью, и
      // после стека — тоже с новой строки.
      const stacks: { project: string; x: number; y: number; w: number; h: number }[] = []
      let ci = 0
      let rowY = by + HEAD_H
      let prev = ''
      const place = (g: string) => {
        const stack = g.startsWith('p:')
        if (ci === COLS || (g !== prev && ci > 0 && (stack || prev.startsWith('p:')))) {
          rowY += TILE_H + GAP
          ci = 0
        }
        if (g !== prev && stack) {
          rowY += STACK_H
          stacks.push({ project: g.slice(2), x: bx + GAP - 4, y: rowY - STACK_H, w: 0, h: 0 })
        }
        const at = { x: bx + GAP + ci * (TILE_W + GAP), y: rowY }
        if (stack) {
          const f = stacks[stacks.length - 1]
          f.w = Math.max(f.w, at.x + TILE_W + 4 - f.x)
          f.h = at.y + TILE_H + 4 - f.y
        }
        ci++
        prev = g
        return at
      }
      const tiles = shown.map((item) => {
        const { x, y } = place(gkey(item))
        const key = tileKey(h.id, item.kind, item.name)
        tilePos.set(key, { x, y })
        return { item, key, x, y }
      })
      let moreAt: { x: number; y: number } | undefined
      const hidden = items.slice(shown.length)
      if (more > 0) {
        moreAt = place('more')
        // Скрытые плитки — связи ведут на «+N ещё».
        for (const it of hidden) tilePos.set(tileKey(h.id, it.kind, it.name), moreAt)
      }
      const bh = offline || hd?.error || cells === 0 ? HEAD_H + 30 : rowY + TILE_H + 2 * GAP - by
      colY[col] += bh + 20
      hostPos.set(h.id, { x: bx, y: by })
      blocks.push({ host: h, x: bx, y: by, h: bh, tiles, stacks, more, hidden, moreAt, error: hd?.error, offline })
    }
    const width = HOSTS_X + ncols * BLOCK_W + (ncols - 1) * 30 + 30
    const height = Math.max(ly, colY[0], colY[1]) + 10
    return { left, blocks, tilePos, hostPos, width, height }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.data, hosts, expandAll, expanded, q, onlyRunning, onlyLinked, showFly, showDeploy, routeColor, pipeColor, t, cols, live])

  // Подсветка: наведённое или закреплённое щелчком — его связи ярко,
  // остальное бледно.
  // Пока тащат плитку на другой хост — без подсветки связей.
  const focus = tileDrag ? null : hover ?? pinned
  const related = useMemo(() => {
    if (!focus) return null
    const s = new Set<string>([focus])
    for (const l of visibleLinks) {
      if (l.from === focus || l.to === focus) {
        s.add(l.from)
        s.add(l.to)
      }
    }
    // Плитка → все связи её маршрутов и конвейеров.
    if (!focus.startsWith('route:') && !focus.startsWith('pipe:')) {
      for (const l of visibleLinks) if (s.has(l.from)) s.add(l.to)
    }
    return s
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focus, links, showFly, showDeploy])
  const dim = (key: string) => (related && !related.has(key) ? 0.18 : 1)

  // Масштаб колесом в точке курсора, сдвиг — перетаскиванием пустого места.
  const drag = useRef<{ sx: number; sy: number; x: number; y: number; moved: boolean } | null>(null)
  useEffect(() => {
    const el = box
    if (!el) return
    const wheel = (e: WheelEvent) => {
      e.preventDefault()
      const r = el.getBoundingClientRect()
      const px = e.clientX - r.left
      const py = e.clientY - r.top
      setView((v) => {
        const k = Math.min(3, Math.max(0.25, v.k * (e.deltaY < 0 ? 1.12 : 1 / 1.12)))
        return { k, x: px - ((px - v.x) * k) / v.k, y: py - ((py - v.y) * k) / v.k }
      })
    }
    el.addEventListener('wheel', wheel, { passive: false })
    return () => el.removeEventListener('wheel', wheel)
  }, [box])
  const zoomBy = (f: number) => {
    const el = boxRef.current
    const cx = (el?.clientWidth ?? 800) / 2
    const cy = (el?.clientHeight ?? 500) / 2
    setView((v) => {
      const k = Math.min(3, Math.max(0.25, v.k * f))
      return { k, x: cx - ((cx - v.x) * k) / v.k, y: cy - ((cy - v.y) * k) / v.k }
    })
  }
  const fit = () => {
    const el = boxRef.current
    if (!el) return
    const k = Math.min(1.5, Math.max(0.25, Math.min(el.clientWidth / layout.width, el.clientHeight / layout.height)))
    setView({ k, x: (el.clientWidth - layout.width * k) / 2, y: 0 })
  }

  const linkPath = (l: Link) => {
    const a = layout.left.find((n) => n.key === l.from)
    const b = l.to.startsWith('host:') ? layout.hostPos.get(Number(l.to.slice(5))) : layout.tilePos.get(l.to)
    if (!a || !b) return null
    const x1 = LEFT_X + LEFT_W
    const y1 = a.y + NODE_H / 2
    const x2 = b.x
    const y2 = b.y + (l.to.startsWith('host:') ? HEAD_H / 2 : TILE_H / 2)
    const mx = (x1 + x2) / 2
    return `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`
  }

  // Живой перенос поверх связей: данные бегут со старой плитки на
  // призрак, у маршрутов Traffic Fly новая цель прорисовывается, ждёт
  // проверки и забирает трафик, старая бледнеет; у конвейера — так же,
  // когда он переключается (и сайт — подписью). Связи, которые перенос
  // рисует сам, из обычных убираются.
  const liveOverlay = useMemo(() => {
    const lines: { key: string; d: string; color: string; mode: LiveMode }[] = []
    const transfers: { key: string; d: string; x: number; y: number; label: string }[] = []
    const labels: { key: string; x: number; y: number; text: string; color: string }[] = []
    const suppress = new Set<string>()
    const path = (from: string, to: string) => linkPath({ from, to, color: '' })
    for (const l of live) {
      const st = liveAt(l.stage)
      const failed = l.stage === 'failed'
      const names = l.containers ?? []
      // Данные: со старой плитки на призрак.
      if (!failed && (l.stage === 'pack' || l.stage === 'copy' || l.stage === 'unpack') && names.length) {
        const a = layout.tilePos.get(tileKey(l.from, 'container', names[0]))
        const b = layout.tilePos.get(tileKey(l.to, 'container', names[0]))
        if (a && b) {
          const ay = a.y + TILE_H / 2
          const by = b.y + TILE_H / 2
          let d: string
          let x: number
          let y: number
          const fx = layout.hostPos.get(l.from)?.x ?? a.x
          const tx = layout.hostPos.get(l.to)?.x ?? b.x
          if (Math.abs(fx - tx) < BLOCK_W / 2) {
            // Один столбец: дугой справа от блоков, от края блока на
            // уровне плитки — не поперёк соседних плиток.
            const ex = Math.max(fx, tx) + BLOCK_W
            const cx = ex + 40 + Math.min(200, Math.abs(by - ay) / 4)
            d = `M${ex},${ay} C${cx},${ay} ${cx},${by} ${ex},${by}`
            x = ex + 8 + (cx - ex) * 0.75
            y = (ay + by) / 2
          } else {
            const [p, q] = a.x < b.x ? [{ x: a.x + TILE_W, y: ay }, { x: b.x, y: by }] : [{ x: a.x, y: ay }, { x: b.x + TILE_W, y: by }]
            const mx = (p.x + q.x) / 2
            d = `M${p.x},${p.y} C${mx},${p.y} ${mx},${q.y} ${q.x},${q.y}`
            x = mx
            y = (p.y + q.y) / 2
          }
          const label = l.stage === 'copy' ? t('hostsMap.live.copy', { p: l.percent }) : t(l.stage === 'pack' ? 'hostsMap.live.pack' : 'hostsMap.live.unpack')
          transfers.push({ key: `t${l.job}`, d, x, y, label })
        }
      }
      // Traffic Fly.
      if (showFly)
        for (const f of l.fly ?? []) {
          const from = `route:${f.route}`
          const oldKey = tileKey(l.from, 'container', f.ref)
          const newKey = tileKey(l.to, 'container', f.ref)
          suppress.add(`${from}|${oldKey}`)
          suppress.add(`${from}|${newKey}`)
          const color = routeColor.get(f.route) ?? 'var(--accent, #1677ff)'
          const switched = !failed && st >= liveAt('fly-switched')
          const od = path(from, oldKey)
          if (od) lines.push({ key: `fo${l.job}${f.route}${f.ref}`, d: od, color, mode: switched ? 'fade' : f.was_active ? 'flow' : 'dashed' })
          const nd = path(from, newKey)
          if (nd && (failed ? !!f.new : st >= liveAt('fly-add')))
            lines.push({
              key: `fn${l.job}${f.route}${f.ref}`,
              d: nd,
              color,
              mode: failed ? 'fail' : switched ? (f.was_active ? 'flow' : 'dashed') : f.alive ? 'solid' : l.stage === 'fly-add' ? 'draw' : 'wait',
            })
        }
      // Конвейер (и сайт) — когда перенос его переключает.
      if (showDeploy && l.pipeline && !failed && st >= liveAt('pipeline')) {
        const from = `pipe:${l.pipeline}`
        const color = pipeColor.get(l.pipeline) ?? 'var(--accent, #1677ff)'
        for (const n of names) {
          const oldKey = tileKey(l.from, 'container', n)
          const newKey = tileKey(l.to, 'container', n)
          suppress.add(`${from}|${oldKey}`)
          suppress.add(`${from}|${newKey}`)
          const od = path(from, oldKey)
          if (od) lines.push({ key: `po${l.job}${n}`, d: od, color, mode: 'fade' })
          const nd = path(from, newKey)
          if (nd) lines.push({ key: `pn${l.job}${n}`, d: nd, color, mode: l.stage === 'pipeline' ? 'draw' : 'solid' })
        }
        const b = names.length ? layout.tilePos.get(tileKey(l.to, 'container', names[0])) : undefined
        if (b && (l.site ?? []).length && l.site_state) {
          labels.push({
            key: `s${l.job}`,
            x: b.x - 8,
            y: b.y - 4,
            text: t(`hostsMap.live.site.${l.site_state}`, { name: l.site![0] }),
            color: l.site_state === 'failed' ? 'var(--status-critical)' : l.site_state === 'ok' ? 'var(--status-good)' : 'var(--accent, #1677ff)',
          })
        }
      }
    }
    return { lines, transfers, labels, suppress }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [live, layout, showFly, showDeploy, routeColor, pipeColor, t])
  const liveOf = (hostId: number, name: string, side: 'from' | 'to') =>
    live.find((l) => (side === 'from' ? l.from : l.to) === hostId && (l.containers ?? []).includes(name))

  // PNG всей карты: копия SVG без сдвига и масштаба, цвета темы —
  // значениями, сверху дата и легенда; в приватном режиме чувствительное
  // заменено «•••» (размывка CSS в картинку не переносится).
  async function exportPng() {
    const src = svgRef.current
    if (!src) return
    setExporting(true)
    try {
      const css = getComputedStyle(document.documentElement)
      // var(--имя, запасное) — значением темы; запасное бывает со своими
      // скобками (rgba(…)), поэтому — со счётом скобок, не регуляркой.
      const resolve = (v: string | null): string | null => {
        if (!v) return v
        let out = v
        for (let i = out.indexOf('var('); i >= 0; i = out.indexOf('var(')) {
          let depth = 0
          let j = i + 3
          for (; j < out.length; j++) {
            if (out[j] === '(') depth++
            else if (out[j] === ')' && --depth === 0) break
          }
          const inner = out.slice(i + 4, j)
          const comma = inner.indexOf(',')
          const name = (comma >= 0 ? inner.slice(0, comma) : inner).trim()
          const def = comma >= 0 ? inner.slice(comma + 1).trim() : ''
          const val = css.getPropertyValue(name).trim() || def || 'transparent'
          out = out.slice(0, i) + val + out.slice(j + 1)
        }
        return out
      }
      const PAD = 16
      const font = getComputedStyle(document.body).fontFamily
      // Шапка: дата и легенда строками по ширине карты.
      const W = layout.width + PAD * 2
      const legend: { x: number; y: number; color: string; label: string; secret: boolean }[] = []
      let lx = PAD
      let ly = PAD + 34
      for (const n of layout.left) {
        const label = midEllipsis(n.label, 36)
        const w = 18 + label.length * 7 + 18
        if (lx + w > W - PAD) {
          lx = PAD
          ly += 20
        }
        legend.push({ x: lx, y: ly, color: n.color, label, secret: n.key.startsWith('route:') })
        lx += w
      }
      const headH = (layout.left.length ? ly + 14 : PAD + 24) + 8
      const H = layout.height + headH + PAD
      const ns = 'http://www.w3.org/2000/svg'
      const out = document.createElementNS(ns, 'svg')
      out.setAttribute('xmlns', ns)
      out.setAttribute('width', String(W))
      out.setAttribute('height', String(H))
      out.setAttribute('viewBox', `0 0 ${W} ${H}`)
      out.setAttribute('font-family', font)
      const bg = document.createElementNS(ns, 'rect')
      bg.setAttribute('width', String(W))
      bg.setAttribute('height', String(H))
      bg.setAttribute('fill', resolve('var(--surface, #ffffff)')!)
      out.appendChild(bg)
      const text = (x: number, y: number, s: string, size: number, color: string, weight = 400) => {
        const el = document.createElementNS(ns, 'text')
        el.setAttribute('x', String(x))
        el.setAttribute('y', String(y))
        el.setAttribute('font-size', String(size))
        el.setAttribute('font-weight', String(weight))
        el.setAttribute('fill', resolve(color)!)
        el.textContent = s
        out.appendChild(el)
      }
      text(PAD, PAD + 14, new Date().toLocaleString(), 14, 'var(--text-primary, #222)', 600)
      for (const l of legend) {
        const r = document.createElementNS(ns, 'rect')
        r.setAttribute('x', String(l.x))
        r.setAttribute('y', String(l.y - 10))
        r.setAttribute('width', '12')
        r.setAttribute('height', '12')
        r.setAttribute('rx', '2')
        r.setAttribute('fill', l.color)
        out.appendChild(r)
        text(l.x + 18, l.y, privacy && l.secret ? '•••' : l.label, 12, 'var(--text-primary, #222)')
      }
      const g = (src.querySelector(':scope > g') as SVGGElement).cloneNode(true) as SVGGElement
      g.setAttribute('transform', `translate(${PAD},${headH})`)
      g.querySelectorAll('title, animate, animateMotion, animateTransform').forEach((el) => el.remove())
      for (const el of [g, ...Array.from(g.querySelectorAll('*'))] as Element[]) {
        for (const a of ['fill', 'stroke']) {
          const v = el.getAttribute(a)
          if (v) el.setAttribute(a, resolve(v)!)
        }
        const st = el.getAttribute('style')
        if (st) el.setAttribute('style', resolve(st)!)
        if (privacy && el.classList.contains('sensitive')) el.textContent = '•••'
        el.removeAttribute('class')
      }
      out.appendChild(g)
      const xml = new XMLSerializer().serializeToString(out)
      const url = URL.createObjectURL(new Blob([xml], { type: 'image/svg+xml;charset=utf-8' }))
      try {
        const img = new Image()
        await new Promise<void>((ok, fail) => {
          img.onload = () => ok()
          img.onerror = () => fail(new Error('svg'))
          img.src = url
        })
        // 2×, но не больше ~16 000 точек по стороне (предел браузера).
        const scale = Math.max(0.5, Math.min(2, 16000 / Math.max(W, H)))
        const canvas = document.createElement('canvas')
        canvas.width = Math.round(W * scale)
        canvas.height = Math.round(H * scale)
        const ctx = canvas.getContext('2d')!
        ctx.scale(scale, scale)
        ctx.drawImage(img, 0, 0)
        const blob = await new Promise<Blob | null>((ok) => canvas.toBlob(ok, 'image/png'))
        if (!blob) throw new Error('png')
        const d = new Date()
        const pad2 = (n: number) => String(n).padStart(2, '0')
        const a = document.createElement('a')
        a.href = URL.createObjectURL(blob)
        a.download = `hosts-map-${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}-${pad2(d.getHours())}${pad2(d.getMinutes())}.png`
        document.body.appendChild(a)
        a.click()
        a.remove()
        window.setTimeout(() => URL.revokeObjectURL(a.href), 10_000)
      } finally {
        URL.revokeObjectURL(url)
      }
    } finally {
      setExporting(false)
    }
  }

  // Первый показ (вид ещё не запоминался) — вся карта в окно.
  const fitted = useRef(store.get<unknown>('view', null) !== null)
  useEffect(() => {
    if (!fitted.current && data.data && boxRef.current) {
      fitted.current = true
      fit()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.data])

  const height = Math.min(Math.max(320, layout.height * view.k + 20), Math.round(window.innerHeight * 0.7))
  const clickable = (fn: () => void) => ({
    onClick: (e: React.MouseEvent) => {
      if (drag.current?.moved) return
      e.stopPropagation()
      fn()
    },
  })

  return (
    <div className="col" style={{ gap: '0.5rem' }}>
      {/* В узком окне второстепенное — в «☰» (OverflowBar): масштаб, поиск,
          перенос и обновление — всегда снаружи. */}
      <OverflowBar
        items={[
          {
            key: 'zoom',
            keep: true,
            node: (
              <Space.Compact size="small">
                <Button size="small" onClick={() => zoomBy(1 / 1.25)}>
                  −
                </Button>
                <Button size="small" onClick={() => setView((v) => ({ ...v, k: 1 }))}>
                  {Math.round(view.k * 100)}%
                </Button>
                <Button size="small" onClick={() => zoomBy(1.25)}>
                  +
                </Button>
              </Space.Compact>
            ),
          },
          {
            key: 'fit',
            keep: true,
            node: (
              <Button size="small" onClick={fit}>
                {t('hostsMap.fit')}
              </Button>
            ),
          },
          {
            key: 'cols',
            active: cols !== 'auto',
            node: (
              <Tooltip title={t('hostsMap.colsHint', { n: ONE_COLUMN_MAX })}>
                <span className="row small" style={{ gap: '0.3rem', alignItems: 'center' }}>
                  {t('hostsMap.cols')}
                  <Segmented
                    size="small"
                    value={cols}
                    onChange={(v) => setCols(v as 'auto' | '1' | '2')}
                    options={[
                      { value: 'auto', label: t('hostsMap.colsAuto') },
                      { value: '1', label: '1' },
                      { value: '2', label: '2' },
                    ]}
                  />
                </span>
              </Tooltip>
            ),
          },
          {
            key: 'expand',
            active: expandAll,
            node: (
              <Checkbox checked={expandAll} onChange={(e) => setExpandAll(e.target.checked)}>
                {t('hostsMap.expandAll')}
              </Checkbox>
            ),
          },
          {
            key: 'search',
            keep: true,
            node: <Input size="small" allowClear placeholder={t('hostsMap.search')} value={q} onChange={(e) => setQ(e.target.value)} style={{ width: 160 }} />,
          },
          {
            key: 'running',
            active: onlyRunning,
            node: (
              <Checkbox checked={onlyRunning} onChange={(e) => setOnlyRunning(e.target.checked)}>
                {t('hostsMap.onlyRunning')}
              </Checkbox>
            ),
          },
          ...(admin
            ? [
                {
                  key: 'move',
                  keep: true,
                  node: (
                    <Tooltip title={t('hostsMap.moveHint')}>
                      <Button size="small" type={moveMode ? 'primary' : 'default'} disabled={settling} onClick={() => setMoveMode(!moveMode)}>
                        {t('hostsMap.moveMode')}
                      </Button>
                    </Tooltip>
                  ),
                },
              ]
            : []),
          ...(moveMode
            ? [
                {
                  key: 'applyNow',
                  active: applyNow,
                  node: (
                    <Tooltip title={drafts.length ? t('hostsMap.applyNowDraft') : t('hostsMap.applyNowHint')}>
                      <span className="row small" style={{ gap: '0.3rem', alignItems: 'center' }}>
                        <Switch size="small" checked={applyNow} disabled={drafts.length > 0 || settling} onChange={setApplyNow} />
                        {t('hostsMap.applyNow')}
                      </span>
                    </Tooltip>
                  ),
                },
              ]
            : []),
          ...(drafts.length > 0
            ? [
                {
                  key: 'drafts',
                  keep: true,
                  node: (
                    <span className="row" style={{ gap: '0.3rem' }}>
                      <Button size="small" type="primary" danger disabled={settling} onClick={() => void applyDrafts()}>
                        {t('hostsMap.applyMoves', { n: drafts.length })}
                      </Button>
                      <Button size="small" onClick={() => setDrafts([])}>
                        {t('hostsMap.discardMoves')}
                      </Button>
                    </span>
                  ),
                },
              ]
            : []),
          ...moving.map((m) => ({
            key: `m${m.id}`,
            keep: true,
            node: (
              <Tag color="processing" style={{ cursor: 'pointer', marginInlineEnd: 0 }} onClick={() => onOpenJob?.(m.id)}>
                ⟳ {m.title} · {Math.round((Date.now() - m.started) / 1000)} {t('hostsMap.sec')}
              </Tag>
            ),
          })),
          ...movesFailed.map((id) => ({
            key: `f${id}`,
            keep: true,
            node: (
              <Tag color="error" closable onClose={() => setMovesFailed((f) => f.filter((x) => x !== id))} style={{ cursor: 'pointer', marginInlineEnd: 0 }} onClick={() => onOpenJob?.(id)}>
                {t('hostsMap.moveFailed', { id })}
              </Tag>
            ),
          })),
          {
            key: 'linked',
            active: onlyLinked,
            node: (
              <Checkbox checked={onlyLinked} onChange={(e) => setOnlyLinked(e.target.checked)}>
                {t('hostsMap.onlyLinked')}
              </Checkbox>
            ),
          },
          {
            key: 'png',
            node: (
              <Tooltip title={privacy ? t('hostsMap.pngPrivacy') : t('hostsMap.pngHint')}>
                <Button size="small" disabled={!data.data || exporting} loading={exporting} onClick={() => void exportPng()}>
                  PNG
                </Button>
              </Tooltip>
            ),
          },
          {
            key: 'refresh',
            keep: true,
            node: (
              <Tooltip title={t('hostsMap.refreshHint')}>
                <Button size="small" icon={<ReloadOutlined />} loading={refreshing || data.loading} onClick={() => void refresh()} />
              </Tooltip>
            ),
          },
        ]}
      />
      {/* Легенда: какие связи показывать и чей цвет; в узком окне — одной
          кнопкой «Легенда (N)». */}
      <OverflowBar
        gap="0.6rem"
        label={t('hostsMap.legend', { n: layout.left.length })}
        items={[
          {
            key: 'fly',
            active: !showFly,
            node: (
              <Checkbox checked={showFly} onChange={(e) => setShowFly(e.target.checked)}>
                <span className="small">Traffic Fly</span>
              </Checkbox>
            ),
          },
          {
            key: 'deploy',
            active: !showDeploy,
            node: (
              <Checkbox checked={showDeploy} onChange={(e) => setShowDeploy(e.target.checked)}>
                <span className="small">{t('hostsMap.deployments')}</span>
              </Checkbox>
            ),
          },
          ...layout.left.map((n) => ({
            key: n.key,
            node: (
              <span
                className="row small"
                style={{ gap: 4, alignItems: 'center', cursor: 'pointer', opacity: dim(n.key) < 1 ? 0.5 : 1 }}
                onMouseEnter={() => setHover(n.key)}
                onMouseLeave={() => setHover(null)}
                onClick={() => setPinned(pinned === n.key ? null : n.key)}
              >
                <span style={{ width: 10, height: 10, borderRadius: 2, background: n.color, display: 'inline-block' }} />
                <span className={n.key.startsWith('route:') ? 'sensitive' : undefined}>{midEllipsis(n.label, 28)}</span>
              </span>
            ),
          })),
          { key: 'lines', node: <span className="small muted">{t('hostsMap.legendLines')}</span> },
        ]}
      />
      {data.error && <Banner kind="error">{data.error}</Banner>}
      {!data.data ? (
        <Loading what={t('hostsMap.title')} />
      ) : (
        <div
          ref={boxCallback}
          style={{ position: 'relative', height, overflow: 'hidden', border: '1px solid var(--border)', borderRadius: 8, background: 'var(--surface)', cursor: drag.current ? 'grabbing' : 'grab', touchAction: 'none', userSelect: 'none' }}
          onPointerDown={(e) => {
            if (e.button !== 0) return
            drag.current = { sx: e.clientX, sy: e.clientY, x: view.x, y: view.y, moved: false }
          }}
          onPointerLeave={() => {
            drag.current = null
          }}
          onPointerMove={(e) => {
            const d = drag.current
            if (!d) return
            if (!d.moved && Math.hypot(e.clientX - d.sx, e.clientY - d.sy) < 4) return
            d.moved = true
            setView((v) => ({ ...v, x: d.x + e.clientX - d.sx, y: d.y + e.clientY - d.sy }))
          }}
          onPointerUp={() => {
            const d = drag.current
            // Щелчок по пустому месту снимает закреплённую подсветку.
            window.setTimeout(() => {
              if (d && !d.moved) setPinned(null)
              drag.current = null
            }, 0)
          }}
        >
          <svg ref={svgRef} width="100%" height="100%" role="img" aria-label={t('hostsMap.title')}>
            <g transform={`translate(${view.x},${view.y}) scale(${view.k})`}>
              {visibleLinks.map((l, i) => {
                if (liveOverlay.suppress.has(`${l.from}|${l.to}`)) return null
                const d = linkPath(l)
                if (!d) return null
                const on = related ? related.has(l.from) && related.has(l.to) : true
                return (
                  <path
                    key={i}
                    d={d}
                    fill="none"
                    stroke={l.color}
                    strokeWidth={on && related ? 2.5 : 1.6}
                    strokeDasharray={l.dashed ? '6 4' : undefined}
                    opacity={on ? (l.dashed ? 0.6 : 0.85) : 0.08}
                  />
                )
              })}
              {liveOverlay.lines.map((x) => (
                <LiveLine key={x.key} d={x.d} color={x.color} mode={x.mode} still={reduceMotion} />
              ))}
              {liveOverlay.transfers.map((x) => (
                <g key={x.key} pointerEvents="none">
                  <path d={x.d} fill="none" stroke="var(--accent, #1677ff)" strokeWidth={2} strokeDasharray="4 4" opacity={0.7} />
                  {!reduceMotion &&
                    [0, 0.4, 0.8].map((b) => (
                      <circle key={b} r={4} fill="var(--accent, #1677ff)">
                        <animateMotion dur="1.2s" begin={`${b}s`} repeatCount="indefinite" path={x.d} />
                      </circle>
                    ))}
                  <rect x={x.x - 6} y={x.y - 11} width={x.label.length * 7 + 14} height={18} rx={9} fill="var(--surface-raised)" stroke="var(--accent, #1677ff)" />
                  <text x={x.x} y={x.y + 2} fontSize={11} fill="var(--accent, #1677ff)">
                    {x.label}
                  </text>
                </g>
              ))}
              {layout.left.map((n) => (
                <g
                  key={n.key}
                  transform={`translate(${LEFT_X},${n.y})`}
                  style={{ cursor: 'pointer', opacity: dim(n.key) }}
                  onMouseEnter={() => setHover(n.key)}
                  onMouseLeave={() => setHover(null)}
                  {...clickable(() => setPinned(pinned === n.key ? null : n.key))}
                >
                  {!privacy && <title>{`${n.label} — ${n.sub}`}</title>}
                  <rect width={LEFT_W} height={NODE_H} rx={7} fill="var(--surface-raised)" stroke={n.color} strokeWidth={pinned === n.key ? 3 : 2} />
                  <rect width={6} height={NODE_H} rx={3} fill={n.color} />
                  <text x={14} y={16} fontSize={12} fill="var(--text-primary)" className={n.key.startsWith('route:') ? 'sensitive' : undefined}>
                    {midEllipsis(n.label, 32)}
                  </text>
                  <text x={14} y={30} fontSize={10} fill={n.bad ? 'var(--status-critical)' : 'var(--text-secondary)'} className={n.key.startsWith('route:') ? 'sensitive' : undefined}>
                    {midEllipsis(n.sub, 38)}
                  </text>
                </g>
              ))}
              {layout.blocks.map((b) => (
                <g key={b.host.id} data-map-host={b.host.id} style={{ opacity: dim(`host:${b.host.id}`) < 1 && b.tiles.every((x) => dim(x.key) < 1) ? 0.5 : 1 }}>
                  <rect
                    x={b.x}
                    y={b.y}
                    width={BLOCK_W}
                    height={b.h}
                    rx={9}
                    fill={overHost === b.host.id ? 'var(--surface-hover, #e8f0fe)' : 'var(--wash, rgba(0,0,0,0.03))'}
                    stroke={overHost === b.host.id ? 'var(--accent, #1677ff)' : b.offline ? 'var(--text-muted)' : 'var(--border-strong, #bbb)'}
                    strokeWidth={overHost === b.host.id ? 3 : 1}
                    strokeDasharray={b.offline ? '6 4' : undefined}
                  />
                  <g style={{ cursor: 'pointer' }} {...clickable(() => onOpenHost(b.host))} onMouseEnter={() => setHover(`host:${b.host.id}`)} onMouseLeave={() => setHover(null)}>
                    <circle cx={b.x + 14} cy={b.y + 17} r={5} fill={b.offline ? 'var(--status-critical)' : 'var(--status-good)'} />
                    <text x={b.x + 26} y={b.y + 21} fontSize={13} fontWeight={600} fill="var(--text-primary)" className="sensitive">
                      {midEllipsis(b.host.name, 40)}
                    </text>
                    <text x={b.x + BLOCK_W - 10} y={b.y + 21} fontSize={10} textAnchor="end" fill="var(--text-secondary)">
                      {moving.some((m) => m.to === b.host.id)
                        ? t('hostsMap.incoming', { names: moving.filter((m) => m.to === b.host.id).map((m) => m.title).join(', ') })
                        : b.offline
                          ? t('hostsMap.offline')
                          : b.error
                            ? t('hostsMap.noAnswer')
                            : t('hostsMap.count', { n: b.tiles.length + b.more })}
                    </text>
                  </g>
                  {!b.offline && !b.error && b.tiles.length === 0 && b.more === 0 && (
                    <text x={b.x + 14} y={b.y + HEAD_H + 16} fontSize={11} fill="var(--text-muted)">
                      {t('hostsMap.empty')}
                    </text>
                  )}
                  {b.error && (
                    <text x={b.x + 14} y={b.y + HEAD_H + 16} fontSize={11} fill="var(--status-critical)">
                      {midEllipsis(b.error, 90)}
                    </text>
                  )}
                  {b.stacks.map((f) => {
                    const hot = tileDrag?.host === b.host.id && tileDrag.project === f.project
                    return (
                      <g key={`s:${f.project}`}>
                        <rect x={f.x} y={f.y} width={f.w} height={f.h} rx={8} fill="none" stroke={hot ? 'var(--accent, #1677ff)' : 'var(--border-strong, #bbb)'} strokeWidth={hot ? 2 : 1} strokeDasharray={hot ? undefined : '4 3'} />
                        <text x={f.x + 8} y={f.y + 12} fontSize={10} fill={hot ? 'var(--accent, #1677ff)' : 'var(--text-secondary)'}>
                          {midEllipsis(`${t('hostsMap.stack')} ${f.project}`, 60)}
                        </text>
                      </g>
                    )
                  })}
                  {b.tiles.map((x) => {
                    const it = x.item
                    if (it.ghost) {
                      // Призрак: стек переезжает сюда, на хосте его ещё нет.
                      const l = live.find((v) => v.job === it.ghost)
                      const fromName = hosts.find((h) => h.id === l?.from)?.name ?? ''
                      return (
                        <g key={x.key} transform={`translate(${x.x},${x.y})`} pointerEvents="none">
                          <rect width={TILE_W} height={TILE_H} rx={6} fill="var(--surface-raised)" stroke="var(--accent, #1677ff)" strokeWidth={2} strokeDasharray="6 4" opacity={0.85}>
                            {!reduceMotion && <animate attributeName="opacity" values="0.45;0.95;0.45" dur="1.6s" repeatCount="indefinite" />}
                          </rect>
                          <text x={8} y={17} fontSize={9} fill="var(--text-secondary)">
                            docker
                          </text>
                          <text x={50} y={17} fontSize={12} fill="var(--text-secondary)">
                            {midEllipsis(it.name, 20)}
                          </text>
                          <text x={8} y={35} fontSize={10} fill="var(--accent, #1677ff)">
                            {midEllipsis(
                              l?.stage === 'copy'
                                ? t('hostsMap.live.ghostCopy', { host: fromName, p: l.percent })
                                : l?.stage === 'unpack'
                                  ? t('hostsMap.live.ghostStart')
                                  : t('hostsMap.live.ghost', { host: fromName }),
                              34,
                            )}
                          </text>
                        </g>
                      )
                    }
                    const flyMarks = visibleLinks.filter((l) => l.to === x.key && l.from.startsWith('route:'))
                    const pipeMarks = visibleLinks.filter((l) => l.to === x.key && l.from.startsWith('pipe:'))
                    const sub = [
                      it.kind === 'container' ? it.image : it.os_info?.name || it.os_info?.id,
                      it.ip,
                      it.ports?.length ? it.ports.slice(0, 3).join(',') : '',
                      it.cpus ? `${it.cpus} CPU` : '',
                      it.mem_bytes ? `${Math.round(it.mem_bytes / (1 << 20))} MB` : '',
                    ]
                      .filter(Boolean)
                      .join(' · ')
                    const kindLabel = it.kind === 'container' ? 'docker' : it.kind === 'vm' ? 'VM' : 'LXD'
                    const mig = it.kind === 'container' ? migratedHere(b.host.id, it.name) : null
                    const movable = moveMode && !settling && it.kind === 'container' && !b.offline && mig?.role !== 'from'
                    const dragged = tileDrag?.host === b.host.id && (tileDrag.project ? it.kind === 'container' && it.project === tileDrag.project : it.name === tileDrag.name)
                    return (
                      <g
                        key={x.key}
                        transform={`translate(${x.x},${x.y})`}
                        style={{ cursor: movable ? 'grab' : 'pointer', opacity: dim(x.key) }}
                        onMouseEnter={() => setHover(x.key)}
                        onMouseLeave={() => setHover(null)}
                        onPointerDown={
                          moveMode && mig?.role === 'from'
                            ? (e) => e.stopPropagation() // старая копия: щелчок — меню, не сдвиг карты
                            : movable
                            ? (e) => {
                                if (e.button !== 0) return
                                // Не сдвиг карты: в режиме переноса плитку тащат на хост.
                                e.stopPropagation()
                                tilePress.current = { from: b.host, item: it, sx: e.clientX, sy: e.clientY, started: false }
                              }
                            : undefined
                        }
                        {...clickable(() => (mig?.role === 'from' || mig?.role === 'second' ? (admin ? setRecMenu(mig.rec) : undefined) : moveMode ? undefined : onOpenAt(b.host, itemPath(it))))}
                      >
                        {!privacy && <title>{`${it.name}${it.project ? ` · ${t('hostsMap.stack')} ${it.project}` : ''}\n${sub}${it.state ? `\n${it.state}` : ''}`}</title>}
                        <rect
                          width={TILE_W}
                          height={TILE_H}
                          rx={6}
                          fill="var(--surface-raised)"
                          stroke={
                            dragged || movingTile(b.host.id, it.name) || draftTile(b.host.id, it.name)
                              ? 'var(--accent, #1677ff)'
                              : mig?.role === 'from'
                                ? 'var(--text-muted)' // старая копия — остановлена, даже если список ещё старый
                                : stateColor(it.state)
                          }
                          strokeWidth={dragged ? 3 : 2}
                          strokeDasharray={draftTile(b.host.id, it.name) || mig?.role === 'from' ? '5 3' : undefined}
                        />
                        {movingTile(b.host.id, it.name) && (
                          <g transform={`translate(${TILE_W - 12},${TILE_H - 12})`}>
                            <circle r={6} fill="none" stroke="var(--border, #ccc)" strokeWidth={2} />
                            <path d="M0,-6 A6,6 0 0,1 6,0" fill="none" stroke="var(--accent, #1677ff)" strokeWidth={2}>
                              <animateTransform attributeName="transform" type="rotate" from="0" to="360" dur="1s" repeatCount="indefinite" />
                            </path>
                          </g>
                        )}
                        <text x={8} y={17} fontSize={9} fill="var(--text-secondary)">
                          {kindLabel}
                        </text>
                        <text x={it.kind === 'container' ? 50 : 32} y={17} fontSize={12} fill="var(--text-primary)">
                          {midEllipsis(it.name, it.kind === 'container' ? 20 : 23)}
                        </text>
                        {planning?.from === b.host.id && planning.name === it.name ? (
                          <text x={8} y={35} fontSize={10} fill="var(--accent, #1677ff)">
                            {midEllipsis(t('hostsMap.planning', { host: planning.to }), 34)}
                          </text>
                        ) : movingTile(b.host.id, it.name) || draftTile(b.host.id, it.name) ? (
                          <text x={8} y={35} fontSize={10} fill="var(--accent, #1677ff)">
                            {midEllipsis(
                              movingTile(b.host.id, it.name)
                                ? t('hostsMap.movingTo', { host: hosts.find((h) => h.id === movingTile(b.host.id, it.name)!.to)?.name ?? '' }) +
                                    (liveOf(b.host.id, it.name, 'from')?.stage === 'copy' ? ` · ${liveOf(b.host.id, it.name, 'from')!.percent}%` : '')
                                : t('hostsMap.plannedTo', { host: draftTile(b.host.id, it.name)!.to.name }),
                              34,
                            )}
                          </text>
                        ) : mig ? (
                          <text x={8} y={35} fontSize={10} fill={mig.role === 'to' ? 'var(--accent, #1677ff)' : mig.role === 'second' ? 'var(--status-critical)' : 'var(--status-warning)'}>
                            {midEllipsis(
                              mig.role === 'from'
                                ? t('hostsMap.migratedFrom', { host: mig.rec.to_name, date: mig.rec.ts.slice(0, 10) })
                                : mig.role === 'second'
                                  ? t('hostsMap.secondCopy', { host: mig.rec.from_name })
                                  : t('hostsMap.migratedTo', { host: mig.rec.from_name, date: mig.rec.ts.slice(0, 10) }),
                              34,
                            )}
                          </text>
                        ) : (
                          <text x={8} y={35} fontSize={10} fill="var(--text-secondary)" className={it.ip ? 'sensitive' : undefined}>
                            {midEllipsis(sub, 32)}
                          </text>
                        )}
                        {/* Метки связей: маршрут Traffic Fly — кружок (пустой — резерв,
                            мигает — не жива), конвейер — квадрат. Больше трёх — два
                            кружка и «+N» (красный и мигает, если среди скрытых есть
                            упавшая цель); весь список — в подсказке. */}
                        {(() => {
                          const old = mig?.role === 'from'
                          const shown = flyMarks.length > MARKS_MAX ? flyMarks.slice(0, MARKS_MAX - 1) : flyMarks
                          const hidden = flyMarks.slice(shown.length)
                          const hiddenDead = !old && hidden.some((l) => l.dead)
                          const width = shown.length * 14 + (hidden.length ? 26 : 0)
                          const tip = flyMarks
                            .map((l) => {
                              const r = routesById.get(l.from.slice('route:'.length))
                              const name = r ? routeTitle(r) : l.from
                              const role = old ? t('hostsMap.flyOldCopy') : l.dashed ? t('hostsMap.flyReserve') : t('hostsMap.flyActive')
                              return `${name} · ${role}${l.dead && !old ? ` · ${t('hostsMap.flyDead')}` : ''}`
                            })
                            .join('\n')
                          return (
                            <g>
                              {!privacy && flyMarks.length > 0 && <title>{tip}</title>}
                              {shown.map((l, i) =>
                                old ? (
                                  // Старая копия после переноса — резерв, остановлена
                                  // намеренно: не мигает, серая.
                                  <circle key={`f${i}`} cx={TILE_W - 10 - i * 14} cy={11} r={5} fill="var(--surface-raised)" stroke="var(--text-muted)" strokeWidth={2} />
                                ) : (
                                  <circle key={`f${i}`} cx={TILE_W - 10 - i * 14} cy={11} r={5} fill={l.dashed ? 'var(--surface-raised)' : l.color} stroke={l.color} strokeWidth={2}>
                                    {l.dead && <animate attributeName="opacity" values="1;0.3;1" dur="1.5s" repeatCount="indefinite" />}
                                  </circle>
                                ),
                              )}
                              {hidden.length > 0 && (
                                <g transform={`translate(${TILE_W - 6 - shown.length * 14 - 22},4)`}>
                                  <rect width={22} height={14} rx={7} fill={hiddenDead ? 'var(--status-critical)' : 'var(--surface-raised)'} stroke={hiddenDead ? 'var(--status-critical)' : 'var(--border-strong, #bbb)'} />
                                  <text x={11} y={10.5} fontSize={9} textAnchor="middle" fill={hiddenDead ? '#fff' : 'var(--text-secondary)'}>
                                    +{hidden.length}
                                  </text>
                                  {hiddenDead && <animate attributeName="opacity" values="1;0.35;1" dur="1.5s" repeatCount="indefinite" />}
                                </g>
                              )}
                              {pipeMarks.slice(0, 1).map((l) => (
                                <rect key="p" x={TILE_W - 15 - width} y={6} width={10} height={10} rx={2} fill={l.color} />
                              ))}
                            </g>
                          )
                        })()}
                      </g>
                    )
                  })}
                  {b.moreAt && (
                    <g
                      transform={`translate(${b.moreAt.x},${b.moreAt.y})`}
                      style={{ cursor: 'pointer' }}
                      {...clickable(() => setExpanded((s) => new Set([...s, b.host.id])))}
                    >
                      <rect width={TILE_W} height={TILE_H} rx={6} fill="var(--surface-raised)" stroke="var(--border-strong, #bbb)" strokeDasharray="5 4" />
                      {(() => {
                        // Скрытые в плане или переносе — видно и на свёрнутом хосте.
                        const k = b.hidden.filter((it) => movingTile(b.host.id, it.name) || draftTile(b.host.id, it.name)).length
                        return (
                          <>
                            <text x={TILE_W / 2} y={k ? 22 : 28} fontSize={12} textAnchor="middle" fill="var(--accent, #1677ff)">
                              {t('hostsMap.more', { n: b.more })}
                            </text>
                            {k > 0 && (
                              <text x={TILE_W / 2} y={37} fontSize={10} textAnchor="middle" fill="var(--accent, #1677ff)">
                                {t('hostsMap.moreMoving', { n: k })}
                              </text>
                            )}
                          </>
                        )
                      })()}
                    </g>
                  )}
                </g>
              ))}
              {liveOverlay.labels.map((x) => (
                <text key={x.key} x={x.x} y={x.y} fontSize={11} textAnchor="end" fill={x.color} pointerEvents="none" className="sensitive">
                  {x.text}
                </text>
              ))}
            </g>
          </svg>
          {settling && (
            // Карта перечитывается: видно, как перенос дорисовывается, но
            // плитки в это время не перетаскивают (movable).
            <span
              className="small"
              style={{ position: 'absolute', top: 8, right: 8, pointerEvents: 'none', padding: '4px 10px', borderRadius: 7, border: '1px solid var(--border)', background: 'var(--surface-raised)', boxShadow: '0 2px 8px rgba(0,0,0,0.15)' }}
            >
              <LoadingOutlined /> {t('hostsMap.settling')}
            </span>
          )}
        </div>
      )}
      {applyErr && <Banner kind="error">{applyErr}</Banner>}
      <span className="small muted">{moveMode ? t('hostsMap.moveModeHint') : t('hostsMap.hint')}</span>
      {tileDrag && (
        <div
          style={{
            position: 'fixed',
            left: tileDrag.x + 12,
            top: tileDrag.y + 12,
            pointerEvents: 'none',
            zIndex: 2000,
            padding: '4px 10px',
            borderRadius: 7,
            fontSize: 12,
            border: '2px solid var(--accent, #1677ff)',
            background: 'var(--surface-raised)',
            boxShadow: '0 4px 12px rgba(0,0,0,0.2)',
          }}
        >
          {tileDrag.label}
        </div>
      )}
      {migrateAsk && (
        <MigrateModal
          ask={migrateAsk}
          applyNow={applyNow}
          onClose={() => setMigrateAsk(null)}
          onConfirm={async (d) => {
            if (!applyNow) {
              setDrafts((l) => [...l.filter((x) => !(x.from.id === d.from.id && x.item.name === d.item.name)), d])
              setMigrateAsk(null)
              return
            }
            const id = await startMove(d)
            setMigrateAsk(null)
            onOpenJob?.(id)
          }}
        />
      )}
      {recMenu && (
        <MigrationMenu
          rec={recMenu}
          newOnline={hosts.find((h) => h.id === recMenu.to)?.status === 'online'}
          onClose={() => setRecMenu(null)}
          onStarted={(id) => {
            setRecMenu(null)
            onOpenJob?.(id)
            const rec = recMenu
            setMoving((l) => [...l, { id, from: rec.from, to: rec.to, names: rec.containers, title: rec.project, started: Date.now() }])
          }}
        />
      )}
    </div>
  )
}

/** Перенос в учёте хаба. */
interface MigrationRecord {
  id: string
  ts: string
  from: number
  from_name: string
  to: number
  to_name: string
  project: string
  kind: string
  containers: string[]
  volumes?: string[]
  paths?: string[]
  state: 'moved' | 'rolled_back' | 'removed' | 'returned'
  /** Откачен без нового хоста — там может работать вторая копия. */
  new_not_stopped?: boolean
}

interface MigratePreview {
  plan: {
    kind: 'compose' | 'standalone'
    project: string
    containers: { name: string; service?: string; image: string; state: string }[]
    images: string[]
    volumes: { name: string; size: number }[]
    paths: { path: string; size: number }[]
    ports: string[]
    skipped?: string[]
    /** Внешние сети стека; others — контейнеры вне стека в них. */
    networks?: { name: string; others?: string[] }[]
  }
  check: { have_images?: string[]; free_bytes: number }
  problems?: string[]
  warnings?: string[]
  fly?: { route: string; title: string; ref: string; was_active: boolean }[]
  /** Стек выложен конвейером: его хост, учёт и сайт переключаются. */
  pipeline?: { name: string; domains?: string[]; can_switch: boolean } | null
  /** Перенос обратно: остатки старой копии на новом хосте. */
  leftovers?: { record: string; host: string; ts: string; containers: string[]; paths?: string[]; volumes?: string[]; problems?: string[] }
}

/** Перенос в плане (до «Применить переносы»). */
interface DraftMove {
  from: HubHost
  to: HubHost
  item: MapItem
  names: string[]
  switchFly: boolean
  cleanLeftovers: boolean
  switchPipeline: boolean
}

/** Перенос в работе. */
/** Линия маршрута или конвейера в живом переносе. still — без движения
 * (prefers-reduced-motion): только итоговый вид. */
function LiveLine({ d, color, mode, still }: { d: string; color: string; mode: LiveMode; still: boolean }) {
  const stroke = mode === 'fail' ? 'var(--status-critical)' : color
  const dash = mode === 'dashed' || mode === 'wait' || mode === 'fade' || mode === 'fail' ? '6 4' : undefined
  if (still)
    return <path d={d} fill="none" stroke={stroke} strokeWidth={2} strokeDasharray={dash} opacity={mode === 'fade' || mode === 'fail' ? 0.25 : 0.9} pointerEvents="none" />
  if (mode === 'draw')
    return (
      <path d={d} fill="none" stroke={stroke} strokeWidth={2.5} pathLength={1} strokeDasharray="1 1" strokeDashoffset={1} pointerEvents="none">
        <animate attributeName="stroke-dashoffset" from="1" to="0" dur="1.2s" fill="freeze" />
      </path>
    )
  return (
    <g pointerEvents="none">
      <path d={d} fill="none" stroke={stroke} strokeWidth={mode === 'flow' || mode === 'solid' ? 2.5 : 2} strokeDasharray={dash} opacity={0.9}>
        {mode === 'wait' && <animate attributeName="stroke-dashoffset" from="0" to="-20" dur="0.8s" repeatCount="indefinite" />}
        {mode === 'fade' && <animate attributeName="opacity" from="0.9" to="0.2" dur="1.5s" fill="freeze" />}
        {mode === 'fail' && <animate attributeName="opacity" from="0.9" to="0" dur="3s" fill="freeze" />}
      </path>
      {mode === 'flow' &&
        [0, 0.8].map((b) => (
          <circle key={b} r={3.5} fill={stroke}>
            <animateMotion dur="1.6s" begin={`${b}s`} repeatCount="indefinite" path={d} />
          </circle>
        ))}
    </g>
  )
}

/** Ход переноса с хаба (GET /hub/migrate/live). */
interface MigrateLive {
  job: number
  from: number
  to: number
  project?: string
  containers?: string[]
  stage: string
  percent: number
  fly?: { route: string; ref: string; old: string; new?: string; was_active: boolean; alive?: boolean }[]
  pipeline?: number
  pipeline_name?: string
  site?: string[]
  site_state?: string
  started: string
}

/** Порядок шагов переноса (для «шаг уже пройден»). */
const LIVE_ORDER = ['plan', 'pack', 'copy', 'unpack', 'fly-add', 'fly-wait', 'fly-switched', 'pipeline', 'site', 'done']
const liveAt = (stage: string) => LIVE_ORDER.indexOf(stage)

/** Линия живого переноса: как рисовать. */
type LiveMode = 'flow' | 'solid' | 'dashed' | 'draw' | 'wait' | 'fade' | 'fail'

interface MovingJob {
  id: number
  from: number
  to: number
  names: string[]
  title: string
  started: number
}

const fmtSize = (n: number) => (n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GB` : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.round(n / 1024)} KB`)

/** Окно переноса: что едет, что мешает, «Перенести». */
function MigrateModal({
  ask,
  applyNow,
  onClose,
  onConfirm,
}: {
  ask: { from: HubHost; to: HubHost; item: MapItem }
  applyNow: boolean
  onClose: () => void
  onConfirm: (d: DraftMove) => Promise<void>
}) {
  const { t } = useTranslation()
  const [pv, setPv] = useState<MigratePreview | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [switchFly, setSwitchFly] = useState(true)
  const [clean, setClean] = useState(true)
  const [switchPipe, setSwitchPipe] = useState(true)
  useEffect(() => {
    api<MigratePreview>('/hub/migrate/preview', { method: 'POST', body: { from: ask.from.id, to: ask.to.id, container: ask.item.name }, timeoutMs: 120_000 })
      .then(setPv)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [ask])
  const p = pv?.plan
  // С галочкой «убрать остатки» — препятствия без них.
  const problems = (pv?.leftovers && clean ? pv.leftovers.problems : pv?.problems) ?? []
  const data = (p?.volumes ?? []).reduce((a, v) => a + v.size, 0) + (p?.paths ?? []).reduce((a, x) => a + x.size, 0)
  const have = new Set(pv?.check.have_images ?? [])
  const start = async () => {
    setBusy(true)
    setError(null)
    try {
      await onConfirm({
        from: ask.from,
        to: ask.to,
        item: ask.item,
        names: (pv?.plan.containers ?? []).map((c) => c.name),
        switchFly: switchFly && (pv?.fly ?? []).length > 0,
        cleanLeftovers: clean && !!pv?.leftovers,
        switchPipeline: switchPipe && !!pv?.pipeline?.can_switch,
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={t('hostsMap.migrateTitle', { name: ask.item.name, from: ask.from.name, to: ask.to.name })} onClose={onClose} width={640}>
      <div className="col small" style={{ gap: '0.5rem' }}>
        {!pv && !error && <Loading what={t('hostsMap.migratePlan')} />}
        {p && (
          <>
            <div>
              <strong>{p.kind === 'compose' ? t('hostsMap.migrateStack', { name: p.project, n: (p.containers ?? []).length }) : t('hostsMap.migrateSolo', { name: p.project })}</strong>
              <div className="muted">{(p.containers ?? []).map((c) => c.name).join(', ')}</div>
            </div>
            {p.kind === 'standalone' && <span className="muted">{t('hostsMap.migrateSoloHint')}</span>}
            <div>
              {t('hostsMap.migrateData', { size: fmtSize(data) })}
              <ul style={{ margin: '0.2rem 0 0', paddingLeft: '1.2rem' }}>
                {(p.volumes ?? []).map((v) => (
                  <li key={v.name}>
                    {t('hostsMap.migrateVolume')} <span className="mono">{v.name}</span> — {fmtSize(v.size)}
                  </li>
                ))}
                {(p.paths ?? []).map((x) => (
                  <li key={x.path}>
                    <span className="mono sensitive">{x.path}</span> — {fmtSize(x.size)}
                  </li>
                ))}
              </ul>
            </div>
            <div>
              {t('hostsMap.migrateImages')}{' '}
              {(p.images ?? []).map((img) => (
                <Tag key={img} color={have.has(img) ? 'success' : 'default'}>
                  {img}
                  {have.has(img) ? ` · ${t('hostsMap.migrateImageHave')}` : ''}
                </Tag>
              ))}
            </div>
            {(p.ports ?? []).length > 0 && (
              <div>
                {t('hostsMap.migratePorts')} <span className="mono">{(p.ports ?? []).join(', ')}</span>
              </div>
            )}
            {(p.networks ?? []).length > 0 && (
              <div>
                {t('hostsMap.migrateNetworks')} <span className="mono">{(p.networks ?? []).map((n) => n.name).join(', ')}</span>
              </div>
            )}
            {(p.skipped ?? []).length > 0 && <Banner kind="warn">{t('hostsMap.migrateSkipped', { list: (p.skipped ?? []).join(', ') })}</Banner>}
            {(pv?.fly ?? []).length > 0 && (
              <div>
                {t('hostsMap.migrateFly')}
                <ul style={{ margin: '0.2rem 0 0', paddingLeft: '1.2rem' }}>
                  {(pv?.fly ?? []).map((f) => (
                    <li key={f.route + f.ref}>
                      <span className="sensitive">{f.title}</span> — {f.ref} · {f.was_active ? t('hostsMap.flyActive') : t('hostsMap.flyReserve')}
                    </li>
                  ))}
                </ul>
                <Checkbox checked={switchFly} onChange={(e) => setSwitchFly(e.target.checked)}>
                  {t('hostsMap.switchFly')}
                </Checkbox>
              </div>
            )}
            {(pv?.warnings ?? []).map((w) => (
              <Banner key={w} kind="warn">
                {w}
              </Banner>
            ))}
            {pv?.pipeline && (
              <div>
                <strong>{t('hostsMap.pipelineStack', { name: pv.pipeline.name })}</strong>
                {pv.pipeline.can_switch ? (
                  <>
                    <div>
                      <Checkbox checked={switchPipe} onChange={(e) => setSwitchPipe(e.target.checked)}>
                        {t('hostsMap.switchPipeline', { host: ask.to.name })}
                      </Checkbox>
                    </div>
                    {switchPipe && (pv.pipeline.domains ?? []).length > 0 && (
                      <span className="muted">
                        {t('hostsMap.pipelineSite')} <span className="mono sensitive">{(pv.pipeline.domains ?? []).join(', ')}</span>
                      </span>
                    )}
                    {!switchPipe && <Banner kind="error">{t('hostsMap.pipelineStays', { name: pv.pipeline.name, host: ask.from.name })}</Banner>}
                  </>
                ) : (
                  <Banner kind="warn">{t('hostsMap.pipelineCannot', { name: pv.pipeline.name, host: ask.from.name })}</Banner>
                )}
              </div>
            )}
            <Banner kind="warn">{t('hostsMap.migrateDowntime')}</Banner>
            {pv?.leftovers && (
              <Banner kind="warn">
                {t('hostsMap.leftovers', { host: ask.to.name, date: pv.leftovers.ts.slice(0, 10) })}
                <div className="mono small sensitive">{[...(pv.leftovers.paths ?? []), ...(pv.leftovers.volumes ?? []), ...pv.leftovers.containers].join(', ')}</div>
                <Checkbox checked={clean} onChange={(e) => setClean(e.target.checked)}>
                  {t('hostsMap.leftoversClean')}
                </Checkbox>
              </Banner>
            )}
            {problems.length > 0 && (
              <Banner kind="error">
                {t('hostsMap.migrateProblems')}
                <ul style={{ margin: '0.2rem 0 0', paddingLeft: '1.2rem' }}>
                  {problems.map((x) => (
                    <li key={x}>{x}</li>
                  ))}
                </ul>
              </Banner>
            )}
          </>
        )}
        {error && <Banner kind="error">{error}</Banner>}
        <Space>
          <Button type="primary" danger loading={busy} disabled={!p || problems.length > 0} onClick={() => void start()}>
            {applyNow ? t('hostsMap.migrateGo') : t('hostsMap.migratePlanAdd')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </Space>
      </div>
    </Modal>
  )
}

/** Перенесённый стек на старом хосте: откатить или убрать. */
function MigrationMenu({ rec, newOnline, onClose, onStarted }: { rec: MigrationRecord; newOnline: boolean; onClose: () => void; onStarted: (jobId: number) => void }) {
  const { t } = useTranslation()
  const [withData, setWithData] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const act = async (action: 'rollback' | 'remove' | 'stopnew', skipNew = false) => {
    const ok = await confirmAction(
      action === 'stopnew'
        ? t('hostsMap.stopSecondConfirm', { name: rec.project, host: rec.to_name })
        : action === 'rollback'
          ? t(skipNew ? 'hostsMap.rollbackSkipConfirm' : 'hostsMap.rollbackConfirm', { name: rec.project, from: rec.from_name, to: rec.to_name })
          : t(withData ? 'hostsMap.removeConfirmData' : 'hostsMap.removeConfirm', { name: rec.project, host: rec.from_name }),
      { okText: action === 'stopnew' ? t('hostsMap.stopSecond') : action === 'rollback' ? t('hostsMap.rollback') : t('hostsMap.removeOld'), danger: action !== 'rollback' || skipNew },
    )
    if (!ok) return
    try {
      const res = await api<{ job_id: number }>(`/hub/migrations/${rec.id}/${action}`, { method: 'POST', body: { data: withData, skip_new: skipNew } })
      onStarted(res.job_id)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }
  if (rec.state === 'rolled_back') {
    // Вторая копия после отката без нового хоста.
    return (
      <Modal title={t('hostsMap.migratedTitle', { name: rec.project })} onClose={onClose} width={480}>
        <div className="col small" style={{ gap: '0.5rem' }}>
          <Banner kind="warn">{t('hostsMap.secondCopyText', { name: rec.project, host: rec.to_name, from: rec.from_name })}</Banner>
          <Button danger disabled={!newOnline} onClick={() => void act('stopnew')}>
            {t('hostsMap.stopSecond')}
          </Button>
          {!newOnline && <span className="muted">{t('hostsMap.newOffline', { host: rec.to_name })}</span>}
          {error && <Banner kind="error">{error}</Banner>}
        </div>
      </Modal>
    )
  }
  return (
    <Modal title={t('hostsMap.migratedTitle', { name: rec.project })} onClose={onClose} width={480}>
      <div className="col small" style={{ gap: '0.5rem' }}>
        <span>{t('hostsMap.migratedText', { from: rec.from_name, to: rec.to_name, date: rec.ts.slice(0, 16).replace('T', ' ') })}</span>
        {newOnline ? (
          <Button onClick={() => void act('rollback')}>{t('hostsMap.rollback')}</Button>
        ) : (
          <>
            <Banner kind="warn">{t('hostsMap.newOffline', { host: rec.to_name })}</Banner>
            <Button danger onClick={() => void act('rollback', true)}>
              {t('hostsMap.rollbackSkip')}
            </Button>
          </>
        )}
        <Checkbox checked={withData} onChange={(e) => setWithData(e.target.checked)}>
          {t('hostsMap.removeWithData', { list: [...(rec.volumes ?? []), ...(rec.paths ?? [])].join(', ') || '—' })}
        </Checkbox>
        {!withData && <span className="muted">{t('hostsMap.removeKeepWarn', { host: rec.from_name })}</span>}
        <Button danger onClick={() => void act('remove')}>
          {t('hostsMap.removeOld')}
        </Button>
        {error && <Banner kind="error">{error}</Banner>}
      </div>
    </Modal>
  )
}
