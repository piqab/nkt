import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import { wsURL } from '../hooks/usePty'
import type { Job, JobLogLine, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, formatDateTime } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { confirmAction } from '../components/confirm'

/** Как часто перечитывать список. Задание может закончиться в любой
 * момент, а список, который врёт полминуты, хуже пустого. */
const LIST_POLL_MS = 5_000

/** Запасной опрос журнала, когда сокет не открылся или оборвался. Живой
 * поток быстрее, но обязателен именно этот путь: с ним раздел работает и
 * там, где веб-сокеты режет прокси. */
const LOG_POLL_MS = 2_000

const STATUS_COLOR: Record<string, string> = {
  queued: 'default',
  running: 'processing',
  succeeded: 'success',
  failed: 'error',
  canceled: 'default',
  interrupted: 'warning',
}

export default function Jobs({ me }: { me: Me }) {
  const { t } = useTranslation()
  const jobs = useApi<{ jobs: Job[]; active: number }>('/jobs', LIST_POLL_MS)
  const [openJob, setOpenJob] = useState<Job | null>(null)

  const columns: TableColumnsType<Job> = [
    {
      title: t('jobs.colTitle'),
      key: 'title',
      render: (_, j) => (
        <div style={{ minWidth: '14rem' }}>
          <strong>{j.title || j.kind}</strong>
          <div className="small muted mono">{j.kind}</div>
        </div>
      ),
    },
    {
      title: t('jobs.colStatus'),
      key: 'status',
      render: (_, j) => (
        <span className="nowrap">
          <Tag color={STATUS_COLOR[j.status] ?? 'default'}>{t(`jobs.status.${j.status}`)}</Tag>
          {j.error && <InfoHint>{j.error}</InfoHint>}
        </span>
      ),
    },
    {
      title: t('jobs.colStep'),
      key: 'step',
      render: (_, j) =>
        j.steps > 0 ? (
          <span className="small nowrap">
            {j.step}/{j.steps}
            {j.step_name && <div className="small muted">{j.step_name}</div>}
          </span>
        ) : (
          <span className="small muted">{j.step_name || '—'}</span>
        ),
    },
    {
      title: t('jobs.colStarted'),
      key: 'started',
      render: (_, j) => (
        <span className="small nowrap">{j.started_at ? formatDateTime(j.started_at) : formatDateTime(j.created_at)}</span>
      ),
    },
    {
      title: t('jobs.colDuration'),
      key: 'duration',
      render: (_, j) => <span className="small nowrap">{duration(j, t)}</span>,
    },
    {
      title: '',
      key: 'actions',
      className: 'nowrap',
      render: (_, j) => (
        <div className="row row-nowrap">
          <Button type="link" size="small" onClick={() => setOpenJob(j)}>
            {t('jobs.openLog')}
          </Button>
          {!isDone(j) && me.is_admin && me.allow_mutations && (
            <Button
              type="link"
              size="small"
              danger
              onClick={async () => {
                if (!(await confirmAction(t('jobs.confirmCancel', { title: j.title || j.kind })))) return
                await api(`/jobs/${j.id}/cancel`, { method: 'POST' }).catch(() => undefined)
                jobs.reload()
              }}
            >
              {t('jobs.cancel')}
            </Button>
          )}
        </div>
      ),
    },
  ]

  const list = jobs.data?.jobs ?? []

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('jobs.title')}
          <InfoHint>{t('jobs.hint')}</InfoHint>
        </h1>
      </div>

      <ErrorNote error={jobs.error} />

      <Card
        title={t('jobs.listTitle')}
        subtitle={t('jobs.listSubtitle', { count: jobs.data?.active ?? 0 })}
      >
        {jobs.loading && !jobs.data ? (
          <Loading what={t('jobs.loading')} />
        ) : list.length === 0 ? (
          <p className="small muted">{t('jobs.empty')}</p>
        ) : (
          <div className="table-wrap">
            <DataTable<Job> dataSource={list} columns={columns} rowKey="id" tableLayout="auto" />
          </div>
        )}
      </Card>

      {openJob && <JobLogModal job={openJob} onClose={() => setOpenJob(null)} />}
    </>
  )
}

function isDone(j: Job): boolean {
  return ['succeeded', 'failed', 'canceled', 'interrupted'].includes(j.status)
}

