import { useState } from 'react'
import { qs, useApi } from '../api'
import type { AuditEntry, JobStatus } from '../types'
import { Card, ErrorNote, Loading, StateBadge, formatDateTime, formatRelative } from '../components/ui'

interface JobsResponse {
  jobs: JobStatus[] | null
  intervals: Record<string, string>
  enabled: boolean
}

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
            <table>
              <thead>
                <tr>
                  <th>Задача</th>
                  <th>Интервал</th>
                  <th>Последний запуск</th>
                  <th className="num">Обработано</th>
                  <th className="num">мс</th>
                  <th className="num">Запусков</th>
                  <th>Ошибка</th>
                </tr>
              </thead>
              <tbody>
                {(jobs.data?.jobs ?? []).map((j) => (
                  <tr key={j.name}>
                    <td>
                      <strong>{j.name}</strong>
                    </td>
                    <td className="small mono">{j.interval}</td>
                    <td className="small nowrap">{formatRelative(j.last_run)}</td>
                    <td className="num">{j.last_count}</td>
                    <td className="num">{j.duration_ms}</td>
                    <td className="num">{j.runs}</td>
                    <td className="small" style={{ color: j.last_error ? 'var(--status-critical)' : undefined }}>
                      {j.last_error || '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card
        title="История изменений"
        actions={
          <>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              действие
              <select value={action} onChange={(e) => setAction(e.target.value)}>
                <option value="">все</option>
                <option value="service">service.*</option>
                <option value="config">config.*</option>
                <option value="firewall">firewall.*</option>
                <option value="container">container.*</option>
                <option value="auth">auth.*</option>
                <option value="user">user.*</option>
                <option value="monitor">monitor.*</option>
                <option value="inventory">inventory.*</option>
              </select>
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              результат
              <select value={result} onChange={(e) => setResult(e.target.value)}>
                <option value="">все</option>
                <option value="ok">ok</option>
                <option value="error">error</option>
              </select>
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              строк
              <select value={limit} onChange={(e) => setLimit(Number(e.target.value))}>
                {[50, 200, 500, 1000].map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
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
            <table>
              <thead>
                <tr>
                  <th>Когда</th>
                  <th>Кто</th>
                  <th>Действие</th>
                  <th>Объект</th>
                  <th>Результат</th>
                  <th>Подробности</th>
                </tr>
              </thead>
              <tbody>
                {audit.data?.entries.map((e) => (
                  <tr key={e.id}>
                    <td className="small nowrap">{formatDateTime(e.ts)}</td>
                    <td className="small">{e.username}</td>
                    <td className="small mono">{e.action}</td>
                    <td className="small mono">{e.target || '—'}</td>
                    <td>
                      <StateBadge state={e.result === 'ok' ? 'active' : 'failed'} />
                    </td>
                    <td className="small mono" style={{ wordBreak: 'break-word', maxWidth: '32rem' }}>
                      {e.detail || '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  )
}
