import { useEffect, useRef, useState } from 'react'
import { OsIconOf } from './OsIcon'
import { Button, Popover, Progress } from 'antd'
import { CloseCircleFilled, LoadingOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import type { Job } from '../types'
import { JobLogModal } from '../pages/Jobs'

/** Идущее задание и где оно идёт (хаб: GET /hub/jobs/active). */
interface ActiveJob {
  host_id: number
  host_name: string
  job: Job
}

/** Опрос — раз в несколько секунд, пока вкладка на виду. */
const POLL_MS = 8000

/**
 * Индикатор фоновых операций — в углу экрана в любом разделе, на хабе и
 * на хосте: что идёт сейчас (задания хоста, хаба, всех хостов — для хаба)
 * с процентами; нажатие открывает журнал. Без него задание было видно
 * только в «Заданиях» своего хоста, и уход в другой раздел или на хаб
 * выглядел как обрыв операции.
 */
export function ActiveJobsIndicator({ isHub }: { isHub: boolean }) {
  const { t } = useTranslation()
  const [list, setList] = useState<ActiveJob[]>([])
  const [open, setOpen] = useState<ActiveJob | null>(null)
  const [popover, setPopover] = useState(false)
  // Задание закончилось ошибкой, пока вы были в другом разделе: кнопка на
  // минуту краснеет — «ошибка, открыть журнал».
  const [failed, setFailed] = useState<ActiveJob | null>(null)
  const prevRef = useRef<ActiveJob[]>([])
  const noteGone = (next: ActiveJob[]) => {
    const gone = prevRef.current.filter((a) => !next.some((b) => b.job.id === a.job.id && b.host_id === a.host_id))
    prevRef.current = next
    for (const a of gone) {
      const path = isHub ? `${a.host_id === -1 ? '/hosts/local' : `/hosts/${a.host_id}`}/jobs/${a.job.id}` : `/jobs/${a.job.id}`
      void api<Job>(path)
        .then((j) => {
          if (j.status === 'failed' || j.status === 'interrupted') setFailed({ ...a, job: j })
        })
        .catch(() => undefined)
    }
  }
  useEffect(() => {
    if (!failed) return
    const tm = setTimeout(() => setFailed(null), 60_000)
    return () => clearTimeout(tm)
  }, [failed])

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      if (document.visibilityState === 'visible') {
        try {
          if (isHub) {
            const res = await api<{ jobs: ActiveJob[] }>('/hub/jobs/active', { timeoutMs: 15_000 })
            if (!cancelled) {
              setList(res.jobs ?? [])
              noteGone(res.jobs ?? [])
            }
          } else {
            // Без хаба — свои задания хоста: путь без области, как и всё
            // на одиночном хосте.
            const res = await api<{ jobs: Job[] }>('/jobs?status=queued,running&limit=20')
            if (!cancelled) {
              const next = (res.jobs ?? []).map((job) => ({ host_id: 0, host_name: '', job }))
              setList(next)
              noteGone(next)
            }
          }
        } catch {
          // Нет ответа — индикатор просто не меняется до следующего раза.
        }
      }
      if (!cancelled) timer = setTimeout(poll, POLL_MS)
    }
    void poll()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [isHub])

  const scopeOf = (a: ActiveJob) => (!isHub ? '' : a.host_id === -1 ? '/hosts/local' : `/hosts/${a.host_id}`)
  const percent = (j: Job) => (j.steps > 0 ? Math.min(100, Math.round((j.step / j.steps) * 100)) : 0)

  const content = (
    <div className="active-jobs-list">
      {list.map((a) => (
        <button
          key={`${a.host_id}:${a.job.id}`}
          className="active-jobs-item ghost"
          onClick={() => {
            setPopover(false)
            setOpen(a)
          }}
        >
          <span className="active-jobs-title">
            {isHub && (
              <span className="muted">
                <OsIconOf kind="host" hostID={a.host_id} name={a.host_name} size={12} />
                {a.host_name} ·{' '}
              </span>
            )}
            {a.job.title}
          </span>
          <span className="small muted">
            {a.job.status === 'queued' ? t('activeJobs.queued') : a.job.step_name || t('activeJobs.running')}
          </span>
          {a.job.status === 'running' && <Progress percent={percent(a.job)} size="small" showInfo={false} />}
        </button>
      ))}
    </div>
  )

  return (
    <>
      {failed && list.length === 0 && (
        <Button
          className="active-jobs-button active-jobs-failed"
          icon={<CloseCircleFilled />}
          onClick={() => {
            setOpen(failed)
            setFailed(null)
          }}
        >
          {t('activeJobs.failed')}
        </Button>
      )}
      {list.length > 0 && (
        <Popover content={content} title={t('activeJobs.title')} trigger="click" open={popover} onOpenChange={setPopover} placement="topRight">
          <Button className="active-jobs-button" icon={<LoadingOutlined />} aria-label={t('activeJobs.title')}>
            {t('activeJobs.count', { count: list.length })}
          </Button>
        </Popover>
      )}
      {open && <JobLogModal job={open.job} scope={scopeOf(open)} onClose={() => setOpen(null)} />}
    </>
  )
}
