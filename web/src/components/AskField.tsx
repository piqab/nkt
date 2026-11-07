import type { CSSProperties } from 'react'
import { AutoComplete, Input } from 'antd'
import type { HubHost } from '../types'

/**
 * Поле ответа на вопрос запуска сценария — одно и то же в окне раздела
 * «Сценарии» и под сценарием из ответа ассистента: два места одного вопроса
 * разъезжаются первыми, а смысл у них общий.
 *
 * Секрет (пароль SSH хоста, токен git, «set … ask») — поле пароля, как было:
 * подставлять там нечего, а значение не должно оседать ни в каком списке.
 *
 * Обычный ответ — автоподстановка хостов хаба. Вопрос вида «целевой хост (IP
 * или имя в хабе)» человек набирает руками, хотя хаб все имена и адреса уже
 * знает, и опечатка в имени стоит задания, которое спотыкается на первом же
 * шаге «on …». Свой текст при этом остаётся набрать: сценарий чаще заводит
 * новый хост, чем выбирает существующий, — запрещать список значило бы
 * сломать половину того, для чего он нужен.
 */
export function AskField({
  prompt,
  secret,
  value,
  onChange,
  hosts,
  style,
}: {
  prompt: string
  secret: boolean
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
