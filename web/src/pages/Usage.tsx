import { useMemo, useState } from 'react'
import { Select } from 'antd'
import { useTranslation } from 'react-i18next'
import { qs, tzOffsetMinutes, useApi } from '../api'
import type { HeatCell, MetricPoint, SubjectTotal } from '../types'
import { BarChart, Heatmap, LineChart, formatBytes, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'
import i18n from '../i18n'

/**
 * Usage series the backend collects. Each entry fixes the unit and the
 * aggregate, so a chart never mixes measures of different scale on one axis.
 */
const SERIES = [
  {
    id: 'docker-net',
    labelKey: 'usage.series.dockerNet',
    source: 'docker',
    metric: 'net_rx_bytes',
    agg: 'sum',
    unitKey: 'usage.unit.bytes',
    format: formatBytes,
  },
  {
    id: 'docker-cpu',
    labelKey: 'usage.series.dockerCpu',
    source: 'docker',
    metric: 'cpu_pct',
    agg: 'avg',
    unitKey: 'usage.unit.percent',
    format: (n: number) => `${n.toFixed(1)}%`,
  },
  {
    id: 'docker-mem',
    labelKey: 'usage.series.dockerMem',
    source: 'docker',
    metric: 'mem_bytes',
    agg: 'avg',
    unitKey: 'usage.unit.bytes',
    format: formatBytes,
  },
  {
    id: 'firewall-bytes',
    labelKey: 'usage.series.firewallBytes',
    source: 'iptables',
    metric: 'bytes',
    agg: 'sum',
    unitKey: 'usage.unit.bytes',
    format: formatBytes,
  },
  {
    id: 'nginx-requests',
    labelKey: 'usage.series.nginxRequests',
    source: 'nginx_log',
    metric: 'requests',
    agg: 'sum',
    unitKey: 'usage.unit.requests',
    format: (n: number) => formatNumber(n),
  },
  {
    id: 'nginx-errors',
    labelKey: 'usage.series.nginxErrors',
    source: 'nginx_log',
    metric: 'errors_5xx',
    agg: 'sum',
    unitKey: 'usage.unit.responses',
    format: (n: number) => formatNumber(n),
  },
  {
    id: 'haproxy-requests',
    labelKey: 'usage.series.haproxyRequests',
    source: 'haproxy_log',
    metric: 'requests',
    agg: 'sum',
    unitKey: 'usage.unit.requests',
    format: (n: number) => formatNumber(n),
  },
] as const

const RANGES = [
  { value: '24h', labelKey: 'availability.range.day', granularity: 'hour' },
  { value: '7d', labelKey: 'availability.range.week', granularity: 'hour' },
  { value: '30d', labelKey: 'availability.range.month', granularity: 'day' },
]

/** Past eight series the palette folds the tail into usage.other rather than cycling hues. */
const MAX_SERIES = 8

export default function Usage() {
  const { t } = useTranslation()
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

    const other = i18n.t('usage.other')
    const grouped = new Map<string, Map<string, number>>()
    for (const p of points) {
      const name = keep.has(p.subject) ? p.subject : other
      const bucketMap = grouped.get(name) ?? new Map<string, number>()
      bucketMap.set(p.bucket, (bucketMap.get(p.bucket) ?? 0) + p.value)
      grouped.set(name, bucketMap)
    }

    // Preserve the ranked order so colour follows the entity, not the loop index.
    const order = [...ranked.filter((n) => keep.has(n)), ...(grouped.has(other) ? [other] : [])]
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
            {t('usage.title')}
            <InfoHint>{t('usage.hint')}</InfoHint>
          </h1>
        </div>
        <div className="row">
          <label>
            {t('usage.metric')}
            <Select
              value={seriesId}
              onChange={setSeriesId}
              style={{ minWidth: '16rem' }}
              options={SERIES.map((s) => ({ value: s.id, label: t(s.labelKey) }))}
            />
          </label>
          <label>
            {t('common.period')}
            <Select
              value={range}
              onChange={setRange}
              style={{ minWidth: '8rem' }}
              options={RANGES.map((r) => ({ value: r.value, label: t(r.labelKey) }))}
            />
          </label>
        </div>
      </div>

      <ErrorNote error={usage.error} />
      {usage.data?.simulated && <Banner kind="warn">{t('usage.simulated')}</Banner>}

      <Card
        title={t(spec.labelKey)}
        subtitle={
          t('usage.unitLabel', { unit: t(spec.unitKey) }) +
          (usage.data?.total != null ? t('usage.totalOnHost', { value: spec.format(usage.data.total) }) : '')
        }
      >
        {usage.loading && !usage.data ? (
          <Loading what={t('usage.metrics')} />
        ) : (
          <LineChart
            series={chartSeries}
            formatValue={spec.format}
            formatX={(x) => shortLabel(x, rangeSpec.granularity)}
            height={260}
            reference={
              usage.data?.total != null
                ? { value: usage.data.total, label: t('usage.total', { value: spec.format(usage.data.total) }) }
                : undefined
            }
          />
        )}
      </Card>

      <div className="grid grid-2">
        <Card title={t('usage.topLoad')} subtitle={t('usage.sumForPeriod', { unit: t(spec.unitKey) })}>
          {top.loading && !top.data ? (
            <Loading what={t('usage.rating')} />
          ) : (
            <BarChart
              data={(top.data?.top ?? []).map((s) => ({
                label: s.subject,
                value: s.total,
                note: t('usage.measurementsCount', { count: formatNumber(s.samples) }),
              }))}
              formatValue={spec.format}
            />
          )}
        </Card>

        <Card
          title={
            <>
              {t('usage.usageSchedule')}
              <InfoHint>{t('usage.scheduleHint')}</InfoHint>
            </>
          }
        >
          {heat.loading && !heat.data ? (
            <Loading what={t('common.schedule')} />
          ) : (
            <Heatmap
              cells={heat.data?.cells ?? []}
              scaleLabel={t('usage.load')}
              formatValue={spec.format}
              emptyLabel={t('usage.noMeasurementsThisHour')}
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
  const locale = i18n.language === 'en' ? 'en-US' : 'ru-RU'
  if (granularity === 'day') {
    return d.toLocaleDateString(locale, { day: '2-digit', month: '2-digit' })
  }
  return d.toLocaleString(locale, { day: '2-digit', month: '2-digit', hour: '2-digit' })
}
