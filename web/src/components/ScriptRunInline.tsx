import { useEffect, useRef, useState } from 'react'
import { Button, Input, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import type { Job, JobLogLine } from '../types'
import { autoScriptName } from '../scriptName'
import { Banner } from './ui'
import { confirmAction } from './confirm'
import { JobLogModal } from '../pages/Jobs'
import type { ScriptOrigin } from './AIExplain'

/**
 * «Сухой прогон» и «Выполнить» прямо под сценарием из ответа модели
 * (ассистент, разбор находки): сценарий сохраняется сам (имя латиницей,
 * заметка об источнике), «Выполнить» — одно задание «сухой прогон →
 * выполнение» после подтверждения; ход и итог — тут же, без перехода в
 * «Сценарии». Запускает человек: модель ничего не выполняет.
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
  const [error, setError] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const lastSeq = useRef(0)

  // Что сценарий спросит при запуске и какие шаги — для подтверждения.
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
    if (!check) return
    if (!dry) {
      const hosts = [...new Set(check.steps.flatMap((s) => (s.hosts && s.hosts.length > 0 ? s.hosts : s.host ? [s.host] : [])))]
      const ok = await confirmAction(
        <div className="col" style={{ gap: '0.3rem' }}>
          <span>{t('scriptInline.confirmBody', { hosts: hosts.join(', ') || '—' })}</span>
          <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>
            {check.steps.map((s) => s.text).join('\n')}
          </pre>
        </div>,
        { title: t('scriptInline.confirmTitle'), okText: t('scriptInline.run'), danger: true },
      )
      if (!ok) return
    }
    setError(null)
    setLines([])
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

  const running = job !== null && (job.status === 'queued' || job.status === 'running')
  const asksReady = (check?.asks ?? []).every((a) => (values[a.key] ?? '').trim() !== '')
  const finished = job !== null && !running
  const ok = job?.status === 'succeeded'
  return (
    <div className="col" style={{ gap: '0.35rem', margin: '0.3rem 0' }}>
      {(check?.asks ?? []).length > 0 && !running && (
        <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap' }}>
          {check!.asks.map((a) =>
            a.secret ? (
              <Input.Password key={a.key} size="small" style={{ width: 220 }} placeholder={a.prompt} value={values[a.key] ?? ''} autoComplete="new-password" onChange={(e) => setValues({ ...values, [a.key]: e.target.value })} />
            ) : (
              <Input key={a.key} size="small" style={{ width: 220 }} placeholder={a.prompt} value={values[a.key] ?? ''} onChange={(e) => setValues({ ...values, [a.key]: e.target.value })} />
            ),
          )}
        </div>
      )}
      <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap', alignItems: 'center' }}>
        <Button size="small" disabled={!check || running || !asksReady} loading={running && mode === 'dry'} onClick={() => void start(true)}>
          {t('scripts.dryRun')}
        </Button>
        <Button size="small" type="primary" danger disabled={!check || running || !asksReady} loading={running && mode === 'run'} onClick={() => void start(false)} title={t('scriptInline.runHint')}>
          {t('scriptInline.run')}
        </Button>
        {job && (
          <>
            <Tag color={ok ? 'success' : finished ? 'error' : 'processing'} style={{ margin: 0 }}>
              {mode === 'dry' ? t('scripts.dryRun') : t('scriptInline.dryThenRun')} · {job.steps > 0 ? `${job.step}/${job.steps} · ` : ''}
              {t(`jobs.status.${job.status}`, { defaultValue: job.status })}
            </Tag>
            <Button size="small" type="link" onClick={() => setOpen(true)}>
              {t('scriptInline.openJob')}
            </Button>
          </>
        )}
      </div>
      {error && <Banner kind="error">{error}</Banner>}
      {job && lines.length > 0 && (
        <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', maxHeight: '14rem', overflow: 'auto', margin: 0 }}>
          {lines.slice(-40).map((l) => l.text).join('\n')}
        </pre>
      )}
      {finished && (
        <Banner kind={ok ? 'success' : 'error'}>
          {ok ? t(mode === 'dry' ? 'scriptInline.dryOK' : 'scriptInline.runOK') : t('scriptInline.failed', { error: job.error ?? '' })}
        </Banner>
      )}
      {finished && onAskAbout && (
        <div>
          <Button
            size="small"
            onClick={() =>
              onAskAbout(
                t('scriptInline.askAbout', {
                  mode: mode === 'dry' ? t('scripts.dryRun') : t('scriptInline.dryThenRun'),
                  status: job.status,
                  error: job.error ?? '',
                  log: lines.slice(-60).map((l) => l.text).join('\n'),
                }),
              )
            }
          >
            {t('scriptInline.askButton')}
          </Button>
        </div>
      )}
      {open && job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setOpen(false)} />}
    </div>
  )
}
