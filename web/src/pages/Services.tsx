import { useState } from 'react'
import { Button, Table, Tag, Tooltip, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { Listener, Me, ServiceUnit } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, StateBadge, formatBytesShort } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'

const ACTION_LABEL: Record<string, string> = {
  start: 'запустить',
  stop: 'остановить',
  restart: 'перезапустить',
  reload: 'перечитать конфиг',
  validate: 'проверить конфиг',
  enable: 'включить автозапуск',
  disable: 'выключить автозапуск',
}

/** Mirrors model.Listener.Public() in Go — a socket bound to all
 * interfaces rather than just loopback/a specific address. */
function isPublic(l: Listener): boolean {
  return l.address === '0.0.0.0' || l.address === '*' || l.address === '::' || l.address === '[::]'
}

/** Rough but readable — the point is "minutes vs months", not precision. */
function formatUptime(seconds?: number): string | null {
  if (!seconds || seconds < 0) return null
  if (seconds < 60) return `${seconds} с`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m} мин`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} ч`
  return `${Math.floor(h / 24)} дн`
}

/**
 * How the process came to be running, from its cgroup. "Запущен вручную"
 * is the one worth reacting to here: it means an interactive login session
 * started it, so nothing will bring it back after a reboot — and nothing
 * declared it in the first place.
 */
