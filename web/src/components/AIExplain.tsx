import { useEffect, useState, type ReactNode } from 'react'
import { Button, Input, Spin, Tooltip } from 'antd'
import { BulbFilled, BulbOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import { Banner, Modal, formatDateTime } from './ui'
import { blurText } from '../privacy'
import { aiAnswerState, currentAIHostID, invalidateAIAnswers, useAIAnswers } from '../aiAnswers'

/**
 * «Объяснить» — одна кнопка для любой строки, о которой можно спросить
 * модель: находка, уязвимость, вредоносное, оповещение хаба, ошибка
 * задания.
 *
 * Запрос всегда уходит на хаб (/hub/ai/…, см. api.ts's unscoped): ключ и
 * адрес модели живут только там, одни на все хосты, и хосту наружу
 * ничего не нужно — у него может не быть интернета.
 *
 * Ответ остаётся у строки: лампочка с ответом — оранжевая, открывается
 * без нового запроса. Та же находка, уже разобранная на другом хосте, —
 * синяя: окно сначала показывает тот ответ с пометкой, откуда он, а
 * свой запрос делается отдельной кнопкой.
 *
 * Модель ничего не выполняет: команды показываются для копирования,
 * применяются обычными кнопками nkt. Приписка об этом стоит под каждым
 * ответом — не из перестраховки, а потому что ответ выглядит уверенно
 * независимо от того, прав он или нет.
 */

export interface AIContext {
  kind: 'finding' | 'vuln' | 'malware' | 'event' | 'job-error' | 'config-error' | 'config' | 'ip' | 'monitoring'
  title: string
  detail?: string
  suggestion?: string
  severity?: string
  service?: string
  object?: string
  file?: string
  line?: number
  /** Для ошибки правки конфигурации: дифф «на диске → черновик» и вывод
   * проверки. Секреты из них хаб вырезает до отправки (ai.RedactSecrets). */
  diff?: string
  output?: string
  /** Помощь по конфигурации: текст файла (хаб обрежет до 32 КБ и
   * вырежет секреты) и вопрос оператора. */
  content?: string
  question?: string
}

interface AISection {
  title: string
  body: string
}

/** Запрос к модели целиком (hub.AIRequest). */
export interface AIRequest {
  provider: string
  model: string
  base_url: string
  timeout_s: number
  anonymize: boolean
  system: string
  system_modified: boolean
  user: string
  /** Что на что заменено — только администратору. */
  aliases?: { alias: string; real: string }[]
}

/**
 * «Показать запрос» — весь запрос, как он ушёл модели: кому (провайдер,
 * модель, адрес), инструкция (стандартная или правленая), сообщение с
 * псевдонимами вместо адресов и имён, таблица замен (администратору).
 * У ответа, сохранённого до v1.11.41, есть только сообщение.
 */
export function AIRequestView({ request, prompt, missing }: { request?: AIRequest | null; prompt?: string; missing?: boolean }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const pre = (text: string) => (
    <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', margin: 0, maxHeight: 320, overflow: 'auto' }}>
      {text}
    </pre>
  )
  if (!request) {
    return (
      <div className="col" style={{ gap: '0.3rem' }}>
        {missing && <div className="small muted">{t('ai.requestMissing')}</div>}
        {prompt && pre(prompt)}
      </div>
    )
  }
  const full = `# ${request.provider} · ${request.model} · ${request.base_url}\n\n## system\n${request.system}\n\n## user\n${request.user}\n`
  async function copy() {
    try {
      await navigator.clipboard.writeText(full)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      // буфер обмена недоступен (не https) — текст всё равно на экране
    }
  }
  return (
    <div className="col" style={{ gap: '0.4rem' }}>
      <div className="small">
        {t('ai.requestMeta', {
          provider: request.provider === 'anthropic' ? 'Anthropic' : t('ai.providerOpenAI'),
          model: request.model,
          url: request.base_url,
          timeout: request.timeout_s,
        })}{' '}
        · {request.anonymize ? t('ai.requestAnonymized', { count: request.aliases?.length ?? 0 }) : t('ai.requestNotAnonymized')}
        <Button size="small" type="link" onClick={() => void copy()}>
          {copied ? t('ai.requestCopied') : t('ai.requestCopy')}
        </Button>
      </div>
      <div className="small">
        <strong>{t('ai.requestSystem')}</strong>{' '}
        <span className="muted">{request.system_modified ? t('ai.requestSystemModified') : t('ai.requestSystemDefault')}</span>
      </div>
      {pre(request.system)}
      <div className="small">
        <strong>{t('ai.requestUser')}</strong>
      </div>
      {pre(request.user)}
      {request.aliases && request.aliases.length > 0 && (
        <>
          <div className="small">
            <strong>{t('ai.requestAliases')}</strong> <span className="muted">{t('ai.requestAliasesHint')}</span>
          </div>
          <table className="small mono" style={{ borderCollapse: 'collapse' }}>
            <tbody>
              {request.aliases.map((a) => (
                <tr key={a.alias}>
                  <td style={{ padding: '0.1rem 0.8rem 0.1rem 0' }}>{a.alias}</td>
                  <td style={{ padding: '0.1rem 0' }}>{blurText(a.real)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  )
}

interface AIAnswer {
  answer: string
  sections: AISection[]
  model: string
  prompt: string
  notice: string
  request?: AIRequest
  request_missing?: boolean
  /** Ответ сохранён раньше для этой находки на этом хосте. */
  stored_at?: string
  /** Ответ той же находки на другом хосте — своего ещё нет. */
  similar?: { host_id: number; host_name: string; created_at: string }
}

export function AIExplain({
  ctx,
  disabled,
  askFirst,
  actions,
  hint: hintOverride,
}: {
  ctx: AIContext
  disabled?: boolean
  /** Перед первым запросом спросить, что нужно (поле вопроса): для
   * помощи по программе, где без задачи ответ — общий обзор. */
  askFirst?: boolean
  /** Кнопки действий под ответом (например, «забанить на всех хостах»
   * у проверки адреса); close — закрыть окно разбора. */
  actions?: (close: () => void) => ReactNode
  /** Своя подсказка у лампочки без ответа. */
  hint?: string
}) {
  const { t } = useTranslation()
  const [question, setQuestion] = useState('')
  const [asked, setAsked] = useState(false)
  const refs = useAIAnswers()
  const hostID = currentAIHostID()
  // Помощь по программе (askFirst): ответы хранятся по вопросу — объект
  // «категория?вопрос». Прежние вопросы этого хоста показываются списком,
  // а лампочка горит, если хоть на один уже есть ответ.
  const prior = askFirst
    ? refs.filter((r) => r.kind === ctx.kind && r.title === ctx.title.trim() && r.object.startsWith(`${(ctx.object ?? '').trim()}?`))
    : []
  const priorOwn = prior.filter((r) => r.host_id === hostID)
  const baseState = aiAnswerState(refs, ctx, hostID)
  const state = baseState !== 'none' ? baseState : priorOwn.length > 0 ? 'own' : prior.length > 0 ? 'similar' : 'none'
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [answer, setAnswer] = useState<AIAnswer | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [showPrompt, setShowPrompt] = useState(false)
  const elapsed = useElapsed(busy)

  async function ask(force = false, withQuestion?: string) {
    setOpen(true)
    // Сохранённый ответ без вопроса есть — он показывается сразу, а не
    // пустое поле вопроса; спросить другое можно кнопкой.
    if (askFirst && !asked && !force && withQuestion === undefined && baseState === 'none') return
    setAsked(true)
    if (!force && withQuestion === undefined && (answer || busy)) return
    setBusy(true)
    setError(null)
    if (force) setAnswer(null)
    try {
      const res = await api<AIAnswer>('/hub/ai/explain', {
        method: 'POST',
        // Настоящий предел — время ожидания в настройках ИИ на хабе (до
        // получаса); здесь только запас, чтобы общий таймаут запросов в
        // 30 с не оборвал ответ раньше модели.
        timeoutMs: AI_REQUEST_TIMEOUT_MS,
        body: {
          ...ctx,
          question: (withQuestion ?? question).trim() || ctx.question,
          // Какому хосту принадлежит находка — по нему хаб добавит в
          // запрос, что там рядом (порты, контейнеры, firewall).
          host_id: hostID,
          force,
        },
      })
      setAnswer(res)
      // Новый ответ сохранён на хабе — лампочки на странице перекрасятся.
      if (!res.stored_at && !res.similar) invalidateAIAnswers()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    try {
      await api('/hub/ai/answers/delete', { method: 'POST', body: { ...ctx, host_id: hostID } })
      setAnswer(null)
      setOpen(false)
      invalidateAIAnswers()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  // Цвета: без ответа — контурная стандартного синего, как остальные
  // иконки действий в строке; свой ответ — залитая оранжевая; ответ с
  // другого хоста — залитая синяя (контурная синяя сливалась бы с «нет
  // ответа»).
  const icon =
    state === 'own' ? (
      <BulbFilled style={{ color: 'var(--status-warning)' }} />
    ) : state === 'similar' ? (
      <BulbFilled style={{ color: 'var(--series-1)' }} />
    ) : (
      <BulbOutlined style={{ color: 'var(--series-1)' }} />
    )
  const hint = state === 'own' ? t('ai.hasAnswer') : state === 'similar' ? t('ai.hasSimilar') : (hintOverride ?? t('ai.explainHint'))

  return (
    <>
      <Tooltip title={hint}>
        <Button type="text" size="small" icon={icon} disabled={disabled} onClick={() => void ask()} />
      </Tooltip>
      {open && (
        <Modal title={blurText(ctx.title)} onClose={() => setOpen(false)} width={760}>
          {askFirst && !asked ? (
            <div className="col">
              <div className="small muted">{t('ai.askFirstHint')}</div>
              {priorOwn.length > 0 && (
                <div className="small">
                  {t('ai.priorQuestions')}{' '}
                  {priorOwn.map((r) => {
                    const q = r.object.slice((ctx.object ?? '').trim().length + 1)
                    return (
                      <Button key={r.key} type="link" size="small" onClick={() => {
                        setQuestion(q)
                        setAnswer(null)
                        void ask(false, q)
                      }}>
                        {q}
                      </Button>
                    )
                  })}
                </div>
              )}
              <Input.TextArea rows={2} value={question} onChange={(e) => setQuestion(e.target.value)} placeholder={t('ai.askFirstPlaceholder')} autoFocus />
              <div>
                <Button type="primary" onClick={() => void ask(true)}>
                  {t('ai.askFirstGo')}
                </Button>
              </div>
            </div>
          ) : busy && !answer ? (
            <Thinking seconds={elapsed} />
          ) : error ? (
            <div className="col">
              <Banner kind="error">{error}</Banner>
              {/* Действие не зависит от ответа модели: ИИ не настроен или
                  не ответил — «забанить на всех» всё равно под рукой. */}
              {actions && <div className="row">{actions(() => setOpen(false))}</div>}
            </div>
          ) : answer ? (
            <div className="col">
              {answer.similar && (
                <Banner kind="info">
                  {blurText(t('ai.similarSeen', { host: answer.similar.host_name, date: formatDateTime(answer.similar.created_at) }))}{' '}
                  <Button type="link" size="small" onClick={() => void ask(true)}>
                    {t('ai.askForThisHost')}
                  </Button>
                </Banner>
              )}
              {answer.sections && answer.sections.length > 0 ? (
                answer.sections.map((s, i) => (
                  <div key={i}>
                    {s.title && <h3 style={{ marginBottom: '0.3rem' }}>{s.title}</h3>}
                    <AIBody text={s.body} />
                  </div>
                ))
              ) : answer.answer?.trim() ? (
                // Разделов не нашлось (модель ответила без заголовков «##»)
                // — текст целиком, а не пустое окно.
                <AIBody text={answer.answer} />
              ) : (
                <Banner kind="warn">{t('ai.emptyAnswer')}</Banner>
              )}
              <div className="small muted">
                {answer.notice}
                {answer.stored_at && <> · {t('ai.storedAt', { date: formatDateTime(answer.stored_at) })}</>}
                <Button type="link" size="small" onClick={() => setShowPrompt((v) => !v)}>
                  {showPrompt ? t('ai.hidePrompt') : t('ai.showPrompt')}
                </Button>
                {askFirst && (
                  <Button type="link" size="small" onClick={() => {
                    setAsked(false)
                    setAnswer(null)
                    setQuestion('')
                  }}>
                    {t('ai.askOther')}
                  </Button>
                )}
                {!answer.similar && (
                  <>
                    <Button type="link" size="small" onClick={() => void ask(true)}>
                      {t('ai.askAgain')}
                    </Button>
                    {answer.stored_at && (
                      <Button type="link" size="small" danger onClick={() => void remove()}>
                        {t('ai.deleteAnswer')}
                      </Button>
                    )}
                  </>
                )}
              </div>
              {showPrompt && <AIRequestView request={answer.request} prompt={answer.prompt} missing={answer.request_missing} />}
              {actions && <div className="row">{actions(() => setOpen(false))}</div>}
            </div>
          ) : null}
        </Modal>
      )}
    </>
  )
}

/** Предел ожидания на стороне браузера — заведомо больше серверного
 * максимума (30 мин), чтобы отказ всегда приходил от хаба с понятной
 * причиной, а не от таймера здесь. */
export const AI_REQUEST_TIMEOUT_MS = 31 * 60_000

/** «Модель думает… N с» — без «Загружаю…» общего Loading: это не
 * загрузка страницы, а ожидание ответа, и счётчик здесь главное. */
export function Thinking({ seconds }: { seconds: number }) {
  const { t } = useTranslation()
  return (
    <div className="chart-empty">
      <Spin size="small" /> {t('ai.thinkingFor', { seconds })}
    </div>
  )
}

/** Секунды с начала запроса — чтобы «модель думает…» не выглядело
 * зависанием: локальная модель отвечает минуты, и счётчик показывает,
 * что ожидание идёт, а не встало. */
export function useElapsed(running: boolean): number {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => {
    if (!running) return
    setSeconds(0)
    const started = Date.now()
    const id = setInterval(() => setSeconds(Math.round((Date.now() - started) / 1000)), 1000)
    return () => clearInterval(id)
  }, [running])
  return seconds
}

/** Текст ответа: блоки ```…``` показываются моноширинно, чтобы команду
 * было видно и можно было выделить целиком. */
function AIBody({ text }: { text: string }) {
  const parts = text.split(/```(?:bash|sh|shell)?\n?/)
  return (
    <>
      {parts.map((part, i) =>
        i % 2 === 1 ? (
          <pre key={i} className="diff mono small sensitive-area" style={{ whiteSpace: 'pre-wrap', margin: '0.3rem 0' }}>
            {part.replace(/\n?$/, '')}
          </pre>
        ) : (
          <p key={i} className="small" style={{ whiteSpace: 'pre-wrap', margin: '0.2rem 0' }}>
            {blurText(part.trim())}
          </p>
        ),
      )}
    </>
  )
}
