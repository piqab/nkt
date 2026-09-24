import { useRef, useState } from 'react'
import { Sensitive, blurText } from '../privacy'
import { Link } from 'react-router-dom'
import { Button, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { useHostRescan } from '../rescan'
import ChangesCard from '../components/ChangesCard'
import { useApi } from '../api'
import type { FirewallPolicy, Me, Outage, Overview, ServiceUnit, SourceStatus } from '../types'
import { StatTile, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, InfoHint, Loading, SeverityBadge, StateBadge, formatDateTime, formatRelative, formatUptime } from '../components/ui'
import { AIExplain } from '../components/AIExplain'
import i18n from '../i18n'
import { DataTable } from '../components/DataTable'

// Module-level column builders take t() as an argument rather than calling
// useTranslation() themselves — they're plain functions, not components, so
// they can't call hooks. See Audit.tsx's jobColumns for the same pattern.
function serviceColumns(t: typeof i18n.t): TableColumnsType<ServiceUnit> {
  return [
    {
      title: t('overview.col.service'),
      key: 'name',
      render: (_, s) => (
        <>
          <strong>{s.name}</strong>
          <div className="small muted">{s.description}</div>
        </>
      ),
    },
    { title: t('overview.col.state'), key: 'state', render: (_, s) => <StateBadge state={s.active_state} /> },
    { title: t('overview.col.autostart'), key: 'enabled', render: (_, s) => <span className="small">{s.enabled || '—'}</span> },
    { title: t('overview.col.pid'), key: 'main_pid', align: 'right', render: (_, s) => <span className="num small">{s.main_pid || '—'}</span> },
  ]
}

function firewallPolicyColumns(t: typeof i18n.t): TableColumnsType<FirewallPolicy> {
  return [
    { title: t('overview.col.chain'), key: 'chain', render: (_, p) => <span className="mono">{p.backend}/{p.chain}</span> },
    {
      title: t('overview.col.policy'),
      key: 'policy',
      render: (_, p) => (
        <>
          <StateBadge state={p.policy === 'DROP' || p.policy === 'REJECT' ? 'active' : 'inactive'} />
          <span className="mono small" style={{ marginLeft: 6 }}>
            {p.policy}
          </span>
        </>
      ),
    },
    { title: t('overview.col.packets'), key: 'packets', align: 'right', render: (_, p) => <span className="num">{formatNumber(p.packets)}</span> },
  ]
}

function outageColumns(t: typeof i18n.t): TableColumnsType<Outage> {
  return [
    { title: t('overview.col.resource'), dataIndex: 'label', key: 'label' },
    { title: t('overview.col.start'), key: 'start', render: (_, o) => <span className="small nowrap">{formatDateTime(o.start)}</span> },
    { title: t('overview.col.checks'), dataIndex: 'checks', key: 'checks', align: 'right' },
    { title: t('overview.col.error'), key: 'error', render: (_, o) => <span className="small mono">{o.error}</span> },
  ]
}

function sourceColumns(t: typeof i18n.t): TableColumnsType<SourceStatus> {
  return [
    { title: t('overview.col.source'), dataIndex: 'name', key: 'name' },
    {
      title: t('overview.col.status'),
      key: 'status',
      render: (_, s) => (
        <>
          <StateBadge state={s.error ? 'failed' : s.available ? 'active' : 'inactive'} />
          {s.error && <div className="small muted">{s.error}</div>}
          {s.warnings?.length ? <div className="small muted">{t('overview.warningsCount', { count: s.warnings.length })}</div> : null}
        </>
      ),
    },
    { title: t('overview.col.version'), key: 'version', render: (_, s) => <span className="small mono">{s.version || '—'}</span> },
    { title: t('overview.col.ms'), dataIndex: 'duration_ms', key: 'duration_ms', align: 'right' },
  ]
}

export default function OverviewPage({ me }: { me: Me }) {
  const { t } = useTranslation()
  const { data, error, loading, reload } = useApi<Overview>('/overview', 60_000)
  const [notice, setNotice] = useState<string | null>(null)

  // Обзор — тот же снимок инвентаря, что и остальные разделы: при входе
  // он пересобирается сам, кнопка остаётся для правок, сделанных руками.
  // Аптайм и вывод о нём: фиксируется при первом приходе данных (см. ниже).
  const uptimeRef = useRef<UptimeState | null>(null)
  if (uptimeRef.current === null && data?.host) {
    uptimeRef.current = uptimeState(data.host.hostname, data.host.uptime_s ?? 0)
  }

  const { rescanning: busy, rescan } = useHostRescan({
    reload,
    // Сканирование меняет состояние на сервере и требует прав: у
    // наблюдателя оно отвечало бы отказом при каждом заходе.
    canScan: me.is_admin && me.allow_mutations,
    onNotice: (_kind, text) => setNotice(text),
  })

  // Сравнение с прошлым заходом делается один раз — при первом приходе
  // данных: обзор перечитывается сам, и пересчёт на каждом обновлении
  // затёр бы сохранённое значение (а с ним и «перезагружался») через
  // секунды после показа.
  const uptime = uptimeRef.current

  if (loading && !data) return <Loading what={t('overview.what')} />
  if (error && !data) return <ErrorNote error={error} />
  if (!data) return null

  const findings = data.findings
  const worst = (findings.critical ?? 0) + (findings.high ?? 0)
  const av = data.availability
  const pkgSource = data.sources.find((s) => s.name === 'packages')
  const pkgAvailable = pkgSource?.available ?? false
  const pkgUpdates = data.package_updates?.packages ?? []

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1><Sensitive>{data.host.hostname}</Sensitive></h1>
          <p>
            {t('overview.summary', {
              os: data.host.os,
              kernel: data.host.kernel || '—',
              relative: formatRelative(data.scanned),
              ms: data.scan_ms,
            })}
          </p>
          {/* Аптайм и что с ним стало с прошлого захода: красный —
              машина перезагружалась (аптайм упал), зелёный — растёт, как
              и должен; первый заход и прыжок часов — без цвета. */}
          {uptime?.show && (
            <p className="small" style={{ marginTop: '-0.35rem', color: uptime.color }}>
              {t('overview.uptime', { uptime: formatUptime(data.host.uptime_s ?? 0) })}
              {uptime.note && <> — {uptime.note}</>}
            </p>
          )}
        </div>
        <div className="row">
          {me.is_admin && (
            <Button onClick={() => void rescan(t('common.hostRescanned'))} loading={busy}>
              {busy ? t('common.scanning') : t('common.rescan')}
            </Button>
          )}
        </div>
      </div>

      {notice && (
        <Banner kind="info" onClose={() => setNotice(null)}>
          {notice}
        </Banner>
      )}
      {data.host.notes?.map((note) => (
        <Banner key={note} kind="info">
          {note}
        </Banner>
      ))}
      {data.package_updates?.reboot_required && <Banner kind="warn">{t('overview.rebootRequired')}</Banner>}

      {/* Первым делом — что изменилось с прошлого захода: ради этого
          вопроса снимки состояния и хранятся. */}
      <ChangesCard />

      <div className="grid grid-4">
        <StatTile
          label={t('overview.problemsNeedAttention')}
          value={formatNumber(worst)}
          note={t('overview.criticalHighNote', { critical: findings.critical ?? 0, high: findings.high ?? 0 })}
          tone={worst > 0 ? 'critical' : 'good'}
        />
        <StatTile
          label={t('overview.listenersDeclared')}
          value={formatNumber(data.counts.endpoints ?? 0)}
          note={t('overview.listenersNote', {
            public: data.counts.endpoints_public ?? 0,
            tls: data.counts.endpoints_tls ?? 0,
          })}
        />
        <StatTile
          label={t('overview.containers')}
          value={`${data.counts.containers_running ?? 0} / ${data.counts.containers ?? 0}`}
          note={t('overview.containersNote', { count: data.counts.containers_declared ?? 0 })}
          tone={
            (data.counts.containers ?? 0) > (data.counts.containers_running ?? 0) ? 'warning' : undefined
          }
        />
        <StatTile
          label={t('overview.availability24h')}
          value={`${av.avg_uptime.toFixed(1)}%`}
          note={t('overview.availabilityNote', { targets: av.targets, up: av.up, down: av.down })}
          tone={av.down > 0 ? 'warning' : 'good'}
        />
        {pkgAvailable && (
          <StatTile
            label={t('overview.packageUpdates')}
            value={formatNumber(pkgUpdates.length)}
            note={data.package_updates?.reboot_required ? t('overview.needsReboot') : undefined}
            tone={pkgUpdates.length > 0 ? 'warning' : 'good'}
          />
        )}
      </div>

      <div className="grid grid-2">
        <Card
          title={
            <>
              {t('overview.whatsBroken')}
              <InfoHint>{t('overview.sortedBySeverity')}</InfoHint>
            </>
          }
          actions={<Link to="/findings">{t('overview.allProblems')}</Link>}
        >
          {data.top_findings.length === 0 ? (
            <div className="chart-empty">{t('overview.noProblemsFound')}</div>
          ) : (
            <div className="col">
              {data.top_findings.map((f) => (
                <div key={f.id} style={{ borderBottom: '1px solid var(--gridline)', paddingBottom: '0.5rem' }}>
                  <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
                    <SeverityBadge severity={f.severity} />
                    <strong style={{ flex: 1, minWidth: 0 }}>{blurText(f.title)}</strong>
                    <AIExplain
                      ctx={{
                        kind: 'finding',
                        title: f.title,
                        detail: f.detail,
                        suggestion: f.suggestion,
                        severity: f.severity,
                        object: f.object,
                      }}
                    />
                  </div>
                  <div className="small secondary">{blurText(f.detail ?? '')}</div>
                  {f.object && <Tag>{blurText(f.object)}</Tag>}
                </div>
              ))}
            </div>
          )}
        </Card>

        <div className="col">
          <Card title={t('overview.services')} actions={<Link to="/services">{t('overview.manage')}</Link>}>
            <div className="table-wrap">
              <DataTable<ServiceUnit> dataSource={data.services} columns={serviceColumns(t)} rowKey="name" />
            </div>
          </Card>

          <Card title={t('overview.firewall')} actions={<Link to="/firewall">{t('overview.rulesLink')}</Link>}>
            <div className="row" style={{ flexWrap: 'wrap', rowGap: '0.4rem' }}>
              {data.firewall.managers.filter((m) => m.installed).length === 0 ? (
                <span className="small muted">{t('overview.ufwFirewalldNotInstalled')}</span>
              ) : (
                data.firewall.managers
                  .filter((m) => m.installed)
                  .map((m) => (
                    <span key={m.name} className="row" style={{ alignItems: 'center', gap: '0.35rem' }}>
                      <StateBadge state={m.active ? 'active' : 'inactive'} />
                      <span className="small secondary">
                        {m.name} · {m.policy || t('overview.policyNotRead')}
                      </span>
                    </span>
                  ))
              )}
            </div>
            <div className="table-wrap" style={{ marginTop: '0.6rem' }}>
              <DataTable<FirewallPolicy>                 dataSource={(data.firewall.policies ?? []).filter((p) => p.table === 'filter')}
                columns={firewallPolicyColumns(t)}
                rowKey={(p) => `${p.backend}/${p.chain}`}
              />
            </div>
          </Card>
        </div>
      </div>

      <div className="grid grid-2">
        <Card title={t('overview.recentOutages')} actions={<Link to="/availability">{t('overview.availabilitySchedule')}</Link>}>
          {av.outages.length === 0 ? (
            <div className="chart-empty">{t('overview.noOutages24h')}</div>
          ) : (
            <div className="table-wrap">
              <DataTable<Outage> dataSource={av.outages} columns={outageColumns(t)} rowKey={(_, i) => i ?? 0} />
            </div>
          )}
        </Card>

        <Card
          title={
            <>
              {t('overview.dataSources')}
              <InfoHint>{t('overview.dataSourcesHint')}</InfoHint>
            </>
          }
        >
          <div className="table-wrap">
            <DataTable<SourceStatus> dataSource={data.sources} columns={sourceColumns(t)} rowKey="name" />
          </div>
        </Card>
      </div>
    </>
  )
}

