import { useState } from 'react'
import { Button, Table, Tag, Tooltip, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { Listener, Me, ServiceUnit } from '../types'
import { Banner, Card, ErrorNote, Loading, StateBadge, formatBytesShort } from '../components/ui'

const ACTION_LABEL: Record<string, string> = {
  start: 'запустить',
  stop: 'остановить',
  restart: 'перезапустить',
  reload: 'перечитать конфиг',
  validate: 'проверить конфиг',
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

const miscColumns: TableColumnsType<Listener> = [
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

/**
 * "Сервисы" — systemd-юниты, и ниже, в том же разделе, «Неучтённые
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

  const canControl = me.is_admin && me.allow_mutations
  const miscListeners = misc.data?.listeners ?? []
  const manual = miscListeners.filter((l) => l.origin === 'manual').length

  async function rescan() {
    setRescanning(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      services.reload()
      misc.reload()
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
      services.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
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
        </div>
      ),
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>Сервисы</h1>
          <p>
            Управление systemd-юнитами. Все действия записываются в журнал с указанием
            пользователя.
          </p>
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
      {!canControl && (
        <Banner kind="info">
          Действия недоступны: нужна роль admin и включённые изменения.
        </Banner>
      )}

      <Card title="systemd">
        {services.loading && !services.data ? (
          <Loading what="состояние сервисов" />
        ) : (
          <div className="table-wrap">
            <Table<ServiceUnit>
              dataSource={services.data?.services ?? []}
              columns={columns}
              rowKey="name"
              pagination={false}
              size="small"
            />
          </div>
        )}
      </Card>

      <ErrorNote error={misc.error} />
      <Card
        title="Неучтённые сервисы"
        subtitle={`сокеты, не описанные ни в одном разобранном конфиге — найдено: ${miscListeners.length}${manual ? ` · из них запущено вручную: ${manual}` : ''}`}
      >
        {misc.loading && !misc.data ? (
          <Loading what="неучтённые сервисы" />
        ) : miscListeners.length === 0 ? (
          <div className="chart-empty">
            Всё, что слушает сеть на этом хосте, описано в разобранных конфигах.
          </div>
        ) : (
          <div className="table-wrap">
            <Table<Listener>
              dataSource={miscListeners}
              columns={miscColumns}
              rowKey={(l) => `${l.address}:${l.port}`}
              pagination={false}
              size="small"
            />
          </div>
        )}
      </Card>
    </>
  )
}
