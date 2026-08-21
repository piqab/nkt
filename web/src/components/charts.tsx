import { useMemo, useState, type ReactNode } from 'react'

/**
 * Chart primitives, hand-built in SVG.
 *
 * Rules applied throughout, per the visualisation guidelines:
 *  - categorical hues are taken in fixed slot order, never cycled or generated;
 *  - magnitude uses one sequential blue ramp, light → dark;
 *  - a legend is present for two or more series, so identity is never colour-alone;
 *  - marks are thin, grid and axes recessive, text in ink tokens;
 *  - every plot has a hover layer.
 */

export const SERIES_COLORS = [
  'var(--series-1)',
  'var(--series-2)',
  'var(--series-3)',
  'var(--series-4)',
  'var(--series-5)',
  'var(--series-6)',
  'var(--series-7)',
  'var(--series-8)',
] as const

/** The sequential ramp, light → dark, for magnitude encodings. */
const SEQ_RAMP = [
  'var(--seq-100)',
  'var(--seq-200)',
  'var(--seq-300)',
  'var(--seq-400)',
  'var(--seq-500)',
  'var(--seq-600)',
  'var(--seq-700)',
] as const

/** Series slot colour. Past the eighth slot the caller must fold into "Другое". */
export function seriesColor(index: number): string {
  return SERIES_COLORS[Math.min(index, SERIES_COLORS.length - 1)]
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n === 0) return '0 Б'
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ', 'ПБ']
  const i = Math.min(Math.floor(Math.log(Math.abs(n)) / Math.log(1024)), units.length - 1)
  const value = n / 1024 ** i
  return `${value.toFixed(value >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}

export function formatNumber(n: number, digits = 0): string {
  return n.toLocaleString('ru-RU', { maximumFractionDigits: digits })
}

export function formatMs(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(2)} с`
  return `${n.toFixed(n < 10 ? 2 : 0)} мс`
}

// ------------------------------------------------------------------- tooltip

interface TipState {
  x: number
  y: number
  title: string
  rows: { label: string; value: string; color?: string }[]
}

function Tooltip({ tip }: { tip: TipState | null }) {
  if (!tip) return null
  // Flip to the left of the cursor near the right edge so the tip stays visible.
  const flip = tip.x > window.innerWidth - 280
  return (
    <div
      className="tooltip"
      style={{
        left: flip ? undefined : tip.x + 14,
        right: flip ? window.innerWidth - tip.x + 14 : undefined,
        top: Math.min(tip.y + 14, window.innerHeight - 120),
      }}
    >
      <div className="tooltip-title">{tip.title}</div>
      {tip.rows.map((row, i) => (
        <div className="tooltip-row" key={i}>
          <span>
            {row.color && (
              <span
                className="legend-swatch"
                style={{ background: row.color, display: 'inline-block', marginRight: 6 }}
              />
            )}
            {row.label}
          </span>
          <span>{row.value}</span>
        </div>
      ))}
    </div>
  )
}

// -------------------------------------------------------------------- legend

export function Legend({ items }: { items: { name: string; color: string }[] }) {
  // A single series needs no legend box — the chart title names it.
  if (items.length < 2) return null
  return (
    <div className="chart-legend">
      {items.map((item) => (
        <span className="legend-item" key={item.name}>
          <span className="legend-swatch" style={{ background: item.color }} />
          {item.name}
        </span>
      ))}
    </div>
  )
}

// ---------------------------------------------------------------- line chart

export interface Series {
  name: string
  points: { x: string; y: number }[]
}

interface LineChartProps {
  series: Series[]
  height?: number
  formatValue?: (n: number) => string
  formatX?: (x: string) => string
  yMax?: number
  yUnit?: string
  area?: boolean
  /** A fixed horizontal ceiling to draw and label — e.g. total host memory
   * or CPU core count, so a value like "4 ГиБ" or "230%" has something
   * concrete to be read against instead of an axis auto-scaled to
   * whatever the data itself happens to peak at. */
  reference?: { value: number; label: string }
}

