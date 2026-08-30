import { useState } from 'react'
import { Button } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, Loading } from './ui'
import PackageInstallModal from './PackageInstallModal'

/**
 * Start/stop x11vnc against this host's existing X11 desktop — one session
 * per host, no in-browser viewer: the returned password is for connecting
 * with an external VNC client (see handleVNCStart's own doc comment for
 * why — this only manages the server side). Sits in the Terminal page
 * alongside the shell, sharing its own admin+AllowMutations+TerminalEnabled
 * gating (canUse comes from the same check Terminal.tsx already makes for
 * the shell itself).
 */
export default function VNCPanel({ canUse }: { canUse: boolean }) {
  const { t } = useTranslation()
  const { data: status, reload: reloadStatus } = useApi<{ installed: boolean; running: boolean; port: number }>(
    '/system/vnc-status',
    10_000,
  )
  const { data: installStatus, reload: reloadInstallStatus } = useApi<{
    active: boolean
    finished: boolean
    succeeded: boolean
  }>('/system/vnc-install/status', 5_000)
  const [installOpen, setInstallOpen] = useState(false)
  const [installOutcome, setInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)
  const [busy, setBusy] = useState(false)
  // Shown exactly once, right after Start — same "you will not see this
  // again" handling as the hub's own bootstrap admin password. Cleared on
  // Stop and on unmount (component-local state, nothing to persist): a
  // fresh Start always means a fresh password anyway.
  const [credentials, setCredentials] = useState<{ password: string; port: number } | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function handleInstallFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>('/system/vnc-install/status').catch(
      () => null,
    )
    reloadInstallStatus()
    reloadStatus()
    setInstallOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
  }

  async function handleStart() {
    if (!window.confirm(t('vnc.confirmStart'))) return
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ password: string; port: number }>('/system/vnc/start', { method: 'POST' })
      setCredentials(res)
      reloadStatus()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function handleStop() {
    if (!window.confirm(t('vnc.confirmStop'))) return
    setBusy(true)
    setError(null)
    try {
      await api('/system/vnc/stop', { method: 'POST' })
      setCredentials(null)
      reloadStatus()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title={t('vnc.title')}>
      {error && <Banner kind="error">{error}</Banner>}
      {!status ? (
        <Loading what={t('vnc.title')} />
      ) : !status.installed ? (
        <>
          <p className="small muted">{t('vnc.notInstalledHint')}</p>
          <Button
            disabled={!canUse}
            loading={installStatus?.active}
            onClick={() => {
              setInstallOutcome(null)
              setInstallOpen(true)
            }}
          >
            {installStatus?.active ? t('vnc.installRunningOpen') : t('vnc.install')}
          </Button>
        </>
      ) : status.running ? (
        <>
          <p className="small">{t('vnc.runningOn', { port: status.port })}</p>
          {credentials && <Banner kind="warn">{t('vnc.passwordShownOnce', { password: credentials.password })}</Banner>}
          <Button danger disabled={!canUse} loading={busy} onClick={handleStop}>
            {t('vnc.stop')}
          </Button>
        </>
      ) : (
        <Button type="primary" disabled={!canUse} loading={busy} onClick={handleStart}>
          {t('vnc.start')}
        </Button>
      )}

      {installOpen && (
        <PackageInstallModal
          packageName="x11vnc"
          wsPath="/system/vnc-install/ws"
          onClose={() => setInstallOpen(false)}
          onFinished={handleInstallFinished}
          outcome={installOutcome}
        />
      )}
    </Card>
  )
}
