import { useEffect, useState } from 'react'
import { OsIcon } from './OsIcon'
import type { OSInfo } from '../types'
import { Button, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { ApiError, LOCAL_HOST_ID, api } from '../api'
import type { Job } from '../types'
import { JobLogModal } from '../pages/Jobs'

/** Ответ хоста GET /compose/engine. */
export interface ComposeEngine {
  engine: string
  version?: string
  compose: boolean
  compose_version?: string
  compose_error?: string
  installable: boolean
}

/** Путь к API хоста с хаба: машина хаба — /hosts/local. */
export const hostBase = (id: number) => `/hosts/${id === LOCAL_HOST_ID ? 'local' : id}`

/**
 * Чем хост поднимает compose-стеки — проверяется сразу при выборе хоста:
 * docker или podman и работает ли compose. Нет — кнопка «Установить
 * Docker» (фоновое задание хоста в стандартном окне журнала).
 */
export function ComposeEngineStatus({ hostId, name, os, admin }: { hostId: number; name: string; os?: OSInfo; admin: boolean }) {
  const { t } = useTranslation()
  const [state, setState] = useState<{ kind: 'loading' } | { kind: 'ok'; e: ComposeEngine } | { kind: 'old' } | { kind: 'error'; text: string }>({ kind: 'loading' })
  const [job, setJob] = useState<Job | null>(null)
  const [installError, setInstallError] = useState<string | null>(null)
  const [seq, setSeq] = useState(0)
  const base = hostBase(hostId)

  useEffect(() => {
    let cancelled = false
    setState({ kind: 'loading' })
    api<ComposeEngine>(`${base}/compose/engine`)
      .then((e) => !cancelled && setState({ kind: 'ok', e }))
      .catch((err) => {
        if (cancelled) return
        if (err instanceof ApiError && (err.status === 404 || err.status === 405)) setState({ kind: 'old' })
        else setState({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      })
    return () => {
      cancelled = true
    }
  }, [base, seq])

  async function install() {
    setInstallError(null)
    try {
      const res = await api<{ job_id?: number }>(`${base}/system/docker-install/ws?job=1`, { method: 'POST' })
      if (typeof res.job_id === 'number') setJob(await api<Job>(`${base}/jobs/${res.job_id}`))
      else setSeq((n) => n + 1)
    } catch (err) {
      setInstallError(err instanceof Error ? err.message : String(err))
    }
  }

  let body
  if (state.kind === 'loading') body = <Tag>{t('composeEngine.checking')}</Tag>
  else if (state.kind === 'old') body = <Tag>{t('composeEngine.old')}</Tag>
  else if (state.kind === 'error')
    body = (
      <Tooltip title={state.text}>
        <Tag color="warning">{t('composeEngine.unreachable')}</Tag>
      </Tooltip>
    )
  else {
    const e = state.e
    const ok = e.engine !== '' && e.compose
    body = (
      <>
        {ok ? (
          <Tooltip title={[e.version, e.compose_version].filter(Boolean).join(' · ') || undefined}>
            <Tag color="success">{t('composeEngine.ok', { engine: e.engine })}</Tag>
          </Tooltip>
        ) : e.engine === '' ? (
          <Tag color="error">{t('composeEngine.none')}</Tag>
        ) : (
          <Tooltip title={e.compose_error}>
            <Tag color="error">{t('composeEngine.noCompose', { engine: e.engine })}</Tag>
          </Tooltip>
        )}
        {!ok && admin && (e.engine === '' || e.engine === 'docker') && (
          <Tooltip title={e.installable ? t('composeEngine.installHint') : t('composeEngine.notInstallable')}>
            <Button size="small" disabled={!e.installable} onClick={() => void install()}>
              {e.engine === '' ? t('composeEngine.install') : t('composeEngine.installCompose')}
            </Button>
          </Tooltip>
        )}
      </>
    )
  }
  return (
    <span className="row" style={{ gap: '0.3rem', alignItems: 'center', flexWrap: 'wrap' }}>
      <span className="small">
        <OsIcon os={os} size={12} />
        {name}:
      </span>
      {body}
      {installError && <span className="small" style={{ color: 'var(--status-error)' }}>{installError}</span>}
      {job && (
        <JobLogModal
          job={job}
          scope={base}
          onClose={() => setJob(null)}
          onDone={() => setSeq((n) => n + 1)}
        />
      )}
    </span>
  )
}
