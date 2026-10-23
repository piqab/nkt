import { useEffect, useMemo, useRef, useState } from 'react'
import { Input } from 'antd'
import type { TextAreaRef } from 'antd/es/input/TextArea'
import { useTranslation } from 'react-i18next'
import { api } from '../../api'
import type { ChatObject } from './ChatObjects'

/**
 * Поле вопроса ассистенту с подсказками объектов хаба. Слово «хост»
 * (host), «контейнер», «служба», «конвейер», «сценарий», «кластер»,
 * «маршрут» и пробел за ним открывают список своих объектов; «@» — общий
 * список. Контейнеры и службы — того хоста, что назван в вопросе
 * последним (их списки — у хоста, читаются при первой подсказке). У
 * хостов первым пунктом — «все хосты». Стрелки, Enter/Tab — выбрать,
 * Esc — закрыть; Ctrl+Enter — отправить.
 */

type Kind = ChatObject['kind']

const TRIGGERS: { re: RegExp; kind: Kind }[] = [
  { re: /^(хост|host)/i, kind: 'host' },
  { re: /^(контейнер|container)/i, kind: 'container' },
  { re: /^(служб|сервис|service)/i, kind: 'service' },
  { re: /^(конвейер|выкладк|pipeline)/i, kind: 'pipeline' },
  { re: /^(сценари|script)/i, kind: 'script' },
  { re: /^(кластер|cluster)/i, kind: 'cluster' },
  { re: /^(маршрут|route)/i, kind: 'route' },
  { re: /^(групп|group)/i, kind: 'group' },
]

interface Suggest {
  kind: Kind | 'all'
  partial: string
  from: number
}

/** Что подсказать у курсора: «@частичное» или «слово-тип частичное». */
function suggestAt(text: string, caret: number): Suggest | null {
  const before = text.slice(0, caret)
  const at = before.match(/(?:^|\s)@([\w.-]*)$/)
  if (at) return { kind: 'all', partial: at[1], from: caret - at[1].length - 1 }
  const m = before.match(/(?:^|\s)([A-Za-zА-Яа-яЁё]+)\s+([\w.-]*)$/)
  if (!m) return null
  const trig = TRIGGERS.find((x) => x.re.test(m[1]))
  return trig ? { kind: trig.kind, partial: m[2], from: caret - m[2].length } : null
}

const hostPath = (id: number) => (id === -1 ? '/hosts/local' : `/hosts/${id}`)

