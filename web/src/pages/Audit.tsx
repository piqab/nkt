import { useState } from 'react'
import { Select, Table, type TableColumnsType } from 'antd'
import { qs, useApi } from '../api'
import type { AuditEntry, JobStatus } from '../types'
import { Card, ErrorNote, Loading, StateBadge, formatDateTime, formatRelative } from '../components/ui'

interface JobsResponse {
  jobs: JobStatus[] | null
  intervals: Record<string, string>
  enabled: boolean
}

const ACTION_OPTIONS = [
  { value: '', label: 'все' },
  { value: 'service', label: 'service.*' },
  { value: 'config', label: 'config.*' },
  { value: 'firewall', label: 'firewall.*' },
  { value: 'container', label: 'container.*' },
  { value: 'auth', label: 'auth.*' },
  { value: 'user', label: 'user.*' },
  { value: 'monitor', label: 'monitor.*' },
  { value: 'inventory', label: 'inventory.*' },
]

const RESULT_OPTIONS = [
  { value: '', label: 'все' },
  { value: 'ok', label: 'ok' },
  { value: 'error', label: 'error' },
]

const LIMIT_OPTIONS = [50, 200, 500, 1000].map((n) => ({ value: n, label: String(n) }))

const jobColumns: TableColumnsType<JobStatus> = [
  { title: 'Задача', dataIndex: 'name', key: 'name', render: (name: string) => <strong>{name}</strong> },
  { title: 'Интервал', dataIndex: 'interval', key: 'interval', className: 'small mono' },
  { title: 'Последний запуск', key: 'last_run', render: (_, j) => <span className="small nowrap">{formatRelative(j.last_run)}</span> },
  { title: 'Обработано', dataIndex: 'last_count', key: 'last_count', align: 'right' },
  { title: 'мс', dataIndex: 'duration_ms', key: 'duration_ms', align: 'right' },
  { title: 'Запусков', dataIndex: 'runs', key: 'runs', align: 'right' },
  {
    title: 'Ошибка',
    key: 'last_error',
    render: (_, j) => (
      <span className="small" style={{ color: j.last_error ? 'var(--status-critical)' : undefined }}>
        {j.last_error || '—'}
      </span>
    ),
  },
]

const auditColumns: TableColumnsType<AuditEntry> = [
  { title: 'Когда', key: 'ts', render: (_, e) => <span className="small nowrap">{formatDateTime(e.ts)}</span> },
  { title: 'Кто', dataIndex: 'username', key: 'username', className: 'small' },
  { title: 'Действие', dataIndex: 'action', key: 'action', className: 'small mono' },
  { title: 'Объект', key: 'target', render: (_, e) => <span className="small mono">{e.target || '—'}</span> },
  { title: 'Результат', key: 'result', render: (_, e) => <StateBadge state={e.result === 'ok' ? 'active' : 'failed'} /> },
  {
    title: 'Подробности',
    key: 'detail',
    render: (_, e) => (
      <span className="small mono" style={{ wordBreak: 'break-word', maxWidth: '32rem', display: 'inline-block' }}>
        {e.detail || '—'}
      </span>
    ),
  },
]

export default function Audit() {
  const [action, setAction] = useState('')
  const [result, setResult] = useState('')
  const [limit, setLimit] = useState(200)

  const audit = useApi<{ entries: AuditEntry[] }>(`/audit${qs({ action, result, limit })}`, 30_000)
  const jobs = useApi<JobsResponse>('/monitor/jobs', 30_000)

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Журнал действий</h1>
          <p>
            Каждое изменение состояния хоста — перезапуск сервиса, правка конфига, правило firewall —
            записывается с указанием пользователя, результата и вывода команды.
          </p>
        </div>
      </div>

      <ErrorNote error={audit.error} />

      <Card title="Фоновые задачи" subtitle={jobs.data?.enabled ? 'планировщик работает' : 'планировщик отключён'}>
        {jobs.loading && !jobs.data ? (
          <Loading what="состояние задач" />
        ) : (
          <div className="table-wrap">
            <Table<JobStatus> dataSource={jobs.data?.jobs ?? []} columns={jobColumns} rowKey="name" pagination={false} size="small" />
          </div>
        )}
      </Card>

      <Card
        title="История изменений"
        actions={
          <>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              действие
              <Select value={action} onChange={setAction} options={ACTION_OPTIONS} style={{ minWidth: '9rem' }} />
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              результат
              <Select value={result} onChange={setResult} options={RESULT_OPTIONS} style={{ minWidth: '6rem' }} />
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              строк
              <Select value={limit} onChange={setLimit} options={LIMIT_OPTIONS} style={{ minWidth: '6rem' }} />
            </label>
          </>
        }
      >
        {audit.loading && !audit.data ? (
          <Loading what="журнал" />
        ) : audit.data?.entries.length === 0 ? (
          <div className="chart-empty">Записей нет.</div>
        ) : (
          <div className="table-wrap">
            <Table<AuditEntry>
              dataSource={audit.data?.entries ?? []}
              columns={auditColumns}
              rowKey="id"
              pagination={false}
              size="small"
            />
          </div>
        )}
      </Card>
    </>
  )
}