export function LineChart({
  series,
  height = 220,
  formatValue = (n) => formatNumber(n, 1),
  formatX = (x) => x,
  yMax,
  yUnit,
  area = false,
  reference,
}: LineChartProps) {
  const [tip, setTip] = useState<TipState | null>(null)
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)

  const { xs, maxY } = useMemo(() => {
    const set = new Set<string>()
    let max = 0
    for (const s of series) {
      for (const p of s.points) {
        set.add(p.x)
        if (p.y > max) max = p.y
      }
    }
    // Deliberately NOT stretched to fit reference (e.g. total host
    // memory/CPU) — usage sitting at a few percent of a multi-core/
    // multi-gigabyte host would otherwise get flattened into an unreadable
    // sliver near the bottom just to leave room for a ceiling far above
    // anything the data ever approaches. The axis stays scaled to what's
    // actually on the chart; the reference line only draws when it
    // naturally falls inside that range (see below) — the caller still
    // shows the raw figure as text regardless (see Usage.tsx's subtitle).
    return { xs: [...set].sort(), maxY: yMax ?? (max === 0 ? 1 : max * 1.12) }
  }, [series, yMax])

  const width = 900
  const pad = { top: 12, right: 16, bottom: 26, left: 52 }
  const plotW = width - pad.left - pad.right
  const plotH = height - pad.top - pad.bottom

  if (xs.length === 0) {
    return <div className="chart-empty">Нет данных за выбранный период.</div>
  }

  const xAt = (i: number) => pad.left + (xs.length === 1 ? plotW / 2 : (i / (xs.length - 1)) * plotW)
  const yAt = (v: number) => pad.top + plotH - (v / maxY) * plotH

  const ticks = 4
  const yTicks = Array.from({ length: ticks + 1 }, (_, i) => (maxY / ticks) * i)
  const xTickEvery = Math.max(1, Math.ceil(xs.length / 7))

  const byIndex = series.map((s) => {
    const map = new Map(s.points.map((p) => [p.x, p.y]))
    return xs.map((x) => map.get(x) ?? null)
  })

  const legend = series.map((s, i) => ({ name: s.name, color: seriesColor(i) }))

  function handleMove(event: React.MouseEvent<SVGSVGElement>) {
    const rect = event.currentTarget.getBoundingClientRect()
    const relX = ((event.clientX - rect.left) / rect.width) * width
    const ratio = (relX - pad.left) / plotW
    const index = Math.round(ratio * (xs.length - 1))
    if (index < 0 || index >= xs.length) {
      setTip(null)
      setHoverIndex(null)
      return
    }
    setHoverIndex(index)
    setTip({
      x: event.clientX,
      y: event.clientY,
      title: formatX(xs[index]),
      rows: series.map((s, si) => ({
        label: s.name,
        color: seriesColor(si),
        value: byIndex[si][index] === null ? '—' : formatValue(byIndex[si][index] as number),
      })),
    })
  }

  return (
    <div className="chart-figure">
      <Legend items={legend} />
      <svg
        viewBox={`0 0 ${width} ${height}`}
        // The default "xMidYMid meet" scales the viewBox uniformly and
        // letterboxes/centers it whenever the rendered box's aspect ratio
        // (rect.width : height, since CSS sets both independently below)
        // doesn't match the viewBox's own (width : height) — which is the
        // common case, since width is 100% of whatever container this
        // lands in while height stays fixed. handleMove's own math assumes
        // a plain linear rect.width -> viewBox-width scale with no such
        // offset, so any letterboxing throws the cursor-to-index mapping
        // off by however wide the (invisible) margin ends up being — the
        // reported "курсор опережает/опаздывает". "none" makes the SVG
        // stretch X and Y independently to fill the box exactly, matching
        // what handleMove already assumes.
        preserveAspectRatio="none"
        role="img"
        style={{ width: '100%', height }}
        onMouseMove={handleMove}
        onMouseLeave={() => {
          setTip(null)
          setHoverIndex(null)
        }}
      >
        {yTicks.map((t, i) => (
          <g key={i}>
            <line
              x1={pad.left}
              x2={width - pad.right}
              y1={yAt(t)}
              y2={yAt(t)}
              stroke="var(--gridline)"
              strokeWidth={1}
            />
            <text x={pad.left - 8} y={yAt(t) + 3.5} textAnchor="end" fontSize={10} fill="var(--text-muted)">
              {formatValue(t)}
            </text>
          </g>
        ))}
        {yUnit && (
          <text x={4} y={pad.top + 4} fontSize={10} fill="var(--text-muted)">
            {yUnit}
          </text>
        )}

        {reference && reference.value <= maxY && (
          <g>
            <line
              x1={pad.left}
              x2={width - pad.right}
              y1={yAt(reference.value)}
              y2={yAt(reference.value)}
              stroke="var(--text-muted)"
              strokeWidth={1}
              strokeDasharray="4 3"
            />
            <text
              x={width - pad.right}
              y={yAt(reference.value) - 4}
              textAnchor="end"
              fontSize={10}
              fill="var(--text-muted)"
            >
              {reference.label}
            </text>
          </g>
        )}

        {xs.map((x, i) =>
          i % xTickEvery === 0 ? (
            <text
              key={x}
              x={xAt(i)}
              y={height - 8}
              textAnchor="middle"
              fontSize={10}
              fill="var(--text-muted)"
            >
              {formatX(x)}
            </text>
          ) : null,
        )}

        {hoverIndex !== null && (
          <line
            x1={xAt(hoverIndex)}
            x2={xAt(hoverIndex)}
            y1={pad.top}
            y2={pad.top + plotH}
            stroke="var(--baseline)"
            strokeWidth={1}
          />
        )}

        {byIndex.map((values, si) => {
          const color = seriesColor(si)
          const segments: string[] = []
          let current: string[] = []
          values.forEach((v, i) => {
            if (v === null) {
              if (current.length) segments.push(current.join(' '))
              current = []
              return
            }
            current.push(`${current.length === 0 ? 'M' : 'L'}${xAt(i)},${yAt(v)}`)
          })
          if (current.length) segments.push(current.join(' '))

          return (
            <g key={si}>
              {area && segments.length === 1 && values.some((v) => v !== null) && (
                <path
                  d={`${segments[0]} L${xAt(xs.length - 1)},${yAt(0)} L${xAt(0)},${yAt(0)} Z`}
                  fill={color}
                  opacity={0.1}
                />
              )}
              {segments.map((d, i) => (
                <path key={i} d={d} fill="none" stroke={color} strokeWidth={2} strokeLinejoin="round" />
              ))}
              {hoverIndex !== null && values[hoverIndex] !== null && (
                <circle
                  cx={xAt(hoverIndex)}
                  cy={yAt(values[hoverIndex] as number)}
                  r={4}
                  fill={color}
                  stroke="var(--surface-1)"
                  strokeWidth={2}
                />
              )}
            </g>
          )
        })}

        <line
          x1={pad.left}
          x2={width - pad.right}
          y1={pad.top + plotH}
          y2={pad.top + plotH}
          stroke="var(--baseline)"
          strokeWidth={1}
        />
      </svg>
      <Tooltip tip={tip} />
    </div>
  )
}

