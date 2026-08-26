import { useMemo, useState } from 'react'
import { Button, Select, Table, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, tzOffsetMinutes, useApi } from '../api'
import type { Bucket, HeatCell, Outage, TargetStatus } from '../types'
import { Heatmap, LineChart, StatTile, formatMs, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, InfoHint, Loading, StateBadge, formatDateTime } from '../components/ui'
import i18n from '../i18n'

interface TargetsResponse {
  targets: TargetStatus[]
  simulated: boolean
  interval: string
}

const RANGES = [
  { value: '24h', labelKey: 'availability.range.day' },
  { value: '7d', labelKey: 'availability.range.week' },
  { value: '14d', labelKey: 'availability.range.twoWeeks' },
  { value: '30d', labelKey: 'availability.range.month' },
]

// Module-level column builders take t() as an argument rather than calling
// useTranslation() themselves — see Overview.tsx's serviceColumns for the
// same pattern.
function targetColumns(
  t: typeof i18n.t,
  checking: number | null,
  checkNow: (id: number) => void,
  toggle: (id: number, enabled: boolean) => void,
): TableColumnsType<TargetStatus> {
  return [
    {
      title: t('availability.col.resource'),
      key: 'label',
      render: (_, tgt) => (
        <>
          <strong>{tgt.label}</strong>
          <div className="small muted">{tgt.source}</div>
        </>
      ),
    },
    {
      title: t('availability.col.address'),
      key: 'addr',
      render: (_, tgt) => (
        <span className="mono small nowrap">
          {tgt.kind}://{tgt.host}:{tgt.port}
          {tgt.host_header ? ` (Host: ${tgt.host_header})` : ''}
        </span>
      ),
    },
    {
      title: t('availability.col.now'),
      key: 'last_ok',
      render: (_, tgt) => (
        <>
          <StateBadge state={tgt.last_ok === undefined ? t('availability.noData') : tgt.last_ok ? 'active' : 'failed'} />
          {tgt.last_error && <div className="small muted mono">{tgt.last_error}</div>}
        </>
      ),
    },
    {
      title: t('availability.col.uptime24h'),
      key: 'uptime_24h',
      align: 'right',
      render: (_, tgt) => <span className="num">{tgt.checks_24h ? `${tgt.uptime_24h.toFixed(1)}%` : '—'}</span>,
    },
    {
      title: t('availability.col.latency'),
      key: 'last_latency_ms',
      align: 'right',
      render: (_, tgt) => <span className="num">{tgt.last_latency_ms ? formatMs(tgt.last_latency_ms) : '—'}</span>,
    },
    { title: t('availability.col.checks'), dataIndex: 'checks_24h', key: 'checks_24h', align: 'right' },
    {
      title: '',
      key: 'actions',
      render: (_, tgt) => (
        <div className="row" onClick={(e) => e.stopPropagation()}>
          <Button type="link" size="small" disabled={checking === tgt.id} onClick={() => checkNow(tgt.id)}>
            {checking === tgt.id ? '…' : t('availability.check')}
          </Button>
          <Button type="link" size="small" onClick={() => toggle(tgt.id, !tgt.enabled)}>
            {tgt.enabled ? t('availability.pause') : t('availability.enable')}
          </Button>
        </div>
      ),
    },
  ]
}

function outageColumns(t: typeof i18n.t): TableColumnsType<Outage> {
  return [
    { title: t('availability.col.resource'), dataIndex: 'label', key: 'label' },
    { title: t('availability.col.start'), key: 'start', render: (_, o) => <span className="small nowrap">{formatDateTime(o.start)}</span> },
    { title: t('availability.col.end'), key: 'end', render: (_, o) => <span className="small nowrap">{formatDateTime(o.end)}</span> },
    { title: t('availability.col.checks'), dataIndex: 'checks', key: 'checks', align: 'right' },
    { title: t('availability.col.error'), key: 'error', render: (_, o) => <span className="small mono">{o.error}</span> },
  ]
}

