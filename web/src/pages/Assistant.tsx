import { useEffect, useRef, useState } from 'react'
import { Button, Input, Tag, Tooltip } from 'antd'
import { RobotOutlined, SendOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card } from '../components/ui'
import { AI_REQUEST_TIMEOUT_MS, AIBody, Thinking, useElapsed } from '../components/AIExplain'
import { TitleHelp } from '../components/Docs'

/**
 * Ассистент хаба: вопрос о парке — модель сама читает данные хаба
 * инструментами (только чтение, от имени того, кто спрашивает) и отвечает.
 * Изменение приходит сценарием: «Открыть как сценарий» под блоком, дальше
 * «Проверить», сухой прогон и запуск — человеком.
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
  model: string
  rounds: number
}

interface Message {
  role: 'user' | 'assistant'
  text: string
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

export default function Assistant() {
  const { t } = useTranslation()
  const status = useApi<{ enabled: boolean; model?: string; tools?: { status: 'tools' | 'no_tools' } }>('/hub/ai')
  const [messages, setMessages] = useState<Message[]>(readStored)
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const elapsed = useElapsed(busy)
  const bottom = useRef<HTMLDivElement>(null)

  useEffect(() => {
    try {
      sessionStorage.setItem(STORE, JSON.stringify(messages))
    } catch {
      /* без хранилища — разговор живёт до перезагрузки */
    }
    bottom.current?.scrollIntoView?.({ block: 'end', behavior: 'smooth' })
  }, [messages, busy])

  async function send(text: string) {
    const q = text.trim()
    if (!q || busy) return
    const next: Message[] = [...messages, { role: 'user', text: q }]
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
      setMessages([...next, { role: 'assistant', text: reply.answer, reply }])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

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
            onClick={() => {
              setMessages([])
              setError(null)
            }}
          >
            {t('assistant.new')}
          </Button>
        )}
      </div>
      {status.data && !status.data.enabled && <Banner kind="warn">{t('assistant.disabled')}</Banner>}
      <Card>
        <div className="col" style={{ gap: '0.8rem' }}>
          {messages.length === 0 && (
            <div className="col" style={{ gap: '0.5rem' }}>
              <span className="small muted">{t('assistant.intro')}</span>
              <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap' }}>
                {examples.map((e) => (
                  <Button key={e} size="small" onClick={() => setDraft(e)} disabled={!status.data?.enabled}>
                    {e}
                  </Button>
                ))}
              </div>
            </div>
          )}
          {messages.map((m, i) =>
            m.role === 'user' ? (
              <div key={i} className="assistant-q">
                {m.text}
              </div>
            ) : (
              <div key={i} className="assistant-a">
                <div className="row small muted" style={{ gap: '0.35rem', marginBottom: '0.3rem' }}>
                  <RobotOutlined />
                  {m.reply && m.reply.steps.length > 0 && (
                    <>
                      <span>{t('assistant.looked')}</span>
                      {m.reply.steps.map((s, n) => (
                        <Tag key={n} color={s.error ? 'red' : undefined} className="mono" style={{ margin: 0 }}>
                          {stepLabel(s)}
                        </Tag>
                      ))}
                    </>
                  )}
                  {m.reply?.mode === 'summary' && (
                    <Tooltip title={t('assistant.summaryHint')}>
                      <Tag color="orange" style={{ margin: 0 }}>
                        {t('assistant.summary')}
                      </Tag>
                    </Tooltip>
                  )}
                </div>
                <AIBody
                  text={m.text}
                  origin={{ kind: 'assistant', label: messages[i - 1]?.text }}
                  check={m.reply?.script ? { issues: m.reply.issues, retried: m.reply.script_retried } : undefined}
                />
                {m.reply && <div className="small muted">{t('assistant.footer', { model: m.reply.model, rounds: m.reply.rounds })}</div>}
              </div>
            ),
          )}
          {busy && <Thinking seconds={elapsed} />}
          {error && (
            <Banner kind="error" onClose={() => setError(null)}>
              {error}
            </Banner>
          )}
          <div className="col" style={{ gap: '0.4rem' }}>
            <Input.TextArea
              rows={3}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder={t('assistant.placeholder')}
              disabled={!status.data?.enabled}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                  e.preventDefault()
                  void send(draft)
                }
              }}
            />
            <div className="row" style={{ gap: '0.4rem' }}>
              <Button type="primary" icon={<SendOutlined />} loading={busy} disabled={!draft.trim() || !status.data?.enabled} onClick={() => void send(draft)}>
                {t('assistant.ask')}
              </Button>
              <span className="small muted">{t('assistant.readOnly')}</span>
            </div>
          </div>
          <div ref={bottom} />
        </div>
      </Card>
    </>
  )
}
