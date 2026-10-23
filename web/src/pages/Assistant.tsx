import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Tag, Tooltip } from 'antd'
import { RobotOutlined, SendOutlined, StarFilled, StarOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, fetchHubJob, useApi } from '../api'
import type { Job, Me } from '../types'
import { Banner, formatDateTime } from '../components/ui'
import { AI_REQUEST_TIMEOUT_MS, AIBody, Thinking, useElapsed } from '../components/AIExplain'
import { TitleHelp } from '../components/Docs'
import { confirmAction } from '../components/confirm'
import { JobLogModal } from './Jobs'
import { linkify, type ChatActions, type ChatObject } from '../components/assistant/ChatObjects'
import { Composer } from '../components/assistant/Composer'
import { FavoritesPanel, hasFavorite, saveFavorites, withFavorite, type FavData } from '../components/assistant/Favorites'

/**
 * Ассистент хаба — переписка: вопрос о парке, модель сама читает данные
 * хаба инструментами (только чтение, от имени того, кто спрашивает) и
 * отвечает. Имена хостов, контейнеров, конвейеров… в сообщениях — ссылки
 * с меню действий; при наборе — подсказки объектов. Справа — избранные
 * вопросы по разделам. Изменение приходит сценарием: «Сухой прогон» и
 * «Выполнить» в окне задания — запускает человек.
 */

interface Step {
  tool: string
  args: Record<string, unknown> | null
  error: boolean
}

interface Reply {
  answer: string
  steps: Step[]
  issues: { line: number; error: string }[]
  script: boolean
  script_retried?: boolean
  mode: 'tools' | 'summary'
  tools_error?: string
  model: string
  rounds: number
  seen?: ChatObject[]
}

interface Message {
  role: 'user' | 'assistant'
  text: string
  ts?: string
  reply?: Reply
}

const STORE = 'nkt-assistant'

function readStored(): Message[] {
  try {
    const raw = sessionStorage.getItem(STORE)
    return raw ? (JSON.parse(raw) as Message[]) : []
  } catch {
    return []
  }
}

function stepLabel(s: Step): string {
  const args = Object.entries(s.args ?? {})
    .map(([k, v]) => `${k}=${typeof v === 'object' ? JSON.stringify(v) : String(v)}`)
    .join(' ')
  return args ? `${s.tool} ${args}` : s.tool
}

/** «Посмотрел N ▾» — что модель прочитала, свёрнуто в строку. */
function Looked({ steps }: { steps: Step[] }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  if (steps.length === 0) return null
  return (
    <div className="chat-looked small muted">
      <button type="button" className="chat-looked-toggle" onClick={() => setOpen(!open)}>
        {t('assistant.lookedCount', { count: steps.length })} {open ? '▴' : '▾'}
      </button>
      {open && (
        <div className="row" style={{ gap: '0.25rem', flexWrap: 'wrap', marginTop: '0.25rem' }}>
          {steps.map((s, n) => (
            <Tag key={n} color={s.error ? 'red' : undefined} className="mono" style={{ margin: 0 }}>
              {stepLabel(s)}
            </Tag>
          ))}
        </div>
      )}
    </div>
  )
}

