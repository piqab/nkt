// Всплывающие уведомления браузера для списка хостов.
//
// Текст берётся из журнала оповещений хаба (internal/store/events.go), а
// не собирается здесь заново: раньше вкладка сама сравнивала два опроса и
// писала «имя: новые проблемы» — без адреса, без подробностей и без следа
// в истории. Теперь переходы находит сам хаб в фоновом опросе, а вкладка
// только показывает то, чего оператор ещё не видел; всплывающее и журнал
// поэтому не могут разойтись, и закрытая вкладка больше не значит
// «событие потеряно» — оно останется в разделе «Оповещения».
//
// Доставка по-прежнему привязана к открытой вкладке: настоящий push при
// закрытом браузере потребовал бы service worker и Push API, а это
// отдельная история.

import type { HostEvent } from './types'
import i18n from './i18n'

const STORAGE_KEY = 'nkt-hub-notify'
/** Докуда события уже показаны этим браузером. */
const SEEN_KEY = 'nkt-hub-notify-seen'

export function notificationsEnabled(): boolean {
  return localStorage.getItem(STORAGE_KEY) === '1'
}

export function setNotificationsEnabled(on: boolean): void {
  localStorage.setItem(STORAGE_KEY, on ? '1' : '0')
}

/** Requests browser permission — must be called from a user gesture (a
 * click handler), never on mount or on a timer; browsers ignore or reject
 * a request that isn't triggered by one. */
export async function requestNotificationPermission(): Promise<boolean> {
  if (!('Notification' in window)) return false
  if (Notification.permission === 'granted') return true
  if (Notification.permission === 'denied') return false
  const result = await Notification.requestPermission()
  return result === 'granted'
}

function seenEventID(): number {
  const raw = Number(localStorage.getItem(SEEN_KEY))
  return Number.isFinite(raw) ? raw : 0
}

/**
 * Показывает уведомления о событиях, которых этот браузер ещё не видел.
 *
 * Первый заход ничего не показывает, а только запоминает границу: иначе
 * открытие списка хостов после выходных высыпало бы десяток уведомлений
 * обо всём, что и так видно в журнале.
 */
export function notifyNewEvents(events: HostEvent[]): void {
  if (events.length === 0) return
  const newest = events[0].id
  const seen = seenEventID()
  localStorage.setItem(SEEN_KEY, String(newest))
  if (seen === 0) return
  if (!notificationsEnabled()) return
  if (!('Notification' in window) || Notification.permission !== 'granted') return

  // От старых к новым: порядок уведомлений должен совпадать с порядком
  // событий, а журнал отдаёт новые первыми.
  for (const e of [...events].reverse()) {
    if (e.id <= seen) continue
    if (e.kind === 'recovered' || e.kind === 'resolved') continue
    notify(
      `${e.host_name} · ${e.host_addr}`,
      `${i18n.t(`events.kind.${e.kind}`, { defaultValue: e.kind })}${e.detail ? `: ${e.detail}` : ''}`,
      `nkt-event-${e.id}`,
    )
  }
}

function notify(title: string, body: string, tag: string): void {
  try {
    // eslint-disable-next-line no-new -- fire-and-forget by design, nothing to await
    new Notification(title, { body, tag })
  } catch {
    // The Notification constructor can throw on some platforms (e.g. iOS
    // Safari, which only allows service-worker-based notifications) — this
    // is a best-effort enhancement, never something to surface as an app
    // error.
  }
}
