import type { CSSProperties } from 'react'
import { AutoComplete, Input } from 'antd'
import type { HubHost } from '../types'

/**
 * Поле ответа на вопрос запуска сценария — одно и то же в окне раздела
 * «Сценарии» и под сценарием из ответа ассистента: два места одного вопроса
 * разъезжаются первыми, а смысл у них общий.
 *
 * Вид поля говорит сценарий, а не окно: «param ИМЯ "…" secret», «param ИМЯ
 * "…" host», «set ИМЯ ask», «password ask», «token ask». До этого окно
 * догадывалось по единственному доступному признаку — «не секрет» — и
 * показывало список хостов в поле пароля и в поле «сколько копий», потому
 * что у несекретного ответа не было способа сказать, чего в нём ждут.
 *
 * secret — поле пароля: подставлять там нечего, а значение не должно
 * оседать ни в каком списке. host — автоподстановка хостов: вопрос вида
 * «целевой хост (IP или имя в хабе)» иначе набирается руками, хотя хаб все
 * имена и адреса знает, и одна опечатка стоит задания, которое спотыкается
 * на первом же шаге «on …». Свой текст остаётся набрать и там: сценарий
 * чаще заводит новый хост, чем выбирает существующий. Остальное — обычное
 * поле.
 */
export function AskField({
  prompt,
  secret,
  host,
  value,
  onChange,
  hosts,
  style,
}: {
  prompt: string
  secret: boolean
  host?: boolean
  value: string
  onChange: (v: string) => void
  hosts: HubHost[]
  style?: CSSProperties
}) {
  if (secret) {
    return (
      <Input.Password
        style={style}
        placeholder={prompt}
        value={value}
        autoComplete="new-password"
        onChange={(e) => onChange(e.target.value)}
      />
    )
  }
  if (!host) {
    return <Input style={style} placeholder={prompt} value={value} onChange={(e) => onChange(e.target.value)} />
  }
  return (
    <AutoComplete
      style={style}
      placeholder={prompt}
      value={value}
      options={hostOptions(hosts)}
      onChange={(v) => onChange(v ?? '')}
      filterOption={(input, option) => (option?.value ?? '').toLowerCase().includes(input.toLowerCase())}
      // Хостов на хабе бывает много: список без прокрутки упирался бы в
      // край окна и закрывал бы кнопку запуска.
      dropdownStyle={{ maxHeight: 320, overflow: 'auto' }}
    />
  )
}

/**
 * Как назвать вопрос в строке, где их перечисляют: подсказку из «param», а
 * если она пустая («param ИМЯ "" secret») — имя параметра из ключа, иначе
 * фраза «будет спрошено: , » ничего не говорит. Само поле при этом
 * остаётся таким, каким его написал автор, — без подписи; то же правило у
 * отказа хаба (Ask.Label на сервере).
 */
export function askLabel(a: { key: string; prompt: string }): string {
  if (a.prompt) return a.prompt
  const i = a.key.indexOf(':')
  return i >= 0 ? a.key.slice(i + 1) : a.key
}

/**
 * Имя и адрес хоста — две отдельные позиции: параметр сценария идёт то в
 * «on ИМЯ» (нужно имя), то в «host ИМЯ АДРЕС» (нужен адрес), и по одному
 * полю окна мы не угадываем, к какому из двух он подставится. В подписи
 * видно, что встанет в поле, если выбрать строку.
 */
function hostOptions(hosts: HubHost[]): { value: string; label: string }[] {
  const out: { value: string; label: string }[] = []
  const seen = new Set<string>()
  const add = (value: string, label: string) => {
    if (!value || seen.has(value)) return
    seen.add(value)
    out.push({ value, label })
  }
  for (const h of [...hosts].sort((a, b) => a.name.localeCompare(b.name))) {
    const group = h.group ? ` · ${h.group}` : ''
    add(h.name, `${h.name}${group}`)
    add(h.addr, `${h.addr} (${h.name}${group})`)
  }
  return out
}
