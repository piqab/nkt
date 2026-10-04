import { useMemo, useState } from 'react'
import { Button, Input, InputNumber, Select, Space, Tabs, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, DiffView, ErrorNote, Loading, Modal, formatRelative } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { Heatmap, LineChart, formatBytes, type Series } from '../components/charts'
import { TitleHelp } from '../components/Docs'
import { unifiedDiff } from '../components/textDiff'
import { AIExplain } from '../components/AIExplain'
import { Sensitive } from '../privacy'

type Range = '24h' | '7d' | '30d' | '90d' | '365d'

interface MonDisk {
  mount: string
  used: number
  size: number
  pct: number
  eta_days?: number
}

interface MonHost {
  id: number
  name: string
  group?: string
  reachable?: boolean
  old?: boolean
  error?: string
  collected_at?: string
  has_data: boolean
  cpu_avg: number
  cpu_max: number
  cpu_now: number
  mem_used: number
  mem_total: number
  mem_avg_pct: number
  mem_max_pct: number
  load_avg: number
  load_max: number
  disks: MonDisk[]
  workloads: number
}

interface MonWorkload {
  host_id: number
  host: string
  source: string
  subject: string
  cpu_avg: number
  cpu_max: number
  mem_avg: number
  mem_max: number
  net_rx: number
  net_tx: number
}

interface MonTarget {
  host_id: number
  host: string
  key: string
  label: string
  kind: string
  service: string
  ok: number
  total: number
  uptime?: number
  latency: number
}

interface Insight {
  kind: string
  severity: 'critical' | 'warning' | 'info'
  tab: 'load' | 'availability'
  host_id: number
  host: string
  source?: string
  subject?: string
  value?: number
  link?: string
  path?: string
  text: string
  quiet_window?: [number, number]
}

interface MonSettings {
  disk_warn_days: number
  disk_crit_days: number
  mem_pct: number
  cpu_pct: number
  leak_days: number
  leak_growth_pct: number
  avail_drop_pp: number
}

interface Overview {
  hosts: MonHost[]
  workloads: MonWorkload[]
  targets: MonTarget[]
  heat_avail: (number | null)[][]
  heat_cpu: (number | null)[][]
  insights: Insight[]
  collecting: boolean
  last_run?: string
  settings: MonSettings
  daily: boolean
}

const SEV_COLOR = { critical: 'red', warning: 'orange', info: 'blue' } as const
const SOURCE_LABEL: Record<string, string> = { docker: 'Docker', podman: 'Podman', lxd: 'LXD', libvirt: 'VM', k8s: 'Kubernetes' }

/** Сдвиг UTC → местное время браузера в часах. */
const tzShift = () => -Math.round(new Date().getTimezoneOffset() / 60)

/** Час недели (UTC) → местный. */
function toLocal(dow: number, hour: number): [number, number] {
  let h = hour + tzShift()
  let d = dow
  while (h < 0) {
    h += 24
    d = (d + 6) % 7
  }
  while (h >= 24) {
    h -= 24
    d = (d + 1) % 7
  }
  return [d, h]
}

function heatCells(grid: (number | null)[][] | undefined, value: (v: number) => number) {
  const out: { dow: number; hour: number; value: number; total?: number }[] = []
  ;(grid ?? []).forEach((row, d) =>
    row.forEach((v, h) => {
      if (v === null || v === undefined) return
      const [ld, lh] = toLocal(d, h)
      out.push({ dow: ld, hour: lh, value: value(v), total: 1 })
    }),
  )
  return out
}

/**
 * «Мониторинг» хаба: доступность и нагрузка всех хостов по истории хаба
 * (он собирает почасовые сводки с хостов раз в час и хранит дольше их),
 * прогнозы по трендам и подсказки, что с этим делать. Хаб сам ничего не
 * меняет — подсказка ведёт в раздел хоста, где это решается.
 */
