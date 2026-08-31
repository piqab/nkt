import { useState } from 'react'
import { Button, Table, Tag, Tooltip, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Listener, Me, ServiceUnit } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, StateBadge, formatBytesShort } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'
import PackageInstallModal from '../components/PackageInstallModal'
import i18n from '../i18n'

const ACTION_LABEL_KEY: Record<string, string> = {
  start: 'services.actionStart',
  stop: 'services.actionStop',
  restart: 'services.actionRestart',
  reload: 'services.actionReload',
  validate: 'services.actionValidate',
  enable: 'services.actionEnable',
  disable: 'services.actionDisable',
}

/** Mirrors model.Listener.Public() in Go — a socket bound to all
 * interfaces rather than just loopback/a specific address. */
function isPublic(l: Listener): boolean {
  return l.address === '0.0.0.0' || l.address === '*' || l.address === '::' || l.address === '[::]'
}

/** Rough but readable — the point is "minutes vs months", not precision. */
function formatUptime(seconds?: number): string | null {
  if (!seconds || seconds < 0) return null
  if (seconds < 60) return i18n.t('services.uptimeSeconds', { count: seconds })
  const m = Math.floor(seconds / 60)
  if (m < 60) return i18n.t('services.uptimeMinutes', { count: m })
  const h = Math.floor(m / 60)
  if (h < 24) return i18n.t('services.uptimeHours', { count: h })
  return i18n.t('services.uptimeDays', { count: Math.floor(h / 24) })
}

/**
 * How the process came to be running, from its cgroup. "Запущен вручную"
 * is the one worth reacting to here: it means an interactive login session
 * started it, so nothing will bring it back after a reboot — and nothing
 * declared it in the first place.
 */
function OriginTag({ l }: { l: Listener }) {
  const { t } = useTranslation()
  if (l.origin === 'manual') {
    return (
      <Tooltip title={t('services.manualOriginTooltip')}>
        <Tag color="warning">{t('services.manualOrigin')}</Tag>
      </Tooltip>
    )
  }
  if (l.origin === 'service') {
    return (
      <Tooltip title={t('services.serviceUnitTooltip', { unit: l.unit })}>
        <Tag color="processing">{l.unit || 'systemd'}</Tag>
      </Tooltip>
    )
  }
  if (l.origin === 'container') {
    return (
      <Tooltip title={l.container_id ? t('services.containerTooltip', { id: l.container_id }) : t('services.containerTooltipGeneric')}>
        <Tag color="default">{t('services.container', { id: l.container_id ? ` ${l.container_id.slice(0, 12)}` : '' })}</Tag>
      </Tooltip>
    )
  }
  return <span className="small muted">—</span>
}

/** listenerKey identifies a row for busy/escalation tracking — pid alone
 * isn't quite enough (in principle two listeners could momentarily share
 * one before a scan catches up), so paired with the socket it's already
 * keyed by in the table (rowKey below). */
function listenerKey(l: Listener): string {
  return `${l.pid ?? '-'}:${l.address}:${l.port}`
}

function buildMiscColumns(
  canControl: boolean,
  killBusy: string | null,
  onKill: (l: Listener, signal: 'TERM' | 'KILL') => void,
): TableColumnsType<Listener> {
  const t = i18n.t.bind(i18n)
  const columns: TableColumnsType<Listener> = [
    {
      title: t('services.colProcess'),
      key: 'process',
      render: (_, l) => {
        const uptime = formatUptime(l.uptime_s)
        return (
          <>
            <strong>{l.process || '—'}</strong>
            {l.command && (
              <div className="small mono muted" style={{ wordBreak: 'break-all' }}>
                {l.command}
              </div>
            )}
            <div className="small muted">
              {l.user ? t('services.fromUser', { user: l.user }) : ''}
              {l.user && (uptime || l.pid) ? ' · ' : ''}
              {uptime ? t('services.running', { uptime }) : ''}
              {uptime && l.pid ? ' · ' : ''}
              {l.pid ? t('services.pid', { pid: l.pid }) : ''}
            </div>
          </>
        )
      },
    },
    { title: t('services.colOrigin'), key: 'origin', render: (_, l) => <OriginTag l={l} /> },
    {
      title: t('services.colSocket'),
      key: 'socket',
      render: (_, l) => (
        <span className="small mono">
          {l.protocol} {l.address}:{l.port}
        </span>
      ),
    },
    {
      title: t('services.colExposure'),
      key: 'exposure',
      render: (_, l) => (isPublic(l) ? <Tag color="warning">{t('services.allInterfaces')}</Tag> : <Tag>{t('services.local')}</Tag>),
    },
  ]
  if (canControl) {
    columns.push({
      title: '',
      key: 'actions',
      render: (_, l) => {
        if (!l.pid || !l.command) return null
        const key = listenerKey(l)
        return (
          <Button
            type="link"
            danger
            size="small"
            loading={killBusy === key}
            onClick={() => onKill(l, 'TERM')}
          >
            {t('services.kill')}
          </Button>
        )
      },
    })
  }
  return columns
}

