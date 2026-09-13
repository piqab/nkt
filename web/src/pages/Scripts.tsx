import { useEffect, useRef, useState } from 'react'
import { Button, ColorPicker, Input, Tabs, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Job, Me } from '../types'
import { Banner, Card, CodeEditor, ErrorNote, InfoHint, Loading, Modal, formatDateTime } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { confirmAction } from '../components/confirm'
import { JobLogModal } from './Jobs'
import ScriptScheme from '../components/ScriptScheme'

/**
 * Сценарии хаба: короткий построчный язык, которым описывают, что
 * развернуть на хостах. Хранятся и версионируются как профили; «Проверить»
 * разбирает текст и сверяет имена, «Выполнить» запускает заданием хаба.
 */

export interface ScriptRow {
  id: number
  name: string
  color?: string
  content?: string
  note?: string
  author?: string
  created_at: string
  updated_at: string
}

interface ScriptVersion {
  id: number
  script_id: number
  ts: string
  author?: string
  note?: string
  content?: string
}

export interface ScriptStep {
  line: number
  kind: string
  host?: string
  hosts?: string[]
  name?: string
  action?: string
  args?: Record<string, string>
  list?: string[]
  block?: string
  text: string
}

export interface CheckResult {
  steps: ScriptStep[]
  issues: { line: number; error: string }[]
  asks: string[]
  hosts: string[]
  groups: string[]
}

interface HelpCommand {
  kind: string
  syntax: string
  summary: string
  args: { name: string; required: boolean; desc: string }[]
  example: string
  block?: boolean
  on_host?: boolean
}

/** Выполнение появляется следующей фазой; до неё кнопка скрыта. */
const RUN_ENABLED = true

const SCRIPT_COLORS = ['#4f86c6', '#5aa66f', '#c9a227', '#d0743c', '#b95c8a', '#7c6bc4', '#3fa5a5', '#8a8a8a']

const TEMPLATE = `# Веб-ферма: группа, два хоста, nginx и стек
group web-farm

host web1 192.0.2.10 user root password ask group web-farm
install web1
wait web1 online 5m

on web1 packages install nginx htop
on web1 firewall allow 443/tcp
on web1 docker install
on web1 docker stack /srv/app/docker-compose.yml up
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
end
`

