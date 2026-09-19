import { useEffect, useState } from 'react'
import { Button, Checkbox, InputNumber } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { usePrivacy } from '../privacy'
import type { HubVersionInfo, HubVulnDBInfo } from '../types'

interface AptCacheInfo {
  available: boolean
  entries: number
  size_bytes: number
  max_bytes: number
  hits: number
  misses: number
  bytes_served: number
  bytes_fetched: number
  connected_hosts: number
}
import { Banner, Card, ErrorNote, InfoHint, Loading, formatBytesShort, formatRelative } from '../components/ui'
import { confirmAction } from '../components/confirm'

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
  const [privacy, setPrivacy] = usePrivacy()
  const version = useApi<HubVersionInfo>('/hub/version', 5 * 60_000)
  const [checking, setChecking] = useState(false)
  const [updating, setUpdating] = useState(false)
  const [rollingBack, setRollingBack] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)

  // 5s while a refresh is actually running (rare — background-refreshed
  // every NKT_HUB_VULNDB_REFRESH_INTERVAL, 12h by default) so a manual
  // "Обновить сейчас" click's progress is visible promptly; 60s otherwise,
  // matching Vulnerabilities.tsx's own scanning/idle poll-rate split.
  const [vulnDBFast, setVulnDBFast] = useState(false)
  const vulndb = useApi<HubVulnDBInfo>('/hub/vulndb', vulnDBFast ? 5_000 : 60_000)
  const [vulnDBBusy, setVulnDBBusy] = useState(false)
  const [clamDBFast, setClamDBFast] = useState(false)
  const clamdb = useApi<HubVulnDBInfo & { size_bytes?: number }>('/hub/clamdb', clamDBFast ? 5_000 : 60_000)
  const [clamDBBusy, setClamDBBusy] = useState(false)
  const aptcache = useApi<AptCacheInfo>('/hub/aptcache', 30_000)
  const [aptMax, setAptMax] = useState<number | null>(null)
  const [aptBusy, setAptBusy] = useState<string | null>(null)
  async function aptCall(path: string, body?: unknown) {
    setAptBusy(path)
    try {
      await api(path, { method: 'POST', body })
      await aptcache.reload()
      setAptMax(null)
    } finally {
      setAptBusy(null)
    }
  }
  useEffect(() => {
    setClamDBFast(!!clamdb.data?.refreshing)
  }, [clamdb.data?.refreshing])
  async function refreshClamDB() {
    setClamDBBusy(true)
    try {
      await api('/hub/clamdb/refresh', { method: 'POST' })
      setClamDBFast(true)
      await clamdb.reload()
    } finally {
      setClamDBBusy(false)
    }
  }

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

  // Заход в раздел — сам повод спросить GitHub: кнопка остаётся для
  // повтора, но первый ответ должен быть свежим, а не часовой давности с
  // фонового цикла. Один раз на открытие, а не при каждом опросе.
  useEffect(() => {
    void checkNow()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- только при открытии раздела
  }, [])

  async function applyUpdate() {
    if (!(await confirmAction(t('about.confirmUpdate', { version: version.data?.latest })))) return
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

  async function rollback() {
    if (!(await confirmAction(t('about.confirmRollback', { current: version.data?.current, version: version.data?.previous })))) return
    setRollingBack(true)
    setNotice(null)
    try {
      await api('/hub/rollback', { method: 'POST' })
      setRestarting(true)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      setRollingBack(false)
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
      {notice && (
        <Banner kind={notice.kind} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}

      {/* Приватный режим — настройка браузера, живёт здесь рядом с
          остальными настройками хаба; включённый виден по оранжевому
          «nkt» в шапке. */}
      <Card title={t('about.privacyTitle')} subtitle={t('app.privacyHint')}>
        <Checkbox checked={privacy} onChange={(e) => setPrivacy(e.target.checked)}>
          {t('app.privacy')}
        </Checkbox>
      </Card>

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
              {info?.previous && (
                <div>
                  <div className="small muted">{t('about.previousVersion')}</div>
                  <div className="mono" style={{ fontSize: '1.1rem' }}>
                    {info.previous}
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
              {info?.previous && info.updatable && (
                <Button danger onClick={rollback} loading={rollingBack} disabled={updating}>
                  {t('about.rollbackTo', { version: info.previous })}
                </Button>
              )}
            </div>
            {info?.previous && info.updatable && (
              <div className="small muted" style={{ marginTop: '0.5rem' }}>
                {t('about.rollbackHint')}
              </div>
            )}

            {info?.update_available && !info.updatable && (
              <div className="small muted" style={{ marginTop: '0.75rem' }}>
                {t('about.updatableFalse')}
              </div>
            )}

            {/* Описание новой версии — то, что стоит прочитать до нажатия
                «обновить». Показывается только когда обновление реально есть:
                текст описывает не установленную версию, а ту, которой ещё
                нет. Рендерится как обычный текст с переносами, не как
                markdown: тело релиза правится на стороне GitHub, и
                интерпретировать его разметку здесь незачем. */}
            {info?.update_available && info.notes && (
              <div style={{ marginTop: '1rem' }}>
                <div className="small muted">
                  {t('about.whatsNew', { version: info.latest })}
                </div>
                <pre
                  style={{
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word',
                    margin: '0.35rem 0 0',
                    padding: '0.6rem 0.75rem',
                    maxHeight: '18rem',
                    overflowY: 'auto',
                    background: 'var(--wash)',
                    border: '1px solid var(--border)',
                    borderRadius: 'var(--radius-sm)',
                    fontFamily: 'inherit',
                  }}
                >
                  {info.notes}
                </pre>
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

      <Card title={t('about.aptCacheTitle')} subtitle={t('about.aptCacheHint')}>
        {aptcache.loading && !aptcache.data ? (
          <Loading what={t('about.aptCacheTitle')} />
        ) : !aptcache.data?.available ? (
          <p className="small muted">{t('about.aptCacheUnavailable')}</p>
        ) : (
          <>
            <div className="row" style={{ gap: '2rem', flexWrap: 'wrap' }}>
              <div>
                <div className="small muted">{t('about.aptCacheSize')}</div>
                <div>
                  {formatBytesShort(aptcache.data.size_bytes)} · {t('about.aptCacheEntries', { count: aptcache.data.entries })}
                </div>
              </div>
              <div>
                <div className="small muted">{t('about.aptCacheHits')}</div>
                <div>
                  {aptcache.data.hits} / {aptcache.data.misses}
                </div>
              </div>
              <div>
                <div className="small muted">{t('about.aptCacheTraffic')}</div>
                <div className="small">{t('about.aptCacheTrafficValue', { served: formatBytesShort(aptcache.data.bytes_served), fetched: formatBytesShort(aptcache.data.bytes_fetched) })}</div>
              </div>
              <div>
                <div className="small muted">{t('about.aptCacheHosts')}</div>
                <div>{aptcache.data.connected_hosts}</div>
              </div>
            </div>
            <div className="row" style={{ marginTop: '1rem', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <span className="small">{t('about.aptCacheLimit')}</span>
              <InputNumber min={0} max={10000} value={aptMax ?? Math.round(aptcache.data.max_bytes / 2 ** 30)} onChange={(v) => setAptMax(v ?? 0)} addonAfter={t('about.gb')} style={{ width: '9rem' }} />
              <Button size="small" disabled={aptMax === null} loading={aptBusy === '/hub/aptcache/settings'} onClick={() => void aptCall('/hub/aptcache/settings', { max_gb: aptMax })}>
                {t('common.save')}
              </Button>
              <Button
                size="small"
                danger
                loading={aptBusy === '/hub/aptcache/clear'}
                onClick={async () => {
                  if (!(await confirmAction(t('about.aptCacheClearConfirm')))) return
                  await aptCall('/hub/aptcache/clear')
                }}
              >
                {t('about.aptCacheClear')}
              </Button>
            </div>
          </>
        )}
      </Card>

      <Card title={t('about.clamDBTitle')} subtitle={t('about.clamDBHint')}>
        {clamdb.loading && !clamdb.data ? (
          <Loading what={t('about.clamDBTitle')} />
        ) : (
          <>
            <div className="row" style={{ gap: '2rem', flexWrap: 'wrap' }}>
              <div>
                <div className="small muted">{t('about.vulnDBStatus')}</div>
                <div>{clamdb.data?.refreshing ? t('about.clamDBRefreshing') : clamdb.data?.available ? t('about.clamDBReady') : t('about.clamDBNotReady')}</div>
              </div>
              {clamdb.data?.updated_at && (
                <div>
                  <div className="small muted">{t('about.checkedAt')}</div>
                  <div className="small">{formatRelative(clamdb.data.updated_at)}</div>
                </div>
              )}
              {clamdb.data?.size_bytes ? (
                <div>
                  <div className="small muted">{t('about.clamDBSize')}</div>
                  <div className="small">{formatBytesShort(clamdb.data.size_bytes)}</div>
                </div>
              ) : null}
            </div>
            {clamdb.data?.progress && <div className="small muted" style={{ marginTop: '0.5rem' }}>{clamdb.data.progress}</div>}
            {clamdb.data?.error && (
              <div className="small" style={{ color: 'var(--status-warning)', marginTop: '0.5rem' }}>
                {t('about.checkFailed', { error: clamdb.data.error })}
              </div>
            )}
            <div className="row" style={{ marginTop: '1rem' }}>
              <Button onClick={refreshClamDB} loading={clamDBBusy || clamdb.data?.refreshing}>
                {t('about.vulnDBRefreshNow')}
              </Button>
            </div>
          </>
        )}
      </Card>
    </>
  )
}