function duration(j: Job, t: (k: string, o?: Record<string, unknown>) => string): string {
  const from = j.started_at || j.created_at
  if (!from) return '—'
  const end = j.finished_at ? new Date(j.finished_at).getTime() : Date.now()
  const secs = Math.max(0, Math.round((end - new Date(from).getTime()) / 1000))
  if (secs < 60) return t('jobs.seconds', { count: secs })
  if (secs < 3600) return t('jobs.minutes', { count: Math.round(secs / 60) })
  return t('jobs.hours', { count: Math.round(secs / 360) / 10 })
}

/**
 * Журнал одного задания.
 *
 * Строки берутся из базы («после какой мы уже видели»), а сокет лишь
 * досылает новые. Поэтому вкладку можно закрыть и вернуться через час:
 * журнал соберётся целиком, а не с момента подключения. Если сокет не
 * открылся, включается опрос — раздел работает и без него.
 */
function JobLogModal({ job, onClose }: { job: Job; onClose: () => void }) {
  const { t } = useTranslation()
  const [lines, setLines] = useState<JobLogLine[]>([])
  const [current, setCurrent] = useState<Job>(job)
  const [live, setLive] = useState(false)
  const lastSeq = useRef(0)
  const bodyRef = useRef<HTMLPreElement | null>(null)

  const fetchTail = useCallback(async () => {
    try {
      const res = await api<{ job: Job; lines: JobLogLine[] }>(
        `/jobs/${job.id}/log${qs({ after: lastSeq.current })}`,
      )
      setCurrent(res.job)
      if (res.lines.length > 0) {
        lastSeq.current = res.lines[res.lines.length - 1].seq
        setLines((prev) => [...prev, ...res.lines])
      }
      return res.job
    } catch {
      // Сеть моргнула — следующий заход дочитает то же самое: номер
      // последней строки не сдвинулся.
      return null
    }
  }, [job.id])

  // Первый заход всегда через базу — до всякого сокета.
  useEffect(() => {
    void fetchTail()
  }, [fetchTail])

  // Живой поток. Строки из сокета не складываются в состояние напрямую:
  // он может пропустить событие под нагрузкой, поэтому по каждому сигналу
  // дочитывается хвост из базы — единственный источник правды.
  useEffect(() => {
    if (isDone(job)) return
    let closed = false
    let ws: WebSocket | null = null
    try {
      ws = new WebSocket(wsURL(`/jobs/${job.id}/ws`))
    } catch {
      return
    }
    ws.onopen = () => !closed && setLive(true)
    ws.onmessage = () => void fetchTail()
    ws.onclose = () => {
      if (!closed) setLive(false)
    }
    ws.onerror = () => {
      if (!closed) setLive(false)
    }
    return () => {
      closed = true
      ws?.close()
    }
  }, [job.id, fetchTail])

  // Запасной опрос: работает, пока задание идёт и живого потока нет.
  useEffect(() => {
    if (live || isDone(current)) return
    const timer = setInterval(() => void fetchTail(), LOG_POLL_MS)
    return () => clearInterval(timer)
  }, [live, current, fetchTail])

  useEffect(() => {
    const el = bodyRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [lines])

  return (
    <Modal title={current.title || current.kind} onClose={onClose} maskClosable={false} width={860}>
      <div className="row" style={{ marginBottom: '0.5rem' }}>
        <Tag color={STATUS_COLOR[current.status] ?? 'default'}>{t(`jobs.status.${current.status}`)}</Tag>
        {current.steps > 0 && (
          <span className="small muted">
            {t('jobs.stepOf', { step: current.step, steps: current.steps })}
            {current.step_name ? ` · ${current.step_name}` : ''}
          </span>
        )}
        {!isDone(current) && !live && <span className="small muted">{t('jobs.polling')}</span>}
      </div>

      {current.status === 'interrupted' && <Banner kind="warn">{t('jobs.interruptedHint')}</Banner>}
      {current.error && <Banner kind="error">{current.error}</Banner>}

      <pre ref={bodyRef} className="diff mono" style={{ maxHeight: '26rem', overflow: 'auto', whiteSpace: 'pre-wrap' }}>
        {lines.map((l) => l.text).join('\n') || t('jobs.noOutput')}
      </pre>
    </Modal>
  )
}