export default function Availability() {
  const { t } = useTranslation()
  const [range, setRange] = useState('7d')
  const [selected, setSelected] = useState<number | null>(null)
  const [checking, setChecking] = useState<number | null>(null)
  const tz = tzOffsetMinutes()

  const targets = useApi<TargetsResponse>('/monitor/targets', 60_000)
  const history = useApi<{ target: TargetStatus; buckets: Bucket[] }>(
    selected ? `/monitor/targets/${selected}/history${qs({ since: range, granularity: 'hour', tz })}` : null,
  )
  const heatmap = useApi<{ cells: HeatCell[] }>(
    `/monitor/heatmap${qs({ since: range === '24h' ? '7d' : range, tz, target: selected ?? undefined })}`,
  )
  const outages = useApi<{ outages: Outage[] }>(`/monitor/outages${qs({ since: range, limit: 40 })}`)

  const sorted = useMemo(() => {
    const list = targets.data?.targets ?? []
    return [...list].sort((a, b) => {
      const aBad = a.last_ok === false ? 0 : 1
      const bBad = b.last_ok === false ? 0 : 1
      if (aBad !== bBad) return aBad - bBad
      return a.uptime_24h - b.uptime_24h
    })
  }, [targets.data])

  const summary = useMemo(() => {
    const list = targets.data?.targets ?? []
    const withData = list.filter((t) => t.checks_24h > 0)
    const avg = withData.length
      ? withData.reduce((acc, t) => acc + t.uptime_24h, 0) / withData.length
      : 0
    const latency = withData.length
      ? withData.reduce((acc, t) => acc + t.avg_latency_24h, 0) / withData.length
      : 0
    return {
      total: list.length,
      down: list.filter((t) => t.last_ok === false).length,
      avg,
      latency,
    }
  }, [targets.data])

  // The heatmap encodes downtime, not uptime: the eye is drawn to dark cells,
  // and the question being asked is "когда было недоступно".
  const downtimeCells = useMemo(
    () =>
      (heatmap.data?.cells ?? []).map((c) => ({
        dow: c.dow,
        hour: c.hour,
        value: Math.max(0, 100 - c.uptime),
        total: c.total,
      })),
    [heatmap.data],
  )

  async function checkNow(id: number) {
    setChecking(id)
    try {
      await api(`/monitor/targets/${id}/check`, { method: 'POST' })
      targets.reload()
    } finally {
      setChecking(null)
    }
  }

  async function toggle(id: number, enabled: boolean) {
    await api(`/monitor/targets/${id}`, { method: 'PATCH', body: { enabled } })
    targets.reload()
  }

  if (targets.loading && !targets.data) return <Loading what={t('availability.what')} />

  const selectedTarget = sorted.find((t) => t.id === selected) ?? null

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            {t('availability.title')}
            <InfoHint>{t('availability.hint', { interval: targets.data?.interval ?? '—' })}</InfoHint>
          </h1>
        </div>
        <label>
          {t('common.period')}
          <Select
            value={range}
            onChange={setRange}
            options={RANGES.map((r) => ({ value: r.value, label: t(r.labelKey) }))}
            style={{ minWidth: '8rem' }}
          />
        </label>
      </div>

      <ErrorNote error={targets.error} />
      {targets.data?.simulated && <Banner kind="warn">{t('availability.simulated')}</Banner>}

      <div className="grid grid-4">
        <StatTile label={t('availability.targetsMonitored')} value={formatNumber(summary.total)} />
        <StatTile
          label={t('availability.downNow')}
          value={formatNumber(summary.down)}
          tone={summary.down > 0 ? 'critical' : 'good'}
        />
        <StatTile label={t('availability.avgUptime24h')} value={`${summary.avg.toFixed(2)}%`} />
        <StatTile label={t('availability.avgLatency')} value={formatMs(summary.latency)} />
      </div>

      <Card
        title={
          <>
            {selectedTarget
              ? t('availability.downtimeByHourFor', { label: selectedTarget.label })
              : t('availability.downtimeByHourAll')}
            <InfoHint>{t('availability.heatmapHint')}</InfoHint>
          </>
        }
        actions={
          selected ? (
            <Button type="link" onClick={() => setSelected(null)}>
              {t('availability.showAllTargets')}
            </Button>
          ) : null
        }
      >
        {heatmap.loading && !heatmap.data ? (
          <Loading what={t('common.schedule')} />
        ) : (
          <Heatmap
            cells={downtimeCells}
            scaleLabel={t('availability.downtimeScale')}
            formatValue={(n) => `${n.toFixed(1)}%`}
            emptyLabel={t('availability.noChecksThisHour')}
          />
        )}
      </Card>

      {selectedTarget && (
        <Card
          title={t('availability.availabilityAndLatencyFor', { label: selectedTarget.label })}
          subtitle={`${selectedTarget.kind}://${selectedTarget.host}:${selectedTarget.port}${selectedTarget.path ?? ''}`}
        >
          {history.loading && !history.data ? (
            <Loading what={t('availability.history')} />
          ) : (
            <>
              <LineChart
                series={[
                  {
                    name: t('availability.uptimePercentSeries'),
                    points: (history.data?.buckets ?? []).map((b) => ({ x: b.bucket, y: b.uptime })),
                  },
                ]}
                yMax={101}
                formatValue={(n) => `${n.toFixed(0)}%`}
                formatX={shortTime}
                area
              />
              <div style={{ marginTop: '0.75rem' }}>
                <LineChart
                  series={[
                    {
                      name: t('availability.avgLatencySeries'),
                      points: (history.data?.buckets ?? []).map((b) => ({
                        x: b.bucket,
                        y: b.avg_latency_ms,
                      })),
                    },
                    {
                      name: t('availability.maxLatencySeries'),
                      points: (history.data?.buckets ?? []).map((b) => ({
                        x: b.bucket,
                        y: b.max_latency_ms,
                      })),
                    },
                  ]}
                  formatValue={formatMs}
                  formatX={shortTime}
                  yUnit={t('availability.msUnit')}
                />
              </div>
            </>
          )}
        </Card>
      )}

      <Card
        title={
          <>
            {t('availability.resources')}
            <InfoHint>{t('availability.resourcesHint')}</InfoHint>
          </>
        }
      >
        <div className="table-wrap">
          <Table<TargetStatus>
            dataSource={sorted}
            rowKey="id"
            pagination={false}
            size="small"
            onRow={(tgt) => ({
              onClick: () => setSelected(tgt.id === selected ? null : tgt.id),
              style: { cursor: 'pointer', opacity: tgt.enabled ? 1 : 0.5 },
            })}
            columns={targetColumns(t, checking, checkNow, toggle)}
          />
        </div>
      </Card>

      <Card
        title={
          <>
            {t('availability.outages')}
            <InfoHint>{t('availability.outagesHint')}</InfoHint>
          </>
        }
      >
        {outages.data?.outages.length ? (
          <div className="table-wrap">
            <Table<Outage>
              dataSource={outages.data.outages}
              rowKey={(_, i) => i ?? 0}
              pagination={false}
              size="small"
              columns={outageColumns(t)}
            />
          </div>
        ) : (
          <div className="chart-empty">{t('availability.noOutagesForPeriod')}</div>
        )}
      </Card>
    </>
  )
}

function shortTime(iso: string): string {
  const d = new Date(iso.length === 13 ? `${iso}:00Z` : iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(i18n.language === 'en' ? 'en-US' : 'ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit' })
}
