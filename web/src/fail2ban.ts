import i18n from './i18n'

/**
 * Помощники раздела fail2ban: правка ключей в тексте джейла (форма над
 * редактором меняет строки, не трогая остальное — комментарии, свои
 * ключи, отступы), адреса в свободном тексте оповещений, длительности.
 */

const header = (line: string) => {
  const t = line.trim()
  return t.startsWith('[') && t.endsWith(']') ? t.slice(1, -1).trim() : null
}

const keyOf = (line: string) => {
  if (/^\s/.test(line)) return null
  const t = line.trim()
  if (!t || t.startsWith('#') || t.startsWith(';')) return null
  const i = t.search(/[=:]/)
  return i > 0 ? t.slice(0, i).trim().toLowerCase() : null
}

/** Значение ключа в секции (строки продолжения склеены). */
export function getIniKey(text: string, section: string, key: string): string | undefined {
  let cur: string | null = null
  let value: string | undefined
  let inKey = false
  for (const line of text.split('\n')) {
    const h = header(line)
    if (h !== null) {
      cur = h
      inKey = false
      continue
    }
    if (cur !== section) continue
    if (inKey && /^\s+\S/.test(line)) {
      value = `${value ?? ''} ${line.trim()}`.trim()
      continue
    }
    inKey = false
    if (keyOf(line) === key.toLowerCase()) {
      const t = line.trim()
      value = t.slice(t.search(/[=:]/) + 1).trim()
      inKey = true
    }
  }
  return value
}

/**
 * Задать (value — строка) или убрать (null/'') ключ в секции. Секции нет —
 * добавляется в конец.
 */
export function setIniKey(text: string, section: string, key: string, value: string | null): string {
  const lines = text.replace(/\r\n/g, '\n').split('\n')
  if (lines.length && lines[lines.length - 1] === '') lines.pop()
  let start = -1
  let end = lines.length
  for (let i = 0; i < lines.length; i++) {
    const h = header(lines[i])
    if (h === null) continue
    if (start >= 0) {
      end = i
      break
    }
    if (h === section) start = i
  }
  const remove = value === null || value.trim() === ''
  if (start < 0) {
    if (remove) return lines.join('\n') + '\n'
    if (lines.length && lines[lines.length - 1].trim() !== '') lines.push('')
    lines.push(`[${section}]`, `${key} = ${value}`)
    return lines.join('\n') + '\n'
  }
  for (let i = start + 1; i < end; i++) {
    if (keyOf(lines[i]) !== key.toLowerCase()) continue
    let j = i + 1
    while (j < end && /^\s+\S/.test(lines[j])) j++
    if (remove) lines.splice(i, j - i)
    else lines.splice(i, j - i, `${key} = ${value}`)
    return lines.join('\n') + '\n'
  }
  if (remove) return lines.join('\n') + '\n'
  // После последней непустой строки секции.
  let at = end
  while (at > start + 1 && lines[at - 1].trim() === '') at--
  lines.splice(at, 0, `${key} = ${value}`)
  return lines.join('\n') + '\n'
}

function v4(ip: string): number[] | null {
  const m = ip.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/)
  if (!m) return null
  const parts = m.slice(1).map(Number)
  return parts.every((p) => p <= 255) ? parts : null
}

/** Входит ли адрес в список ignoreip (адреса и сети IPv4 CIDR). */
export function ignoreCovers(list: string[], ip: string): boolean {
  const a = v4(ip)
  for (const item of list) {
    if (item === ip) return true
    if (!a) continue
    const [net, bitsRaw] = item.split('/')
    const n = v4(net)
    if (!n || bitsRaw === undefined) continue
    const bits = Number(bitsRaw)
    const toInt = (p: number[]) => ((p[0] << 24) | (p[1] << 16) | (p[2] << 8) | p[3]) >>> 0
    const mask = bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0
    if ((toInt(a) & mask) === (toInt(n) & mask)) return true
  }
  return false
}

/** Внешний (публичный) адрес — не частный, не loopback, не link-local. */
export function isExternalIP(ip: string): boolean {
  const a = v4(ip)
  if (a) {
    const [x, y] = a
    if (x === 10 || x === 127 || x === 0 || x >= 224) return false
    if (x === 172 && y >= 16 && y <= 31) return false
    if (x === 192 && y === 168) return false
    if (x === 169 && y === 254) return false
    if (x === 100 && y >= 64 && y <= 127) return false
    return true
  }
  const l = ip.toLowerCase()
  if (!l.includes(':')) return false
  if (l === '::1' || l === '::' || l.startsWith('fe8') || l.startsWith('fe9') || l.startsWith('fea') || l.startsWith('feb')) return false
  if (l.startsWith('fc') || l.startsWith('fd')) return false
  return true
}

const ipv4Re = /\b(?:\d{1,3}\.){3}\d{1,3}\b/g
const ipv6Re = /(?<![\w:])[0-9a-fA-F]{0,4}(?::[0-9a-fA-F]{0,4}){2,7}(?![\w:])/g

/** Адреса в свободном тексте (оповещение), без повторов, по порядку. */
export function ipsInText(text: string): string[] {
  const out: string[] = []
  const push = (s: string) => {
    if (!out.includes(s)) out.push(s)
  }
  for (const m of text.matchAll(ipv4Re)) if (v4(m[0])) push(m[0])
  for (const m of text.matchAll(ipv6Re)) {
    const s = m[0]
    // Время «10:00:00» — не адрес: у IPv6 есть «::» или 8 групп.
    if (s.includes('::') || s.split(':').length === 8) push(s)
  }
  return out
}

/** Длительность в секундах коротко: «10 мин», «1 ч», «7 дн». */
export function fmtDuration(sec: number): string {
  const t = i18n.t.bind(i18n)
  if (sec < 0) return t('fail2ban.durForever')
  if (sec % 86400 === 0 && sec >= 86400) return t('fail2ban.durDays', { n: sec / 86400 })
  if (sec % 3600 === 0 && sec >= 3600) return t('fail2ban.durHours', { n: sec / 3600 })
  if (sec % 60 === 0 && sec >= 60) return t('fail2ban.durMinutes', { n: sec / 60 })
  return t('fail2ban.durSeconds', { n: sec })
}

/** Сроки ручного бана в окне. */
export const BAN_TIMES = [3600, 86400, 7 * 86400, 30 * 86400, 365 * 86400]