function OriginTag({ l }: { l: Listener }) {
  if (l.origin === 'manual') {
    return (
      <Tooltip title="Процесс запущен из интерактивной сессии (SSH), а не сервисом — после перезагрузки не вернётся">
        <Tag color="warning">запущен вручную</Tag>
      </Tooltip>
    )
  }
  if (l.origin === 'service') {
    return (
      <Tooltip title={`systemd-юнит: ${l.unit}`}>
        <Tag color="processing">{l.unit || 'systemd'}</Tag>
      </Tooltip>
    )
  }
  if (l.origin === 'container') {
    return (
      <Tooltip title={l.container_id ? `Контейнер ${l.container_id}` : 'Процесс внутри контейнера'}>
        <Tag color="default">контейнер{l.container_id ? ` ${l.container_id.slice(0, 12)}` : ''}</Tag>
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
  const columns: TableColumnsType<Listener> = [
    {
      title: 'Процесс',
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
              {l.user ? `от ${l.user}` : ''}
              {l.user && (uptime || l.pid) ? ' · ' : ''}
              {uptime ? `работает ${uptime}` : ''}
              {uptime && l.pid ? ' · ' : ''}
              {l.pid ? `pid ${l.pid}` : ''}
            </div>
          </>
        )
      },
    },
    { title: 'Происхождение', key: 'origin', render: (_, l) => <OriginTag l={l} /> },
    {
      title: 'Сокет',
      key: 'socket',
      render: (_, l) => (
        <span className="small mono">
          {l.protocol} {l.address}:{l.port}
        </span>
      ),
    },
    {
      title: 'Доступность',
      key: 'exposure',
      render: (_, l) => (isPublic(l) ? <Tag color="warning">все интерфейсы</Tag> : <Tag>локально</Tag>),
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
            завершить
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
      setNotice({ kind: 'info', text: 'Хост пересканирован.' })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  async function act(service: string, action: string) {
    if (action !== 'validate' && action !== 'reload') {
      if (!window.confirm(`Выполнить «${ACTION_LABEL[action]}» для сервиса ${service}?`)) return
    }
    setBusy(`${service}:${action}`)
    setNotice(null)
    try {
      const path = action === 'validate' ? `/services/${service}/validate` : `/services/${service}/${action}`
      const res = await api<{ output?: string; valid?: boolean; simulated?: boolean }>(path, {
        method: 'POST',
      })
      const suffix = res.simulated ? ' (симуляция, режим снапшота)' : ''
      setNotice({
        kind: res.valid === false ? 'error' : 'info',
        text: `${service}: ${ACTION_LABEL[action]} — ${res.output?.trim() || 'выполнено'}${suffix}`,
      })
      await services.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
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
    const verb = signal === 'TERM' ? 'завершить процесс (SIGTERM)' : 'принудительно завершить процесс (SIGKILL)'
    if (!window.confirm(`${verb} ${l.pid}${l.process ? ` (${l.process})` : ''}?`)) return
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
        setNotice({ kind: 'info', text: `Процесс ${l.pid} не завершился после SIGTERM.` })
      } else {
        setNotice({ kind: 'info', text: `Процесс ${l.pid}: сигнал ${signal} отправлен.` })
      }
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setKillBusy(null)
    }
  }

  const columns: TableColumnsType<ServiceUnit> = [
    {
      title: 'Сервис',
      key: 'name',
      render: (_, s) => (
        <>
          <strong>{s.name}</strong>
          <div className="small muted">{s.description || s.unit}</div>
          {!s.installed && <div className="small muted">не установлен на хосте</div>}
        </>
      ),
    },
    {
      title: 'Состояние',
      key: 'state',
      render: (_, s) => (
        <>
          <StateBadge state={s.active_state} />
          {s.sub_state && <div className="small muted">{s.sub_state}</div>}
        </>
      ),
    },
    { title: 'Автозапуск', key: 'enabled', render: (_, s) => <span className="small">{s.enabled || '—'}</span> },
    { title: 'PID', key: 'main_pid', align: 'right', render: (_, s) => <span className="small">{s.main_pid || '—'}</span> },
    {
      title: 'Память',
      key: 'memory_bytes',
      align: 'right',
      render: (_, s) => <span className="small">{s.memory_bytes ? formatBytesShort(s.memory_bytes) : '—'}</span>,
    },
    { title: 'Перезапусков', key: 'restarts', align: 'right', render: (_, s) => <span className="small">{s.restarts ?? 0}</span> },
    {
      title: 'Действия',
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
              {ACTION_LABEL[a] ?? a}
            </Button>
          ))}
          <Button type="link" size="small" onClick={() => setLogsFor(s)}>
            логи
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
            Сервисы
            <InfoHint>Управление systemd-юнитами. Все действия записываются в журнал с указанием пользователя.</InfoHint>
          </h1>
        </div>
        <div className="row">
          {me.is_admin && (
            <Button
              onClick={rescan}
              loading={rescanning}
              title="Список ниже — снапшот, обновляется раз в несколько минут; эта кнопка пересканирует хост сейчас"
            >
              {rescanning ? 'Сканирую…' : 'Пересканировать'}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={services.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {killEscalation && (
        <Banner kind="warn">
          Процесс {killEscalation.pid}
          {killEscalation.process ? ` (${killEscalation.process})` : ''} не завершился после SIGTERM.{' '}
          <Button
            size="small"
            danger
            loading={killBusy === listenerKey(killEscalation)}
            onClick={() => kill(killEscalation, 'KILL')}
          >
            Отправить SIGKILL
          </Button>
        </Banner>
      )}
      {!canControl && (
        <Banner kind="info">
          Действия недоступны: нужна роль admin и включённые изменения.
        </Banner>
      )}

      <Card title="systemd">
        {services.loading && !services.data ? (
          <Loading what="состояние сервисов" />
        ) : (
          <>
            <InactiveSummary
              items={inactiveServices}
              getKey={(s) => s.name}
              getLabel={(s) => s.name}
              getTooltip={(s) => (
                <>
                  <div>{s.description || s.unit}</div>
                  <div>
                    состояние: {s.active_state}
                    {s.sub_state ? ` (${s.sub_state})` : ''}
                  </div>
                  {!s.installed && <div>не установлен на хосте</div>}
                </>
              )}
              onRescan={rescan}
              rescanning={rescanning}
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
        title="Остальные сервисы"
        subtitle={`сокеты, не описанные ни в одном разобранном конфиге — найдено: ${miscListeners.length}${manual ? ` · из них запущено вручную: ${manual}` : ''}`}
      >
        {misc.loading && !misc.data ? (
          <Loading what="остальные сервисы" />
        ) : miscListeners.length === 0 ? (
          <div className="chart-empty">
            Всё, что слушает сеть на этом хосте, описано в разобранных конфигах.
          </div>
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
    </>
  )
}

/** A static journalctl -u snapshot (see handleServiceLogs) — "обновить"
 * refetches rather than streaming live, deliberately: this is for "what
 * just happened", not a tail -f, and it's available to any viewer (no
 * canControl gate — reading a log is not a mutation). */
function ServiceLogsModal({ service, onClose }: { service: ServiceUnit; onClose: () => void }) {
  const logs = useApi<{ output: string }>(`/services/${service.name}/logs?lines=200`)

  return (
    <Modal title={`Логи: ${service.name}`} onClose={onClose}>
      <div className="row" style={{ justifyContent: 'flex-end', marginBottom: '0.5rem' }}>
        <Button size="small" onClick={() => logs.reload()} loading={logs.loading}>
          обновить
        </Button>
      </div>
      <ErrorNote error={logs.error} />
      {logs.loading && !logs.data ? (
        <Loading what="логи" />
      ) : (
        <pre className="diff" style={{ maxHeight: '28rem' }}>
          {logs.data?.output?.trim() || '(пусто)'}
        </pre>
      )}
    </Modal>
  )
}
