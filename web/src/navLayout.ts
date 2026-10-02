/** Раскладка бокового меню, общая для всех на хабе: порядок разделов и
 * скрытые (только у меню хоста). Хранится на хабе (/hub/ui/nav/{hub|host}).
 * Скрытие убирает раздел только из меню — по адресу он открывается. */
export interface NavLayout {
  order: string[]
  hidden: string[]
}

/** Порядок по раскладке: сначала разделы в её порядке, затем те, которых
 * в ней нет (новые), — в порядке по умолчанию; ключи, которых нет в
 * интерфейсе, пропускаются. withHidden=false убирает скрытые. */
export function applyNavLayout<T>(items: T[], keyOf: (item: T) => string, layout?: NavLayout | null, withHidden = false): T[] {
  if (!layout) return items
  const byKey = new Map(items.map((i) => [keyOf(i), i]))
  const out: T[] = []
  for (const k of layout.order) {
    const item = byKey.get(k)
    if (item) {
      out.push(item)
      byKey.delete(k)
    }
  }
  for (const i of items) if (byKey.has(keyOf(i))) out.push(i)
  return withHidden ? out : out.filter((i) => !layout.hidden.includes(keyOf(i)))
}
