import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Checkbox, Input, Space, Switch, Tag, Tooltip } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
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
const HEAD_H = 34
const BLOCK_W = COLS * TILE_W + (COLS + 1) * GAP
const LEFT_W = 250
const LEFT_X = 10
const HOSTS_X = LEFT_X + LEFT_W + 120
const NODE_H = 38

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
  const [view, setView] = useState<{ x: number; y: number; k: number }>(() => store.get('view', { x: 0, y: 0, k: 1 }))
  const [hover, setHover] = useState<string | null>(null)
  const [pinned, setPinned] = useState<string | null>(null)
  // Перенос контейнера: режим (перетаскивание плитки Docker на другой
  // хост вместо сдвига карты), что тащат, куда, окно плана, меню
  // перенесённого.
  const [moveMode, setMoveMode] = useState(false)
  const [tileDrag, setTileDrag] = useState<{ x: number; y: number; label: string } | null>(null)
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
  const [tick, setTick] = useState(0)
  const [recMenu, setRecMenu] = useState<MigrationRecord | null>(null)
  const migrations = useApi<{ migrations: MigrationRecord[] }>('/hub/migrations', 60_000)
  const movingRef = useRef(moving)
  movingRef.current = moving
  const hasMoving = moving.length > 0
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
        // Перенос закончился — списки хостов мимо кэша, затем карта,
        // учёт переносов и маршруты Traffic Fly (они в ответе карты).
        await api('/hub/map?refresh=1').catch(() => undefined)
        await Promise.all([data.reload(), migrations.reload()])
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
    const res = await api<{ job_id: number }>('/hub/migrate', { method: 'POST', body: { from: d.from.id, to: d.to.id, container: d.item.name, switch_fly: d.switchFly } })
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
      setTileDrag({ x: e.clientX, y: e.clientY, label: p.item.name })
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
      if (to && to.id !== p.from.id && to.status === 'online') setMigrateAsk({ from: p.from, to, item: p.item })
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    return () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
  }, [moveMode])
  // Перенесённое: «здесь остановлен, работает там» или «перенесён сюда».
  const migratedHere = (hostId: number, name: string): { rec: MigrationRecord; role: 'from' | 'to' } | null => {
    for (const r of [...(migrations.data?.migrations ?? [])].reverse()) {
      if (r.state !== 'moved' || !r.containers.includes(name)) continue
      if (r.from === hostId) return { rec: r, role: 'from' }
      if (r.to === hostId) return { rec: r, role: 'to' }
    }
    return null
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
      more: number
      moreAt?: { x: number; y: number }
      error?: string
      offline: boolean
    }[] = []
    const tilePos = new Map<string, { x: number; y: number }>()
    const hostPos = new Map<number, { x: number; y: number }>()
    for (const h of hosts) {
      const hd = data.data?.hosts[String(h.id)]
      const offline = h.status !== 'online'
      // «Только запущенные» — и без хостов, которые не на связи.
      if (onlyRunning && (offline || hd?.error)) continue
      let items = (hd?.items ?? []).filter((it) => {
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
      const open = expandAll || expanded.has(h.id)
      const shown = open || items.length <= COLLAPSED ? items : items.slice(0, COLLAPSED - 1)
      const more = items.length - shown.length
      const cells = shown.length + (more > 0 ? 1 : 0)
      const rows = Math.max(1, Math.ceil(cells / COLS))
      const bh = HEAD_H + (offline || hd?.error || cells === 0 ? 30 : rows * (TILE_H + GAP) + GAP)
      const col = colY[0] <= colY[1] ? 0 : 1
      const bx = HOSTS_X + col * (BLOCK_W + 30)
      const by = colY[col]
      colY[col] += bh + 20
      const tiles = shown.map((item, i) => {
        const x = bx + GAP + (i % COLS) * (TILE_W + GAP)
        const y = by + HEAD_H + Math.floor(i / COLS) * (TILE_H + GAP)
        const key = tileKey(h.id, item.kind, item.name)
        tilePos.set(key, { x, y })
        return { item, key, x, y }
      })
      let moreAt: { x: number; y: number } | undefined
      if (more > 0) {
        const i = shown.length
        moreAt = { x: bx + GAP + (i % COLS) * (TILE_W + GAP), y: by + HEAD_H + Math.floor(i / COLS) * (TILE_H + GAP) }
        // Скрытые плитки — связи ведут на «+N ещё».
        for (const it of items.slice(shown.length)) tilePos.set(tileKey(h.id, it.kind, it.name), moreAt)
      }
      hostPos.set(h.id, { x: bx, y: by })
      blocks.push({ host: h, x: bx, y: by, h: bh, tiles, more, moreAt, error: hd?.error, offline })
    }
    const width = HOSTS_X + 2 * BLOCK_W + 60
    const height = Math.max(ly, colY[0], colY[1]) + 10
    return { left, blocks, tilePos, hostPos, width, height }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.data, hosts, expandAll, expanded, q, onlyRunning, onlyLinked, showFly, showDeploy, routeColor, pipeColor, t])

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
      g.querySelectorAll('title, animate').forEach((el) => el.remove())
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
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
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
        <Button size="small" onClick={fit}>
          {t('hostsMap.fit')}
        </Button>
        <Checkbox checked={expandAll} onChange={(e) => setExpandAll(e.target.checked)}>
          {t('hostsMap.expandAll')}
        </Checkbox>
        <Input size="small" allowClear placeholder={t('hostsMap.search')} value={q} onChange={(e) => setQ(e.target.value)} style={{ width: 180 }} />
        <Checkbox checked={onlyRunning} onChange={(e) => setOnlyRunning(e.target.checked)}>
          {t('hostsMap.onlyRunning')}
        </Checkbox>
        {admin && (
          <Tooltip title={t('hostsMap.moveHint')}>
            <Button size="small" type={moveMode ? 'primary' : 'default'} onClick={() => setMoveMode(!moveMode)}>
              {t('hostsMap.moveMode')}
            </Button>
          </Tooltip>
        )}
        {moveMode && (
          <Tooltip title={drafts.length ? t('hostsMap.applyNowDraft') : t('hostsMap.applyNowHint')}>
            <span className="row small" style={{ gap: '0.3rem', alignItems: 'center' }}>
              <Switch size="small" checked={applyNow} disabled={drafts.length > 0} onChange={setApplyNow} />
              {t('hostsMap.applyNow')}
            </span>
          </Tooltip>
        )}
        {drafts.length > 0 && (
          <>
            <Button size="small" type="primary" danger onClick={() => void applyDrafts()}>
              {t('hostsMap.applyMoves', { n: drafts.length })}
            </Button>
            <Button size="small" onClick={() => setDrafts([])}>
              {t('hostsMap.discardMoves')}
            </Button>
          </>
        )}
        {moving.map((m) => (
          <Tag key={m.id} color="processing" style={{ cursor: 'pointer' }} onClick={() => onOpenJob?.(m.id)}>
            ⟳ {m.title} · {Math.round((Date.now() - m.started) / 1000)} {t('hostsMap.sec')}
          </Tag>
        ))}
        {movesFailed.map((id) => (
          <Tag key={id} color="error" closable onClose={() => setMovesFailed((f) => f.filter((x) => x !== id))} style={{ cursor: 'pointer' }} onClick={() => onOpenJob?.(id)}>
            {t('hostsMap.moveFailed', { id })}
          </Tag>
        ))}
        <Checkbox checked={onlyLinked} onChange={(e) => setOnlyLinked(e.target.checked)}>
          {t('hostsMap.onlyLinked')}
        </Checkbox>
        <span style={{ flex: 1 }} />
        <Tooltip title={privacy ? t('hostsMap.pngPrivacy') : t('hostsMap.pngHint')}>
          <Button size="small" disabled={!data.data || exporting} loading={exporting} onClick={() => void exportPng()}>
            PNG
          </Button>
        </Tooltip>
        <Tooltip title={t('hostsMap.refreshHint')}>
          <Button size="small" icon={<ReloadOutlined />} loading={refreshing || data.loading} onClick={() => void refresh()} />
        </Tooltip>
      </div>
      {/* Легенда: какие связи показывать и чей цвет. */}
      <div className="row small" style={{ gap: '0.6rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <Checkbox checked={showFly} onChange={(e) => setShowFly(e.target.checked)}>
          Traffic Fly
        </Checkbox>
        <Checkbox checked={showDeploy} onChange={(e) => setShowDeploy(e.target.checked)}>
          {t('hostsMap.deployments')}
        </Checkbox>
        {layout.left.map((n) => (
          <span
            key={n.key}
            className="row"
            style={{ gap: 4, alignItems: 'center', cursor: 'pointer', opacity: dim(n.key) < 1 ? 0.5 : 1 }}
            onMouseEnter={() => setHover(n.key)}
            onMouseLeave={() => setHover(null)}
            onClick={() => setPinned(pinned === n.key ? null : n.key)}
          >
            <span style={{ width: 10, height: 10, borderRadius: 2, background: n.color, display: 'inline-block' }} />
            <span className={n.key.startsWith('route:') ? 'sensitive' : undefined}>{midEllipsis(n.label, 28)}</span>
          </span>
        ))}
        <span className="muted">{t('hostsMap.legendLines')}</span>
      </div>
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
                  {b.tiles.map((x) => {
                    const it = x.item
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
                    const movable = moveMode && it.kind === 'container' && !b.offline && mig?.role !== 'from'
                    return (
                      <g
                        key={x.key}
                        transform={`translate(${x.x},${x.y})`}
                        style={{ cursor: movable ? 'grab' : 'pointer', opacity: dim(x.key) }}
                        onMouseEnter={() => setHover(x.key)}
                        onMouseLeave={() => setHover(null)}
                        onPointerDown={
                          movable
                            ? (e) => {
                                if (e.button !== 0) return
                                // Не сдвиг карты: в режиме переноса плитку тащат на хост.
                                e.stopPropagation()
                                tilePress.current = { from: b.host, item: it, sx: e.clientX, sy: e.clientY, started: false }
                              }
                            : undefined
                        }
                        {...clickable(() => (mig?.role === 'from' && admin ? setRecMenu(mig.rec) : moveMode ? undefined : onOpenAt(b.host, itemPath(it))))}
                      >
                        {!privacy && <title>{`${it.name}${it.project ? ` · ${t('hostsMap.stack')} ${it.project}` : ''}\n${sub}${it.state ? `\n${it.state}` : ''}`}</title>}
                        <rect
                          width={TILE_W}
                          height={TILE_H}
                          rx={6}
                          fill="var(--surface-raised)"
                          stroke={movingTile(b.host.id, it.name) || draftTile(b.host.id, it.name) ? 'var(--accent, #1677ff)' : stateColor(it.state)}
                          strokeWidth={2}
                          strokeDasharray={draftTile(b.host.id, it.name) ? '5 3' : undefined}
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
                        {movingTile(b.host.id, it.name) || draftTile(b.host.id, it.name) ? (
                          <text x={8} y={35} fontSize={10} fill="var(--accent, #1677ff)">
                            {midEllipsis(
                              movingTile(b.host.id, it.name)
                                ? t('hostsMap.movingTo', { host: hosts.find((h) => h.id === movingTile(b.host.id, it.name)!.to)?.name ?? '' })
                                : t('hostsMap.plannedTo', { host: draftTile(b.host.id, it.name)!.to.name }),
                              34,
                            )}
                          </text>
                        ) : mig ? (
                          <text x={8} y={35} fontSize={10} fill={mig.role === 'from' ? 'var(--status-warning)' : 'var(--accent, #1677ff)'}>
                            {midEllipsis(
                              mig.role === 'from'
                                ? t('hostsMap.migratedFrom', { host: mig.rec.to_name, date: mig.rec.ts.slice(0, 10) })
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
                            красный центр — не жива), конвейер — квадрат. */}
                        {flyMarks.map((l, i) => (
                          <circle key={`f${i}`} cx={TILE_W - 10 - i * 14} cy={11} r={5} fill={l.dashed ? 'var(--surface-raised)' : l.color} stroke={l.color} strokeWidth={2}>
                            {l.dead && <animate attributeName="opacity" values="1;0.3;1" dur="1.5s" repeatCount="indefinite" />}
                          </circle>
                        ))}
                        {pipeMarks.slice(0, 1).map((l) => (
                          <rect key="p" x={TILE_W - 15 - flyMarks.length * 14} y={6} width={10} height={10} rx={2} fill={l.color} />
                        ))}
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
                      <text x={TILE_W / 2} y={28} fontSize={12} textAnchor="middle" fill="var(--accent, #1677ff)">
                        {t('hostsMap.more', { n: b.more })}
                      </text>
                    </g>
                  )}
                </g>
              ))}
            </g>
          </svg>
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
          docker · {tileDrag.label}
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
  state: 'moved' | 'rolled_back' | 'removed'
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
  }
  check: { have_images?: string[]; free_bytes: number }
  problems?: string[]
  fly?: { route: string; title: string; ref: string; was_active: boolean }[]
}

/** Перенос в плане (до «Применить переносы»). */
interface DraftMove {
  from: HubHost
  to: HubHost
  item: MapItem
  names: string[]
  switchFly: boolean
}

/** Перенос в работе. */
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
  useEffect(() => {
    api<MigratePreview>('/hub/migrate/preview', { method: 'POST', body: { from: ask.from.id, to: ask.to.id, container: ask.item.name }, timeoutMs: 120_000 })
      .then(setPv)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [ask])
  const p = pv?.plan
  const data = (p?.volumes ?? []).reduce((a, v) => a + v.size, 0) + (p?.paths ?? []).reduce((a, x) => a + x.size, 0)
  const have = new Set(pv?.check.have_images ?? [])
  const start = async () => {
    setBusy(true)
    setError(null)
    try {
      await onConfirm({ from: ask.from, to: ask.to, item: ask.item, names: (pv?.plan.containers ?? []).map((c) => c.name), switchFly: switchFly && (pv?.fly ?? []).length > 0 })
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
            <Banner kind="warn">{t('hostsMap.migrateDowntime')}</Banner>
            {(pv?.problems ?? []).length > 0 && (
              <Banner kind="error">
                {t('hostsMap.migrateProblems')}
                <ul style={{ margin: '0.2rem 0 0', paddingLeft: '1.2rem' }}>
                  {(pv?.problems ?? []).map((x) => (
                    <li key={x}>{x}</li>
                  ))}
                </ul>
              </Banner>
            )}
          </>
        )}
        {error && <Banner kind="error">{error}</Banner>}
        <Space>
          <Button type="primary" danger loading={busy} disabled={!p || (pv?.problems ?? []).length > 0} onClick={() => void start()}>
            {applyNow ? t('hostsMap.migrateGo') : t('hostsMap.migratePlanAdd')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </Space>
      </div>
    </Modal>
  )
}

/** Перенесённый стек на старом хосте: откатить или убрать. */
function MigrationMenu({ rec, onClose, onStarted }: { rec: MigrationRecord; onClose: () => void; onStarted: (jobId: number) => void }) {
  const { t } = useTranslation()
  const [withData, setWithData] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const act = async (action: 'rollback' | 'remove') => {
    const ok = await confirmAction(
      action === 'rollback'
        ? t('hostsMap.rollbackConfirm', { name: rec.project, from: rec.from_name, to: rec.to_name })
        : t(withData ? 'hostsMap.removeConfirmData' : 'hostsMap.removeConfirm', { name: rec.project, host: rec.from_name }),
      { okText: action === 'rollback' ? t('hostsMap.rollback') : t('hostsMap.removeOld'), danger: action === 'remove' },
    )
    if (!ok) return
    try {
      const res = await api<{ job_id: number }>(`/hub/migrations/${rec.id}/${action}`, { method: 'POST', body: { data: withData } })
      onStarted(res.job_id)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }
  return (
    <Modal title={t('hostsMap.migratedTitle', { name: rec.project })} onClose={onClose} width={480}>
      <div className="col small" style={{ gap: '0.5rem' }}>
        <span>{t('hostsMap.migratedText', { from: rec.from_name, to: rec.to_name, date: rec.ts.slice(0, 16).replace('T', ' ') })}</span>
        <Button onClick={() => void act('rollback')}>{t('hostsMap.rollback')}</Button>
        <Checkbox checked={withData} onChange={(e) => setWithData(e.target.checked)}>
          {t('hostsMap.removeWithData', { list: (rec.volumes ?? []).join(', ') || '—' })}
        </Checkbox>
        <Button danger onClick={() => void act('remove')}>
          {t('hostsMap.removeOld')}
        </Button>
        {error && <Banner kind="error">{error}</Banner>}
      </div>
    </Modal>
  )
}
