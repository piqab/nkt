import { useEffect, useMemo, useState } from 'react'
import { Button, Select, Tabs } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, tzOffsetMinutes, useApi } from '../api'
import type { HeatCell, Me, MetricPoint, SubjectTotal } from '../types'
import { BarChart, Heatmap, LineChart, formatBytes, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'
import { PtyToolbar } from '../components/PtyToolbar'
import { usePty, wsURL } from '../hooks/usePty'
import PackageInstallModal from '../components/PackageInstallModal'
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

/**
 * "Нагрузка" — historical usage charts, plus a live btop tab for the same
 * host (see UsageBtop). Two different ways of looking at the same
 * question ("what's this host's load like"), one after the other rather
 * than after collected/aggregated numbers, so they share this page instead
 * of splitting into two nav entries.
 */
export default function Usage({ me }: { me: Me }) {
  const { t } = useTranslation()

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            {t('usage.title')}
            <InfoHint>{t('usage.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <Tabs
        defaultActiveKey="charts"
        items={[
          { key: 'charts', label: t('usage.tabCharts'), children: <UsageCharts /> },
          { key: 'btop', label: t('usage.tabBtop'), children: <UsageBtop me={me} /> },
        ]}
      />
    </>
  )
}

function UsageCharts() {
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
      <div className="row" style={{ marginBottom: '0.75rem' }}>
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

/**
 * A live, fully interactive btop session — same PTY/WebSocket bridge the
 * Terminal page's plain shell and tmux buttons use (see handleBtopWS),
 * gated identically: admin + AllowMutations + NKT_TERMINAL_ENABLED.
 * Deliberately not tmux-wrapped for persistence like "Открыть в tmux" —
 * closing this tab just ends the btop process, matching "Открыть терминал"'s
 * plain (non-persistent) behaviour rather than tmux's.
 */
function UsageBtop({ me }: { me: Me }) {
  const { t } = useTranslation()
  const canUse = me.is_admin && me.allow_mutations

  const { data: terminalConfig } = useApi<{ idle_timeout_s: number }>('/terminal/config')
  const wsUrl = wsURL('/terminal/btop/ws')
  const { containerRef, status, start, stop, focus, copySelection, clear, changeFontSize, search, getIdleRemainingMs } =
    usePty(wsUrl, terminalConfig ? terminalConfig.idle_timeout_s * 1000 : undefined)

  useEffect(() => {
    if (status === 'connected') focus()
  }, [status, focus])

  // Whether btop is on this host's PATH — the "Открыть btop" button uses
  // this to decide between connecting directly and offering to install it
  // first, same shape as Terminal.tsx's tmux button. terminal_enabled/
  // fixtures_mode/apt_get_available ride along on the same REST call
  // specifically because a browser's WebSocket API cannot read the
  // response body of a rejected upgrade — handleBtopWS/handleBtopInstallWS
  // reject with exactly these reasons, but that 403 is otherwise invisible
  // to this page beyond "the connection failed somehow" (see status ===
  // 'error' below). Checking them here first is what lets the button
  // explain why instead of just failing silently.
  const { data: btopStatus, reload: reloadBtopStatus } = useApi<{
    available: boolean
    terminal_enabled: boolean
    fixtures_mode: boolean
    apt_get_available: boolean
  }>('/system/btop-status', 30_000)
  const { data: btopInstallStatus, reload: reloadBtopInstallStatus } = useApi<{
    active: boolean
    finished: boolean
    succeeded: boolean
  }>('/system/btop-install/status', 5_000)
  const [btopInstallOpen, setBtopInstallOpen] = useState(false)
  const [btopInstallOutcome, setBtopInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  const btopBlockedReason = !btopStatus
    ? null
    : btopStatus.fixtures_mode
      ? t('usage.btopFixturesDisabled')
      : !btopStatus.terminal_enabled
        ? t('usage.btopDisabled')
        : null

  function handleStart() {
    if (!window.confirm(t('usage.confirmOpenBtop'))) return
    start()
  }

  function handleBtopButtonClick() {
    if (btopStatus?.available) {
      handleStart()
      return
    }
    if (btopInstallStatus?.active) {
      setBtopInstallOutcome(null)
      setBtopInstallOpen(true)
      return
    }
    if (btopStatus && !btopStatus.apt_get_available) {
      window.alert(t('usage.btopAptGetMissing'))
      return
    }
    if (window.confirm(t('usage.confirmInstallBtop'))) {
      setBtopInstallOutcome(null)
      setBtopInstallOpen(true)
    }
  }

  async function handleBtopInstallFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>('/system/btop-install/status').catch(
      () => null,
    )
    reloadBtopInstallStatus()
    reloadBtopStatus()
    const ok = !!fresh?.succeeded
    setBtopInstallOutcome(ok ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    if (ok) {
      setBtopInstallOpen(false)
      handleStart()
    }
  }

  return (
    <>
      <div className="row" style={{ marginBottom: '0.75rem' }}>
        {status === 'connected' ? (
          <Button danger onClick={stop}>
            {t('usage.closeBtop')}
          </Button>
        ) : (
          <Button
            loading={status === 'connecting'}
            disabled={!canUse || !!btopBlockedReason}
            onClick={handleBtopButtonClick}
          >
            {status === 'connecting' ? t('terminal.connecting') : t('usage.openBtop')}
          </Button>
        )}
      </div>

      {!canUse && <Banner kind="info">{t('common.adminMutationsOnly')}</Banner>}
      {canUse && btopBlockedReason && <Banner kind="warn">{btopBlockedReason}</Banner>}
      {status === 'error' && <Banner kind="error">{t('terminal.connectError')}</Banner>}
      {status === 'closed' && <Banner kind="info">{t('terminal.sessionEnded')}</Banner>}

      <div className="row" style={{ alignItems: 'flex-start', gap: '1rem' }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <Card>
            {status === 'connected' && (
              <PtyToolbar
                onCopy={copySelection}
                onClear={clear}
                onFontSize={changeFontSize}
                onSearch={search}
                getIdleRemainingMs={getIdleRemainingMs}
              />
            )}
            <div style={{ position: 'relative' }}>
              <div
                ref={containerRef}
                style={{
                  height: '65vh',
                  background: '#141414',
                  borderRadius: 'var(--radius-sm)',
                  padding: '0.5rem',
                }}
              />
              {status === 'idle' && (
                <div
                  className="chart-empty"
                  style={{
                    position: 'absolute',
                    inset: 0,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    background: '#141414',
                    borderRadius: 'var(--radius-sm)',
                  }}
                >
                  {t('usage.btopNotOpenYet')}
                </div>
              )}
            </div>
          </Card>
        </div>

        <BtopHints />
      </div>

      {btopInstallOpen && (
        <PackageInstallModal
          packageName="btop"
          wsPath="/system/btop-install/ws"
          onClose={() => setBtopInstallOpen(false)}
          onFinished={handleBtopInstallFinished}
          outcome={btopInstallOutcome}
        />
      )}
    </>
  )
}

// BTOP_HINTS groups btop's own default key sequences — reference only, the
// same "documentation, not live controls" shape as Terminal.tsx's
// TMUX_HINTS (see its own doc comment for why: running these as commands
// against the session separately from what the operator actually presses
// inside btop itself proved unreliable to keep in sync).
const BTOP_HINTS: { titleKey: string; rows: [string, string][] }[] = [
  {
    titleKey: 'usage.btopGeneralGroup',
    rows: [
      ['Esc / m', 'usage.btopMenu'],
      ['q', 'usage.btopQuit'],
      ['+ / -', 'usage.btopUpdateInterval'],
      ['1 2 3 4', 'usage.btopToggleBox'],
    ],
  },
  {
    titleKey: 'usage.btopProcessGroup',
    rows: [
      ['↑ ↓', 'usage.btopNavigate'],
      ['Enter', 'usage.btopDetails'],
      ['f', 'usage.btopFilter'],
      ['t', 'usage.btopTree'],
      ['Space', 'usage.btopSelect'],
      ['k', 'usage.btopKill'],
    ],
  },
]

// BTOP_HINTS_COLLAPSED_KEY persists BtopHints' collapsed/expanded state
// across reloads/reopens — same reasoning as Terminal.tsx's own
// TMUX_HINTS_COLLAPSED_KEY, a separate key since the two panels collapse
// independently.
const BTOP_HINTS_COLLAPSED_KEY = 'nkt-btop-hints-collapsed'

function readBtopHintsCollapsed(): boolean {
  try {
    return localStorage.getItem(BTOP_HINTS_COLLAPSED_KEY) === '1'
  } catch {
    return false
  }
}

/** BtopHints — a static reference for btop's own default key bindings,
 * shown next to the live session. See Terminal.tsx's TmuxHints for the
 * chip-vs-disabled-button reasoning this copies verbatim. */
function BtopHints() {
  const { t } = useTranslation()
  const [collapsed, setCollapsed] = useState(readBtopHintsCollapsed)

  function toggle() {
    setCollapsed((prev) => {
      const next = !prev
      try {
        localStorage.setItem(BTOP_HINTS_COLLAPSED_KEY, next ? '1' : '0')
      } catch {
        // Per-viewer convenience only — fine to just not persist it.
      }
      return next
    })
  }

  if (collapsed) {
    return (
      <div style={{ flexShrink: 0 }}>
        <Button size="small" onClick={toggle}>
          {t('terminal.show')}
        </Button>
      </div>
    )
  }

  return (
    <div style={{ width: 240, flexShrink: 0 }}>
      <Card
        title={t('usage.btopHintsTitle')}
        actions={
          <Button size="small" onClick={toggle}>
            {t('terminal.hide')}
          </Button>
        }
      >
        {BTOP_HINTS.map((group) => (
          <div key={group.titleKey} style={{ marginTop: '0.5rem' }}>
            <div className="small muted" style={{ marginBottom: '0.2rem' }}>
              {t(group.titleKey)}
            </div>
            <div className="col" style={{ gap: '0.15rem' }}>
              {group.rows.map(([keys, descKey]) => (
                <div
                  key={keys}
                  className="row"
                  style={{ flexWrap: 'nowrap', gap: '0.4rem', alignItems: 'baseline' }}
                >
                  <span
                    className="mono"
                    style={{
                      flexShrink: 0,
                      fontSize: '0.72rem',
                      padding: '0.05rem 0.3rem',
                      background: 'var(--surface-1)',
                      border: '1px solid var(--border)',
                      borderRadius: 'var(--radius-sm)',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {keys}
                  </span>
                  <span className="small muted">{t(descKey)}</span>
                </div>
              ))}
            </div>
          </div>
        ))}
      </Card>
    </div>
  )
}
