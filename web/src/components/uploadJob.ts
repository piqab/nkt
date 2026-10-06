import { ApiError, api, apiURL } from '../api'
import type { Job } from '../types'

/**
 * Загрузка файла с компьютера заданием хоста (POST /uploads/begin): хост
 * заводит задание сразу, до первого байта, и дальше сам пишет в него
 * проценты по дошедшим байтам — ход виден в «Заданиях» и в индикаторе
 * фоновых операций из любого раздела. Затем файл уходит с выданным
 * токеном. null — хост старый и так не умеет: вызывающий грузит прежним
 * способом.
 */
export async function beginUploadJob(body: {
  target: 'archive' | 'vmimage'
  name: string
  size: number
  load?: boolean
}): Promise<{ job: Job; token: string } | null> {
  let res: { job_id: number; token: string }
  try {
    res = await api<{ job_id: number; token: string }>('/uploads/begin', { method: 'POST', body })
  } catch (err) {
    if (err instanceof ApiError && (err.status === 404 || err.status === 405)) return null
    throw err
  }
  const job = await api<Job>(`/jobs/${res.job_id}`)
  return { job, token: res.token }
}

// Пока идёт хоть одна передача, закрытие или перезагрузка вкладки её
// оборвёт — браузер переспросит. Переход по разделам и на хаб передачу не
// прерывает: она живёт здесь, а не в окне, откуда начата.
let sending = 0
function warnOnUnload(e: BeforeUnloadEvent) {
  e.preventDefault()
  e.returnValue = ''
}
function track(delta: number) {
  sending += delta
  if (sending === 1 && delta > 0) window.addEventListener('beforeunload', warnOnUnload)
  if (sending === 0) window.removeEventListener('beforeunload', warnOnUnload)
}

/** Защита передачи из браузера, которая идёт своим путём (не sendFile):
 * пока не вызван возвращённый release, закрытие вкладки переспросит. */
export function guardUnload(): () => void {
  track(1)
  let released = false
  return () => {
    if (!released) {
      released = true
      track(-1)
    }
  }
}

/** Отправка файла XMLHttpRequest — только он сообщает, сколько ушло. */
export function sendFile(method: 'PUT' | 'POST', path: string, file: File, onProgress: (percent: number) => void): Promise<void> {
  track(1)
  return new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open(method, apiURL(path))
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      if (xhr.status === 200) {
        resolve()
        return
      }
      try {
        reject(new Error((JSON.parse(xhr.responseText) as { error?: string }).error ?? `HTTP ${xhr.status}`))
      } catch {
        reject(new Error(`HTTP ${xhr.status}`))
      }
    }
    xhr.onerror = () => reject(new Error('network'))
    xhr.send(file)
  }).finally(() => track(-1))
}
