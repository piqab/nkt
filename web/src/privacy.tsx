import { useEffect, useSyncExternalStore, type ReactNode } from 'react'

/**
 * Приватный режим — для показа экрана и скриншотов: чувствительное
 * (адреса, имена хостов, пользователи, ключи, домены, почта) размывается
 * CSS-фильтром и не выделяется. Состояние живёт в этом браузере
 * (localStorage) и висит классом `privacy` на корневом элементе — так
 * его видят и модалки, и откреплённые окна терминала/логов (те читают его
 * при открытии).
 *
 * Два способа пометить: <Sensitive> вокруг заведомо чувствительного
 * значения и blurText() для свободного текста (журналы, оповещения) — там
 * размываются IPv4/IPv6, MAC, e-mail и имена хостов из списка хаба.
 */

const KEY = 'nkt-privacy'

export function readPrivacy(): boolean {
  try {
    return localStorage.getItem(KEY) === '1'
  } catch {
    return false
  }
}

export function applyPrivacy(on: boolean) {
  document.documentElement.classList.toggle('privacy', on)
  try {
    localStorage.setItem(KEY, on ? '1' : '0')
  } catch {
    // без localStorage режим просто не переживёт перезагрузку
  }
}

// Одно состояние на все компоненты (галочка в подвале сайдбара и метка
// в его шапке): подписчики через useSyncExternalStore.
let current = readPrivacy()
const listeners = new Set<() => void>()
function subscribe(fn: () => void) {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}
function setPrivacy(on: boolean) {
  current = on
  applyPrivacy(on)
  for (const fn of listeners) fn()
}

export function usePrivacy(): [boolean, (on: boolean) => void] {
  const on = useSyncExternalStore(subscribe, () => current)
  useEffect(() => {
    applyPrivacy(current)
  }, [])
  return [on, setPrivacy]
}

/** Значение, которое в приватном режиме размывается. */
export function Sensitive({ children, block }: { children: ReactNode; block?: boolean }) {
  return <span className={block ? 'sensitive sensitive-block' : 'sensitive'}>{children}</span>
}

// Известные имена — хосты и машины хаба (страница списка), домены и
// lineage сертификатов (страница сертификатов): свободный текст и пути
// размывают и их. Каждый источник кладёт свой список, длинные имена
// проверяются первыми («api.example.com» раньше «example.com»).
const knownBySource = new Map<string, string[]>()
export function setKnownNames(names: string[], source = 'hosts') {
  knownBySource.set(
    source,
    names.filter((n) => n.length >= 3),
  )
}
function knownNames(): string[] {
  const all = new Set<string>()
  for (const list of knownBySource.values()) for (const n of list) all.add(n)
  return [...all].sort((a, b) => b.length - a.length)
}

// Доменные имена: метка.метка.TLD с TLD из списка ходовых — общий
// «что-то.что-то» задел бы имена файлов (config.toml, nkt.env), а
// пропущенный редкий TLD лучше, чем размытый каждый второй файл. Пути
// вроде /etc/letsencrypt/live/example.com/fullchain.pem попадают тоже.
const TLDS =
  'com|net|org|io|ru|su|рф|dev|app|info|biz|edu|gov|mil|me|co|uk|de|fr|eu|nl|pl|ua|kz|by|us|ca|au|jp|cn|br|es|se|fi|dk|ch|cz|xyz|cloud|online|site|tech|store|shop|pro|name|mobi|tv|cc|ws|top|club|host|network|systems|digital|team|space|link|live|life|world|today|news|email|zone|domains|center|expert|guru|agency|company|solutions|services|studio|design|media|group|global|one|ai|ly|local|lan|internal|home|intranet|corp|test|example|localhost'

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

function sensitivePattern(): RegExp {
  const parts = [
    String.raw`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?(?:\/\d{1,2})?\b`, // IPv4[:port][/mask]
    String.raw`\b[0-9a-f]{2}(?::[0-9a-f]{2}){5}\b`, // MAC
    String.raw`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`, // e-mail
    String.raw`\b(?:[0-9a-f]{1,4}:){2,7}[0-9a-f]{1,4}\b`, // IPv6 (без ::-сокращений)
    // Домен; после TLD — не продолжение метки и не «.ещё» (config.toml),
    // кроме расширений файлов сертификатов (app.example.com.pem).
    String.raw`\b(?:[a-zа-я0-9](?:[a-zа-я0-9-]{0,61}[a-zа-я0-9])?\.)+(?:` + TLDS + String.raw`)\b(?!-?[a-zа-я0-9])(?!\.(?!(?:pem|crt|key|cer|csr|conf|cfg|log|lock)\b)[a-zа-я0-9])`,
  ]
  const names = knownNames()
  if (names.length) parts.unshift(String.raw`\b(?:` + names.map(escapeRe).join('|') + String.raw`)\b`)
  return new RegExp(parts.join('|'), 'giu')
}

/** Свободный текст с размытыми чувствительными фрагментами; не строка
 * (готовый узел) — возвращается как есть. */
export function blurText(text: string): ReactNode
export function blurText(text: ReactNode): ReactNode
export function blurText(text: ReactNode): ReactNode {
  if (typeof text !== 'string') return text
  const re = sensitivePattern()
  const out: ReactNode[] = []
  let last = 0
  let m: RegExpExecArray | null
  let i = 0
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index))
    out.push(
      <span key={i++} className="sensitive">
        {m[0]}
      </span>,
    )
    last = m.index + m[0].length
    if (m[0].length === 0) re.lastIndex++
  }
  if (out.length === 0) return text
  if (last < text.length) out.push(text.slice(last))
  return <>{out}</>
}
