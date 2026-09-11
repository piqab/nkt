import { useCallback, useEffect, useRef, useState } from 'react'
import { api, hostScope } from './api'

/**
 * Пересканирование хоста для разделов, у которых есть кнопка
 * «Пересканировать».
 *
 * Разделы показывают снимок инвентаря, а не живое состояние: снимок
 * собирается по расписанию, и всё, что случилось между сканированиями,
 * до него не доходит. Поднятый только что стек compose виден в docker, но
 * не в разделе — и выглядит это как «ничего не запустилось», хотя всё
 * работает. Поэтому вход в раздел сам просит свежий снимок, а кнопка
 * остаётся для случая «я поменял что-то руками на сервере».
 *
 * Сканирование общее для всего хоста: одного хватает всем разделам, и
 * повторять его при переключении вкладок незачем — отсюда общая на всё
 * приложение отметка времени. Ключ у неё — выбранный хост: в хабе
 * переключение на другой хост должно сканировать его, а не считать
 * свежим чужой снимок.
 */
const FRESH_MS = 10_000

const lastScanAt = new Map<number | null, number>()
const inFlight = new Map<number | null, Promise<void>>()

function refreshSnapshot(): Promise<void> {
  const key = hostScope.id
  const running = inFlight.get(key)
  if (running) return running
  // Сканирование обходит весь хост и на большой машине занимает заметно
  // больше обычного запроса — своё ограничение времени, иначе вход в
  // раздел выглядел бы как ошибка.
  const p = api('/inventory/refresh', { method: 'POST', timeoutMs: 120_000 })
    .then(() => {
      lastScanAt.set(key, Date.now())
    })
    .finally(() => {
      inFlight.delete(key)
    })
  inFlight.set(key, p)
  return p
}

export function useHostRescan({
  reload,
  canScan,
  onNotice,
}: {
  /** Перечитать данные раздела после сканирования. */
  reload: () => unknown
  /** Сканирование меняет состояние на сервере и требует прав. */
  canScan: boolean
  /** Сообщение по нажатию кнопки; автоматическое сканирование молчит. */
  onNotice?: (kind: 'info' | 'error', text: string) => void
}): { rescanning: boolean; rescan: (successText?: string) => Promise<void> } {
  const [rescanning, setRescanning] = useState(false)
  // Ссылки, а не зависимости: reload и onNotice пересоздаются на каждой
  // отрисовке, и в списке зависимостей они запускали бы сканирование
  // заново без конца.
  const reloadRef = useRef(reload)
  reloadRef.current = reload
  const noticeRef = useRef(onNotice)
  noticeRef.current = onNotice

  const run = useCallback(async (manual: boolean, successText?: string) => {
    setRescanning(true)
    try {
      await refreshSnapshot()
      await reloadRef.current()
      if (manual && successText) noticeRef.current?.('info', successText)
    } catch (err) {
      // Автоматическое сканирование молчит: раздел покажет прошлый
      // снимок, и ругаться на это при каждом заходе незачем. Нажатую
      // кнопку — наоборот, надо объяснить.
      if (manual) noticeRef.current?.('error', err instanceof Error ? err.message : String(err))
    } finally {
      setRescanning(false)
    }
  }, [])

  useEffect(() => {
    if (!canScan) return
    const when = lastScanAt.get(hostScope.id) ?? 0
    if (Date.now() - when < FRESH_MS) return
    void run(false)
  }, [canScan, run])

  return { rescanning, rescan: (successText?: string) => run(true, successText) }
}