export default function Assistant() {
  const { t } = useTranslation()
  const status = useApi<{ enabled: boolean; model?: string; tools?: { status: 'tools' | 'no_tools' } }>('/hub/ai')
  const me = useApi<Me>('/auth/me')
  const objectsApi = useApi<{ objects: ChatObject[] }>('/hub/assistant/objects')
  const favApi = useApi<FavData>('/hub/assistant/favorites')
  const [messages, setMessages] = useState<Message[]>(readStored)
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'success' | 'error'; text: string } | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  const [fav, setFav] = useState<FavData | null>(null)
  const elapsed = useElapsed(busy)
  const bottom = useRef<HTMLDivElement>(null)
  const favorites = fav ?? favApi.data ?? { sections: [] }

  useEffect(() => {
    try {
      sessionStorage.setItem(STORE, JSON.stringify(messages))
    } catch {
      /* без хранилища — разговор живёт до перезагрузки */
    }
    bottom.current?.scrollIntoView?.({ block: 'end', behavior: 'smooth' })
  }, [messages, busy])

  // Объекты для ссылок: хаба и те контейнеры и службы, что модель прочитала.
  const objects = useMemo(() => {
    const out = [...(objectsApi.data?.objects ?? [])]
    for (const m of messages) for (const o of m.reply?.seen ?? []) out.push(o)
    return out
  }, [objectsApi.data, messages])

  async function send(text: string) {
    const q = text.trim()
    if (!q || busy) return
    const next: Message[] = [...messages, { role: 'user', text: q, ts: new Date().toISOString() }]
    setMessages(next)
    setDraft('')
    setBusy(true)
    setError(null)
    try {
      const reply = await api<Reply>('/hub/assistant', {
        method: 'POST',
        timeoutMs: AI_REQUEST_TIMEOUT_MS,
        body: { messages: next.map((m) => ({ role: m.role, text: m.text })) },
      })
      setMessages([...next, { role: 'assistant', text: reply.answer, ts: new Date().toISOString(), reply }])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function updateFavorites(next: FavData) {
    setFav(next)
    setFav(await saveFavorites(next))
  }

  const actions: ChatActions = {
    ask: (text) => void send(text),
    openJob: (id) => void fetchHubJob<Job>(id).then(setJob).catch(() => undefined),
    notice: (kind, text) => setNotice({ kind, text }),
    admin: !!me.data?.is_admin,
  }
  const render = (text: string) => linkify(text, objects, actions)
  const enabled = !!status.data?.enabled
  const examples = [t('assistant.example1'), t('assistant.example2'), t('assistant.example3')]

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('assistant.title')}
          <TitleHelp>{t('assistant.hint')}</TitleHelp>
          {status.data?.tools && (
            <Tooltip title={t(status.data.tools.status === 'tools' ? 'assistant.modelToolsHint' : 'assistant.summaryHint')}>
              <Tag color={status.data.tools.status === 'tools' ? 'success' : 'orange'} style={{ marginLeft: 8, verticalAlign: 'middle' }}>
                {status.data.model} · {t(`ai.tools.${status.data.tools.status}`)}
              </Tag>
            </Tooltip>
          )}
        </h1>
        {messages.length > 0 && (
          <Button
            disabled={busy}
            onClick={async () => {
              if (!(await confirmAction(t('assistant.clearConfirm'), { okText: t('assistant.clear') }))) return
              setMessages([])
              setError(null)
              setNotice(null)
            }}
          >
            {t('assistant.clear')}
          </Button>
        )}
      </div>
      {status.data && !status.data.enabled && <Banner kind="warn">{t('assistant.disabled')}</Banner>}
      <div className="chat-layout">
        <div className="chat-main">
          <div className="chat-feed">
            {messages.length === 0 && (
              <div className="chat-empty col" style={{ gap: '0.5rem' }}>
                <span className="small muted">{t('assistant.intro')}</span>
                <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap' }}>
                  {examples.map((e) => (
                    <Button key={e} size="small" onClick={() => setDraft(e)} disabled={!enabled}>
                      {e}
                    </Button>
                  ))}
                </div>
              </div>
            )}
            {messages.map((m, i) =>
              m.role === 'user' ? (
                <div key={i} className="chat-row me">
                  <div className="chat-bubble me">
                    <div className="chat-text">{render(m.text)}</div>
                    <div className="chat-meta">
                      {m.ts && <span>{formatDateTime(m.ts)}</span>}
                      <Tooltip title={t(hasFavorite(favorites, m.text) ? 'assistant.fav.inFavorites' : 'assistant.fav.add')}>
                        <button
                          type="button"
                          className="chat-star"
                          aria-label={t('assistant.fav.add')}
                          onClick={() => {
                            if (!hasFavorite(favorites, m.text)) void updateFavorites(withFavorite(favorites, m.text, t('assistant.fav.defaultSection'))).catch((err) => setError(String(err)))
                          }}
                        >
                          {hasFavorite(favorites, m.text) ? <StarFilled /> : <StarOutlined />}
                        </button>
                      </Tooltip>
                    </div>
                  </div>
                </div>
              ) : (
                <div key={i} className="chat-row bot">
                  <div className="chat-avatar">
                    <RobotOutlined />
                  </div>
                  <div className="chat-bubble bot">
                    {m.reply && <Looked steps={m.reply.steps} />}
                    {m.reply?.mode === 'summary' && (
                      <div className="small muted" style={{ marginBottom: '0.3rem' }}>
                        <Tooltip title={t('assistant.summaryHint')}>
                          <Tag color="orange" style={{ margin: 0 }}>
                            {t('assistant.summary')}
                          </Tag>
                        </Tooltip>{' '}
                        {m.reply.tools_error}
                      </div>
                    )}
                    <AIBody
                      text={m.text}
                      origin={{ kind: 'assistant', label: messages[i - 1]?.text }}
                      check={m.reply?.script ? { issues: m.reply.issues, retried: m.reply.script_retried } : undefined}
                      onAskAbout={(text) => void send(text)}
                      renderText={render}
                    />
                    <div className="chat-meta">
                      {m.ts && <span>{formatDateTime(m.ts)}</span>}
                      {m.reply && <span>{t('assistant.footer', { model: m.reply.model, rounds: m.reply.rounds })}</span>}
                    </div>
                  </div>
                </div>
              ),
            )}
            {busy && (
              <div className="chat-row bot">
                <div className="chat-avatar">
                  <RobotOutlined />
                </div>
                <div className="chat-bubble bot">
                  <Thinking seconds={elapsed} />
                </div>
              </div>
            )}
            <div ref={bottom} />
          </div>
          {error && (
            <Banner kind="error" onClose={() => setError(null)}>
              {error}
            </Banner>
          )}
          {notice && (
            <Banner kind={notice.kind} onClose={() => setNotice(null)}>
              {notice.text}
            </Banner>
          )}
          <div className="chat-input">
            <Composer value={draft} onChange={setDraft} onSend={() => void send(draft)} objects={objects} disabled={!enabled || busy} placeholder={t('assistant.placeholder')} />
            <div className="row" style={{ gap: '0.4rem', alignItems: 'center' }}>
              <Button type="primary" icon={<SendOutlined />} loading={busy} disabled={!draft.trim() || !enabled} onClick={() => void send(draft)}>
                {t('assistant.ask')}
              </Button>
              <span className="small muted">{t('assistant.readOnly')}</span>
            </div>
          </div>
        </div>
        <FavoritesPanel data={favorites} onChange={updateFavorites} onSend={(text) => void send(text)} disabled={!enabled || busy} />
      </div>
      {job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setJob(null)} />}
    </>
  )
}