export default function HubMonitoring({ me, onOpenHost }: { me: Me; onOpenHost: (id: number, name: string, path: string) => void }) {
  const { t } = useTranslation()
  const [range, setRange] = useState<Range>('7d')
  const data = useApi<Overview>(`/hub/monitoring/overview?range=${range}`, 5 * 60_000)
  const [busy, setBusy] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [hostOpen, setHostOpen] = useState<MonHost | null>(null)
  const [chart, setChart] = useState<{ title: string; hostId: number; source: string; subject: string; metric?: string } | null>(null)
  const d = data.data

  async function collect() {
    setBusy(true)
    try {
      await api('/hub/monitoring/collect', { method: 'POST' })
      window.setTimeout(() => void data.reload(), 4000)
    } finally {
      setBusy(false)
    }
  }

  const insightList = (tab: Insight['tab']) => {
    const list = (d?.insights ?? []).filter((i) => i.tab === tab)
    return (
      <Card title={t('monitoring.insightsTitle')} subtitle={t('monitoring.insightsHint')}>
        {list.length === 0 ? (
          <span className="small muted">{t('monitoring.noInsights')}</span>
        ) : (
          <div className="col" style={{ gap: '0.35rem' }}>
            {list.map((i, n) => (
              <div key={n} className="row" style={{ gap: '0.5rem', alignItems: 'flex-start', flexWrap: 'wrap' }}>
                <Tag color={SEV_COLOR[i.severity]}>{t(`monitoring.sev.${i.severity}`)}</Tag>
                <span style={{ flex: 1, minWidth: '16rem' }}>
                  {i.kind === 'quiet_window' && i.quiet_window
                    ? (() => {
                        const [ld, lh] = toLocal(i.quiet_window[0], i.quiet_window[1])
                        return t('monitoring.quietWindowLocal', {
                          day: t(['charts.dowSun', 'charts.dowMon', 'charts.dowTue', 'charts.dowWed', 'charts.dowThu', 'charts.dowFri', 'charts.dowSat'][ld]),
                          from: `${String(lh).padStart(2, '0')}:00`,
                          to: `${String((lh + 2) % 24).padStart(2, '0')}:00`,
                          load: i.value,
                        })
                      })()
                    : i.text}
                </span>
                {i.link && i.host_id !== 0 && (
                  <Button size="small" onClick={() => onOpenHost(i.host_id, i.host, i.path || `/${i.link}`)}>
                    {t(`monitoring.link.${i.link}`, { defaultValue: t('monitoring.openHost') })}
                  </Button>
                )}
              </div>
            ))}
          </div>
        )}
        {list.length > 0 && (
          <div style={{ marginTop: '0.6rem' }}>
            <AIExplain
              ctx={{
                kind: 'monitoring',
                title: t(tab === 'load' ? 'monitoring.aiTitleLoad' : 'monitoring.aiTitleAvail', { range: t(`monitoring.range.${range}`) }),
                detail: JSON.stringify({ insights: list.map((i) => ({ severity: i.severity, text: i.text })), hosts: tab === 'load' ? loadDigest(d) : availDigest(d) }),
              }}
            />
          </div>
        )}
      </Card>
    )
  }

  const hostColumns: TableColumnsType<MonHost> = [
    {
      title: t('monitoring.colHost'),
      key: 'name',
      render: (_, h) => (
        <div className="col">
          <strong>
            <Sensitive>{h.name}</Sensitive>
          </strong>
          {h.old ? (
            <span className="small" style={{ color: 'var(--status-warning)' }}>{t('monitoring.oldNkt')}</span>
          ) : !h.has_data ? (
            <span className="small muted">{h.error ? t('monitoring.collectError', { error: h.error }) : t('monitoring.noData')}</span>
          ) : null}
        </div>
      ),
    },
    {
      title: t('monitoring.colCPU'),
      key: 'cpu',
      render: (_, h) => (h.has_data ? <span className="small nowrap">{t('monitoring.avgMax', { avg: h.cpu_avg, max: h.cpu_max })}%</span> : '—'),
    },
    {
      title: t('monitoring.colMem'),
      key: 'mem',
      render: (_, h) =>
        h.mem_total > 0 ? (
          <span className="small nowrap">
            {formatBytes(h.mem_used)} / {formatBytes(h.mem_total)} · {t('monitoring.avgMax', { avg: h.mem_avg_pct, max: h.mem_max_pct })}%
          </span>
        ) : (
          '—'
        ),
    },
    { title: t('monitoring.colLoad'), key: 'load', render: (_, h) => (h.has_data ? <span className="small">{h.load_avg} / {h.load_max}</span> : '—') },
    {
      title: t('monitoring.colDisks'),
      key: 'disks',
      render: (_, h) => (
        <div className="col" style={{ gap: '0.1rem' }}>
          {h.disks.map((dk) => (
            <span key={dk.mount} className="small nowrap">
              <span className="mono">{dk.mount}</span> {dk.pct}%{' '}
              {dk.eta_days !== undefined && (
                <Tag color={dk.eta_days <= (d?.settings.disk_crit_days ?? 2) ? 'red' : dk.eta_days <= (d?.settings.disk_warn_days ?? 7) ? 'orange' : 'blue'}>
                  {t('monitoring.diskEta', { days: dk.eta_days })}
                </Tag>
              )}
            </span>
          ))}
        </div>
      ),
    },
    { title: t('monitoring.colWorkloads'), key: 'wl', align: 'right', render: (_, h) => h.workloads || '—' },
  ]

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('monitoring.title')}
          <TitleHelp>{t('monitoring.hint')}</TitleHelp>
        </h1>
        <Space wrap>
          <Select<Range>
            value={range}
            onChange={setRange}
            options={(['24h', '7d', '30d', '90d', '365d'] as Range[]).map((r) => ({ value: r, label: t(`monitoring.range.${r}`) }))}
            style={{ width: '9rem' }}
          />
          {me.is_admin && (
            <>
              <Button loading={busy || d?.collecting} onClick={() => void collect()}>
                {t('monitoring.collectNow')}
              </Button>
              <Button onClick={() => setSettingsOpen(true)}>{t('monitoring.thresholds')}</Button>
            </>
          )}
        </Space>
      </div>
      {d?.last_run && <p className="small muted" style={{ marginTop: '-0.4rem' }}>{t('monitoring.lastRun', { when: formatRelative(d.last_run) })}</p>}
      <ErrorNote error={data.error} />
      {!d ? (
        <Loading what={t('monitoring.title')} />
      ) : (
        <Tabs
          items={[
            {
              key: 'availability',
              label: t('monitoring.tabAvailability'),
              children: (
                <>
                  {insightList('availability')}
                  <Card title={t('monitoring.reachTitle')}>
                    <div className="row" style={{ flexWrap: 'wrap', gap: '0.4rem' }}>
                      {d.hosts.map((h) => (
                        <Tag key={h.id} color={h.reachable === false ? 'red' : h.reachable ? 'green' : 'default'}>
                          <Sensitive>{h.name}</Sensitive>: {h.reachable === false ? t('monitoring.unreachable') : h.reachable ? t('monitoring.reachable') : '—'}
                        </Tag>
                      ))}
                    </div>
                  </Card>
                  <Card title={t('monitoring.targetsTitle')} subtitle={t('monitoring.targetsHint', { count: d.targets.length })}>
                    <TargetsTable targets={d.targets} onOpen={(tg) => setChart({ title: `${tg.host} · ${tg.label}`, hostId: tg.host_id, source: 'probe', subject: tg.key })} />
                  </Card>
                  <Card title={t('monitoring.heatAvailTitle')} subtitle={t('monitoring.heatLocal')}>
                    <Heatmap
                      cells={heatCells(d.heat_avail, (v) => Math.max(0, 100 - v))}
                      scaleLabel={t('availability.downtimeScale')}
                      formatValue={(n) => `${n.toFixed(1)}%`}
                      emptyLabel={t('availability.noChecksThisHour')}
                    />
                  </Card>
                </>
              ),
            },
            {
              key: 'load',
              label: t('monitoring.tabLoad'),
              children: (
                <>
                  {insightList('load')}
                  <Card title={t('monitoring.hostsTitle')} subtitle={t('monitoring.hostsHint')}>
                    <div className="table-wrap">
                      <DataTable<MonHost> dataSource={d.hosts} rowKey="id" columns={hostColumns} onRow={(h) => ({ onClick: () => h.has_data && setHostOpen(h), style: { cursor: h.has_data ? 'pointer' : undefined } })} />
                    </div>
                  </Card>
                  <Card title={t('monitoring.workloadsTitle')} subtitle={t('monitoring.workloadsHint')}>
                    <WorkloadsTable workloads={d.workloads} onOpen={(w) => setChart({ title: `${w.host} · ${w.subject}`, hostId: w.host_id, source: w.source, subject: w.subject })} />
                  </Card>
                  <Card title={t('monitoring.heatCPUTitle')} subtitle={t('monitoring.heatLocal')}>
                    <Heatmap cells={heatCells(d.heat_cpu, (v) => v)} scaleLabel={t('monitoring.cpuScale')} formatValue={(n) => `${n.toFixed(1)}%`} />
                  </Card>
                </>
              ),
            },
          ]}
        />
      )}
      {hostOpen && <HostModal host={hostOpen} range={range} onClose={() => setHostOpen(null)} onWorkload={(w) => setChart(w)} workloads={(d?.workloads ?? []).filter((w) => w.host_id === hostOpen.id)} />}
      {chart && <SeriesModal {...chart} range={range} onClose={() => setChart(null)} />}
      {settingsOpen && d && <SettingsModal saved={d.settings} onClose={() => setSettingsOpen(false)} onSaved={() => void data.reload()} />}
    </>
  )
}

