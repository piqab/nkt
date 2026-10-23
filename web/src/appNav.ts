/**
 * Переход из любого места интерфейса (ссылки в чате ассистента): хост на
 * нужной странице или раздел хаба на нужной вкладке. App ловит событие
 * nkt-open и делает то же, что щелчок по меню.
 */
export interface OpenTarget {
  /** Хост — страница хоста (path: «/», «/containers?focus=имя» …). */
  host?: { id: number; name: string }
  path?: string
  /** Раздел хаба без хоста: hosts, deploy, profiles, clusters, fly … */
  view?: string
  /** Вкладка раздела (profiles: scripts, services; deploy: sites …). */
  tab?: string
}

export function openInApp(target: OpenTarget) {
  window.dispatchEvent(new CustomEvent<OpenTarget>('nkt-open', { detail: target }))
}
