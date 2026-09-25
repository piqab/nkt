import { useEffect, useState, type ReactNode } from 'react'
import { ApiError, api } from '../api'
import { Banner, Loading, Modal } from './ui'
import type { Job } from '../types'
import { JobLogModal } from '../pages/Jobs'

/**
 * Запуск долгой операции фоновым заданием хоста: POST с ?job=1, в ответ —
 * job_id, и сразу открывается стандартное окно журнала задания. Закрытие
 * окна операцию не прерывает — задание остаётся в «Заданиях». Старый хост
 * флаг ?job=1 не знает и выполняет операцию сразу: ответ без job_id —
 * значит, всё уже сделано, onDone вызывается тут же.
 */
export function useJobLauncher(onDone?: (job: Job | null) => void): {
  start: (path: string, body?: unknown) => Promise<void>
  modal: ReactNode
} {
  const [job, setJob] = useState<Job | null>(null)

  async function start(path: string, body?: unknown) {
    const url = path + (path.includes('?') ? '&' : '?') + 'job=1'
    const res = await api<{ job_id?: number }>(url, { method: 'POST', body })
    if (res && typeof res.job_id === 'number') {
      setJob(await api<Job>(`/jobs/${res.job_id}`))
      return
    }
    onDone?.(null)
  }

  const modal = job ? <JobLogModal job={job} onClose={() => setJob(null)} onDone={(j) => onDone?.(j)} /> : null
  return { start, modal }
}

/**
 * Окно долгой операции «сначала задание»: POST path?job=1 → стандартное
 * окно журнала задания. Хост старой версии такого POST не знает (405/404)
 * или операция заданием не выполняется (501) — тогда показывается прежний
 * живой вывод (live). Прочие ошибки (неверные пакеты и т. п.) — в окне.
 */
export function JobFirst({
  path,
  title,
  onClose,
  onDone,
  live,
}: {
  path: string
  title: string
  onClose: () => void
  onDone?: () => void
  live: () => ReactNode
}) {
  const [state, setState] = useState<{ mode: 'probe' } | { mode: 'job'; job: Job } | { mode: 'live' } | { mode: 'error'; text: string }>({ mode: 'probe' })

  useEffect(() => {
    let cancelled = false
    const url = path + (path.includes('?') ? '&' : '?') + 'job=1'
    api<{ job_id?: number }>(url, { method: 'POST' })
      .then(async (res) => {
        if (cancelled) return
        if (res && typeof res.job_id === 'number') {
          const job = await api<Job>(`/jobs/${res.job_id}`)
          if (!cancelled) setState({ mode: 'job', job })
        } else {
          setState({ mode: 'live' })
        }
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof ApiError && [404, 405, 501].includes(err.status)) setState({ mode: 'live' })
        else setState({ mode: 'error', text: err instanceof Error ? err.message : String(err) })
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- одна попытка на окно
  }, [])

  if (state.mode === 'live') return <>{live()}</>
  if (state.mode === 'job') return <JobLogModal job={state.job} onClose={onClose} onDone={() => onDone?.()} />
  return (
    <Modal title={title} onClose={onClose}>
      {state.mode === 'error' ? <Banner kind="error">{state.text}</Banner> : <Loading what={title} />}
    </Modal>
  )
}
