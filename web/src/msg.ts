import i18n from './i18n'

/**
 * Сообщение для показа позже (баннер, уведомление): либо готовый текст
 * (ошибка сервера — она уже на языке запроса), либо ключ каталога с
 * параметрами. Ключ переводится в момент показа, поэтому сообщение
 * сразу меняет язык вместе с интерфейсом, а не остаётся на том, на
 * котором появилось.
 */
export type Msg = string | { k: string; a?: Record<string, unknown>; s?: string }

/** Сообщение ключом; параметры-сообщения тоже переводятся при показе.
 * suffix — непереводимый хвост (сырой ответ сервера и т. п.). */
export function tx(k: string, a?: Record<string, unknown>, suffix?: string): Msg {
  return { k, a, s: suffix }
}

/** Текст сообщения на текущем языке. */
export function msg(m: Msg | null | undefined): string {
  if (!m) return ''
  if (typeof m === 'string') return m
  const args: Record<string, unknown> = {}
  for (const [key, v] of Object.entries(m.a ?? {})) {
    args[key] = v && typeof v === 'object' && 'k' in (v as object) ? msg(v as Msg) : v
  }
  return String(i18n.t(m.k, args)) + (m.s ?? '')
}
