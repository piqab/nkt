import { useEffect, useState } from 'react'
import { Button } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HubVersionInfo, HubVulnDBInfo } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, formatRelative } from '../components/ui'

/**
 * The hub's own "About" page — its running version, whatever
 * versionCheckLoop last learned from GitHub Releases, and the one action
 * that applies it. Reachable from the host picker screen (see App.tsx),
 * alongside "Хосты" — this is about the hub itself, not any managed host,
 * so it lives at that level rather than inside the per-host NAV.
 *
 * No client-side admin gating here, matching Hosts.tsx's own convention:
 * the buttons are shown to everyone and a non-admin's click simply comes
 * back as a server error surfaced through the same notice banner every
 * other action here already uses.
 */
export default function About() {
  const { t } = useTranslation()
  const version = useApi<HubVersionInfo>('/hub/version', 5 * 60_000)
  const [checking, setChecking] = useState(false)
  const [updating, setUpdating] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)

  // 5s while a refresh is actually running (rare — background-refreshed
  // every NKT_HUB_VULNDB_REFRESH_INTERVAL, 12h by default) so a manual
  // "Обновить сейчас" click's progress is visible promptly; 60s otherwise,
  // matching Vulnerabilities.tsx's own scanning/idle poll-rate split.
  const [vulnDBFast, setVulnDBFast] = useState(false)
  const vulndb = useApi<HubVulnDBInfo>('/hub/vulndb', vulnDBFast ? 5_000 : 60_000)
  const [vulnDBBusy, setVulnDBBusy] = useState(false)

  useEffect(() => {
    setVulnDBFast(!!vulndb.data?.refreshing)
  }, [vulndb.data?.refreshing])

  async function refreshVulnDB() {
    setVulnDBBusy(true)
    setNotice(null)
    try {
      await api('/hub/vulndb/refresh', { method: 'POST' })
      setVulnDBFast(true)
      await vulndb.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setVulnDBBusy(false)
    }
  }

  async function checkNow() {
    setChecking(true)
    setNotice(null)
    try {
      await api('/hub/version/check', { method: 'POST' })
      await version.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setChecking(false)
    }
  }

  async function applyUpdate() {
    if (!window.confirm(t('about.confirmUpdate', { version: version.data?.latest }))) return
    setUpdating(true)
    setNotice(null)
    try {
      await api('/hub/update', { method: 'POST' })
      // The hub restarts itself mid-flight from here on — nothing in this
      // request/response cycle can watch that same process finish, so
      // "restarting" hands off to the polling effect below instead of
      // reporting a normal success/failure from this call.
      setRestarting(true)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      setUpdating(false)
    }
  }

  // Polls /api/health from a clean slate once an update has been triggered
  // — the hub's own restart kills the connection this very request was on,
  // so there is no "job" left to poll the way a managed host's install job
  // works. A full page reload (not just a state update) once it answers
  // again is deliberate: it makes App.tsx re-fetch /auth/me from scratch,
  // which is what actually picks up the new hub_version everywhere else
  // in the UI that shows it (e.g. Hosts.tsx's own outdated-host badges).
  useEffect(() => {
    if (!restarting) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const res = await fetch('/api/health', { cache: 'no-store' })
        if (!cancelled && res.ok) {
          window.location.reload()
          return
        }
      } catch {
        // Still down — expected for most of the restart window, keep polling.
      }
      if (!cancelled) timer = setTimeout(poll, 2000)
    }
    timer = setTimeout(poll, 3000)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [restarting])

  const info = version.data

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('about.title')}
            <InfoHint>{t('about.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={version.error} />
      {notice && <Banner kind={notice.kind}>{notice.text}</Banner>}

      <Card title={t('about.hubVersionTitle')}>
        {version.loading && !info ? (
          <Loading what={t('about.loadingVersion')} />
        ) : restarting ? (
          <div>{t('about.restarting')}</div>
        ) : (
          <>
            <div className="row" style={{ gap: '2rem', flexWrap: 'wrap' }}>
              <div>
                <div className="small muted">{t('about.currentVersion')}</div>
                <div className="mono" style={{ fontSize: '1.1rem' }}>
                  {info?.current}
                </div>
              </div>
              {info?.latest && (
                <div>
                  <div className="small muted">{t('about.latestVersion')}</div>
                  <div className="mono" style={{ fontSize: '1.1rem' }}>
                    {info.latest}
                  </div>
                </div>
              )}
              {info?.checked_at && (
                <div>
                  <div className="small muted">{t('about.checkedAt')}</div>
                  <div className="small">{formatRelative(info.checked_at)}</div>
                </div>
              )}
            </div>

            {info?.check_error && (
              <div className="small" style={{ color: 'var(--status-warning)', marginTop: '0.5rem' }}>
                {t('about.checkFailed', { error: info.check_error })}
              </div>
            )}

            <div className="row" style={{ marginTop: '1rem', gap: '0.5rem' }}>
              <Button onClick={checkNow} loading={checking}>
                {t('about.checkNow')}
              </Button>
              {info?.update_available && info.updatable && (
                <Button type="primary" onClick={applyUpdate} loading={updating}>
                  {t('about.updateTo', { version: info.latest })}
                </Button>
              )}
            </div>

            {info?.update_available && !info.updatable && (
              <div className="small muted" style={{ marginTop: '0.75rem' }}>
                {t('about.updatableFalse')}
              </div>
            )}
          </>
        )}
      </Card>

      <Card title={t('about.vulnDBTitle')} subtitle={t('about.vulnDBHint')}>
        {vulndb.loading && !vulndb.data ? (
          <Loading what={t('about.loadingVulnDB')} />
        ) : (
          <>
            <div className="row" style={{ gap: '2rem', flexWrap: 'wrap' }}>
              <div>
                <div className="small muted">{t('about.vulnDBStatus')}</div>
                <div>
                  {vulndb.data?.refreshing
                    ? t('about.vulnDBRefreshing')
                    : vulndb.data?.available
                      ? t('about.vulnDBReady')
                      : t('about.vulnDBNotReady')}
                </div>
              </div>
              {vulndb.data?.updated_at && (
                <div>
                  <div className="small muted">{t('about.checkedAt')}</div>
                  <div className="small">{formatRelative(vulndb.data.updated_at)}</div>
                </div>
              )}
            </div>

            {vulndb.data?.progress && <div className="small muted" style={{ marginTop: '0.5rem' }}>{vulndb.data.progress}</div>}
            {vulndb.data?.error && (
              <div className="small" style={{ color: 'var(--status-warning)', marginTop: '0.5rem' }}>
                {t('about.checkFailed', { error: vulndb.data.error })}
              </div>
            )}

            <div className="row" style={{ marginTop: '1rem' }}>
              <Button onClick={refreshVulnDB} loading={vulnDBBusy || vulndb.data?.refreshing}>
                {t('about.vulnDBRefreshNow')}
              </Button>
            </div>
          </>
        )}
      </Card>
    </>
  )
}
