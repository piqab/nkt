import { Table, Tag, Tooltip, type TableColumnsType } from 'antd'
import { useApi } from '../api'
import type { Listener } from '../types'
import { Card, ErrorNote, Loading } from '../components/ui'

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
 * is the one worth reacting to on this page: it means an interactive
 * login session started it, so nothing will bring it back after a reboot
 * — and nothing declared it in the first place.
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

/**
 * Services actually listening on the network that no parsed config
 * (nginx/haproxy/docker) accounts for — the same set
 * analyze.ruleListeningNotDeclared turns into findings, here as a plain
 * inventory instead of a problem report. Useful for spotting things nobody
 * remembers configuring: a debug server left running, a database exposed
 * outside docker-compose, an ad-hoc process someone started by hand.
 */
export default function Misc() {
  const { data, error, loading } = useApi<{ listeners: Listener[] }>('/misc', 60_000)
  const listeners = data?.listeners ?? []
  const manual = listeners.filter((l) => l.origin === 'manual').length

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Разное</h1>
          <p>
            Сокеты, которые реально слушают на хосте, но не описаны ни в одном разобранном
            конфиге nginx, haproxy или docker — ни одна из уже существующих страниц их не
            покажет. Стоит проверить: забытый отладочный сервер, база данных вне
            docker-compose, процесс, запущенный вручную в обход конфигурации.
          </p>
        </div>
      </div>

      <ErrorNote error={error} />

      <Card
        title="Неучтённые сервисы"
        subtitle={`найдено: ${listeners.length}${manual ? ` · из них запущено вручную: ${manual}` : ''}`}
      >
        {loading && !data ? (
          <Loading what="неучтённые сервисы" />
        ) : listeners.length === 0 ? (
          <div className="chart-empty">
            Всё, что слушает сеть на этом хосте, описано в разобранных конфигах.
          </div>
        ) : (
          <div className="table-wrap">
            <Table<Listener>
              dataSource={listeners}
              columns={columns}
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
