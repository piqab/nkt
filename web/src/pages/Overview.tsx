import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Button, Table, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { FirewallPolicy, Me, Outage, Overview, ServiceUnit, SourceStatus } from '../types'
import { StatTile, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, InfoHint, Loading, SeverityBadge, StateBadge, formatDateTime, formatRelative } from '../components/ui'
import UpdateModal from '../components/UpdateModal'
import CommonPackagesCard from '../components/CommonPackagesCard'
import i18n from '../i18n'

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
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [updating, setUpdating] = useState(false)
  const [updateOutcome, setUpdateOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)
  // Polled independently of whether the update dialog is open — otherwise
  // there's no way to tell, from the button alone, whether "обновить"
  // would reattach to an apt-get already running (started earlier, or by
  // someone else) or start a brand new one; both look identical at first
  // glance (a black terminal with a spinner) once the dialog opens.
  const { data: updateStatus, reload: reloadUpdateStatus } = useApi<{
    active: boolean
    finished: boolean
    succeeded: boolean
  }>('/updates/status', 5_000)
  const updateActive = updateStatus?.active ?? false

  async function rescan(successNotice = t('common.hostRescanned')) {
    setBusy(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      reload()
      setNotice(successNotice)
    } catch (err) {
      setNotice(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  /**
   * Called the moment the update session's socket closes, i.e. as soon as
   * apt actually exits. A plain reload() would not be enough: /overview
   * serves the last inventory scan, so the package list would still show
   * the versions apt just replaced — which reads as "the upgrade never
   * finished" rather than "nothing rescanned the host yet". Only a
   * successful run is worth rescanning for; a failed one leaves the
   * previous state in place and its error on screen instead.
   */
  async function handleUpdateFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>('/updates/status').catch(() => null)
    reloadUpdateStatus()
    if (fresh?.succeeded) {
      setUpdateOutcome({ ok: true })
      await rescan(t('overview.packagesUpdatedRescanned'))
    } else {
      setUpdateOutcome({ ok: false, exitCode: fresh?.exit_code })
    }
  }

  if (loading && !data) return <Loading what={t('overview.what')} />
  if (error && !data) return <ErrorNote error={error} />
  if (!data) return null

  const findings = data.findings
  const worst = (findings.critical ?? 0) + (findings.high ?? 0)
  const av = data.availability
  const pkgSource = data.sources.find((s) => s.name === 'packages')
  const pkgAvailable = pkgSource?.available ?? false
  const pkgUpdates = data.package_updates?.packages ?? []
  const canUpdate = me.is_admin && me.allow_mutations && pkgAvailable
  const canReopenUpdate = me.is_admin && me.allow_mutations

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>{data.host.hostname}</h1>
          <p>
            {t('overview.summary', {
              os: data.host.os,
              kernel: data.host.kernel || '—',
              relative: formatRelative(data.scanned),
              ms: data.scan_ms,
            })}
          </p>
        </div>
        <div className="row">
          {updateActive
            ? canReopenUpdate && (
                <Button
                  type="primary"
                  onClick={() => {
                    setUpdateOutcome(null)
                    setUpdating(true)
                  }}
                >
                  {t('overview.updateRunningOpen')}
                </Button>
              )
            : canUpdate && (
                <Button
                  type={pkgUpdates.length > 0 ? 'primary' : 'default'}
                  disabled={pkgUpdates.length === 0}
                  onClick={() => {
                    if (window.confirm(t('overview.confirmUpdate', { count: pkgUpdates.length }))) {
                      setUpdateOutcome(null)
                      setUpdating(true)
                    }
                  }}
                >
                  {pkgUpdates.length > 0 ? t('overview.updateCount', { count: pkgUpdates.length }) : t('overview.noUpdates')}
                </Button>
              )}
          {me.is_admin && (
            <Button onClick={() => rescan()} loading={busy}>
              {busy ? t('common.scanning') : t('common.rescan')}
            </Button>
          )}
        </div>
      </div>

      {notice && <Banner kind="info">{notice}</Banner>}
      {data.host.notes?.map((note) => (
        <Banner key={note} kind="info">
          {note}
        </Banner>
      ))}
      {data.package_updates?.reboot_required && <Banner kind="warn">{t('overview.rebootRequired')}</Banner>}

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
                  <div className="row" style={{ gap: '0.5rem' }}>
                    <SeverityBadge severity={f.severity} />
                    <strong>{f.title}</strong>
                  </div>
                  <div className="small secondary">{f.detail}</div>
                  {f.object && <Tag>{f.object}</Tag>}
                </div>
              ))}
            </div>
          )}
        </Card>

        <div className="col">
          <Card title={t('overview.services')} actions={<Link to="/services">{t('overview.manage')}</Link>}>
            <div className="table-wrap">
              <Table<ServiceUnit> dataSource={data.services} columns={serviceColumns(t)} rowKey="name" pagination={false} size="small" />
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
              <Table<FirewallPolicy>
                dataSource={(data.firewall.policies ?? []).filter((p) => p.table === 'filter')}
                columns={firewallPolicyColumns(t)}
                rowKey={(p) => `${p.backend}/${p.chain}`}
                pagination={false}
                size="small"
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
              <Table<Outage> dataSource={av.outages} columns={outageColumns(t)} rowKey={(_, i) => i ?? 0} pagination={false} size="small" />
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
            <Table<SourceStatus> dataSource={data.sources} columns={sourceColumns(t)} rowKey="name" pagination={false} size="small" />
          </div>
        </Card>
      </div>

      <CommonPackagesCard canUse={canReopenUpdate} />

      {updating && (
        <UpdateModal
          packages={pkgUpdates}
          outcome={updateOutcome}
          rescanning={busy}
          onFinished={handleUpdateFinished}
          onClose={() => {
            setUpdating(false)
            reload()
          }}
        />
      )}
    </>
  )
}
