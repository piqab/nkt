import { useMemo, useState } from 'react'
import { Button, Select, Table, type TableColumnsType } from 'antd'
import { api, qs, tzOffsetMinutes, useApi } from '../api'
import type { Bucket, HeatCell, Outage, TargetStatus } from '../types'
import { Heatmap, LineChart, StatTile, formatMs, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, Loading, StateBadge, formatDateTime } from '../components/ui'

interface TargetsResponse {
  targets: TargetStatus[]
  simulated: boolean
  interval: string
}

const RANGES = [
  { value: '24h', label: 'сутки' },
  { value: '7d', label: '7 дней' },
  { value: '14d', label: '14 дней' },
  { value: '30d', label: '30 дней' },
]

function targetColumns(
  checking: number | null,
  checkNow: (id: number) => void,
  toggle: (id: number, enabled: boolean) => void,
): TableColumnsType<TargetStatus> {
  return [
    {
      title: 'Ресурс',
      key: 'label',
      render: (_, t) => (
        <>
          <strong>{t.label}</strong>
          <div className="small muted">{t.source}</div>
        </>
      ),
    },
    {
      title: 'Адрес',
      key: 'addr',
      render: (_, t) => (
        <span className="mono small nowrap">
          {t.kind}://{t.host}:{t.port}
          {t.host_header ? ` (Host: ${t.host_header})` : ''}
        </span>
      ),
    },
    {
      title: 'Сейчас',
      key: 'last_ok',
      render: (_, t) => (
        <>
          <StateBadge state={t.last_ok === undefined ? 'нет данных' : t.last_ok ? 'active' : 'failed'} />
          {t.last_error && <div className="small muted mono">{t.last_error}</div>}
        </>
      ),
    },
    {
      title: 'Доступность 24 ч',
      key: 'uptime_24h',
      align: 'right',
      render: (_, t) => <span className="num">{t.checks_24h ? `${t.uptime_24h.toFixed(1)}%` : '—'}</span>,
    },
    {
      title: 'Задержка',
      key: 'last_latency_ms',
      align: 'right',
      render: (_, t) => <span className="num">{t.last_latency_ms ? formatMs(t.last_latency_ms) : '—'}</span>,
    },
    { title: 'Проверок', dataIndex: 'checks_24h', key: 'checks_24h', align: 'right' },
    {
      title: '',
      key: 'actions',
      render: (_, t) => (
        <div className="row" onClick={(e) => e.stopPropagation()}>
          <Button type="link" size="small" disabled={checking === t.id} onClick={() => checkNow(t.id)}>
            {checking === t.id ? '…' : 'проверить'}
          </Button>
          <Button type="link" size="small" onClick={() => toggle(t.id, !t.enabled)}>
            {t.enabled ? 'пауза' : 'включить'}
          </Button>
        </div>
      ),
    },
  ]
}

const outageColumns: TableColumnsType<Outage> = [
  { title: 'Ресурс', dataIndex: 'label', key: 'label' },
  { title: 'Начало', key: 'start', render: (_, o) => <span className="small nowrap">{formatDateTime(o.start)}</span> },
  { title: 'Окончание', key: 'end', render: (_, o) => <span className="small nowrap">{formatDateTime(o.end)}</span> },
  { title: 'Проверок', dataIndex: 'checks', key: 'checks', align: 'right' },
  { title: 'Ошибка', key: 'error', render: (_, o) => <span className="small mono">{o.error}</span> },
]

