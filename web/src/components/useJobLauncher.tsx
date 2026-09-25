import { useState, type ReactNode } from 'react'
import { api } from '../api'
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