function loadDigest(d?: Overview | null) {
  return (d?.hosts ?? []).map((h) => ({
    host: h.name,
    cpu: [h.cpu_avg, h.cpu_max],
    mem_pct: [h.mem_avg_pct, h.mem_max_pct],
    disks: h.disks.map((x) => ({ mount: x.mount, pct: x.pct, eta_days: x.eta_days })),
  }))
}

function availDigest(d?: Overview | null) {
  return (d?.targets ?? []).slice(0, 30).map((x) => ({ host: x.host, target: x.label, uptime: x.uptime, latency_ms: x.latency }))
}

function TargetsTable({ targets, onOpen }: { targets: MonTarget[]; onOpen: (t: MonTarget) => void }) {
  const { t } = useTranslation()
  const [q, setQ] = useState('')
  const list = useMemo(() => {
    const s = q.trim().toLowerCase()
    return s ? targets.filter((x) => `${x.host} ${x.label} ${x.service}`.toLowerCase().includes(s)) : targets
  }, [targets, q])
  return (
    <>
      <Input.Search allowClear placeholder={t('monitoring.search')} value={q} onChange={(e) => setQ(e.target.value)} style={{ maxWidth: '20rem', marginBottom: '0.5rem' }} />
      <div className="table-wrap">
        <DataTable<MonTarget>
          dataSource={list}
          rowKey={(r) => `${r.host_id}|${r.key}`}
          onRow={(r) => ({ onClick: () => onOpen(r), style: { cursor: 'pointer' } })}
          columns={[
            { title: t('monitoring.colHost'), key: 'host', render: (_, r) => <Sensitive>{r.host}</Sensitive> },
            { title: t('monitoring.colTarget'), key: 'label', render: (_, r) => <span className="small">{r.label}</span> },
            { title: t('monitoring.colService'), key: 'svc', render: (_, r) => <span className="small muted">{r.service || r.kind}</span> },
            {
              title: t('monitoring.colUptime'),
              key: 'up',
              align: 'right',
              render: (_, r) =>
                r.uptime === undefined ? '—' : <Tag color={r.uptime >= 99.9 ? 'green' : r.uptime >= 99 ? 'gold' : 'red'}>{r.uptime}%</Tag>,
            },
            { title: t('monitoring.colChecks'), key: 'n', align: 'right', render: (_, r) => <span className="small">{r.ok} / {r.total}</span> },
            { title: t('monitoring.colLatency'), key: 'lat', align: 'right', render: (_, r) => <span className="small">{r.latency ? `${r.latency} ms` : '—'}</span> },
          ]}
        />
      </div>
    </>
  )
}

