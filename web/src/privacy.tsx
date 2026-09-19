import { useEffect, useState, type ReactNode } from 'react'

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

export function usePrivacy(): [boolean, (on: boolean) => void] {
  const [on, setOn] = useState(readPrivacy)
  useEffect(() => {
    applyPrivacy(on)
  }, [on])
  return [on, setOn]
}

/** Значение, которое в приватном режиме размывается. */
export function Sensitive({ children, block }: { children: ReactNode; block?: boolean }) {
  return <span className={block ? 'sensitive sensitive-block' : 'sensitive'}>{children}</span>
}

// Имена хостов и машин хаба — подставляются страницей списка, чтобы
// свободный текст размывал и их.
let knownNames: string[] = []
export function setKnownNames(names: string[]) {
  knownNames = names.filter((n) => n.length >= 3)
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

function sensitivePattern(): RegExp {
  const parts = [
    String.raw`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?(?:\/\d{1,2})?\b`, // IPv4[:port][/mask]
    String.raw`\b[0-9a-f]{2}(?::[0-9a-f]{2}){5}\b`, // MAC
    String.raw`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`, // e-mail
    String.raw`\b(?:[0-9a-f]{1,4}:){2,7}[0-9a-f]{1,4}\b`, // IPv6 (без ::-сокращений)
  ]
  if (knownNames.length) parts.push(String.raw`\b(?:` + knownNames.map(escapeRe).join('|') + String.raw`)\b`)
  return new RegExp(parts.join('|'), 'gi')
}

/** Свободный текст с размытыми чувствительными фрагментами. */
export function blurText(text: string): ReactNode {
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
