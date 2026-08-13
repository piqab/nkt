import { Table, Tag, type TableColumnsType } from 'antd'
import { useApi } from '../api'
import type { Listener } from '../types'
import { Card, ErrorNote, Loading } from '../components/ui'

/** Mirrors model.Listener.Public() in Go — a socket bound to all
 * interfaces rather than just loopback/a specific address. */
function isPublic(l: Listener): boolean {
  return l.address === '0.0.0.0' || l.address === '*' || l.address === '::' || l.address === '[::]'
}

const columns: TableColumnsType<Listener> = [
  { title: 'Процесс', key: 'process', render: (_, l) => <strong>{l.process || '—'}</strong> },
  { title: 'PID', key: 'pid', align: 'right', render: (_, l) => <span className="small">{l.pid || '—'}</span> },
  { title: 'Протокол', dataIndex: 'protocol', key: 'protocol', className: 'small mono' },
  { title: 'Адрес', key: 'address', render: (_, l) => <span className="small mono">{l.address}</span> },
  { title: 'Порт', dataIndex: 'port', key: 'port', align: 'right', className: 'small mono' },
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

      <Card title="Неучтённые сервисы" subtitle={`найдено: ${listeners.length}`}>
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