function WorkloadsTable({ workloads, onOpen }: { workloads: MonWorkload[]; onOpen: (w: MonWorkload) => void }) {
  const { t } = useTranslation()
  const [source, setSource] = useState<string>('')
  const [q, setQ] = useState('')
  const list = useMemo(() => {
    const s = q.trim().toLowerCase()
    return workloads.filter((w) => (!source || w.source === source) && (!s || `${w.host} ${w.subject}`.toLowerCase().includes(s)))
  }, [workloads, source, q])
  const sources = [...new Set(workloads.map((w) => w.source))]
  return (
    <>
      <Space wrap style={{ marginBottom: '0.5rem' }}>
        <Select
          value={source}
          onChange={setSource}
          style={{ width: '10rem' }}
          options={[{ value: '', label: t('monitoring.allKinds') }, ...sources.map((s) => ({ value: s, label: SOURCE_LABEL[s] ?? s }))]}
        />
        <Input.Search allowClear placeholder={t('monitoring.search')} value={q} onChange={(e) => setQ(e.target.value)} style={{ width: '18rem' }} />
      </Space>
      <div className="table-wrap">
        <DataTable<MonWorkload>
          dataSource={list}
          rowKey={(r) => `${r.host_id}|${r.source}|${r.subject}`}
          onRow={(r) => ({ onClick: () => onOpen(r), style: { cursor: 'pointer' } })}
          columns={[
            { title: t('monitoring.colHost'), key: 'host', render: (_, r) => <Sensitive>{r.host}</Sensitive> },
            { title: t('monitoring.colKind'), key: 'src', render: (_, r) => <Tag>{SOURCE_LABEL[r.source] ?? r.source}</Tag> },
            { title: t('monitoring.colName'), key: 'name', render: (_, r) => <span className="mono small">{r.subject}</span> },
            { title: t('monitoring.colCPU'), key: 'cpu', align: 'right', sorter: (a, b) => a.cpu_avg - b.cpu_avg, render: (_, r) => <span className="small nowrap">{r.cpu_avg} / {r.cpu_max}%</span> },
            { title: t('monitoring.colMem'), key: 'mem', align: 'right', sorter: (a, b) => a.mem_avg - b.mem_avg, render: (_, r) => <span className="small nowrap">{formatBytes(r.mem_avg)} / {formatBytes(r.mem_max)}</span> },
            { title: t('monitoring.colNet'), key: 'net', align: 'right', sorter: (a, b) => a.net_rx + a.net_tx - b.net_rx - b.net_tx, render: (_, r) => <span className="small nowrap">↓{formatBytes(r.net_rx)} ↑{formatBytes(r.net_tx)}</span> },
          ]}
        />
      </div>
    </>
  )
}

