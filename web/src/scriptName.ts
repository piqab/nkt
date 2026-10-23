/** Имя черновика из ответа модели — латиницей, без пробелов: ai-ДАТА-ХОСТ
 * (ассистент) или fix-ХОСТ-ДАТА (разбор находки); хост — из первой строки
 * «on ХОСТ …»; занятое имя — с суффиксом -2, -3… */
export function autoScriptName(kind: 'assistant' | 'finding', content: string, taken: Set<string>, now: Date = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}`
  const host = (content.match(/^\s*on\s+([A-Za-z0-9._-]+)/m)?.[1] ?? '').toLowerCase()
  const base = kind === 'finding' ? `fix-${host || 'host'}-${stamp}` : `ai-${stamp}${host ? `-${host}` : ''}`
  let name = base
  for (let n = 2; taken.has(name); n++) name = `${base}-${n}`
  return name
}
