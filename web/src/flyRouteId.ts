/**
 * Идентификатор маршрута Traffic Fly: a-z, 0-9 и «-», первая — буква или
 * цифра, до 41 знака (как проверяет хаб). Новый маршрут получает его сам —
 * из первого имени («git.example.com» → «git-example-com»), иначе из
 * подписи (кириллица — транслитом), иначе из вида и порта; занятый —
 * с суффиксом «-2», «-3».
 */

const RU: Record<string, string> = {
  а: 'a', б: 'b', в: 'v', г: 'g', д: 'd', е: 'e', ё: 'e', ж: 'zh', з: 'z', и: 'i', й: 'y', к: 'k', л: 'l', м: 'm', н: 'n', о: 'o', п: 'p',
  р: 'r', с: 's', т: 't', у: 'u', ф: 'f', х: 'h', ц: 'c', ч: 'ch', ш: 'sh', щ: 'sch', ъ: '', ы: 'y', ь: '', э: 'e', ю: 'yu', я: 'ya',
}

export const ROUTE_ID_RE = /^[a-z0-9][a-z0-9-]{0,40}$/
const MAX = 41

/** Ввод на лету: строчные, транслит, пробел _ . / → «-», прочее —
 * отбрасывается, дефисы не подряд и не в начале (в конце — можно, пока
 * человек печатает). */
export function sanitizeRouteId(s: string): string {
  return [...s.toLowerCase()]
    .map((c) => RU[c] ?? c)
    .join('')
    .replace(/[\s_./:]+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/-+/g, '-')
    .replace(/^-+/, '')
    .slice(0, MAX)
}

/** Готовый идентификатор из текста (без дефиса в конце). */
export function slugRouteId(s: string): string {
  return sanitizeRouteId(s).replace(/-+$/, '')
}

/** Идентификатор для нового маршрута, не занятый в taken. */
export function suggestRouteId(names: string[] | undefined, label: string | undefined, kind: string, port: number, taken: Set<string>): string {
  const base = slugRouteId(names?.[0] ?? '') || slugRouteId(label ?? '') || slugRouteId(port > 0 ? `${kind}-${port}` : kind) || 'route'
  if (!taken.has(base)) return base
  for (let n = 2; ; n++) {
    const suffix = `-${n}`
    const id = base.slice(0, MAX - suffix.length).replace(/-+$/, '') + suffix
    if (!taken.has(id)) return id
  }
}
