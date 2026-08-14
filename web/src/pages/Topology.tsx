import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Checkbox } from 'antd'
import { useApi } from '../api'
import type { Graph, GraphEdge, GraphNode } from '../types'
import { Card, ErrorNote, Loading, SeverityBadge } from '../components/ui'

/**
 * The resource map is laid out in fixed columns by node kind rather than by a
 * force simulation: traffic flows left to right (внешняя сеть → сервис →
 * слушатель → пул → backend → контейнер), so the reading order matches the
 * direction requests actually travel, and the layout is stable between scans.
 */
const COLUMNS: { kind: string; title: string }[] = [
  { kind: 'internet', title: 'Внешняя сеть' },
  { kind: 'service', title: 'Сервисы' },
  { kind: 'endpoint', title: 'Слушатели' },
  { kind: 'undeclared', title: 'Разное' },
  { kind: 'upstream', title: 'Пулы' },
  { kind: 'backend', title: 'Backend-адреса' },
  { kind: 'container', title: 'Контейнеры' },
  { kind: 'podman_container', title: 'Podman' },
  { kind: 'lxd_instance', title: 'LXD' },
  { kind: 'vm', title: 'Виртуальные машины' },
  { kind: 'network', title: 'Сети docker' },
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

interface Placed extends GraphNode {
  x: number
  y: number
}

export default function TopologyPage() {
  const { data, error, loading } = useApi<Graph>('/topology', 120_000)
  const [selected, setSelected] = useState<string | null>(null)
  const [hovered, setHovered] = useState<string | null>(null)
  const [hideHealthy, setHideHealthy] = useState(false)
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const dragRef = useRef<{ x: number; y: number; panX: number; panY: number } | null>(null)
  const svgRef = useRef<SVGSVGElement>(null)

  const { placed, edges, width, height, columns } = useMemo(() => {
    if (!data) return { placed: [], edges: [], width: 100, height: 100, columns: [] as typeof COLUMNS }

    let nodes = data.nodes
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
    const visibleEdges = (data.edges ?? []).filter((e) => positions.has(e.from) && positions.has(e.to))

    return {
      placed: out,
      edges: visibleEdges,
      width: 40 + usedColumns.length * (NODE_W + COL_GAP),
      height: 70 + maxRows * (NODE_H + ROW_GAP),
      columns: usedColumns,
    }
  }, [data, hideHealthy])

  const positions = useMemo(() => new Map(placed.map((n) => [n.id, n])), [placed])

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
  const connected = useMemo(() => {
    if (!focus) return null
    const forward = new Map<string, string[]>()
    const backward = new Map<string, string[]>()
    for (const e of edges) {
      ;(forward.get(e.from) ?? forward.set(e.from, []).get(e.from)!).push(e.to)
      ;(backward.get(e.to) ?? backward.set(e.to, []).get(e.to)!).push(e.from)
    }
    const walk = (adjacency: Map<string, string[]>) => {
      const seen = new Set<string>([focus])
      const queue = [focus]
      while (queue.length) {
        const cur = queue.shift()!
        for (const next of adjacency.get(cur) ?? []) {
          if (!seen.has(next)) {
            seen.add(next)
            queue.push(next)
          }
        }
      }
      return seen
    }
    const ancestors = walk(backward)
    const descendants = walk(forward)
    return new Set([...ancestors, ...descendants])
  }, [focus, edges])

  if (loading && !data) return <Loading what="карту ресурсов" />
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
  const selectedFindings = selected ? (data.findings ?? []).filter((f) => f.node_id === selected) : []
  const selectedMeta = Object.entries(selectedNode?.meta ?? {}).filter(([, v]) => v)

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>Карта сетевых ресурсов</h1>
          <p>
            Красным — критичные проблемы, жёлтым — предупреждения. Наведите на узел или щёлкните
            его, чтобы увидеть весь путь — от внешней сети до backend'а и обратно, — и подробности.
          </p>
        </div>

        {/* Fixed height and always rendered, whether or not something is
            selected, so switching between nodes with very different amounts
            of info never shifts the map below up or down. Shares the row
            with the title when there's room (page-head wraps), drops below
            it otherwise. */}
        <div className="topology-focus-panel">
          {selectedNode ? (
            <>
              <div className="topology-focus-head">
                <strong>{selectedNode.label}</strong>
                <Button type="text" size="small" onClick={() => setSelected(null)} title="закрыть" style={{ padding: '0 0.3rem' }}>
                  ×
                </Button>
              </div>
              <div className="small muted">
                {selectedNode.kind}
                {selectedNode.sublabel ? ` · ${selectedNode.sublabel}` : ''} · {selectedNode.status}
              </div>
              {selectedFindings.map((f, i) => (
                <span key={i} className="topology-finding-chip">
                  <SeverityBadge severity={f.severity} />
                  {f.title}
                </span>
              ))}
              {selectedMeta.length > 0 && (
                <div className="topology-focus-meta">
                  {selectedMeta.map(([k, v]) => (
                    <span key={k}>
                      <span className="muted">{k}:</span> {v}
                    </span>
                  ))}
                </div>
              )}
            </>
          ) : (
            <span className="small muted">
              {data.findings.length > 0
                ? `${data.findings.length} находок на карте — щёлкните узел, чтобы увидеть подробности`
                : 'Щёлкните узел на карте, чтобы увидеть подробности.'}
            </span>
          )}
        </div>
      </div>

      <Card
        actions={
          <>
            <div className="chart-legend">
              <span className="legend-item">
                <span className="legend-swatch" style={{ background: STATUS_COLOR.ok }} /> в порядке
              </span>
              <span className="legend-item">
                <span className="legend-swatch" style={{ background: STATUS_COLOR.warn }} /> предупреждение
              </span>
              <span className="legend-item">
                <span className="legend-swatch" style={{ background: STATUS_COLOR.error }} /> проблема
              </span>
              <span className="legend-item">
                <span className="legend-swatch" style={{ background: STATUS_COLOR.unknown }} /> неизвестно
              </span>
            </div>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              <Checkbox checked={hideHealthy} onChange={(e) => setHideHealthy(e.target.checked)} />
              только проблемы
            </label>
            <span className="small muted">
              {data.nodes.length} узлов, {data.edges.length} связей
            </span>
          </>
        }
      >
        <div className="map-wrap">
          <div className="map-controls">
            <Button size="small" onClick={() => setZoom((z) => Math.min(z * ZOOM_STEP, MAX_ZOOM))} title="Приблизить">
              +
            </Button>
            <Button size="small" onClick={() => setZoom((z) => Math.max(z / ZOOM_STEP, MIN_ZOOM))} title="Отдалить">
              −
            </Button>
            <Button
              size="small"
              onClick={() => {
                setZoom(1)
                setPan({ x: 0, y: 0 })
              }}
              title="Сбросить вид"
            >
              ⤢
            </Button>
          </div>

          <svg
            ref={svgRef}
            viewBox={`${pan.x} ${pan.y} ${viewW} ${viewH}`}
            style={{ height: Math.min(height, 720) }}
            onMouseDown={(e) => {
              dragRef.current = { x: e.clientX, y: e.clientY, panX: pan.x, panY: pan.y }
            }}
            onMouseMove={(e) => {
              const drag = dragRef.current
              if (!drag) return
              const rect = e.currentTarget.getBoundingClientRect()
              const scale = viewW / rect.width
              setPan({
                x: drag.panX - (e.clientX - drag.x) * scale,
                y: drag.panY - (e.clientY - drag.y) * scale,
              })
            }}
            onMouseUp={() => {
              dragRef.current = null
            }}
            onMouseLeave={() => {
              dragRef.current = null
              setHovered(null)
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
                {col.title}
              </text>
            ))}

            {edges.map((e) => (
              <EdgePath
                key={e.id}
                edge={e}
                from={positions.get(e.from)!}
                to={positions.get(e.to)!}
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
                onHover={setHovered}
                onSelect={(id) => setSelected((cur) => (cur === id ? null : id))}
              />
            ))}
          </svg>
        </div>
      </Card>
    </>
  )
}

function EdgePath({
  edge,
  from,
  to,
  dimmed,
  highlighted,
}: {
  edge: GraphEdge
  from: Placed
  to: Placed
  dimmed: boolean
  highlighted: boolean
}) {
  const x1 = from.x + NODE_W
  const y1 = from.y + NODE_H / 2
  const x2 = to.x
  const y2 = to.y + NODE_H / 2
  const mid = (x1 + x2) / 2
  const d = `M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`

  return (
    <g opacity={dimmed ? 0.12 : 1}>
      <path
        d={d}
        fill="none"
        stroke={highlighted ? 'var(--series-1)' : 'var(--baseline)'}
        strokeWidth={highlighted ? 2 : 1.25}
      />
      {highlighted && edge.label && (
        <text className="edge-label" x={mid} y={(y1 + y2) / 2 - 4} textAnchor="middle">
          {edge.label}
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