/**
 * "Сервисы" — systemd-юниты, и ниже, в том же разделе, «Остальные
 * сервисы» (бывшая отдельная вкладка/страница «Разное», перенесённая
 * сюда же — после ps/cgroup enrichment большинство из них на деле
 * оказывается systemd-юнитом, а не контейнером, так что это соседствует
 * с остальным перечнем сервисов, а не с Docker/Podman/LXD/VMs).
 */
export default function Services({ me }: { me: Me }) {
  const { t } = useTranslation()
  const services = useApi<{ services: ServiceUnit[]; allow_mutations: boolean }>('/services', 30_000)
  const misc = useApi<{ listeners: Listener[] }>('/misc', 60_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [rescanning, setRescanning] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [killBusy, setKillBusy] = useState<string | null>(null)
  // Set when a SIGTERM'd process is still listed after a rescan — offers
  // the SIGKILL escalation the operator asked for, without jumping
  // straight to it: TERM first always, KILL only on request.
  const [killEscalation, setKillEscalation] = useState<Listener | null>(null)
  const [logsFor, setLogsFor] = useState<ServiceUnit | null>(null)
  // Set when the operator clicks a not-installed service's chip in the
  // inactive summary below — opens PackageInstallModal against
  // /services/{name}/install/ws, the same shared apt-get-install-live
  // component tmux/dbus already use, just pointed at this
  // service's own install route.
  const [installTarget, setInstallTarget] = useState<string | null>(null)
  const [installOutcome, setInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  const canControl = me.is_admin && me.allow_mutations
  const allServices = services.data?.services ?? []
  const activeServices = allServices.filter((s) => s.active_state === 'active')
  const inactiveServices = allServices.filter((s) => s.active_state !== 'active')
  const miscListeners = misc.data?.listeners ?? []
  const manual = miscListeners.filter((l) => l.origin === 'manual').length

  async function rescan() {
    setRescanning(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      await Promise.all([services.reload(), misc.reload()])
      setNotice({ kind: 'info', text: t('common.hostRescanned') })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  async function act(service: string, action: string) {
    if (action !== 'validate' && action !== 'reload') {
      if (!window.confirm(t('services.confirmAction', { action: t(ACTION_LABEL_KEY[action]), service }))) return
    }
    setBusy(`${service}:${action}`)
    setNotice(null)
    try {
      const path = action === 'validate' ? `/services/${service}/validate` : `/services/${service}/${action}`
      const res = await api<{ output?: string; valid?: boolean; simulated?: boolean }>(path, {
        method: 'POST',
      })
      const suffix = res.simulated ? t('services.simulatedSuffix') : ''
      setNotice({
        kind: res.valid === false ? 'error' : 'info',
        text: t('services.actionDone', { service, action: t(ACTION_LABEL_KEY[action]), output: res.output?.trim() || t('services.done'), suffix }),
      })
      // The backend only kicks off a fire-and-forget background rescan
      // (rescanLater) — a bare reload() right after would just reread the
      // still-stale cached snapshot. /inventory/refresh runs the same
      // rescan synchronously, same as "Пересканировать" below (and same
      // as kill() just above already does).
      await api('/inventory/refresh', { method: 'POST' })
      await services.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function handleInstallFinished() {
    if (!installTarget) return
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>(
      `/services/${installTarget}/install/status`,
    ).catch(() => null)
    setInstallOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    await api('/inventory/refresh', { method: 'POST' }).catch(() => {})
    await services.reload()
  }

  /**
   * "Остальные сервисы" has no systemd unit to act on, only a raw PID from
   * the last scan — kill is the only control available for it (see
   * ServiceManager.KillProcess's own doc comment on why command has to
   * come along: the server re-verifies it right before signalling, to
   * guard against the PID having been reused for something else since the
   * scan that surfaced it). Always TERM first; KILL only fires when the
   * operator explicitly asks for it after seeing the process survive TERM
   * (see the killEscalation banner below), never as this function's own
   * automatic follow-up.
   */
  async function kill(l: Listener, signal: 'TERM' | 'KILL') {
    if (!l.pid || !l.command) return
    const verb = signal === 'TERM' ? t('services.confirmTerm') : t('services.confirmKill')
    if (!window.confirm(t('services.confirmKillKind', { verb, pid: l.pid, process: l.process ? ` (${l.process})` : '' }))) return
    const key = listenerKey(l)
    setKillBusy(key)
    setNotice(null)
    setKillEscalation(null)
    try {
      await api('/misc/kill', { method: 'POST', body: { pid: l.pid, command: l.command, signal } })
      await api('/inventory/refresh', { method: 'POST' })
      const fresh = await api<{ listeners: Listener[] }>('/misc')
      await misc.reload()
      const stillThere = fresh.listeners.some((x) => listenerKey(x) === key)
      if (signal === 'TERM' && stillThere) {
        setKillEscalation(l)
        setNotice({ kind: 'info', text: t('services.notTerminated', { pid: l.pid }) })
      } else {
        setNotice({ kind: 'info', text: t('services.signalSent', { pid: l.pid, signal }) })
      }
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setKillBusy(null)
    }
  }

  const columns: TableColumnsType<ServiceUnit> = [
    {
      title: t('services.colService'),
      key: 'name',
      render: (_, s) => (
        <>
          <strong>{s.name}</strong>
          <div className="small muted">{s.description || s.unit}</div>
          {!s.installed && <div className="small muted">{t('services.notInstalled')}</div>}
        </>
      ),
    },
    {
      title: t('services.colState'),
      key: 'state',
      render: (_, s) => (
        <>
          <StateBadge state={s.active_state} />
          {s.sub_state && <div className="small muted">{s.sub_state}</div>}
        </>
      ),
    },
    { title: t('services.colAutostart'), key: 'enabled', render: (_, s) => <span className="small">{s.enabled || '—'}</span> },
    { title: 'PID', key: 'main_pid', align: 'right', render: (_, s) => <span className="small">{s.main_pid || '—'}</span> },
    {
      title: t('services.colMemory'),
      key: 'memory_bytes',
      align: 'right',
      render: (_, s) => <span className="small">{s.memory_bytes ? formatBytesShort(s.memory_bytes) : '—'}</span>,
    },
    { title: t('services.colRestarts'), key: 'restarts', align: 'right', render: (_, s) => <span className="small">{s.restarts ?? 0}</span> },
    {
      title: t('services.colActions'),
      key: 'actions',
      render: (_, s) => (
        <div className="row">
          {(s.actions ?? []).map((a) => (
            <Button
              key={a}
              type="link"
              size="small"
              disabled={!canControl}
              loading={busy === `${s.name}:${a}`}
              onClick={() => act(s.name, a)}
            >
              {ACTION_LABEL_KEY[a] ? t(ACTION_LABEL_KEY[a]) : a}
            </Button>
          ))}
          <Button type="link" size="small" onClick={() => setLogsFor(s)}>
            {t('services.logs')}
          </Button>
        </div>
      ),
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            {t('services.title')}
            <InfoHint>{t('services.hint')}</InfoHint>
          </h1>
        </div>
        <div className="row">
          {me.is_admin && (
            <Button
              onClick={rescan}
              loading={rescanning}
              title={t('services.rescanTooltip')}
            >
              {rescanning ? t('common.scanning') : t('common.rescan')}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={services.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {killEscalation && (
        <Banner kind="warn">
          {t('services.notTerminatedInline', { pid: killEscalation.pid, process: killEscalation.process ? ` (${killEscalation.process})` : '' })}
          <Button
            size="small"
            danger
            loading={killBusy === listenerKey(killEscalation)}
            onClick={() => kill(killEscalation, 'KILL')}
          >
            {t('services.sendSigkill')}
          </Button>
        </Banner>
      )}
      {!canControl && <Banner kind="info">{t('common.mutationsDisabled')}</Banner>}

      <Card title={t('services.systemdTitle')}>
        {services.loading && !services.data ? (
          <Loading what={t('services.loadingServiceState')} />
        ) : (
          <>
            <InactiveSummary
              items={inactiveServices}
              getKey={(s) => s.name}
              getLabel={(s) => s.name}
              getTooltip={(s) => (
                <>
                  <div>{s.description || s.unit}</div>
                  <div>{t('services.state', { state: s.active_state, sub: s.sub_state ? ` (${s.sub_state})` : '' })}</div>
                  {!canControl ? (
                    !s.installed && <div>{t('services.notInstalled')}</div>
                  ) : (
                    <div>{s.installed ? t('services.clickToStart') : t('services.clickToInstall')}</div>
                  )}
                </>
              )}
              onRescan={rescan}
              rescanning={rescanning}
              onItemClick={(s) => {
                if (!s.installed) {
                  setInstallOutcome(null)
                  setInstallTarget(s.name)
                  return
                }
                // Already installed, just not active — no table row of its
                // own to offer a Start button (activeServices filters it
                // out), so the chip that already tells you it's stopped is
                // the only place left to act on that.
                void act(s.name, 'start')
              }}
              isClickable={() => canControl}
              getColor={(s) => (s.installed ? undefined : 'gold')}
            />
            <div className="table-wrap">
              <Table<ServiceUnit>
                dataSource={activeServices}
                columns={columns}
                rowKey="name"
                pagination={false}
                size="small"
              />
            </div>
          </>
        )}
      </Card>

      <ErrorNote error={misc.error} />
      <Card
        title={t('services.otherServicesTitle')}
        subtitle={t('services.otherServicesSubtitle', {
          count: miscListeners.length,
          manual: manual ? t('services.manualCount', { count: manual }) : '',
        })}
      >
        {misc.loading && !misc.data ? (
          <Loading what={t('services.loadingOtherServices')} />
        ) : miscListeners.length === 0 ? (
          <div className="chart-empty">{t('services.allDescribed')}</div>
        ) : (
          <div className="table-wrap">
            <Table<Listener>
              dataSource={miscListeners}
              columns={buildMiscColumns(canControl, killBusy, kill)}
              rowKey={(l) => `${l.address}:${l.port}`}
              pagination={false}
              size="small"
            />
          </div>
        )}
      </Card>

      {logsFor && <ServiceLogsModal service={logsFor} onClose={() => setLogsFor(null)} />}

      {installTarget && (
        <PackageInstallModal
          packageName={installTarget}
          wsPath={`/services/${installTarget}/install/ws`}
          onClose={() => setInstallTarget(null)}
          onFinished={handleInstallFinished}
          outcome={installOutcome}
        />
      )}
    </>
  )
}

/** A static journalctl -u snapshot (see handleServiceLogs) — "обновить"
 * refetches rather than streaming live, deliberately: this is for "what
 * just happened", not a tail -f, and it's available to any viewer (no
 * canControl gate — reading a log is not a mutation). */
function ServiceLogsModal({ service, onClose }: { service: ServiceUnit; onClose: () => void }) {
  const { t } = useTranslation()
  const logs = useApi<{ output: string }>(`/services/${service.name}/logs?lines=200`)

  return (
    <Modal title={t('services.logsTitle', { name: service.name })} onClose={onClose}>
      <div className="row" style={{ justifyContent: 'flex-end', marginBottom: '0.5rem' }}>
        <Button size="small" onClick={() => logs.reload()} loading={logs.loading}>
          {t('services.refresh')}
        </Button>
      </div>
      <ErrorNote error={logs.error} />
      {logs.loading && !logs.data ? (
        <Loading what={t('services.loadingLogs')} />
      ) : (
        <pre className="diff" style={{ maxHeight: '28rem' }}>
          {logs.data?.output?.trim() || t('services.empty')}
        </pre>
      )}
    </Modal>
  )
}