// ----------------------------------------------------------------- bar chart

interface BarChartProps {
  data: { label: string; value: number; note?: string }[]
  formatValue?: (n: number) => string
  color?: string
  height?: number
}

/** Horizontal bars: the right form when the category labels are names, not time. */
export function BarChart({ data, formatValue = (n) => formatNumber(n), color }: BarChartProps) {
  const [tip, setTip] = useState<TipState | null>(null)
  if (data.length === 0) return <div className="chart-empty">Нет данных за выбранный период.</div>

  const max = Math.max(...data.map((d) => d.value), 1)
  const rowH = 26
  const width = 640
  const labelW = 190
  const height = data.length * rowH + 8

  return (
    <div className="chart-figure">
      <svg viewBox={`0 0 ${width} ${height}`} role="img" style={{ width: '100%', height }}>
        {data.map((d, i) => {
          const y = i * rowH + 4
          const barW = Math.max((d.value / max) * (width - labelW - 76), 2)
          return (
            <g
              key={d.label}
              onMouseMove={(e) =>
                setTip({
                  x: e.clientX,
                  y: e.clientY,
                  title: d.label,
                  rows: [
                    { label: 'значение', value: formatValue(d.value) },
                    ...(d.note ? [{ label: 'подробности', value: d.note }] : []),
                  ],
                })
              }
              onMouseLeave={() => setTip(null)}
            >
              <rect x={0} y={y - 2} width={width} height={rowH - 2} fill="transparent" />
              <text x={0} y={y + 13} fontSize={11.5} fill="var(--text-secondary)">
                {d.label.length > 30 ? `${d.label.slice(0, 29)}…` : d.label}
              </text>
              {/* 4px rounded data-end, square against the baseline */}
              <rect
                x={labelW}
                y={y + 3}
                width={barW}
                height={12}
                rx={4}
                fill={color ?? 'var(--series-1)'}
              />
              <text x={labelW + barW + 8} y={y + 13} fontSize={11} fill="var(--text-secondary)">
                {formatValue(d.value)}
              </text>
            </g>
          )
        })}
      </svg>
      <Tooltip tip={tip} />
    </div>
  )
}

// ------------------------------------------------------------------- heatmap