export default function Scripts({ me }: { me: Me }) {
  const { t } = useTranslation()
  const canEdit = me.is_admin && me.allow_mutations
  const list = useApi<{ scripts: ScriptRow[] }>('/hub/scripts', 60_000)
  const [selected, setSelected] = useState<number | null>(null)
  const [name, setName] = useState('')
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [color, setColor] = useState('')
  const [tab, setTab] = useState<'text' | 'scheme' | 'help'>('text')
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [check, setCheck] = useState<CheckResult | null>(null)
  const [showVersions, setShowVersions] = useState(false)
  const [openJob, setOpenJob] = useState<Job | null>(null)
  const [askModal, setAskModal] = useState<CheckResult | null>(null)
  const importRef = useRef<HTMLInputElement | null>(null)

  const current = useApi<ScriptRow>(selected ? `/hub/scripts/${selected}` : null)
  useEffect(() => {
    if (current.data) {
      setName(current.data.name)
      setDraft(current.data.content ?? '')
      setColor(current.data.color ?? '')
      setNote('')
      setCheck(null)
    }
  }, [current.data])

  const scripts = list.data?.scripts ?? []

  function startNew() {
    setSelected(null)
    setName('')
    setDraft(TEMPLATE)
    setNote('')
    setCheck(null)
    setColor(SCRIPT_COLORS.find((c) => !scripts.some((s) => s.color === c)) ?? SCRIPT_COLORS[0])
    setTab('text')
  }

  async function save(): Promise<number | null> {
    setBusy('save')
    setNotice(null)
    try {
      let id = selected
      if (id) {
        await api(`/hub/scripts/${id}`, { method: 'PUT', body: { name, content: draft, note, color } })
      } else {
        const res = await api<{ id: number }>('/hub/scripts', { method: 'POST', body: { name, content: draft, note, color } })
        id = res.id
        setSelected(id)
      }
      await list.reload()
      setNotice({ kind: 'info', text: t('scripts.saved') })
      return id
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      return null
    } finally {
      setBusy(null)
    }
  }

  async function runCheck(): Promise<CheckResult | null> {
    setBusy('check')
    setNotice(null)
    try {
      const res = await api<CheckResult>('/hub/scripts/check', { method: 'POST', body: { content: draft } })
      setCheck(res)
      return res
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      return null
    } finally {
      setBusy(null)
    }
  }

  // Запуск: сохранить (выполняется сохранённое, а не черновик), проверить,
  // спросить пароли для «password ask» и отдать заданию.
  async function run() {
    const id = await save()
    if (!id) return
    const res = await runCheck()
    if (!res || res.issues.length > 0) return
    setAskModal(res)
  }

  async function startRun(passwords: Record<string, string>) {
    if (!selected) return
    setAskModal(null)
    setBusy('run')
    try {
      const res = await api<{ job_id: number }>(`/hub/scripts/${selected}/run`, { method: 'POST', body: { passwords } })
      // Раздел работает в области localhost (см. App.tsx): задания хаба — по
      // обычному пути, без своей приставки.
      const job = await api<Job>(`/jobs/${res.job_id}`)
      setOpenJob(job)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  function importFile(file: File) {
    void file.text().then((text) => {
      setDraft(text)
      if (!name) setName(file.name.replace(/\.(nkt|txt)$/i, ''))
      setCheck(null)
      setTab('text')
    })
  }

  const trimmedName = name.trim()

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('scripts.title')}
          <InfoHint>{t('scripts.hint')}</InfoHint>
        </h1>
        {canEdit && <Button onClick={startNew}>{t('scripts.newScript')}</Button>}
      </div>

      <ErrorNote error={list.error} />
      {notice && (
        <Banner kind={notice.kind} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}

      <div className="grid grid-2" style={{ gridTemplateColumns: 'minmax(220px, 1fr) minmax(0, 3fr)', alignItems: 'start' }}>
        <Card title={t('scripts.listTitle')} subtitle={t('scripts.listSubtitle', { count: scripts.length })}>
          {list.loading && !list.data ? (
            <Loading what={t('scripts.title')} />
          ) : scripts.length === 0 ? (
            <p className="small muted">{t('scripts.empty')}</p>
          ) : (
            <div className="col" style={{ gap: '0.2rem' }}>
              {scripts.map((s) => (
                <Button
                  key={s.id}
                  type={s.id === selected ? 'default' : 'text'}
                  style={{ textAlign: 'left', height: 'auto', padding: '0.35rem 0.5rem' }}
                  onClick={() => setSelected(s.id)}
                >
                  <span className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
                    <span className="profile-swatch" style={{ background: s.color || 'transparent' }} />
                    <span>
                      <strong>{s.name}</strong>
                      <div className="small muted">{t('scripts.updated', { when: formatDateTime(s.updated_at) })}</div>
                    </span>
                  </span>
                </Button>
              ))}
            </div>
          )}
        </Card>

        <div className="col">
          <Card
            title={current.data?.name ?? (selected ? '…' : t('scripts.newScript'))}
            actions={
              <div className="row" style={{ gap: '0.3rem', flexWrap: 'wrap' }}>
                {selected && (
                  <Button size="small" onClick={() => setShowVersions(true)}>
                    {t('scripts.history')}
                  </Button>
                )}
                {selected && (
                  <Button size="small" href={`/api/hub/scripts/${selected}/export`} target="_blank">
                    {t('scripts.export')}
                  </Button>
                )}
                {canEdit && (
                  <>
                    <Button size="small" onClick={() => importRef.current?.click()}>
                      {t('scripts.import')}
                    </Button>
                    <input
                      ref={importRef}
                      type="file"
                      accept=".nkt,.txt,text/plain"
                      hidden
                      onChange={(e) => {
                        const f = e.target.files?.[0]
                        e.target.value = ''
                        if (f) importFile(f)
                      }}
                    />
                    <Button size="small" loading={busy === 'save'} disabled={!trimmedName || busy !== null} onClick={() => void save()}>
                      {t('common.save')}
                    </Button>
                    <Button size="small" loading={busy === 'check'} disabled={busy !== null} onClick={() => void runCheck()}>
                      {t('scripts.check')}
                    </Button>
                    {RUN_ENABLED && (
                      <Tooltip title={t('scripts.runHint')}>
                        <Button size="small" type="primary" loading={busy === 'run'} disabled={!trimmedName || busy !== null} onClick={() => void run()}>
                          {t('scripts.run')}
                        </Button>
                      </Tooltip>
                    )}
                  </>
                )}
                {selected && canEdit && (
                  <Button
                    size="small"
                    danger
                    onClick={async () => {
                      if (!(await confirmAction(t('scripts.confirmDelete', { name: current.data?.name })))) return
                      await api(`/hub/scripts/${selected}`, { method: 'DELETE' })
                      setSelected(null)
                      setDraft('')
                      setName('')
                      await list.reload()
                    }}
                  >
                    {t('common.delete')}
                  </Button>
                )}
              </div>
            }
          >
            {canEdit && (
              <div className="filters" style={{ marginBottom: '0.5rem', alignItems: 'flex-end' }}>
                <label style={{ minWidth: '12rem' }}>
                  {t('scripts.name')}
                  <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="web-farm" />
                </label>
                <label style={{ flex: 1, minWidth: '14rem' }}>
                  {t('scripts.note')}
                  <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('scripts.notePlaceholder')} />
                </label>
                <label>
                  {t('profiles.color')}
                  <span className="row" style={{ gap: '0.3rem', alignItems: 'center' }}>
                    <ColorPicker
                      size="small"
                      value={color || null}
                      allowClear
                      presets={[{ label: t('profiles.colorPresets'), colors: SCRIPT_COLORS }]}
                      onChange={(c) => setColor(c ? c.toHexString().slice(0, 7) : '')}
                      onClear={() => setColor('')}
                    />
                    <span className="small muted mono">{color || t('profiles.colorNone')}</span>
                  </span>
                </label>
              </div>
            )}
            <Tabs
              activeKey={tab}
              onChange={(k) => setTab(k as typeof tab)}
              items={[
                {
                  key: 'text',
                  label: t('scripts.tabText'),
                  children: <CodeEditor value={draft} onChange={(e) => { setDraft(e.target.value); setCheck(null) }} rows={22} readOnly={!canEdit} />,
                },
                { key: 'scheme', label: t('scripts.tabScheme'), children: <ScriptScheme content={draft} /> },
                { key: 'help', label: t('scripts.tabHelp'), children: <ScriptHelp /> },
              ]}
            />
          </Card>

          {check && (
            <Card
              title={check.issues.length > 0 ? t('scripts.checkIssues', { count: check.issues.length }) : t('scripts.checkOk', { count: check.steps.length })}
            >
              {check.issues.length > 0 ? (
                <ul className="col" style={{ gap: '0.2rem', margin: 0, paddingLeft: '1.2rem' }}>
                  {check.issues.map((i, n) => (
                    <li key={n}>
                      <span className="mono muted">{t('scripts.line', { line: i.line })}</span> {i.error}
                    </li>
                  ))}
                </ul>
              ) : (
                <>
                  {check.asks.length > 0 && (
                    <p className="small muted" style={{ marginTop: 0 }}>
                      {t('scripts.willAsk', { hosts: check.asks.join(', ') })}
                    </p>
                  )}
                  <div className="table-wrap">
                    <DataTable<ScriptStep>
                      dataSource={check.steps}
                      rowKey="line"
                      columns={[
                        { title: '#', key: 'line', width: 60, render: (_, s) => <span className="mono muted">{s.line}</span> },
                        { title: t('scripts.colHost'), key: 'host', width: 140, render: (_, s) => (s.host ? <Tag>{s.host}</Tag> : <span className="muted">—</span>) },
                        { title: t('scripts.colStep'), key: 'text', render: (_, s) => <code className="mono">{s.text}</code> },
                      ]}
                    />
                  </div>
                </>
              )}
            </Card>
          )}
        </div>
      </div>

      {showVersions && selected && (
        <Modal title={t('scripts.history')} onClose={() => setShowVersions(false)} width={720}>
          <ScriptVersions
            scriptID={selected}
            onRestore={(content) => {
              setDraft(content)
              setShowVersions(false)
            }}
          />
        </Modal>
      )}

      {askModal && <AskPasswordsModal result={askModal} onClose={() => setAskModal(null)} onStart={startRun} />}

      {openJob && <JobLogModal job={openJob} onClose={() => setOpenJob(null)} />}
    </>
  )
}

