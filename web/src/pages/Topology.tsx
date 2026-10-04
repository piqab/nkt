import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Checkbox, InputNumber } from 'antd'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { nodeTarget, useFocusRow } from '../focus'
import { useApi } from '../api'
import type { Graph, GraphEdge, GraphNode } from '../types'
import { AIReviewCard } from '../components/AIReview'
import { hostScope } from '../api'
import { Card, ErrorNote, Loading, Modal, SeverityBadge } from '../components/ui'
import { TitleHelp } from '../components/Docs'

/**
 * The resource map is laid out in fixed columns by node kind rather than by a
 * force simulation: traffic flows left to right (внешняя сеть → сервис →
 * слушатель → пул → backend → контейнер), so the reading order matches the
 * direction requests actually travel, and the layout is stable between scans.
 */
const COLUMNS: { kind: string; titleKey: string }[] = [
  { kind: 'internet', titleKey: 'topology.colInternet' },
  { kind: 'service', titleKey: 'topology.colServices' },
  { kind: 'endpoint', titleKey: 'topology.colListeners' },
  { kind: 'undeclared', titleKey: 'topology.colUndeclared' },
  { kind: 'upstream', titleKey: 'topology.colUpstreams' },
  { kind: 'backend', titleKey: 'topology.colBackends' },
  { kind: 'k8s_ingress', titleKey: 'topology.colK8sIngress' },
  { kind: 'k8s_service', titleKey: 'topology.colK8sService' },
  { kind: 'k8s_pod', titleKey: 'topology.colK8sPod' },
  { kind: 'k8s_node', titleKey: 'topology.colK8sNode' },
  { kind: 'container', titleKey: 'topology.colContainers' },
  { kind: 'podman_container', titleKey: 'topology.colPodman' },
  { kind: 'lxd_instance', titleKey: 'topology.colLxd' },
  { kind: 'vm', titleKey: 'topology.colVms' },
  { kind: 'network', titleKey: 'topology.colNetworks' },
]

const NODE_W = 168
const NODE_H = 40
const COL_GAP = 78
const ROW_GAP = 14
const MIN_ZOOM = 0.5
const MAX_ZOOM = 3
const ZOOM_STEP = 1.25

const STATUS_COLOR: Record<string, string> = {
  ok: 'var(--status-good)',
  warn: 'var(--status-warning)',
  error: 'var(--status-critical)',
  unknown: 'var(--text-muted)',
}

// The same categorical palette the charts use elsewhere, reused here to
// tell apart several lines fanning out of one node (e.g. docker → many
// containers) — with only 8 colors, a node with more outgoing edges than
// that cycles back through them, which is fine: colors only need to be
// locally distinct among one node's own siblings, not globally unique.
const FAN_PALETTE = [
  'var(--series-1)',
  'var(--series-2)',
  'var(--series-3)',
  'var(--series-4)',
  'var(--series-5)',
  'var(--series-6)',
  'var(--series-7)',
  'var(--series-8)',
]

interface Placed extends GraphNode {
  x: number
  y: number
}

/** Свёрнутые порты одной службы: узел карты и те, кого он заменяет. */
interface PortGroup {
  key: string
  node: GraphNode
  members: GraphNode[]
}

// Слушатели одной службы сворачиваются в один узел, если их больше порога:
// nginx с двадцатью server-блоками или процесс, открывший десяток портов, —
// это одна служба, и двадцать одинаковых прямоугольников только прячут
// остальную карту.
const GROUP_KINDS = new Set(['endpoint', 'undeclared'])
const GROUP_KEY = 'nkt.topology.groupOver'
const STATUS_RANK: Record<string, number> = { error: 3, warn: 2, unknown: 1, ok: 0 }

function groupKeyOf(n: GraphNode): string {
  if (n.kind === 'endpoint') return n.group || n.meta?.service || n.label
  return `${n.label}|${n.meta?.unit ?? n.meta?.container_id ?? ''}`
}

function readGroupOver(): number {
  try {
    const v = Number(localStorage.getItem(GROUP_KEY))
    return Number.isFinite(v) && v >= 1 ? v : 4
  } catch {
    return 4
  }
}

