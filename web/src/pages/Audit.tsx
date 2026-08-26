import { useState } from 'react'
import { Select, Table, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { qs, useApi } from '../api'
import type { AuditEntry, JobStatus } from '../types'
import { Card, ErrorNote, InfoHint, Loading, StateBadge, formatDateTime, formatRelative } from '../components/ui'

interface JobsResponse {
  jobs: JobStatus[] | null
  intervals: Record<string, string>
  enabled: boolean
}

const LIMIT_OPTIONS = [50, 200, 500, 1000].map((n) => ({ value: n, label: String(n) }))

export default function Audit() {
  const { t } = useTranslation()
  const [action, setAction] = useState('')
  const [result, setResult] = useState('')
  const [limit, setLimit] = useState(200)

  const audit = useApi<{ entries: AuditEntry[] }>(`/audit${qs({ action, result, limit })}`, 30_000)
  const jobs = useApi<JobsResponse>('/monitor/jobs', 30_000)

  // action.*/config.* etc. are literal audit-log prefixes (Go's own action
  // naming, see internal/api's writeAudit calls), not translatable text.
  const actionOptions = [
    { value: '', label: t('audit.actionAll') },
    { value: 'service', label: 'service.*' },
    { value: 'config', label: 'config.*' },
    { value: 'firewall', label: 'firewall.*' },
    { value: 'container', label: 'container.*' },
    { value: 'auth', label: 'auth.*' },
    { value: 'user', label: 'user.*' },
    { value: 'monitor', label: 'monitor.*' },
    { value: 'inventory', label: 'inventory.*' },
  ]

  const resultOptions = [
    { value: '', label: t('audit.resultAll') },
    { value: 'ok', label: 'ok' },
    { value: 'error', label: 'error' },
  ]

  const jobColumns: TableColumnsType<JobStatus> = [
    { title: t('audit.jobName'), dataIndex: 'name', key: 'name', render: (name: string) => <strong>{name}</strong> },
    { title: t('audit.jobInterval'), dataIndex: 'interval', key: 'interval', className: 'small mono' },
    { title: t('audit.jobLastRun'), key: 'last_run', render: (_, j) => <span className="small nowrap">{formatRelative(j.last_run)}</span> },
    { title: t('audit.jobProcessed'), dataIndex: 'last_count', key: 'last_count', align: 'right' },
    { title: t('audit.jobMs'), dataIndex: 'duration_ms', key: 'duration_ms', align: 'right' },
    { title: t('audit.jobRuns'), dataIndex: 'runs', key: 'runs', align: 'right' },
    {
      title: t('audit.jobError'),
      key: 'last_error',
      render: (_, j) => (
        <span className="small" style={{ color: j.last_error ? 'var(--status-critical)' : undefined }}>
          {j.last_error || '—'}
        </span>
      ),
    },
  ]

  const auditColumns: TableColumnsType<AuditEntry> = [
    { title: t('audit.auditWhen'), key: 'ts', render: (_, e) => <span className="small nowrap">{formatDateTime(e.ts)}</span> },
    { title: t('audit.auditWho'), dataIndex: 'username', key: 'username', className: 'small' },
    { title: t('audit.auditAction'), dataIndex: 'action', key: 'action', className: 'small mono' },
    { title: t('audit.auditTarget'), key: 'target', render: (_, e) => <span className="small mono">{e.target || '—'}</span> },
    { title: t('audit.auditResult'), key: 'result', render: (_, e) => <StateBadge state={e.result === 'ok' ? 'active' : 'failed'} /> },
    {
      title: t('audit.auditDetail'),
      key: 'detail',
      render: (_, e) => (
        <span className="small mono" style={{ wordBreak: 'break-word', maxWidth: '32rem', display: 'inline-block' }}>
          {e.detail || '—'}
        </span>
      ),
    },
  ]

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('audit.title')}
            <InfoHint>{t('audit.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={audit.error} />

      <Card title={t('audit.backgroundJobs')} subtitle={jobs.data?.enabled ? t('audit.schedulerRunning') : t('audit.schedulerDisabled')}>
        {jobs.loading && !jobs.data ? (
          <Loading what={t('audit.loadingJobStatus')} />
        ) : (
          <div className="table-wrap">
            <Table<JobStatus> dataSource={jobs.data?.jobs ?? []} columns={jobColumns} rowKey="name" pagination={false} size="small" />
          </div>
        )}
      </Card>

      <Card
        title={t('audit.changeHistory')}
        actions={
          <>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              {t('audit.action')}
              <Select value={action} onChange={setAction} options={actionOptions} style={{ minWidth: '9rem' }} />
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              {t('audit.result')}
              <Select value={result} onChange={setResult} options={resultOptions} style={{ minWidth: '6rem' }} />
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              {t('audit.rows')}
              <Select value={limit} onChange={setLimit} options={LIMIT_OPTIONS} style={{ minWidth: '6rem' }} />
            </label>
          </>
        }
      >
        {audit.loading && !audit.data ? (
          <Loading what={t('audit.loadingLog')} />
        ) : audit.data?.entries.length === 0 ? (
          <div className="chart-empty">{t('audit.noEntries')}</div>
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