function ScriptVersions({ scriptID, onRestore }: { scriptID: number; onRestore: (content: string) => void }) {
  const { t } = useTranslation()
  const versions = useApi<{ versions: ScriptVersion[] }>(`/hub/scripts/${scriptID}/versions`)
  if (versions.loading && !versions.data) return <Loading what={t('scripts.history')} />
  return (
    <div className="col" style={{ gap: '0.35rem' }}>
      {(versions.data?.versions ?? []).map((v) => (
        <div key={v.id} className="row spread">
          <span className="small">
            {formatDateTime(v.ts)} · {v.author || '—'}
            {v.note ? ` · ${v.note}` : ''}
          </span>
          <Button
            size="small"
            onClick={async () => {
              const full = await api<ScriptVersion>(`/hub/scripts/versions/${v.id}`)
              onRestore(full.content ?? '')
            }}
          >
            {t('profiles.restoreVersion')}
          </Button>
        </div>
      ))}
    </div>
  )
}

/** Пароли для «password ask»: спрашиваются перед запуском и уходят
 * только в задание — в тексте сценария их нет. */
function AskPasswordsModal({
  result,
  onClose,
  onStart,
}: {
  result: CheckResult
  onClose: () => void
  onStart: (passwords: Record<string, string>) => void
}) {
  const { t } = useTranslation()
  const [passwords, setPasswords] = useState<Record<string, string>>({})
  const ready = result.asks.every((h) => (passwords[h] ?? '') !== '')
  return (
    <Modal title={t('scripts.runTitle')} onClose={onClose} closeLabel={t('common.cancel')}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <p className="small muted" style={{ margin: 0 }}>
          {t('scripts.runBody', { steps: result.steps.length })}
        </p>
        {result.asks.map((h) => (
          <label key={h} className="col" style={{ gap: '0.2rem' }}>
            {t('scripts.askPassword', { host: h })}
            <Input.Password value={passwords[h] ?? ''} onChange={(e) => setPasswords({ ...passwords, [h]: e.target.value })} autoComplete="new-password" />
          </label>
        ))}
        <div className="row" style={{ justifyContent: 'flex-end' }}>
          <Button type="primary" disabled={!ready} onClick={() => onStart(passwords)}>
            {t('scripts.runStart')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/** Справка по командам — с сервера, из той же таблицы, что и разбор. */
function ScriptHelp() {
  const { t } = useTranslation()
  const help = useApi<{ commands: HelpCommand[]; intro: string }>('/hub/scripts/help')
  if (help.loading && !help.data) return <Loading what={t('scripts.tabHelp')} />
  if (help.error) return <Banner kind="error">{help.error}</Banner>
  return (
    <div className="col" style={{ gap: '0.8rem' }}>
      <p className="small" style={{ margin: 0 }}>{help.data?.intro}</p>
      {(help.data?.commands ?? []).map((c) => (
        <div key={c.kind} className="script-help-cmd">
          <pre className="mono script-help-syntax">{c.syntax}</pre>
          <div className="small">{c.summary}</div>
          {c.args.length > 0 && (
            <ul className="small" style={{ margin: '0.3rem 0 0', paddingLeft: '1.2rem' }}>
              {c.args.map((a) => (
                <li key={a.name}>
                  <code className="mono">{a.name}</code>
                  {a.required && <span className="muted"> *</span>} — {a.desc}
                </li>
              ))}
            </ul>
          )}
          <pre className="mono small script-help-example">{c.example}</pre>
        </div>
      ))}
    </div>
  )
}
