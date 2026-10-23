import { useEffect, useRef, useState } from 'react'
import { Button, Input, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import type { Job, JobLogLine } from '../types'
import { autoScriptName } from '../scriptName'
import { Banner, Modal } from './ui'
import { JobLogModal } from '../pages/Jobs'
import type { ScriptOrigin } from './AIExplain'

/**
 * «Сухой прогон» и «Выполнить» под сценарием из ответа модели (ассистент,
 * разбор находки). Щелчок открывает окно задания: что сделает сценарий
 * (хосты, шаги), вопросы при запуске, затем ход и журнал. «Выполнить» —
 * одно задание «сухой прогон → выполнение»; прошло — окно закрывается
 * само, в сообщении остаётся строка итога со ссылкой на журнал. Не
 * прошло — окно остаётся с ошибкой. Сценарий сохраняется сам (имя
 * латиницей, заметка об источнике). Запускает человек: модель ничего не
 * выполняет.
 */

interface Ask {
  key: string
  prompt: string
  secret: boolean
}
interface Check {
  steps: { line: number; host?: string; hosts?: string[]; text: string }[]
  asks: Ask[]
  issues: { line: number; error: string }[]
}

// Через сколько окно удачного «Выполнить» закрывается само.
const AUTO_CLOSE_MS = 2500

export function ScriptRunInline({
  content,
  origin,
  onAskAbout,
}: {
  content: string
  origin: ScriptOrigin
  /** Ассистент: «Спросить ассистента о результате» с итогом и журналом. */
  onAskAbout?: (text: string) => void
}) {
  const { t } = useTranslation()
  const [scriptID, setScriptID] = useState<number | null>(null)
  const [check, setCheck] = useState<Check | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  const [job, setJob] = useState<Job | null>(null)
  const [lines, setLines] = useState<JobLogLine[]>([])
  const [mode, setMode] = useState<'dry' | 'run' | null>(null)
  // Окно: какой запуск готовится или идёт (null — закрыто).
  const [dialog, setDialog] = useState<'dry' | 'run' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [fullLog, setFullLog] = useState(false)
  const lastSeq = useRef(0)
  const logRef = useRef<HTMLPreElement | null>(null)

  // Что сценарий спросит при запуске и какие шаги — для окна.
  useEffect(() => {
    void api<Check>('/hub/scripts/check', { method: 'POST', body: { content } }).then(setCheck).catch(() => undefined)
  }, [content])

  // Ход задания: состояние и новые строки журнала.
  useEffect(() => {
    if (!job || (job.status !== 'queued' && job.status !== 'running')) return
    const timer = setInterval(async () => {
      try {
        const res = await api<{ job: Job; lines: JobLogLine[] }>(`/hub/jobs/${job.id}/log?after=${lastSeq.current}`)
        if (res.lines.length > 0) {
          lastSeq.current = res.lines[res.lines.length - 1].seq
          setLines((prev) => [...prev, ...res.lines].slice(-300))
        }
        setJob(res.job)
      } catch {
        /* следующий опрос */
      }
    }, 1500)
    return () => clearInterval(timer)
  }, [job])

  useEffect(() => {
    logRef.current?.scrollTo?.({ top: logRef.current.scrollHeight })
  }, [lines])

  const running = job !== null && (job.status === 'queued' || job.status === 'running')
  const finished = job !== null && !running
  const ok = job?.status === 'succeeded'

  // Удачное «Выполнить» закрывает окно само; сухой прогон читают — нет.
  useEffect(() => {
    if (!finished || !ok || mode !== 'run' || dialog === null) return
    const id = window.setTimeout(() => setDialog(null), AUTO_CLOSE_MS)
    return () => window.clearTimeout(id)
  }, [finished, ok, mode, dialog])

  async function ensureSaved(): Promise<number> {
    if (scriptID !== null) return scriptID
    const list = await api<{ scripts: { name: string }[] }>('/hub/scripts')
    const name = autoScriptName(origin.kind, content, new Set(list.scripts.map((s) => s.name)))
    const label = (origin.label ?? '').replace(/\s+/g, ' ').slice(0, 200)
    const note = origin.kind === 'finding' ? t('scripts.noteFromFinding', { label }) : t('scripts.noteFromAssistant', { label })
    const res = await api<{ id: number }>('/hub/scripts', { method: 'POST', body: { name, content, note, color: '', origin: 'ai' } })
    setScriptID(res.id)
    return res.id
  }

  async function start(dry: boolean) {
    setError(null)
    setLines([])
    setJob(null)
    lastSeq.current = 0
    setMode(dry ? 'dry' : 'run')
    try {
      const id = await ensureSaved()
      const res = await api<{ job_id: number }>(`/hub/scripts/${id}/run`, {
        method: 'POST',
        body: dry ? { values, dry_run: true } : { values, dry_first: true },
      })
      setJob(await api<Job>(`/hub/jobs/${res.job_id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setMode(null)
    }
  }

  const asksReady = (check?.asks ?? []).every((a) => (values[a.key] ?? '').trim() !== '')
  const hosts = check ? [...new Set(check.steps.flatMap((s) => (s.hosts && s.hosts.length > 0 ? s.hosts : s.host ? [s.host] : [])))] : []
  const modeLabel = mode === 'dry' ? t('scripts.dryRun') : t('scriptInline.dryThenRun')
  const askAbout = () =>
    onAskAbout?.(
      t('scriptInline.askAbout', {
        mode: modeLabel,
        status: job?.status ?? '',
        error: job?.error ?? '',
        log: lines.slice(-60).map((l) => l.text).join('\n'),
      }),
    )
  const resultText = job && finished ? (ok ? t(mode === 'dry' ? 'scriptInline.dryOK' : 'scriptInline.runOK') : t('scriptInline.failed', { error: job.error ?? '' })) : ''

  return (
    <div className="col" style={{ gap: '0.35rem', margin: '0.3rem 0' }}>
      <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap', alignItems: 'center' }}>
        <Button size="small" disabled={!check || running} loading={running && mode === 'dry'} onClick={() => setDialog('dry')}>
          {t('scripts.dryRun')}
        </Button>
        <Button size="small" type="primary" danger disabled={!check || running} loading={running && mode === 'run'} onClick={() => setDialog('run')} title={t('scriptInline.runHint')}>
          {t('scriptInline.run')}
        </Button>
        {/* Итог в сообщении — после закрытия окна; ссылка открывает его снова. */}
        {job && (
          <Button size="small" type="link" style={{ padding: 0 }} onClick={() => setDialog(mode)}>
            <Tag color={ok ? 'success' : finished ? 'error' : 'processing'} style={{ margin: 0 }}>
              {finished ? (ok ? '✓ ' : '✗ ') : ''}
              {modeLabel} · {finished ? t(`jobs.status.${job.status}`, { defaultValue: job.status }) : `${job.step}/${job.steps}`}
            </Tag>{' '}
            {t('scriptInline.log')}
          </Button>
        )}
      </div>
      {dialog && (
        <Modal title={t(dialog === 'dry' ? 'scriptInline.dryTitle' : 'scriptInline.confirmTitle')} onClose={() => setDialog(null)} width={820} maskClosable={!running}>
          <div className="col" style={{ gap: '0.6rem' }}>
            {!job || mode !== dialog ? (
              <>
                <span>{t(dialog === 'dry' ? 'scriptInline.dryBody' : 'scriptInline.confirmBody', { hosts: hosts.join(', ') || '—' })}</span>
                <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', margin: 0, maxHeight: '30vh', overflow: 'auto' }}>
                  {(check?.steps ?? []).map((s) => s.text).join('\n')}
                </pre>
                {(check?.asks ?? []).length > 0 && (
                  <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap' }}>
                    {check!.asks.map((a) =>
                      a.secret ? (
                        <Input.Password key={a.key} style={{ width: 240 }} placeholder={a.prompt} value={values[a.key] ?? ''} autoComplete="new-password" onChange={(e) => setValues({ ...values, [a.key]: e.target.value })} />
                      ) : (
                        <Input key={a.key} style={{ width: 240 }} placeholder={a.prompt} value={values[a.key] ?? ''} onChange={(e) => setValues({ ...values, [a.key]: e.target.value })} />
                      ),
                    )}
                  </div>
                )}
                {error && <Banner kind="error">{error}</Banner>}
                <div className="row" style={{ gap: '0.4rem' }}>
                  <Button type="primary" danger={dialog === 'run'} disabled={!asksReady || running} onClick={() => void start(dialog === 'dry')}>
                    {t(dialog === 'dry' ? 'scriptInline.dryStart' : 'scriptInline.run')}
                  </Button>
                </div>
              </>
            ) : (
              <>
                <div className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
                  <Tag color={ok ? 'success' : finished ? 'error' : 'processing'} style={{ margin: 0 }}>
                    {modeLabel} · {job.steps > 0 ? `${job.step}/${job.steps} · ` : ''}
                    {t(`jobs.status.${job.status}`, { defaultValue: job.status })}
                  </Tag>
                  <Button size="small" type="link" onClick={() => setFullLog(true)}>
                    {t('scriptInline.openJob')}
                  </Button>
                </div>
                <pre ref={logRef} className="diff mono small" style={{ whiteSpace: 'pre-wrap', maxHeight: '45vh', minHeight: '8rem', overflow: 'auto', margin: 0 }}>
                  {lines.map((l) => l.text).join('\n')}
                </pre>
                {finished && <Banner kind={ok ? 'success' : 'error'}>{resultText}</Banner>}
                {finished && ok && mode === 'run' && <span className="small muted">{t('scriptInline.autoClose')}</span>}
                {finished && onAskAbout && (
                  <div>
                    <Button
                      size="small"
                      onClick={() => {
                        askAbout()
                        setDialog(null)
                      }}
                    >
                      {t('scriptInline.askButton')}
                    </Button>
                  </div>
                )}
              </>
            )}
          </div>
        </Modal>
      )}
      {fullLog && job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setFullLog(false)} />}
    </div>
  )
}
