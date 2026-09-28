import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Button } from 'antd'
import { useTranslation } from 'react-i18next'
import { ApiError, api } from '../api'
import type { Job } from '../types'
import { JobLogModal } from '../pages/Jobs'

/**
 * Удаление потенциально долгих объектов «Контейнеров и ВМ» заданием хоста
 * (POST /deletions): один объект или выбранные разом. Пока объект
 * удаляется, его строка заблокирована — это видно и после перезагрузки
 * страницы (GET /deletions), а повторное нажатие вернёт то же задание.
 * По окончании — onDone: пересканировать хост и перестроить таблицы.
 *
 * Хост старой версии /deletions не знает — тогда вызывается legacy
 * (прежнее мгновенное удаление по одному).
 */
export interface DeleteItem {
  kind: 'docker' | 'podman' | 'lxd' | 'lxd-snapshot' | 'vm' | 'image' | 'k8s'
  name: string
  snapshot?: string
  k8s_kind?: string
  namespace?: string
  force?: boolean
  remove_storage?: boolean
}

export function deleteKey(it: DeleteItem): string {
  if (it.kind === 'lxd-snapshot') return `${it.kind}:${it.name}/${it.snapshot}`
  if (it.kind === 'k8s') return `${it.kind}:${it.k8s_kind}:${it.namespace ?? ''}/${it.name}`
  return `${it.kind}:${it.name}`
}

export function useDeletions(onDone: () => void | Promise<void>): {
  remove: (items: DeleteItem[], legacy?: (it: DeleteItem) => Promise<void>) => Promise<void>
  isDeleting: (it: DeleteItem) => boolean
  modal: ReactNode
} {
  const [active, setActive] = useState<Record<string, number>>({})
  const [job, setJob] = useState<Job | null>(null)
  const unsupported = useRef(false)
  const done = useRef(onDone)
  done.current = onDone

  const load = useCallback(async () => {
    if (unsupported.current) return
    try {
      const res = await api<{ active: Record<string, number> }>('/deletions')
      setActive((prev) => {
        // Что-то исчезло из списка — удаление закончилось: перестроить.
        if (Object.keys(prev).some((k) => !(k in res.active))) void done.current()
        return res.active
      })
    } catch (err) {
      if (err instanceof ApiError && [404, 405].includes(err.status)) unsupported.current = true
    }
  }, [])

  const busy = Object.keys(active).length > 0
  useEffect(() => {
    void load()
    const id = window.setInterval(() => void load(), busy ? 3000 : 20000)
    return () => window.clearInterval(id)
  }, [load, busy])

  async function remove(items: DeleteItem[], legacy?: (it: DeleteItem) => Promise<void>) {
    if (items.length === 0) return
    if (unsupported.current && legacy) {
      for (const it of items) await legacy(it)
      await done.current()
      return
    }
    try {
      const res = await api<{ job_id: number }>('/deletions', { method: 'POST', body: { items } })
      setActive((prev) => {
        const next = { ...prev }
        for (const it of items) next[deleteKey(it)] = res.job_id
        return next
      })
      setJob(await api<Job>(`/jobs/${res.job_id}`))
    } catch (err) {
      if (err instanceof ApiError && [404, 405].includes(err.status) && legacy) {
        unsupported.current = true
        for (const it of items) await legacy(it)
        await done.current()
        return
      }
      throw err
    }
  }

  const modal = job ? (
    <JobLogModal
      job={job}
      onClose={() => setJob(null)}
      onDone={() => {
        void load()
        void done.current()
      }}
    />
  ) : null
  return { remove, isDeleting: (it) => deleteKey(it) in active, modal }
}

/** Панель над таблицей: сколько выбрано и «Удалить выбранные». */
export function BulkDeleteBar({
  count,
  onDelete,
  onClear,
  disabled,
  extra,
}: {
  count: number
  onDelete: () => void
  onClear: () => void
  disabled?: boolean
  extra?: ReactNode
}) {
  const { t } = useTranslation()
  if (count === 0) return null
  return (
    <div className="row small" style={{ gap: '0.5rem', alignItems: 'center', margin: '0.3rem 0' }}>
      <span>{t('bulk.selected', { count })}</span>
      {extra}
      <Button size="small" danger disabled={disabled} onClick={onDelete}>
        {t('bulk.deleteSelected', { count })}
      </Button>
      <Button size="small" type="link" onClick={onClear}>
        {t('bulk.clear')}
      </Button>
    </div>
  )
}
