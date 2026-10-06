import { useEffect, useState } from 'react'
import { Button } from 'antd'
import { useTranslation } from 'react-i18next'

/** Имя собранного скрипта интерфейса: /assets/index-<хэш>.js. */
const BUNDLE_RE = /\/assets\/index-[\w-]+\.js/

function loadedBundle(): string | null {
  for (const s of Array.from(document.querySelectorAll('script[src]'))) {
    const m = (s.getAttribute('src') ?? '').match(BUNDLE_RE)
    if (m) return m[0]
  }
  return null
}

/** Как часто сверяться с сервером (и ещё — при возврате на вкладку). */
const CHECK_MS = 2 * 60_000

/**
 * «Интерфейс обновился — обновите страницу». Вкладка, открытая до
 * обновления nkt, продолжает работать на старом коде интерфейса, пока её
 * не перезагрузить, — и ведёт себя по-старому (так загрузка с компьютера
 * шла без задания уже после обновления хаба). Сверяется имя собранного
 * скрипта в открытой странице с тем, что сервер отдаёт сейчас.
 */
export function StaleUIBanner() {
  const { t } = useTranslation()
  const [stale, setStale] = useState(false)

  useEffect(() => {
    const mine = loadedBundle()
    // Разработка (vite dev) — собранного скрипта нет, сверять нечего.
    if (!mine) return
    let cancelled = false
    const check = async () => {
      if (document.visibilityState !== 'visible') return
      try {
        const res = await fetch('/', { cache: 'no-store', credentials: 'same-origin' })
        const m = (await res.text()).match(BUNDLE_RE)
        if (!cancelled && m && m[0] !== mine) setStale(true)
      } catch {
        // Сервер перезапускается — сверимся в следующий раз.
      }
    }
    const timer = setInterval(() => void check(), CHECK_MS)
    const onVisible = () => void check()
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      cancelled = true
      clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [])

  if (!stale) return null
  return (
    <div className="stale-ui-banner" role="status">
      <span>{t('staleUI.text')}</span>
      <Button size="small" type="primary" onClick={() => window.location.reload()}>
        {t('staleUI.reload')}
      </Button>
    </div>
  )
}