interface SeriesResp {
  series: { source: string; subject: string; metric: string; points: [string, number, number, number][] }[]
  daily: boolean
}

const METRIC_FMT: Record<string, (n: number) => string> = {
  cpu_pct: (n) => `${n.toFixed(1)}%`,
  mem_bytes: formatBytes,
  mem_used_bytes: formatBytes,
  used_bytes: formatBytes,
  net_rx_bytes: formatBytes,
  net_tx_bytes: formatBytes,
  latency_ms: (n) => `${n.toFixed(0)} ms`,
  load1: (n) => n.toFixed(2),
  uptime: (n) => `${n.toFixed(2)}%`,
}

/** Графики ряда (нагрузка, цель доступности) за выбранный период. */
function SeriesModal({ title, hostId, source, subject, range, onClose }: { title: string; hostId: number; source: string; subject: string; metric?: string; range: Range; onClose: () => void }) {
  const { t } = useTranslation()
  const res = useApi<SeriesResp>(`/hub/monitoring/series?host=${hostId}&source=${encodeURIComponent(source)}&subject=${encodeURIComponent(subject)}&range=${range}`)
  const charts = useMemo(() => {
    const list = res.data?.series ?? []
    if (source === 'probe') {
      const ok = list.find((s) => s.metric === 'ok')
      const total = list.find((s) => s.metric === 'total')
      const lat = list.find((s) => s.metric === 'latency_ms')
      const totals = new Map((total?.points ?? []).map((p) => [p[0], p[3]]))
      const up: Series = { name: t('monitoring.colUptime'), points: (ok?.points ?? []).filter((p) => (totals.get(p[0]) ?? 0) > 0).map((p) => ({ x: p[0], y: (p[3] / (totals.get(p[0]) ?? 1)) * 100 })) }
      const out: { metric: string; series: Series[] }[] = [{ metric: 'uptime', series: [up] }]
      if (lat) out.push({ metric: 'latency_ms', series: [{ name: t('monitoring.colLatency'), points: lat.points.map((p) => ({ x: p[0], y: p[1] })) }] })
      return out
    }
    return list
      .filter((s) => s.metric !== 'mem_total_bytes' && s.metric !== 'size_bytes')
      .map((s) => ({
        metric: s.metric,
        series: [
          { name: t('monitoring.avg'), points: s.points.map((p) => ({ x: p[0], y: s.metric.startsWith('net_') ? p[3] : p[1] })) },
          ...(s.metric.startsWith('net_') ? [] : [{ name: t('monitoring.peak'), points: s.points.map((p) => ({ x: p[0], y: p[2] })) }]),
        ],
      }))
  }, [res.data, source, t])
  return (
    <Modal title={title} onClose={onClose} width={960} sizeKey="mon-series">
      {res.error && <Banner kind="error">{res.error}</Banner>}
      {!res.data ? (
        <Loading />
      ) : charts.length === 0 ? (
        <p className="small muted">{t('monitoring.noData')}</p>
      ) : (
        charts.map((c) => (
          <div key={c.metric} style={{ marginBottom: '0.8rem' }}>
            <div className="small" style={{ marginBottom: '0.2rem' }}>
              <strong>{t(`monitoring.metric.${c.metric}`, { defaultValue: c.metric })}</strong>
            </div>
            <LineChart series={c.series} height={180} formatValue={METRIC_FMT[c.metric] ?? ((n) => n.toFixed(1))} formatX={(x) => (x.length > 10 ? `${x.slice(5, 10)} ${x.slice(11, 13)}:00` : x.slice(5))} />
          </div>
        ))
      )}
    </Modal>
  )
}

