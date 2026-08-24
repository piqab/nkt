import { useMemo, useState } from 'react'
import { Select } from 'antd'
import { qs, tzOffsetMinutes, useApi } from '../api'
import type { HeatCell, MetricPoint, SubjectTotal } from '../types'
import { BarChart, Heatmap, LineChart, formatBytes, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'

/**
 * Usage series the backend collects. Each entry fixes the unit and the
 * aggregate, so a chart never mixes measures of different scale on one axis.
 */
const SERIES = [
  {
    id: 'docker-net',
    label: 'Сетевой трафик контейнеров',
    source: 'docker',
    metric: 'net_rx_bytes',
    agg: 'sum',
    unit: 'байт',
    format: formatBytes,
  },
  {
    id: 'docker-cpu',
    label: 'CPU контейнеров',
    source: 'docker',
    metric: 'cpu_pct',
    agg: 'avg',
    unit: '%',
    format: (n: number) => `${n.toFixed(1)}%`,
  },
  {
    id: 'docker-mem',
    label: 'Память контейнеров',
    source: 'docker',
    metric: 'mem_bytes',
    agg: 'avg',
    unit: 'байт',
    format: formatBytes,
  },
  {
    id: 'firewall-bytes',
    label: 'Трафик по правилам firewall',
    source: 'iptables',
    metric: 'bytes',
    agg: 'sum',
    unit: 'байт',
    format: formatBytes,
  },
  {
    id: 'nginx-requests',
    label: 'Запросы nginx (из access-логов)',
    source: 'nginx_log',
    metric: 'requests',
    agg: 'sum',
    unit: 'запросов',
    format: (n: number) => formatNumber(n),
  },
  {
    id: 'nginx-errors',
    label: 'Ошибки 5xx у nginx',
    source: 'nginx_log',
    metric: 'errors_5xx',
    agg: 'sum',
    unit: 'ответов',
    format: (n: number) => formatNumber(n),
  },
  {
    id: 'haproxy-requests',
    label: 'Запросы haproxy',
    source: 'haproxy_log',
    metric: 'requests',
    agg: 'sum',
    unit: 'запросов',
    format: (n: number) => formatNumber(n),
  },
] as const

const RANGES = [
  { value: '24h', label: 'сутки', granularity: 'hour' },
  { value: '7d', label: '7 дней', granularity: 'hour' },
  { value: '30d', label: '30 дней', granularity: 'day' },
]

/** Past eight series the palette folds the tail into "Другое" rather than cycling hues. */
const MAX_SERIES = 8

export default function Usage() {
  const [seriesId, setSeriesId] = useState<string>(SERIES[0].id)
  const [range, setRange] = useState('7d')
  const tz = tzOffsetMinutes()

  const spec = SERIES.find((s) => s.id === seriesId)!
  const rangeSpec = RANGES.find((r) => r.value === range)!

  const usage = useApi<{ points: MetricPoint[]; simulated: boolean; total: number | null }>(
    `/monitor/usage${qs({
      source: spec.source,
      metric: spec.metric,
      agg: spec.agg,
      since: range,
      granularity: rangeSpec.granularity,
      tz,
    })}`,
    120_000,
  )
  const top = useApi<{ top: SubjectTotal[] }>(
    `/monitor/usage/top${qs({ source: spec.source, metric: spec.metric, since: range, limit: 10 })}`,
  )
  const heat = useApi<{ cells: HeatCell[] }>(
    `/monitor/usage/heatmap${qs({ source: spec.source, metric: spec.metric, since: range === '24h' ? '7d' : range, tz })}`,
  )

  const chartSeries = useMemo(() => {
    const points = usage.data?.points ?? []
    const totals = new Map<string, number>()
    for (const p of points) totals.set(p.subject, (totals.get(p.subject) ?? 0) + p.value)

    const ranked = [...totals.entries()].sort((a, b) => b[1] - a[1]).map(([name]) => name)
    const keep = new Set(ranked.slice(0, MAX_SERIES - (ranked.length > MAX_SERIES ? 1 : 0)))

    const grouped = new Map<string, Map<string, number>>()
    for (const p of points) {
      const name = keep.has(p.subject) ? p.subject : 'Другое'
      const bucketMap = grouped.get(name) ?? new Map<string, number>()
      bucketMap.set(p.bucket, (bucketMap.get(p.bucket) ?? 0) + p.value)
      grouped.set(name, bucketMap)
    }

    // Preserve the ranked order so colour follows the entity, not the loop index.
    const order = [...ranked.filter((n) => keep.has(n)), ...(grouped.has('Другое') ? ['Другое'] : [])]
    return order
      .filter((name) => grouped.has(name))
      .map((name) => ({
        name,
        points: [...grouped.get(name)!.entries()]
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([x, y]) => ({ x, y })),
      }))
  }, [usage.data])

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            Использование сетевых ресурсов
            <InfoHint>
              Счётчики firewall, статистика контейнеров и разбор access-логов nginx и haproxy. Логи
              разбираются по времени записи, поэтому график показывает, когда нагрузка была на самом
              деле, а не когда её собрали.
            </InfoHint>
          </h1>
        </div>
        <div className="row">
          <label>
            Показатель
            <Select
              value={seriesId}
              onChange={setSeriesId}
              style={{ minWidth: '16rem' }}
              options={SERIES.map((s) => ({ value: s.id, label: s.label }))}
            />
          </label>
          <label>
            Период
            <Select value={range} onChange={setRange} style={{ minWidth: '8rem' }} options={RANGES} />
          </label>
        </div>
      </div>

      <ErrorNote error={usage.error} />
      {usage.data?.simulated && (
        <Banner kind="warn">
          Метрики синтетические: снимок хоста статичен, поэтому счётчики моделируются по
          суточному профилю нагрузки. На реальном хосте здесь будут приросты счётчиков iptables и
          docker stats.
        </Banner>
      )}

      <Card
        title={spec.label}
        subtitle={
          `Единица измерения: ${spec.unit}` +
          (usage.data?.total != null ? ` · всего на хосте: ${spec.format(usage.data.total)}` : '')
        }
      >
        {usage.loading && !usage.data ? (
          <Loading what="метрики" />
        ) : (
          <LineChart
            series={chartSeries}
            formatValue={spec.format}
            formatX={(x) => shortLabel(x, rangeSpec.granularity)}
            height={260}
            reference={
              usage.data?.total != null
                ? { value: usage.data.total, label: `всего: ${spec.format(usage.data.total)}` }
                : undefined
            }
          />
        )}
      </Card>

      <div className="grid grid-2">
        <Card title="Кто нагружает больше всех" subtitle={`Сумма за период, ${spec.unit}`}>
          {top.loading && !top.data ? (
            <Loading what="рейтинг" />
          ) : (
            <BarChart
              data={(top.data?.top ?? []).map((t) => ({
                label: t.subject,
                value: t.total,
                note: `измерений: ${formatNumber(t.samples)}`,
              }))}
              formatValue={spec.format}
            />
          )}
        </Card>

        <Card
          title={
            <>
              Расписание использования
              <InfoHint>Средняя нагрузка по часам недели: видно рабочие часы, ночные окна и выходные.</InfoHint>
            </>
          }
        >
          {heat.loading && !heat.data ? (
            <Loading what="расписание" />
          ) : (
            <Heatmap
              cells={heat.data?.cells ?? []}
              scaleLabel="нагрузка"
              formatValue={spec.format}
              emptyLabel="в этот час измерений не было"
            />
          )}
        </Card>
      </div>
    </>
  )
}

function shortLabel(x: string, granularity: string): string {
  const iso = x.length === 13 ? `${x}:00Z` : x.length === 10 ? `${x}T00:00:00Z` : x
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return x
  if (granularity === 'day') {
    return d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
  }
  return d.toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit' })
}
