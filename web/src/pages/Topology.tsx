import { useMemo, useRef, useState } from 'react'
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
  { kind: 'upstream', title: 'Пулы' },
  { kind: 'backend', title: 'Backend-адреса' },
  { kind: 'container', title: 'Контейнеры' },
  { kind: 'podman_container', title: 'Podman' },
  { kind: 'network', title: 'Сети docker' },
]

const NODE_W = 168
const NODE_H = 40
const COL_GAP = 78
const ROW_GAP = 14

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

  const { placed, edges, width, height, columns } = useMemo(() => {
    if (!data) return { placed: [], edges: [], width: 100, height: 100, columns: [] as typeof COLUMNS }

    let nodes = data.nodes
    if (hideHealthy) {
      const keep = new Set<string>()
      for (const n of nodes) {
        if (n.status === 'error' || n.status === 'warn') keep.add(n.id)
      }
      // Keep one hop of context around every problem node.
      for (const e of data.edges) {
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
    const visibleEdges = data.edges.filter((e) => positions.has(e.from) && positions.has(e.to))

    return {
      placed: out,
      edges: visibleEdges,
      width: 40 + usedColumns.length * (NODE_W + COL_GAP),
      height: 70 + maxRows * (NODE_H + ROW_GAP),
      columns: usedColumns,
    }
  }, [data, hideHealthy])

  const positions = useMemo(() => new Map(placed.map((n) => [n.id, n])), [placed])

  const focus = hovered ?? selected
  const connected = useMemo(() => {
    if (!focus) return null
    const set = new Set<string>([focus])
    for (const e of edges) {
      if (e.from === focus) set.add(e.to)
      if (e.to === focus) set.add(e.from)
    }
    return set
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
  const selectedFindings = selected ? data.findings.filter((f) => f.node_id === selected) : []
  const selectedMeta = Object.entries(selectedNode?.meta ?? {}).filter(([, v]) => v)

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>Карта сетевых ресурсов</h1>
          <p>Красным — критичные проблемы, жёлтым — предупреждения. Щёлкните узел, чтобы увидеть подробности.</p>
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
                <button className="ghost" onClick={() => setSelected(null)} title="закрыть" style={{ padding: '0 0.3rem' }}>
                  ×
                </button>
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
              <input
                type="checkbox"
                checked={hideHealthy}
                onChange={(e) => setHideHealthy(e.target.checked)}
                style={{ width: 'auto' }}
              />
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
            <button className="ghost" onClick={() => setZoom((z) => Math.min(z * 1.25, 3))} title="Приблизить">
              +
            </button>
            <button className="ghost" onClick={() => setZoom((z) => Math.max(z / 1.25, 0.5))} title="Отдалить">
              −
            </button>
            <button
              className="ghost"
              onClick={() => {
                setZoom(1)
                setPan({ x: 0, y: 0 })
              }}
              title="Сбросить вид"
            >
              ⤢
            </button>
          </div>

          <svg
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
                highlighted={focus !== null && (e.from === focus || e.to === focus)}
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