/** Куда вести с отдельного порта в окне свёрнутой службы. */
function portTarget(n: GraphNode): { to: string; labelKey: string; name?: string } | null {
  if (n.kind === 'undeclared' && n.port) return { to: `/firewall?${new URLSearchParams({ focus: String(n.port) })}`, labelKey: 'topology.portToFirewall', name: String(n.port) }
  return nodeTarget(n)
}

export default function TopologyPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { data, error, loading } = useApi<Graph>('/topology', 120_000)
  // Чей это разбор: настоящий хост хаба или сам хаб/одиночный nkt.
  // LOCAL_HOST_ID (-1) и «хост не выбран» (null) — это 0, то есть «свой».
  const reviewHostID = Math.max(hostScope.id ?? 0, 0)
  const [selected, setSelected] = useState<string | null>(null)
  const [hovered, setHovered] = useState<string | null>(null)
  const [hideHealthy, setHideHealthy] = useState(false)
  // Остановленные службы включены в карту, но по умолчанию не показываются:
  // на обычном хосте их больше, чем работающих (systemd держит десятки
  // юнитов «на всякий случай»), и они забивают столбец, ради которого на
  // карту и смотрят.
  const [hideInactive, setHideInactive] = useState(true)
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  // Перетаскивание: указатель захвачен svg (курсор может уйти за край),
  // сдвиг применяется раз в кадр, а сдвинутая карта не считается щелчком.
  const dragRef = useRef<{ x: number; y: number; panX: number; panY: number; scale: number; moved: boolean; pointer: number } | null>(null)
  const frameRef = useRef<number | null>(null)
  const suppressClick = useRef(false)
  const [dragging, setDragging] = useState(false)
  const svgRef = useRef<SVGSVGElement>(null)
  const [groupOver, setGroupOverState] = useState(readGroupOver)
  const setGroupOver = (v: number) => {
    setGroupOverState(v)
    try {
      localStorage.setItem(GROUP_KEY, String(v))
    } catch {
      // не запомнится — не страшно
    }
  }
  // Свёрнутые службы, которые развернули из их окна.
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())

  const { placed, edges, width, height, columns, groups, memberOf } = useMemo(() => {
    const groups = new Map<string, PortGroup>()
    const memberOf = new Map<string, string>()
    if (!data) return { placed: [], edges: [], width: 100, height: 100, columns: [] as typeof COLUMNS, groups, memberOf }

    let nodes = data.nodes
    if (hideInactive) {
      // Признак берётся из подписи узла: для службы туда кладётся её
      // ActiveState (см. internal/topology). Всё, что не active, — не
      // работает прямо сейчас.
      nodes = nodes.filter((n) => !(n.kind === 'service' && n.sublabel && n.sublabel !== 'active'))
    }
    if (hideHealthy) {
      const keep = new Set<string>()
      for (const n of nodes) {
        if (n.status === 'error' || n.status === 'warn') keep.add(n.id)
      }
      // Keep one hop of context around every problem node.
      for (const e of data.edges ?? []) {
        if (keep.has(e.from)) keep.add(e.to)
        if (keep.has(e.to)) keep.add(e.from)
      }
      nodes = nodes.filter((n) => keep.has(n.id))
    }

    // Свернуть слушателей одной службы сверх порога в один узел.
    const buckets = new Map<string, GraphNode[]>()
    for (const n of nodes) {
      if (!GROUP_KINDS.has(n.kind)) continue
      const key = `${n.kind}|${groupKeyOf(n)}`
      ;(buckets.get(key) ?? buckets.set(key, []).get(key)!).push(n)
    }
    for (const [key, list] of buckets) {
      if (list.length <= groupOver || expanded.has(key)) continue
      const worst = list.reduce((a, b) => ((STATUS_RANK[b.status] ?? 0) > (STATUS_RANK[a.status] ?? 0) ? b : a))
      const name = list[0].kind === 'endpoint' ? list[0].group || list[0].meta?.service || list[0].label : list[0].label
      const node: GraphNode = {
        id: `group:${key}`,
        kind: list[0].kind,
        label: name,
        sublabel: t('topology.portsCount', { count: list.length }),
        group: list[0].group,
        status: worst.status,
        findings: list.reduce((sum, m) => sum + (m.findings || 0), 0),
        public: list.some((m) => m.public),
      }
      groups.set(node.id, { key, node, members: list })
      for (const m of list) memberOf.set(m.id, node.id)
    }
    if (groups.size > 0) {
      const placedGroups = new Set<string>()
      const next: GraphNode[] = []
      for (const n of nodes) {
        const gid = memberOf.get(n.id)
        if (!gid) next.push(n)
        else if (!placedGroups.has(gid)) {
          placedGroups.add(gid)
          next.push(groups.get(gid)!.node)
        }
      }
      nodes = next
    }

    const byKind = new Map<string, GraphNode[]>()
    for (const n of nodes) {
      const list = byKind.get(n.kind) ?? []
      list.push(n)
      byKind.set(n.kind, list)
    }
    // The host node shares the services column.
    const hostNodes = byKind.get('host') ?? []
    byKind.set('service', [...hostNodes, ...(byKind.get('service') ?? [])])

    const usedColumns = COLUMNS.filter((c) => (byKind.get(c.kind) ?? []).length > 0)
    const out: Placed[] = []
    let maxRows = 0

    usedColumns.forEach((col, ci) => {
      const list = byKind.get(col.kind) ?? []
      maxRows = Math.max(maxRows, list.length)
      list.forEach((n, ri) => {
        out.push({
          ...n,
          x: 20 + ci * (NODE_W + COL_GAP),
          y: 44 + ri * (NODE_H + ROW_GAP),
        })
      })
    })

    const positions = new Map(out.map((n) => [n.id, n]))
    // Рёбра свёрнутых портов ведут к узлу службы; одинаковые — одним.
    const seen = new Set<string>()
    const visibleEdges: GraphEdge[] = []
    for (const e of data.edges ?? []) {
      const from = memberOf.get(e.from) ?? e.from
      const to = memberOf.get(e.to) ?? e.to
      if (from === to || !positions.has(from) || !positions.has(to)) continue
      if (from === e.from && to === e.to) {
        visibleEdges.push(e)
        continue
      }
      const id = `${from}>${to}>${e.kind}`
      if (seen.has(id)) continue
      seen.add(id)
      visibleEdges.push({ ...e, id, from, to, label: undefined })
    }

    return {
      placed: out,
      edges: visibleEdges,
      width: 40 + usedColumns.length * (NODE_W + COL_GAP),
      height: 70 + maxRows * (NODE_H + ROW_GAP),
      columns: usedColumns,
      groups,
      memberOf,
    }
  }, [data, hideHealthy, hideInactive, groupOver, expanded, t])

  const positions = useMemo(() => new Map(placed.map((n) => [n.id, n])), [placed])

  // Переход из находки «неучтённый слушатель»: ?focus=<адрес:порт> или id
  // узла — узел (или свёрнутая служба с ним) выбран, окно открыто.
  const focusParam = useFocusRow(!!data)
  const [focusMember, setFocusMember] = useState<string | null>(null)
  const focusDone = useRef(false)
  useEffect(() => {
    if (!focusParam || !data || focusDone.current) return
    const hit = data.nodes.find((n) => n.id === focusParam || n.id.endsWith(`:${focusParam}`) || (n.sublabel ?? '').endsWith(` ${focusParam}`))
    if (!hit) return
    focusDone.current = true
    setFocusMember(hit.id)
    setSelected(memberOf.get(hit.id) ?? hit.id)
  }, [focusParam, data, memberOf])
  // Модели — ровно то, что на экране: узлы, скрытые фильтрами, и рёбра к
  // ним в разбор не попадают.
  const reviewGraph = useMemo<Graph | undefined>(() => {
    if (!data) return undefined
    const visible = new Set(placed.map((n) => n.id))
    for (const id of memberOf.keys()) visible.add(id)
    return {
      ...data,
      nodes: data.nodes.filter((n) => visible.has(n.id)),
      edges: (data.edges ?? []).filter((e) => visible.has(e.from) && visible.has(e.to)),
    }
  }, [data, placed, memberOf])

  // A node with several incoming (or outgoing) edges used to have every
  // one of them meet at the exact same point — the box's dead centre.
  // Two edges converging on identical coordinates read as one continuous
  // line: a public endpoint reachable both directly from "внешняя сеть"
  // (ingress) and via the service that owns it (listens) looked like a
  // single path running straight through the service's box, when they're
  // two independent facts that just happen to end at the same place.
  // Spreading each node's edges evenly along its box height — sorted by
  // where the other end sits, so the fan-out doesn't cross itself — is
  // the standard fix: it makes plain that separate lines stay separate
  // all the way to their own distinct point on the box.
  // Shared by the anchor spread below and by the route walk further down —
  // both need "every edge leaving this node" / "every edge arriving at
  // this node" grouped the same way.
  const adjacency = useMemo(() => {
    const outByNode = new Map<string, GraphEdge[]>()
    const inByNode = new Map<string, GraphEdge[]>()
    for (const e of edges) {
      ;(outByNode.get(e.from) ?? outByNode.set(e.from, []).get(e.from)!).push(e)
      ;(inByNode.get(e.to) ?? inByNode.set(e.to, []).get(e.to)!).push(e)
    }
    return { outByNode, inByNode }
  }, [edges])

  const edgeAnchors = useMemo(() => {
    const spread = (idx: number, count: number) => {
      if (count <= 1) return NODE_H / 2
      const usable = NODE_H - 16 // keep clear of the rounded corners
      return 8 + (usable * idx) / (count - 1)
    }

    const y1ByEdge = new Map<string, number>()
    for (const list of adjacency.outByNode.values()) {
      const sorted = [...list].sort((a, b) => (positions.get(a.to)?.y ?? 0) - (positions.get(b.to)?.y ?? 0))
      sorted.forEach((e, i) => y1ByEdge.set(e.id, spread(i, sorted.length)))
    }
    const y2ByEdge = new Map<string, number>()
    for (const list of adjacency.inByNode.values()) {
      const sorted = [...list].sort((a, b) => (positions.get(a.from)?.y ?? 0) - (positions.get(b.from)?.y ?? 0))
      sorted.forEach((e, i) => y2ByEdge.set(e.id, spread(i, sorted.length)))
    }

    const result = new Map<string, { y1: number; y2: number }>()
    for (const e of edges) {
      result.set(e.id, { y1: y1ByEdge.get(e.id) ?? NODE_H / 2, y2: y2ByEdge.get(e.id) ?? NODE_H / 2 })
    }
    return result
  }, [edges, positions, adjacency])

  // React 18 attaches its own JSX onWheel listener as passive at the root
  // for scroll performance, so e.preventDefault() inside a plain onWheel
  // prop is silently ignored (and warns in dev) — the page would scroll
  // out from under the map on every zoom attempt. A manually attached,
  // non-passive listener is the only way to actually claim the wheel
  // event for zooming instead. Re-attached whenever zoom/pan/size change
  // so the handler always closes over fresh values rather than stale ones
  // captured at mount — wheel events are infrequent enough that the
  // remove/add churn this causes is not worth avoiding.
  useEffect(() => {
    const el = svgRef.current
    if (!el) return

    function onWheel(e: WheelEvent) {
      e.preventDefault()
      const rect = el!.getBoundingClientRect()
      const curViewW = width / zoom
      const curViewH = height / zoom
      // The map point under the cursor, in the SVG's own coordinate space
      // — kept fixed on screen across the zoom change below, the way
      // every other zoom-under-cursor implementation (maps, image
      // viewers) behaves. Without this, zooming in while looking at a
      // node on the right edge shoves it off-screen instead of growing
      // it in place.
      const fx = (e.clientX - rect.left) / rect.width
      const fy = (e.clientY - rect.top) / rect.height
      const anchorX = pan.x + fx * curViewW
      const anchorY = pan.y + fy * curViewH

      const factor = e.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP
      const nextZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom * factor))
      if (nextZoom === zoom) return

      const nextViewW = width / nextZoom
      const nextViewH = height / nextZoom
      setZoom(nextZoom)
      setPan({ x: anchorX - fx * nextViewW, y: anchorY - fy * nextViewH })
    }

    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [zoom, pan, width, height])

  const focus = hovered ?? selected
  // The full route through the focused node — every ancestor back to
  // "внешняя сеть"/the host, and every descendant down to whatever backend
  // or container actually serves it — not just its immediate neighbours.
  // One hop used to mean clicking a backend address highlighted only its
  // pool, with no way to see the endpoint (let alone the internet) that
  // route actually starts from; the map showed a graph but couldn't answer
  // "how does traffic get here" for anything more than one link away.
  //
  // Ancestors and descendants are walked as two SEPARATE directed BFS
  // passes (backward-only, then forward-only) rather than one traversal
  // that follows edges in either direction — that distinction is what
  // keeps the highlight to the actual route instead of exploding through
  // any hub node it passes. "внешняя сеть"/"host" fan out to every public
  // endpoint and every service on the host; an undirected walk reaching
  // "внешняя сеть" would then walk straight back down into all of them,
  // lighting up unrelated services that merely share that same hub node —
  // exactly the "путь уходит на другие сервисы" confusion this replaces.
  // A directed walk only ever climbs from a node to what feeds it (or
  // descends to what it feeds), so it stops there instead of fanning back
  // out.
  // Color is assigned once, at the very first branch out of the focused
  // node, and then simply inherited edge-by-edge for the rest of that
  // branch's length — no matter how many more times it splits further on.
  // Recoloring at every intermediate node (the previous approach) made a
  // single continuous route change color mid-way for no reason other than
  // passing through a node that happened to fan out again; a route should
  // read as one color from where it leaves the focus to wherever it ends.
  // If two differently-colored branches reconverge on the same node (e.g.
  // a container attached to two networks), whichever branch's BFS gets
  // there first "owns" that node and everything downstream of it — the
  // other branch's own edge into that node still keeps its own color, it
  // just doesn't get to repaint what's already claimed.
  const route = useMemo(() => {
    if (!focus) return null

    const walk = (byNode: Map<string, GraphEdge[]>, other: (e: GraphEdge) => string) => {
      const nodes = new Set<string>([focus])
      const edgeColors = new Map<string, string>()
      const nodeColor = new Map<string, string>()

      const first = [...(byNode.get(focus) ?? [])].sort(
        (a, b) => (positions.get(other(a))?.y ?? 0) - (positions.get(other(b))?.y ?? 0),
      )
      const queue: string[] = []
      first.forEach((e, i) => {
        const color = FAN_PALETTE[i % FAN_PALETTE.length]
        edgeColors.set(e.id, color)
        const next = other(e)
        if (!nodes.has(next)) {
          nodes.add(next)
          nodeColor.set(next, color)
          queue.push(next)
        }
      })

      while (queue.length) {
        const cur = queue.shift()!
        const color = nodeColor.get(cur)!
        for (const e of byNode.get(cur) ?? []) {
          edgeColors.set(e.id, color)
          const next = other(e)
          if (!nodes.has(next)) {
            nodes.add(next)
            nodeColor.set(next, color)
            queue.push(next)
          }
        }
      }
      return { nodes, edgeColors }
    }

    const descendants = walk(adjacency.outByNode, (e) => e.to)
    const ancestors = walk(adjacency.inByNode, (e) => e.from)
    return {
      nodes: new Set([...descendants.nodes, ...ancestors.nodes]),
      edgeColors: new Map([...descendants.edgeColors, ...ancestors.edgeColors]),
    }
  }, [focus, adjacency, positions])

  const connected = route?.nodes ?? null
  const edgeColors = route?.edgeColors ?? null

  if (loading && !data) return <Loading what={t('topology.loadingMap')} />
  if (error && !data) return <ErrorNote error={error} />
  if (!data) return null

  const viewW = width / zoom
  const viewH = height / zoom
  // Scoped to whatever's under the cursor or clicked — a full list/panel for
  // every node ate half the screen; this answers "what is this, and what's
  // wrong with it" without leaving the header row.
  // Deliberately keyed on selected, not focus (hover) — hover still drives
  // the highlight on the map itself, but the info panel only reacts to a
  // click, so it doesn't flicker as the cursor crosses the diagram.
  const selectedNode = selected ? positions.get(selected) : null
  const selectedGroup = selected ? groups.get(selected) : undefined
  const selectedIDs = new Set(selectedGroup ? selectedGroup.members.map((m) => m.id) : selected ? [selected] : [])
  // У свёрнутой службы одна и та же проблема висит на каждом её порту —
  // в окне она одна.
  const selectedFindings = (data.findings ?? [])
    .filter((f) => selectedIDs.has(f.node_id))
    .filter((f, i, all) => all.findIndex((o) => o.title === f.title && o.severity === f.severity) === i)
  const selectedMeta = Object.entries(selectedNode?.meta ?? {}).filter(([, v]) => v)

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            {t('topology.title')}
            <TitleHelp>{t('topology.hint')}</TitleHelp>
          </h1>
        </div>
      </div>
      <p className="small muted" style={{ marginTop: '-0.4rem' }}>
        {data.findings.length > 0 ? t('topology.findingsHint', { count: data.findings.length }) : t('topology.clickNodeHint')}
      </p>

      {selectedNode && (
        <Modal title={selectedNode.label} onClose={() => setSelected(null)} width={680}>
          <div className="col" style={{ gap: '0.6rem' }}>
            <div className="small muted">
              {selectedNode.kind}
              {selectedNode.sublabel ? ` · ${selectedNode.sublabel}` : ''} · {selectedNode.status}
            </div>
            {selectedGroup && (
              <div className="col" style={{ gap: '0.35rem' }}>
                <div className="row" style={{ alignItems: 'center', gap: '0.5rem' }}>
                  <span className="small muted">{t('topology.groupHint', { count: selectedGroup.members.length })}</span>
                  <Button
                    size="small"
                    style={{ marginLeft: 'auto' }}
                    onClick={() => {
                      setExpanded((cur) => new Set(cur).add(selectedGroup.key))
                      setSelected(null)
                    }}
                  >
                    {t('topology.expandGroup')}
                  </Button>
                </div>
                <div className="table-wrap">
                  <table className="topology-group-table">
                    <tbody>
                      {selectedGroup.members.map((m) => {
                        const target = portTarget(m)
                        const count = (data.findings ?? []).filter((f) => f.node_id === m.id).length
                        return (
                          <tr key={m.id} className={m.id === focusMember ? 'row-focus' : undefined}>
                            <td>
                              <span className="legend-swatch" style={{ background: STATUS_COLOR[m.status] ?? STATUS_COLOR.unknown, display: 'inline-block', marginRight: 6 }} />
                              <span className="mono">{m.label}</span>
                            </td>
                            <td className="mono small muted">{m.sublabel}</td>
                            <td className="small">{count > 0 ? t('topology.findingsCount', { count }) : ''}</td>
                            <td style={{ textAlign: 'right' }}>
                              {target && (
                                <Button size="small" onClick={() => navigate(target.to)}>
                                  {t(target.labelKey, { name: target.name ?? '' })}
                                </Button>
                              )}
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
            {!selectedGroup && (() => {
              const target = nodeTarget(selectedNode)
              return target ? (
                <div>
                  <Button type="primary" size="small" onClick={() => navigate(target.to)}>
                    {t(target.labelKey, { name: target.name ?? '' })}
                  </Button>
                </div>
              ) : null
            })()}
            {selectedFindings.length > 0 && (
              <div className="col" style={{ gap: '0.3rem' }}>
                {selectedFindings.map((f, i) => (
                  <span key={i} className="topology-finding-chip">
                    <SeverityBadge severity={f.severity} />
                    {f.title}
                  </span>
                ))}
              </div>
            )}
            {selectedMeta.length > 0 && (
              <dl className="topology-node-meta">
                {selectedMeta.map(([k, v]) => (
                  <div key={k}>
                    <dt className="muted">{k}</dt>
                    <dd className="mono">{v}</dd>
                  </div>
                ))}
              </dl>
            )}
          </div>
        </Modal>
      )}

      <Card
        actions={
          <>
            <div className="chart-legend">
              <span className="legend-item">
                <span className="legend-swatch" style={{ background: STATUS_COLOR.ok }} /> {t('topology.legendOk')}
              </span>
              <span className="legend-item">
                <span className="legend-swatch" style={{ background: STATUS_COLOR.warn }} /> {t('topology.legendWarn')}
              </span>
              <span className="legend-item">
                <span className="legend-swatch" style={{ background: STATUS_COLOR.error }} /> {t('topology.legendError')}
              </span>
              <span className="legend-item">
                <span className="legend-swatch" style={{ background: STATUS_COLOR.unknown }} /> {t('topology.legendUnknown')}
              </span>
            </div>
            {/* Обе галочки одной строкой и с одинаковыми отступами: они об
                одном и том же — что показывать на карте. */}
            <div className="row" style={{ alignItems: 'center', gap: '1rem' }}>
              <Checkbox checked={hideInactive} onChange={(e) => setHideInactive(e.target.checked)}>
                {t('topology.hideInactive')}
              </Checkbox>
              <Checkbox checked={hideHealthy} onChange={(e) => setHideHealthy(e.target.checked)}>
                {t('topology.onlyProblems')}
              </Checkbox>
              <span className="row row-nowrap" style={{ alignItems: 'center', gap: '0.35rem' }}>
                {t('topology.groupOver')}
                <InputNumber size="small" min={1} max={99} value={groupOver} onChange={(v) => v && setGroupOver(Number(v))} style={{ width: 64 }} />
              </span>
            </div>
            <span className="small muted">
              {t('topology.nodesEdgesCount', { nodes: data.nodes.length, edges: data.edges.length })}
            </span>
          </>
        }
      >
        <div className="map-wrap">
          <div className="map-controls">
            <Button size="small" onClick={() => setZoom((z) => Math.min(z * ZOOM_STEP, MAX_ZOOM))} title={t('topology.zoomIn')}>
              +
            </Button>
            <Button size="small" onClick={() => setZoom((z) => Math.max(z / ZOOM_STEP, MIN_ZOOM))} title={t('topology.zoomOut')}>
              −
            </Button>
            <Button
              size="small"
              onClick={() => {
                setZoom(1)
                setPan({ x: 0, y: 0 })
              }}
              title={t('topology.resetView')}
            >
              ⤢
            </Button>
          </div>

          <svg
            ref={svgRef}
            viewBox={`${pan.x} ${pan.y} ${viewW} ${viewH}`}
            style={{ height: Math.min(height, 720) }}
            className={`sensitive-area${dragging ? ' map-dragging' : ''}`}
            onPointerDown={(e) => {
              if (e.button !== 0) return
              const rect = e.currentTarget.getBoundingClientRect()
              // viewBox вписан в svg с сохранением пропорций (meet): один
              // пиксель экрана — это наибольшее из двух отношений. Брать
              // только ширину — карта ползла медленнее курсора.
              const scale = Math.max(viewW / rect.width, viewH / rect.height)
              dragRef.current = { x: e.clientX, y: e.clientY, panX: pan.x, panY: pan.y, scale, moved: false, pointer: e.pointerId }
            }}
            onPointerMove={(e) => {
              const drag = dragRef.current
              if (!drag) return
              const dx = e.clientX - drag.x
              const dy = e.clientY - drag.y
              if (!drag.moved) {
                if (Math.abs(dx) + Math.abs(dy) < 4) return
                drag.moved = true
                e.currentTarget.setPointerCapture(drag.pointer)
                setDragging(true)
                setHovered(null)
              }
              e.preventDefault()
              if (frameRef.current !== null) cancelAnimationFrame(frameRef.current)
              frameRef.current = requestAnimationFrame(() => {
                frameRef.current = null
                setPan({ x: drag.panX - dx * drag.scale, y: drag.panY - dy * drag.scale })
              })
            }}
            onPointerUp={(e) => {
              const drag = dragRef.current
              dragRef.current = null
              if (drag?.moved) {
                // Отпускание после сдвига — не щелчок по узлу под курсором.
                suppressClick.current = true
                window.setTimeout(() => (suppressClick.current = false), 0)
                if (e.currentTarget.hasPointerCapture(drag.pointer)) e.currentTarget.releasePointerCapture(drag.pointer)
              }
              setDragging(false)
            }}
            onPointerCancel={() => {
              dragRef.current = null
              setDragging(false)
            }}
            onMouseLeave={() => {
              if (!dragRef.current) setHovered(null)
            }}
          >
            {columns.map((col, i) => (
              <text
                key={col.kind}
                x={20 + i * (NODE_W + COL_GAP)}
                y={22}
                fontSize={11}
                fontWeight={600}
                fill="var(--text-muted)"
              >
                {t(col.titleKey)}
              </text>
            ))}

            {edges.map((e) => (
              <EdgePath
                key={e.id}
                edge={e}
                from={positions.get(e.from)!}
                to={positions.get(e.to)!}
                anchor={edgeAnchors.get(e.id)!}
                color={edgeColors?.get(e.id) ?? null}
                dimmed={connected !== null && !(connected.has(e.from) && connected.has(e.to))}
                highlighted={connected !== null && connected.has(e.from) && connected.has(e.to)}
              />
            ))}

            {placed.map((n) => (
              <NodeBox
                key={n.id}
                node={n}
                dimmed={connected !== null && !connected.has(n.id)}
                selected={selected === n.id}
                onHover={(id) => !dragging && setHovered(id)}
                onSelect={(id) => {
                  if (suppressClick.current) return
                  setFocusMember(null)
                  setSelected((cur) => (cur === id ? null : id))
                }}
              />
            ))}
          </svg>
        </div>
      </Card>
      {/* Разбор архитектуры: карта уже собрана — модель смотрит на неё
          целиком и говорит о том, чего в ней не хватает. */}
      <AIReviewCard graph={reviewGraph} totalNodes={data.nodes.length} hostID={reviewHostID} scope="host" />
    </>
  )
}

function EdgePath({
  edge,
  from,
  to,
  anchor,
  color,
  dimmed,
  highlighted,
}: {
  edge: GraphEdge
  from: Placed
  to: Placed
  anchor: { y1: number; y2: number }
  color: string | null
  dimmed: boolean
  highlighted: boolean
}) {
  const x1 = from.x + NODE_W
  const y1 = from.y + anchor.y1
  const x2 = to.x
  const y2 = to.y + anchor.y2
  const mid = (x1 + x2) / 2
  const d = `M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`

  // Unlike node labels (truncated in NodeBox below), edge labels come
  // straight from config as-is — a haproxy `use_backend ... if <acl
  // condition>` carries the raw ACL expression verbatim, which can run
  // to a full line of its own. Drawn without a limit, one long condition
  // stretched clear across the diagram, reading like stray file content
  // pasted onto the map rather than a label. <title> keeps the whole
  // thing one hover away instead of losing it outright.
  const label = edge.label && edge.label.length > 34 ? `${edge.label.slice(0, 33)}…` : edge.label

  return (
    <g opacity={dimmed ? 0.12 : 1}>
      <path
        d={d}
        fill="none"
        // color is only ever set for edges on the highlighted route (see
        // the `route` walk above) — a fresh palette color per branch right
        // where it splits off the focused node, inherited unchanged for
        // the rest of that branch's length. Everything not on the route
        // (including the default nothing-selected state) stays the plain
        // neutral baseline, same as before per-branch color existed.
        stroke={highlighted ? (color ?? 'var(--series-1)') : 'var(--baseline)'}
        strokeWidth={highlighted ? 2 : 1.25}
      />
      {highlighted && label && (
        <text className="edge-label" x={mid} y={(y1 + y2) / 2 - 4} textAnchor="middle">
          {edge.label !== label && <title>{edge.label}</title>}
          {label}
        </text>
      )}
    </g>
  )
}

function NodeBox({
  node,
  dimmed,
  selected,
  onHover,
  onSelect,
}: {
  node: Placed
  dimmed: boolean
  selected: boolean
  onHover: (id: string | null) => void
  onSelect: (id: string) => void
}) {
  const color = STATUS_COLOR[node.status] ?? STATUS_COLOR.unknown
  const label = node.label.length > 24 ? `${node.label.slice(0, 23)}…` : node.label
  const sub = node.sublabel && node.sublabel.length > 26 ? `${node.sublabel.slice(0, 25)}…` : node.sublabel

  return (
    <g
      opacity={dimmed ? 0.18 : 1}
      style={{ cursor: 'pointer' }}
      onMouseEnter={() => onHover(node.id)}
      onMouseLeave={() => onHover(null)}
      onClick={() => onSelect(node.id)}
    >
      <rect
        x={node.x}
        y={node.y}
        width={NODE_W}
        height={NODE_H}
        rx={7}
        fill="var(--surface-raised)"
        stroke={selected ? 'var(--series-1)' : 'var(--border-strong)'}
        strokeWidth={selected ? 2 : 1}
      />
      <rect x={node.x} y={node.y} width={4} height={NODE_H} rx={2} fill={color} />
      <text className="node-label" x={node.x + 12} y={node.y + 17}>
        {label}
      </text>
      {sub && (
        <text className="node-sub" x={node.x + 12} y={node.y + 30}>
          {sub}
        </text>
      )}
      {node.findings > 0 && (
        <>
          <circle cx={node.x + NODE_W - 13} cy={node.y + 13} r={8} fill={color} />
          <text
            x={node.x + NODE_W - 13}
            y={node.y + 16.5}
            textAnchor="middle"
            fontSize={9.5}
            fontWeight={700}
            fill="#fff"
          >
            {node.findings}
          </text>
        </>
      )}
    </g>
  )
}
