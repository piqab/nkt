import { useState, type ReactNode } from 'react'
import { Button, Input } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, CodeEditor, Loading, Modal } from './ui'
import { EditTextModal } from './EditTextModal'
import { VersionHistory } from './VersionHistory'

type LXDConfig = { content: string; sha256: string; expanded?: string }

/** Конфигурация инстанса LXD (lxc config show / edit): текст, быстрые
 * поля лимитов, история версий; запись — через окно диффа. */
export default function LXDConfigModal({
  name,
  me,
  canControl,
  onClose,
  onSaved,
  edit,
  intro,
}: {
  name: string
  me: Me
  canControl: boolean
  /** Заготовка черновика из сохранённого текста (добавить/убрать порт) —
   * запись всё равно через дифф. */
  edit?: (saved: string) => string
  /** Пояснение над редактором вместо общего. */
  intro?: ReactNode
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const url = `/lxd/instances/${encodeURIComponent(name)}/config`
  const cfg = useApi<{ config: LXDConfig; history_path: string }>(url)
  const [draft, setDraft] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [view, setView] = useState<'history' | 'expanded' | null>(null)

  if (!cfg.data) {
    return (
      <Modal title={t('lxdConfig.title', { name })} onClose={onClose}>
        {cfg.error ? <Banner kind="error">{cfg.error}</Banner> : <Loading what={t('lxdConfig.loading')} />}
      </Modal>
    )
  }
  const saved = cfg.data.config.content
  const text = draft ?? (edit ? edit(saved) : saved)

  async function save(): Promise<boolean> {
    setBusy(true)
    setError(null)
    try {
      await api(url, { method: 'PUT', body: { content: text, note: note || t('lxdConfig.editNote'), expected_sha256: cfg.data?.config.sha256 ?? '' } })
      onSaved()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return false
    } finally {
      setBusy(false)
    }
  }

  const limit = (key: string) => yamlConfigValue(text, key)
  const setLimit = (key: string, v: string) => setDraft(setYamlConfigValue(text, key, v.trim()))

  return (
    <>
      <EditTextModal
        title={t('lxdConfig.title', { name })}
        saved={saved}
        draft={text}
        onDraft={setDraft}
        busy={busy}
        onSave={canControl ? save : async () => false}
        onHistory={() => setView('history')}
        onClose={onClose}
        fields={
          <>
            {intro ?? <p className="small muted">{t('lxdConfig.hint')}</p>}
            {edit && draft === null && edit(saved) === saved && <Banner kind="warn">{t('lxdConfig.editNoop')}</Banner>}
            <div className="row" style={{ gap: '0.75rem', alignItems: 'center', flexWrap: 'wrap', marginBottom: '0.5rem' }}>
              <span className="small">limits.cpu</span>
              <Input size="small" style={{ width: '7rem' }} value={limit('limits.cpu')} placeholder="2" onChange={(e) => setLimit('limits.cpu', e.target.value)} />
              <span className="small">limits.memory</span>
              <Input size="small" style={{ width: '8rem' }} value={limit('limits.memory')} placeholder="2GiB" onChange={(e) => setLimit('limits.memory', e.target.value)} />
              <Input size="small" style={{ width: '18rem' }} value={note} placeholder={t('lxdConfig.notePlaceholder')} onChange={(e) => setNote(e.target.value)} />
              {cfg.data.config.expanded && (
                <Button size="small" onClick={() => setView('expanded')}>
                  {t('lxdConfig.expanded')}
                </Button>
              )}
            </div>
          </>
        }
        below={
          error ? (
            <Banner kind="error" onClose={() => setError(null)}>
              {error}
            </Banner>
          ) : !canControl ? (
            <Banner kind="info">{t('common.mutationsDisabled')}</Banner>
          ) : null
        }
      />
      {view === 'history' && (
        <Modal title={t('configs.versionHistoryTitle')} onClose={() => setView(null)} width={960}>
          <VersionHistory
            path={cfg.data.history_path}
            me={me}
            apply
            onChanged={() => {
              setDraft(null)
              void cfg.reload()
              onSaved()
            }}
          />
        </Modal>
      )}
      {view === 'expanded' && (
        <Modal title={t('lxdConfig.expandedTitle', { name })} onClose={() => setView(null)} width={900}>
          <p className="small muted">{t('lxdConfig.expandedHint')}</p>
          <CodeEditor value={cfg.data.config.expanded ?? ''} readOnly rows={24} />
        </Modal>
      )}
    </>
  )
}

const keyRe = (key: string) => new RegExp(`^  ${key.replace(/\./g, '\\.')}:[ \\t]*(.*)$`, 'm')

/** Значение ключа из блока config: (простой YAML lxc config show). */
export function yamlConfigValue(text: string, key: string): string {
  const m = keyRe(key).exec(configBlock(text).body)
  if (!m) return ''
  return m[1].trim().replace(/^"(.*)"$/, '$1').replace(/^'(.*)'$/, '$1')
}

/** Ставит (или убирает при пустом значении) ключ в блоке config:. */
export function setYamlConfigValue(text: string, key: string, value: string): string {
  const { start, end, body } = configBlock(text)
  if (start < 0) return text
  const line = `  ${key}: "${value.replace(/"/g, '\\"')}"`
  const lines = body.split('\n').filter((l) => l !== '')
  const i = lines.findIndex((l) => keyRe(key).test(l))
  if (i >= 0) {
    if (value === '') lines.splice(i, 1)
    else lines[i] = line
  } else if (value === '') {
    return text
  } else {
    // Ключи lxc выводит по алфавиту — вставка на своё место (среди
    // ключей первого уровня, не внутри многострочных значений).
    const j = lines.findIndex((l) => /^  \S/.test(l) && l.trim().split(':')[0] > key)
    lines.splice(j < 0 ? lines.length : j, 0, line)
  }
  const head = lines.length === 0 ? 'config: {}\n' : 'config:\n'
  return text.slice(0, start) + head + lines.map((l) => l + '\n').join('') + text.slice(end)
}

/** Блок config: — от заголовка до первой строки без отступа. */
function configBlock(text: string): { start: number; end: number; body: string } {
  const m = /^config:[ \t]*(\{\})?[ \t]*\n/m.exec(text)
  if (!m) return { start: -1, end: -1, body: '' }
  const bodyStart = m.index + m[0].length
  if (m[1]) return { start: m.index, end: bodyStart, body: '' }
  let end = bodyStart
  while (end < text.length) {
    const nl = text.indexOf('\n', end)
    const lineEnd = nl < 0 ? text.length : nl + 1
    if (!text.startsWith('  ', end)) break
    end = lineEnd
  }
  return { start: m.index, end, body: text.slice(bodyStart, end) }
}

/** Блок devices: — как configBlock, для устройств. */
function devicesBlock(text: string): { start: number; end: number; body: string } {
  const m = /^devices:[ \t]*(\{\})?[ \t]*\n/m.exec(text)
  if (!m) return { start: -1, end: -1, body: '' }
  const bodyStart = m.index + m[0].length
  if (m[1]) return { start: m.index, end: bodyStart, body: '' }
  let end = bodyStart
  while (end < text.length && text.startsWith('  ', end)) {
    const nl = text.indexOf('\n', end)
    end = nl < 0 ? text.length : nl + 1
  }
  return { start: m.index, end, body: text.slice(bodyStart, end) }
}

/** Устройства из блока devices: имя → строки (с отступом). */
function splitDevices(body: string): [string, string[]][] {
  const out: [string, string[]][] = []
  for (const l of body.split('\n')) {
    if (l === '') continue
    const m = /^  (\S[^:]*):\s*$/.exec(l)
    if (m) out.push([m[1], []])
    else if (out.length) out[out.length - 1][1].push(l)
  }
  return out
}

function joinDevices(text: string, start: number, end: number, devs: [string, string[]][]): string {
  const body = devs.map(([n, ls]) => `  ${n}:\n` + ls.map((l) => l + '\n').join('')).join('')
  return text.slice(0, start) + (devs.length ? 'devices:\n' : 'devices: {}\n') + body + text.slice(end)
}

/** Добавляет (или заменяет) устройство; ключи — по алфавиту, как у lxc. */
export function setYamlDevice(text: string, name: string, fields: Record<string, string>): string {
  const b = devicesBlock(text)
  if (b.start < 0) return text
  const devs = splitDevices(b.body).filter(([n]) => n !== name)
  const lines = Object.keys(fields)
    .sort()
    .map((k) => `    ${k}: ${fields[k]}`)
  devs.push([name, lines])
  devs.sort((a, c) => a[0].localeCompare(c[0]))
  return joinDevices(text, b.start, b.end, devs)
}

/** Убирает устройство из блока devices:. */
export function removeYamlDevice(text: string, name: string): string {
  const b = devicesBlock(text)
  if (b.start < 0) return text
  const devs = splitDevices(b.body)
  const left = devs.filter(([n]) => n !== name)
  if (left.length === devs.length) return text
  return joinDevices(text, b.start, b.end, left)
}

/** Имена устройств — чтобы не занять существующее. */
export function yamlDeviceNames(text: string): string[] {
  return splitDevices(devicesBlock(text).body).map(([n]) => n)
}
