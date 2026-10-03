/**
 * «После обновления хаба — обновить все хосты»: галочка в подтверждении
 * обновления хаба. Хаб перезапускается, страница перезагружается — намерение
 * переживает перезагрузку в этом браузере, и список хостов открывает окно
 * «Обновить всё», как только версия хаба действительно сменилась.
 */
const KEY = 'nkt-update-all-after-hub'
/** Сколько ждать новой версии: дольше — обновление не состоялось. */
const TTL_MS = 30 * 60_000

export function requestUpdateAllAfterHub(fromVersion: string) {
  try {
    localStorage.setItem(KEY, JSON.stringify({ from: fromVersion, at: Date.now() }))
  } catch {
    // нет хранилища — окно просто не откроется само
  }
}

export function cancelUpdateAllAfterHub() {
  try {
    localStorage.removeItem(KEY)
  } catch {
    // нечего убирать
  }
}

/** true — версия хаба сменилась после запроса: пора открыть «Обновить всё»
 * (запрос снимается). Протухший запрос снимается молча. */
export function takeUpdateAllAfterHub(hubVersion: string | undefined): boolean {
  let req: { from?: string; at?: number } | null = null
  try {
    req = JSON.parse(localStorage.getItem(KEY) ?? 'null')
  } catch {
    return false
  }
  if (!req || !hubVersion) return false
  if (hubVersion !== req.from) {
    cancelUpdateAllAfterHub()
    return true
  }
  if (!req.at || Date.now() - req.at > TTL_MS) cancelUpdateAllAfterHub()
  return false
}