export default function Availability() {
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

  if (targets.loading && !targets.data) return <Loading what="цели мониторинга" />

  const selectedTarget = sorted.find((t) => t.id === selected) ?? null

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>Расписание доступности</h1>
          <p>
            Каждый объявленный слушатель и каждый backend из пулов проверяется по расписанию
            (интервал {targets.data?.interval ?? '—'}). Ниже — когда ресурсы реально были доступны,
            а не только их текущее состояние.
          </p>
        </div>
        <label>
          Период
          <Select value={range} onChange={setRange} options={RANGES} style={{ minWidth: '8rem' }} />
        </label>
      </div>

      <ErrorNote error={targets.error} />
      {targets.data?.simulated && (
        <Banner kind="warn">
          Значения проб синтетические: в режиме снапшота описанных сокетов на этой машине не
          существует, поэтому результаты моделируются, а не измеряются.
        </Banner>
      )}

      <div className="grid grid-4">
        <StatTile label="Целей под наблюдением" value={formatNumber(summary.total)} />
        <StatTile
          label="Недоступно сейчас"
          value={formatNumber(summary.down)}
          tone={summary.down > 0 ? 'critical' : 'good'}
        />
        <StatTile label="Средняя доступность за 24 ч" value={`${summary.avg.toFixed(2)}%`} />
        <StatTile label="Средняя задержка" value={formatMs(summary.latency)} />
      </div>

      <Card
        title={
          selectedTarget
            ? `Недоступность по часам недели — ${selectedTarget.label}`
            : 'Недоступность по часам недели — все ресурсы'
        }
        subtitle="Каждая клетка — час недели. Чем темнее, тем больше проверок в этот час завершились ошибкой."
        actions={
          selected ? (
            <Button type="link" onClick={() => setSelected(null)}>
              показать все ресурсы
            </Button>
          ) : null
        }
      >
        {heatmap.loading && !heatmap.data ? (
          <Loading what="расписание" />
        ) : (
          <Heatmap
            cells={downtimeCells}
            scaleLabel="недоступность"
            formatValue={(n) => `${n.toFixed(1)}%`}
            emptyLabel="в этот час проверок не было"
          />
        )}
      </Card>

      {selectedTarget && (
        <Card
          title={`Доступность и задержка — ${selectedTarget.label}`}
          subtitle={`${selectedTarget.kind}://${selectedTarget.host}:${selectedTarget.port}${selectedTarget.path ?? ''}`}
        >
          {history.loading && !history.data ? (
            <Loading what="историю" />
          ) : (
            <>
              <LineChart
                series={[
                  {
                    name: 'доступность, %',
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
                      name: 'средняя задержка',
                      points: (history.data?.buckets ?? []).map((b) => ({
                        x: b.bucket,
                        y: b.avg_latency_ms,
                      })),
                    },
                    {
                      name: 'максимальная задержка',
                      points: (history.data?.buckets ?? []).map((b) => ({
                        x: b.bucket,
                        y: b.max_latency_ms,
                      })),
                    },
                  ]}
                  formatValue={formatMs}
                  formatX={shortTime}
                  yUnit="мс"
                />
              </div>
            </>
          )}
        </Card>
      )}

      <Card
        title="Ресурсы"
        subtitle="Щёлкните по строке, чтобы посмотреть историю конкретного ресурса."
      >
        <div className="table-wrap">
          <Table<TargetStatus>
            dataSource={sorted}
            rowKey="id"
            pagination={false}
            size="small"
            onRow={(t) => ({
              onClick: () => setSelected(t.id === selected ? null : t.id),
              style: { cursor: 'pointer', opacity: t.enabled ? 1 : 0.5 },
            })}
            columns={targetColumns(checking, checkNow, toggle)}
          />
        </div>
      </Card>

      <Card title="Простои" subtitle={`Непрерывные серии неудачных проверок за выбранный период`}>
        {outages.data?.outages.length ? (
          <div className="table-wrap">
            <Table<Outage>
              dataSource={outages.data.outages}
              rowKey={(_, i) => i ?? 0}
              pagination={false}
              size="small"
              columns={outageColumns}
            />
          </div>
        ) : (
          <div className="chart-empty">Простоев за период не зафиксировано.</div>
        )}
      </Card>
    </>
  )
}

function shortTime(iso: string): string {
  const d = new Date(iso.length === 13 ? `${iso}:00Z` : iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit' })
}