/** Хост: его графики (CPU, память, нагрузка, диски) и его нагрузки. */
function HostModal({ host, range, workloads, onClose, onWorkload }: { host: MonHost; range: Range; workloads: MonWorkload[]; onClose: () => void; onWorkload: (c: { title: string; hostId: number; source: string; subject: string }) => void }) {
  const { t } = useTranslation()
  const hostSeries = useApi<SeriesResp>(`/hub/monitoring/series?host=${host.id}&source=host&range=${range}`)
  const diskSeries = useApi<SeriesResp>(`/hub/monitoring/series?host=${host.id}&source=disk&range=${range}`)
  // Диски разного размера — в процентах заполнения, иначе мелкие
  // сливаются с нулём на общей шкале байт.
  const diskPct = useMemo(() => {
    const list = diskSeries.data?.series ?? []
    return list
      .filter((s) => s.metric === 'used_bytes')
      .map((s) => {
        const size = new Map((list.find((x) => x.subject === s.subject && x.metric === 'size_bytes')?.points ?? []).map((p) => [p[0], p[2]]))
        return { name: s.subject, points: s.points.filter((p) => (size.get(p[0]) ?? 0) > 0).map((p) => ({ x: p[0], y: (p[2] / (size.get(p[0]) ?? 1)) * 100 })) }
      })
  }, [diskSeries.data])
  const pick = (m: string) => hostSeries.data?.series.find((s) => s.metric === m)
  const line = (m: string, field: 1 | 2 = 1): Series => ({ name: field === 1 ? t('monitoring.avg') : t('monitoring.peak'), points: (pick(m)?.points ?? []).map((p) => ({ x: p[0], y: p[field] })) })
  const fx = (x: string) => (x.length > 10 ? `${x.slice(5, 10)} ${x.slice(11, 13)}:00` : x.slice(5))
  return (
    <Modal title={host.name} onClose={onClose} width={1000} sizeKey="mon-host">
      {!hostSeries.data ? (
        <Loading />
      ) : (
        <div className="col" style={{ gap: '0.8rem' }}>
          <div>
            <div className="small"><strong>{t('monitoring.metric.cpu_pct')}</strong></div>
            <LineChart series={[line('cpu_pct'), line('cpu_pct', 2)]} height={170} formatValue={(n) => `${n.toFixed(1)}%`} formatX={fx} />
          </div>
          <div>
            <div className="small"><strong>{t('monitoring.metric.mem_used_bytes')}</strong></div>
            <LineChart series={[line('mem_used_bytes'), line('mem_used_bytes', 2)]} height={170} formatValue={formatBytes} formatX={fx} reference={host.mem_total ? { value: host.mem_total, label: formatBytes(host.mem_total) } : undefined} />
          </div>
          <div>
            <div className="small"><strong>{t('monitoring.metric.load1')}</strong></div>
            <LineChart series={[line('load1'), line('load1', 2)]} height={150} formatValue={(n) => n.toFixed(2)} formatX={fx} />
          </div>
          {diskPct.length > 0 && (
            <div>
              <div className="small"><strong>{t('monitoring.metric.disk_pct')}</strong></div>
              <LineChart series={diskPct} height={170} yMax={100} formatValue={(n) => `${n.toFixed(1)}%`} formatX={fx} />
            </div>
          )}
          {workloads.length > 0 && (
            <WorkloadsTable workloads={workloads} onOpen={(w) => onWorkload({ title: `${w.host} · ${w.subject}`, hostId: w.host_id, source: w.source, subject: w.subject })} />
          )}
        </div>
      )}
    </Modal>
  )
}

