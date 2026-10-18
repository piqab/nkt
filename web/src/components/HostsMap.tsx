import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Checkbox, Input, Space, Tooltip } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Loading } from './ui'
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

export function HostsMap({ hosts, onOpenHost, onOpenAt }: { hosts: HubHost[]; onOpenHost: (h: HubHost) => void; onOpenAt: (h: HubHost, path: string) => void }) {
  const { t } = useTranslation()
  const [privacy] = usePrivacy()
  const data = useApi<MapData>('/hub/map', 60_000)
  const [refreshing, setRefreshing] = useState(false)
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
  const boxRef = useRef<HTMLDivElement>(null)

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
  const focus = hover ?? pinned
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
    const el = boxRef.current
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
  }, [])
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
        <Checkbox checked={onlyLinked} onChange={(e) => setOnlyLinked(e.target.checked)}>
          {t('hostsMap.onlyLinked')}
        </Checkbox>
        <span style={{ flex: 1 }} />
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
          ref={boxRef}
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
          <svg width="100%" height="100%" role="img" aria-label={t('hostsMap.title')}>
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
                  <text x={14} y={30} fontSize={10} fill={n.bad ? 'var(--status-critical)' : 'var(--text-secondary)'}>
                    {midEllipsis(n.sub, 38)}
                  </text>
                </g>
              ))}
              {layout.blocks.map((b) => (
                <g key={b.host.id} style={{ opacity: dim(`host:${b.host.id}`) < 1 && b.tiles.every((x) => dim(x.key) < 1) ? 0.5 : 1 }}>
                  <rect x={b.x} y={b.y} width={BLOCK_W} height={b.h} rx={9} fill="var(--wash, rgba(0,0,0,0.03))" stroke={b.offline ? 'var(--text-muted)' : 'var(--border-strong, #bbb)'} strokeDasharray={b.offline ? '6 4' : undefined} />
                  <g style={{ cursor: 'pointer' }} {...clickable(() => onOpenHost(b.host))} onMouseEnter={() => setHover(`host:${b.host.id}`)} onMouseLeave={() => setHover(null)}>
                    <circle cx={b.x + 14} cy={b.y + 17} r={5} fill={b.offline ? 'var(--status-critical)' : 'var(--status-good)'} />
                    <text x={b.x + 26} y={b.y + 21} fontSize={13} fontWeight={600} fill="var(--text-primary)" className="sensitive">
                      {midEllipsis(b.host.name, 40)}
                    </text>
                    <text x={b.x + BLOCK_W - 10} y={b.y + 21} fontSize={10} textAnchor="end" fill="var(--text-secondary)">
                      {b.offline ? t('hostsMap.offline') : b.error ? t('hostsMap.noAnswer') : t('hostsMap.count', { n: b.tiles.length + b.more })}
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
                    return (
                      <g
                        key={x.key}
                        transform={`translate(${x.x},${x.y})`}
                        style={{ cursor: 'pointer', opacity: dim(x.key) }}
                        onMouseEnter={() => setHover(x.key)}
                        onMouseLeave={() => setHover(null)}
                        {...clickable(() => onOpenAt(b.host, itemPath(it)))}
                      >
                        {!privacy && <title>{`${it.name}${it.project ? ` · ${t('hostsMap.stack')} ${it.project}` : ''}\n${sub}${it.state ? `\n${it.state}` : ''}`}</title>}
                        <rect width={TILE_W} height={TILE_H} rx={6} fill="var(--surface-raised)" stroke={stateColor(it.state)} strokeWidth={2} />
                        <text x={8} y={17} fontSize={9} fill="var(--text-secondary)">
                          {kindLabel}
                        </text>
                        <text x={it.kind === 'container' ? 50 : 32} y={17} fontSize={12} fill="var(--text-primary)">
                          {midEllipsis(it.name, it.kind === 'container' ? 20 : 23)}
                        </text>
                        <text x={8} y={35} fontSize={10} fill="var(--text-secondary)" className={it.ip ? 'sensitive' : undefined}>
                          {midEllipsis(sub, 32)}
                        </text>
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
      <span className="small muted">{t('hostsMap.hint')}</span>
    </div>
  )
}