export function Composer({
  value,
  onChange,
  onSend,
  objects,
  disabled,
  placeholder,
}: {
  value: string
  onChange: (v: string) => void
  onSend: () => void
  objects: ChatObject[]
  disabled: boolean
  placeholder: string
}) {
  const { t } = useTranslation()
  const ref = useRef<TextAreaRef>(null)
  const [caret, setCaret] = useState(0)
  const [active, setActive] = useState(0)
  const [closed, setClosed] = useState(false)
  // Контейнеры и службы хостов — по запросу, один раз на хост.
  const [onHost, setOnHost] = useState<Record<string, ChatObject[]>>({})

  const sug = closed ? null : suggestAt(value, caret)
  const hosts = useMemo(() => objects.filter((o) => o.kind === 'host'), [objects])
  // Хост — последний названный до курсора.
  const contextHost = useMemo(() => {
    const before = value.slice(0, caret)
    let best: ChatObject | null = null
    let pos = -1
    for (const h of hosts) {
      const i = before.lastIndexOf(h.name)
      if (i > pos) {
        pos = i
        best = h
      }
    }
    return best
  }, [value, caret, hosts])

  const needKind = sug && (sug.kind === 'container' || sug.kind === 'service') ? sug.kind : null
  const cacheKey = contextHost && needKind ? `${needKind}:${contextHost.id}` : ''
  useEffect(() => {
    if (!cacheKey || onHost[cacheKey] || !contextHost || !needKind) return
    const h = contextHost
    const path = `${hostPath(h.id ?? 0)}/${needKind === 'container' ? 'containers' : 'services'}`
    void api<{ containers?: { name: string; state?: string }[]; services?: { name: string; active_state?: string }[] }>(path)
      .then((res) => {
        const list = (needKind === 'container' ? res.containers : res.services) ?? []
        setOnHost((prev) => ({
          ...prev,
          [cacheKey]: list.map((x) => ({ kind: needKind, name: x.name.replace(/^\//, ''), host_id: h.id, host: h.name, status: 'state' in x ? x.state : 'active_state' in x ? x.active_state : undefined })),
        }))
      })
      .catch(() => setOnHost((prev) => ({ ...prev, [cacheKey]: [] })))
  }, [cacheKey, contextHost, needKind, onHost])

  const options: { label: string; insert: string; hint?: string }[] = useMemo(() => {
    if (!sug) return []
    const p = sug.partial.toLowerCase()
    const match = (o: ChatObject) => o.name.toLowerCase().includes(p)
    let list: ChatObject[]
    if (sug.kind === 'all') list = objects
    else if (sug.kind === 'container' || sug.kind === 'service') list = cacheKey ? onHost[cacheKey] ?? [] : []
    else list = objects.filter((o) => o.kind === sug.kind)
    const out = list
      .filter(match)
      .slice(0, 50)
      .map((o) => ({ label: o.name, insert: o.name, hint: [sug.kind === 'all' ? t(`assistant.obj.kind.${o.kind}`) : '', o.host && sug.kind === 'all' ? o.host : '', o.status ?? ''].filter(Boolean).join(' · ') }))
    if (sug.kind === 'host' && p === '' && hosts.length > 1) out.unshift({ label: t('assistant.composer.allHosts'), insert: hosts.map((h) => h.name).join(', '), hint: String(hosts.length) })
    return out
  }, [sug, objects, onHost, cacheKey, hosts, t])

  const waitingHost = sug && (sug.kind === 'container' || sug.kind === 'service') && !contextHost
  const loading = !!cacheKey && !onHost[cacheKey]
  const open = !!sug && !disabled && (options.length > 0 || waitingHost || loading)

  function choose(i: number) {
    const o = options[i]
    if (!o || !sug) return
    const next = value.slice(0, sug.from) + o.insert + ' ' + value.slice(caret)
    onChange(next)
    const pos = sug.from + o.insert.length + 1
    setCaret(pos)
    setActive(0)
    requestAnimationFrame(() => {
      const el = ref.current?.resizableTextArea?.textArea
      el?.focus()
      el?.setSelectionRange(pos, pos)
    })
  }

  return (
    <div className="chat-composer">
      {open && (
        <div className="chat-suggest" role="listbox">
          {waitingHost ? (
            <div className="small muted chat-suggest-note">{t('assistant.composer.hostFirst')}</div>
          ) : loading ? (
            <div className="small muted chat-suggest-note">{t('assistant.composer.loading', { host: contextHost?.name })}</div>
          ) : (
            options.map((o, i) => (
              <div
                key={o.label + i}
                role="option"
                aria-selected={i === active}
                className={`chat-suggest-item${i === active ? ' is-active' : ''}`}
                onMouseDown={(e) => {
                  e.preventDefault()
                  choose(i)
                }}
              >
                <span className="mono">{o.label}</span>
                {o.hint && <span className="small muted">{o.hint}</span>}
              </div>
            ))
          )}
        </div>
      )}
      <Input.TextArea
        ref={ref}
        autoSize={{ minRows: 2, maxRows: 8 }}
        value={value}
        disabled={disabled}
        placeholder={placeholder}
        onChange={(e) => {
          onChange(e.target.value)
          setCaret(e.target.selectionStart ?? e.target.value.length)
          setClosed(false)
          setActive(0)
        }}
        onSelect={(e) => setCaret((e.target as HTMLTextAreaElement).selectionStart ?? 0)}
        onKeyDown={(e) => {
          if (open && options.length > 0) {
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
              e.preventDefault()
              setActive((a) => (a + (e.key === 'ArrowDown' ? 1 : options.length - 1)) % options.length)
              return
            }
            if ((e.key === 'Enter' && !e.ctrlKey && !e.metaKey) || e.key === 'Tab') {
              e.preventDefault()
              choose(active)
              return
            }
          }
          if (open && e.key === 'Escape') {
            e.preventDefault()
            setClosed(true)
            return
          }
          if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
            e.preventDefault()
            onSend()
          }
        }}
      />
    </div>
  )
}