const SETTING_KEYS: (keyof MonSettings)[] = ['disk_warn_days', 'disk_crit_days', 'mem_pct', 'cpu_pct', 'leak_days', 'leak_growth_pct', 'avail_drop_pp']

/** Пороги прогнозов и оповещений — с диффом перед записью. */
function SettingsModal({ saved, onClose, onSaved }: { saved: MonSettings; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<MonSettings>(saved)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const text = (s: MonSettings) => SETTING_KEYS.map((k) => `${t(`monitoring.set.${k}`)}: ${s[k]}`).join('\n') + '\n'
  const diff = unifiedDiff(text(saved), text(draft), t('tokens.saved'), t('tokens.draft'))
  const changed = text(saved) !== text(draft)
  async function save() {
    setBusy(true)
    setError(null)
    try {
      await api('/hub/monitoring/settings', { method: 'PUT', body: draft })
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={t('monitoring.thresholds')} onClose={onClose} width={620}>
      <p className="small muted">{t('monitoring.thresholdsHint')}</p>
      <div className="col" style={{ gap: '0.4rem' }}>
        {SETTING_KEYS.map((k) => (
          <label key={k} className="row" style={{ gap: '0.6rem', alignItems: 'center' }}>
            <span style={{ flex: 1 }}>{t(`monitoring.set.${k}`)}</span>
            <InputNumber value={draft[k]} min={0} step={k.endsWith('days') ? 1 : 0.5} onChange={(v) => setDraft({ ...draft, [k]: Number(v ?? 0) })} />
          </label>
        ))}
      </div>
      {changed && (
        <>
          <p className="small muted" style={{ marginTop: '0.6rem' }}>{t('tokens.diffHint')}</p>
          <DiffView text={diff} />
        </>
      )}
      {error && <Banner kind="error">{error}</Banner>}
      <Space style={{ marginTop: '0.6rem' }}>
        <Button type="primary" loading={busy} disabled={!changed} onClick={() => void save()}>
          {t('tokens.save')}
        </Button>
      </Space>
    </Modal>
  )
}