/** Что показывать про аптайм: цвет и пояснение по сравнению с прошлым
 * заходом на эту страницу. Прошлое значение (аптайм и момент, когда его
 * видели) живёт в браузере по имени хоста — хаб про свои оповещения
 * знает сам, а тут вопрос «с моего прошлого захода».
 *
 * Аптайм меньше прежнего — машина перезагружалась: красный. Вырос
 * примерно на прошедшее время (±10 %, но не меньше 5 минут допуска —
 * часы хоста и браузера расходятся) — всё в порядке, зелёный. Вырос
 * необъяснимо больше — часы прыгнули, выводов не делаем.
 */
interface UptimeState {
  show: boolean
  color?: string
  note?: string
}

function uptimeState(host: string, uptimeS: number): UptimeState {
  if (uptimeS <= 0) return { show: false }
  const key = `nkt-uptime:${host}`

  let prev: { s: number; at: number } | null = null
  try {
    const raw = localStorage.getItem(key)
    if (raw) prev = JSON.parse(raw) as { s: number; at: number }
  } catch {
    prev = null
  }
  try {
    localStorage.setItem(key, JSON.stringify({ s: uptimeS, at: Date.now() }))
  } catch {
    // Приватное окно — обойдёмся без сравнения.
  }
  if (!prev || prev.s <= 0) return { show: true }
  if (uptimeS < prev.s) {
    return { show: true, color: 'var(--status-critical)', note: i18n.t('overview.uptimeRebooted', { was: formatUptime(prev.s) }) }
  }
  const elapsed = Math.max(0, (Date.now() - prev.at) / 1000)
  const grew = uptimeS - prev.s
  if (Math.abs(grew - elapsed) <= Math.max(300, elapsed * 0.1)) {
    return { show: true, color: 'var(--status-good)', note: i18n.t('overview.uptimeSteady') }
  }
  return { show: true }
}
