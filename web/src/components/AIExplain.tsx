import { useEffect, useState } from 'react'
import { Button, Spin, Tooltip } from 'antd'
import { BulbOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, hostScope, LOCAL_HOST_ID } from '../api'
import { Banner, Modal } from './ui'
import { blurText } from '../privacy'

/**
 * «Объяснить» — одна кнопка для любой строки, о которой можно спросить
 * модель: находка, уязвимость, вредоносное, оповещение хаба, ошибка
 * задания.
 *
 * Запрос всегда уходит на хаб (/hub/ai/…, см. api.ts's unscoped): ключ и
 * адрес модели живут только там, одни на все хосты, и хосту наружу
 * ничего не нужно — у него может не быть интернета.
 *
 * Модель ничего не выполняет: команды показываются для копирования,
 * применяются обычными кнопками nkt. Приписка об этом стоит под каждым
 * ответом — не из перестраховки, а потому что ответ выглядит уверенно
 * независимо от того, прав он или нет.
 */

export interface AIContext {
  kind: 'finding' | 'vuln' | 'malware' | 'event' | 'job-error'
  title: string
  detail?: string
  suggestion?: string
  severity?: string
  service?: string
  object?: string
  file?: string
  line?: number
}

interface AISection {
  title: string
  body: string
}

interface AIAnswer {
  answer: string
  sections: AISection[]
  model: string
  cached: boolean
  prompt: string
  notice: string
}

export function AIExplain({ ctx, disabled }: { ctx: AIContext; disabled?: boolean }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [answer, setAnswer] = useState<AIAnswer | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [showPrompt, setShowPrompt] = useState(false)
  const elapsed = useElapsed(busy)

  async function ask() {
    setOpen(true)
    if (answer || busy) return
    setBusy(true)
    setError(null)
    try {
      const res = await api<AIAnswer>('/hub/ai/explain', {
        method: 'POST',
        // Настоящий предел — время ожидания в настройках ИИ на хабе (до
        // получаса); здесь только запас, чтобы общий таймаут запросов в
        // 30 с не оборвал ответ раньше модели.
        timeoutMs: AI_REQUEST_TIMEOUT_MS,
        body: {
          ...ctx,
          // Какому хосту принадлежит находка — по нему хаб добавит в
          // запрос, что там рядом (порты, контейнеры, firewall).
          host_id: hostScope.id !== null && hostScope.id !== LOCAL_HOST_ID ? hostScope.id : 0,
        },
      })
      setAnswer(res)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Tooltip title={t('ai.explainHint')}>
        <Button type="text" size="small" icon={<BulbOutlined />} disabled={disabled} onClick={() => void ask()} />
      </Tooltip>
      {open && (
        <Modal title={blurText(ctx.title)} onClose={() => setOpen(false)} width={760}>
          {busy && !answer ? (
            <Thinking seconds={elapsed} />
          ) : error ? (
            <Banner kind="error">{error}</Banner>
          ) : answer ? (
            <div className="col">
              {answer.sections.map((s, i) => (
                <div key={i}>
                  {s.title && <h3 style={{ marginBottom: '0.3rem' }}>{s.title}</h3>}
                  <AIBody text={s.body} />
                </div>
              ))}
              <div className="small muted">
                {answer.notice}
                {answer.cached && <> · {t('ai.fromCache')}</>}
                <Button type="link" size="small" onClick={() => setShowPrompt((v) => !v)}>
                  {showPrompt ? t('ai.hidePrompt') : t('ai.showPrompt')}
                </Button>
              </div>
              {showPrompt && (
                <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>
                  {answer.prompt}
                </pre>
              )}
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