const DOW_LABELS = ['вс', 'пн', 'вт', 'ср', 'чт', 'пт', 'сб']

interface HeatmapProps {
  cells: { dow: number; hour: number; value: number; total?: number }[]
  formatValue?: (n: number) => string
  /** Legend caption describing what dark means. */
  scaleLabel: string
  emptyLabel?: string
}

/**
 * Weekly schedule grid: 7 rows (days) × 24 columns (hours), magnitude on a single
 * sequential hue. Cells without measurements stay at surface colour rather than
 * pretending to be zero.
 */
export function Heatmap({
  cells,
  formatValue = (n) => formatNumber(n, 1),
  scaleLabel,
  emptyLabel = 'нет измерений',
}: HeatmapProps) {
  const [tip, setTip] = useState<TipState | null>(null)

  const { grid, max } = useMemo(() => {
    const g = new Map<string, { value: number; total: number }>()
    let m = 0
    for (const c of cells) {
      g.set(`${c.dow}:${c.hour}`, { value: c.value, total: c.total ?? 0 })
      if (c.value > m) m = c.value
    }
    return { grid: g, max: m || 1 }
  }, [cells])

  const cell = 22
  const gap = 2
  const left = 30
  const top = 18
  const width = left + 24 * (cell + gap)
  const height = top + 7 * (cell + gap) + 4

  function rampColor(value: number): string {
    const ratio = Math.min(value / max, 1)
    const step = Math.min(Math.floor(ratio * SEQ_RAMP.length), SEQ_RAMP.length - 1)
    return SEQ_RAMP[step]
  }

  return (
    <div className="chart-figure">
      <svg viewBox={`0 0 ${width} ${height}`} role="img" style={{ width: '100%', maxWidth: width }}>
        {Array.from({ length: 24 }, (_, h) =>
          h % 3 === 0 ? (
            <text key={h} x={left + h * (cell + gap) + cell / 2} y={11} textAnchor="middle" fontSize={9} fill="var(--text-muted)">
              {h}
            </text>
          ) : null,
        )}
        {Array.from({ length: 7 }, (_, d) => (
          <text key={d} x={left - 6} y={top + d * (cell + gap) + cell / 2 + 3.5} textAnchor="end" fontSize={9.5} fill="var(--text-muted)">
            {DOW_LABELS[d]}
          </text>
        ))}
        {Array.from({ length: 7 }, (_, d) =>
          Array.from({ length: 24 }, (_, h) => {
            const entry = grid.get(`${d}:${h}`)
            const x = left + h * (cell + gap)
            const y = top + d * (cell + gap)
            return (
              <rect
                key={`${d}:${h}`}
                x={x}
                y={y}
                width={cell}
                height={cell}
                rx={3}
                fill={entry ? rampColor(entry.value) : 'var(--surface-page)'}
                stroke="var(--border)"
                strokeWidth={entry ? 0 : 1}
                onMouseMove={(e) =>
                  setTip({
                    x: e.clientX,
                    y: e.clientY,
                    title: `${DOW_LABELS[d]}, ${String(h).padStart(2, '0')}:00`,
                    rows: entry
                      ? [
                          { label: scaleLabel, value: formatValue(entry.value) },
                          ...(entry.total ? [{ label: 'измерений', value: formatNumber(entry.total) }] : []),
                        ]
                      : [{ label: '', value: emptyLabel }],
                  })
                }
                onMouseLeave={() => setTip(null)}
              />
            )
          }),
        )}
      </svg>
      <div className="chart-legend">
        <span className="legend-item">{scaleLabel}: меньше</span>
        {SEQ_RAMP.map((c) => (
          <span key={c} className="legend-swatch" style={{ background: c }} />
        ))}
        <span className="legend-item">больше</span>
      </div>
      <Tooltip tip={tip} />
    </div>
  )
}

// ---------------------------------------------------------------- stat tile

export function StatTile({
  label,
  value,
  note,
  tone,
}: {
  label: string
  value: ReactNode
  note?: ReactNode
  tone?: 'good' | 'warning' | 'critical'
}) {
  const color =
    tone === 'critical'
      ? 'var(--status-critical)'
      : tone === 'warning'
        ? 'var(--status-warning)'
        : tone === 'good'
          ? 'var(--success-text)'
          : undefined
  return (
    <div className="stat">
      <span className="stat-label">{label}</span>
      <span className="stat-value" style={{ color }}>
        {value}
      </span>
      {note && <span className="stat-note">{note}</span>}
    </div>
  )
}
