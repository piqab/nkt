import { useEffect, useMemo, useState } from 'react'
import { Button, Checkbox, Input, Pagination, Select, Tag } from 'antd'
import { CheckOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, Spinner } from './ui'
import { RowAction } from './RowAction'
import PackageInstallModal from './PackageInstallModal'

/**
 * Локали и синхронизация времени — две карточки раздела «Системные
 * настройки». Локали — ячейками, как пакеты: имён пять сотен, по одному в
 * строку их не просмотреть; поиск выстраивает и раскрашивает их так же,
 * как поиск пакетов (точное имя, начало, вхождение).
 */

interface LocaleInfo {
  name: string
  charset?: string
  installed: boolean
  current: boolean
}

interface Locales {
  locales: LocaleInfo[]
  current?: string
  can_generate: boolean
  note?: string
}

type Match = 'exact' | 'prefix' | 'name'
const MATCH_RANK: Record<Match, number> = { exact: 0, prefix: 1, name: 2 }
const GRID_PAGE = 120

function rankLocales(rows: LocaleInfo[], query: string): (LocaleInfo & { match?: Match })[] {
  const q = query.trim().toLowerCase()
  if (!q) return rows
  const out: (LocaleInfo & { match: Match })[] = []
  for (const l of rows) {
    const name = l.name.toLowerCase()
    const match: Match | undefined = name === q ? 'exact' : name.startsWith(q) ? 'prefix' : name.includes(q) ? 'name' : undefined
    if (match) out.push({ ...l, match })
  }
  return out.sort((a, b) => MATCH_RANK[a.match] - MATCH_RANK[b.match] || a.name.localeCompare(b.name))
}

export function LocaleCard({ canUse }: { canUse: boolean }) {
  const { t } = useTranslation()
  const locales = useApi<Locales>('/system/locales')
  const [query, setQuery] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const [page, setPage] = useState(1)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const rows = useMemo(() => rankLocales(locales.data?.locales ?? [], query), [locales.data, query])
  useEffect(() => setPage(1), [rows])
  const visible = rows.slice((page - 1) * GRID_PAGE, page * GRID_PAGE)
  const installedCount = (locales.data?.locales ?? []).filter((l) => l.installed).length

  async function apply(body: Record<string, unknown>, key: string, done: string) {
    setBusy(key)
    setError(null)
    setNotice(null)
    try {
      await api('/system/locales', { method: 'POST', body, timeoutMs: 300_000 })
      setNotice(done)
      setPicked([])
      await locales.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <Card
      title={t('sysSettings.locales')}
      subtitle={t('sysSettings.localesHint', { current: locales.data?.current || '—', installed: installedCount })}
    >
      {locales.error && <Banner kind="error">{locales.error}</Banner>}
      {locales.data?.note && <Banner kind="warn">{locales.data.note}</Banner>}
      {error && <Banner kind="error">{error}</Banner>}
      {notice && (
        <Banner kind="info" onClose={() => setNotice(null)}>
          {notice}
        </Banner>
      )}
      <div className="filters" style={{ alignItems: 'center' }}>
        <Input
          allowClear
          size="small"
          style={{ flex: 1, minWidth: '12rem', maxWidth: '24rem' }}
          placeholder={t('sysSettings.localeSearch')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <span className="small muted">{t('sysSettings.localeCount', { shown: rows.length })}</span>
        {canUse && locales.data?.can_generate && (
          <Button
            size="small"
            type="primary"
            disabled={picked.length === 0 || busy !== null}
            loading={busy === 'gen'}
            onClick={() => apply({ generate: picked }, 'gen', t('sysSettings.localesGenerated', { count: picked.length }))}
          >
            {t('sysSettings.generateLocales', { count: picked.length })}
          </Button>
        )}
      </div>
      {locales.loading && !locales.data ? (
        <div className="small muted">
          <Spinner /> {t('sysSettings.localesLoading')}
        </div>
      ) : (
        <>
          <div className="pkg-grid" style={{ marginTop: '0.5rem' }}>
            {visible.map((l) => (
              <div key={l.name} className={`pkg-cell${l.match ? ` pkg-match-${l.match}` : ''}`}>
                <Checkbox
                  checked={picked.includes(l.name)}
                  disabled={!canUse || l.installed || !locales.data?.can_generate}
                  onChange={(e) => setPicked(e.target.checked ? [...picked, l.name] : picked.filter((n) => n !== l.name))}
                />
                <code className="mono" title={l.name}>
                  {l.name}
                </code>
                {l.charset && <span className="small muted pkg-version">{l.charset}</span>}
                {l.current ? (
                  <Tag color="blue">{t('sysSettings.localeCurrent')}</Tag>
                ) : (
                  l.installed && <Tag color="green">{t('sysSettings.localeInstalled')}</Tag>
                )}
                <span className="pkg-action">
                  {canUse && l.installed && !l.current && (
                    <RowAction
                      icon={<CheckOutlined />}
                      label={t('sysSettings.makeDefaultLocale')}
                      loading={busy === `def:${l.name}`}
                      disabled={busy !== null}
                      onClick={() => apply({ default: l.name }, `def:${l.name}`, t('sysSettings.localeSet', { name: l.name }))}
                    />
                  )}
                </span>
              </div>
            ))}
          </div>
          {rows.length > GRID_PAGE && (
            <Pagination
              size="small"
              style={{ marginTop: '0.5rem' }}
              current={page}
              pageSize={GRID_PAGE}
              total={rows.length}
              showSizeChanger={false}
              onChange={setPage}
            />
          )}
        </>
      )}
      <p className="small muted" style={{ marginTop: '0.5rem' }}>
        {t('sysSettings.localeNote')}
      </p>
    </Card>
  )
}

interface TimeSyncState {
  service?: string
  installed: boolean
  active: boolean
  ntp: boolean
  synchronized: boolean
  servers: string[]
  server?: string
  offset?: string
  stratum?: string
  can_configure: boolean
  install_package?: string
  presets: string[]
  note?: string
}

export function TimeSyncCard({ canUse }: { canUse: boolean }) {
  const { t } = useTranslation()
  const sync = useApi<TimeSyncState>('/system/timesync', 60_000)
  const [servers, setServers] = useState<string[] | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [installing, setInstalling] = useState(false)
  const [installOutcome, setInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  useEffect(() => {
    if (sync.data && servers === null) setServers(sync.data.servers ?? [])
  }, [sync.data, servers])

  const pkg = sync.data?.install_package ?? 'systemd-timesyncd'
  const dirty = servers !== null && JSON.stringify(servers) !== JSON.stringify(sync.data?.servers ?? [])

  async function post(body: Record<string, unknown>, key: string) {
    setBusy(key)
    setError(null)
    try {
      await api('/system/timesync', { method: 'POST', body, timeoutMs: 120_000 })
      setServers(null)
      await sync.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  async function handleInstallFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>(`/system/apt/packages/${pkg}/install/status`).catch(() => null)
    setInstallOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    setServers(null)
    await sync.reload()
  }

  const d = sync.data
  const options = [...(d?.presets ?? []), ...(servers ?? [])]
    .filter((v, i, a) => a.indexOf(v) === i)
    .map((v) => ({ value: v, label: v }))

  return (
    <Card title={t('sysSettings.timeSync')} subtitle={d?.note}>
      {sync.error && <Banner kind="error">{sync.error}</Banner>}
      {error && <Banner kind="error">{error}</Banner>}
      {d && (
        <div className="col" style={{ gap: '0.5rem' }}>
          <div className="row small" style={{ gap: '0.5rem', flexWrap: 'wrap', alignItems: 'center' }}>
            <span>{t('sysSettings.timeService')}:</span>
            {d.installed ? (
              <>
                <code className="mono">{d.service}</code>
                <Tag color={d.active ? 'green' : 'default'}>{d.active ? t('sysSettings.active') : t('sysSettings.inactive')}</Tag>
                <Tag color={d.synchronized ? 'green' : 'orange'}>
                  {d.synchronized ? t('sysSettings.ntpSynced') : t('sysSettings.ntpNotSynced')}
                </Tag>
                {d.server && (
                  <span className="muted">
                    {t('sysSettings.syncWith')} <code className="mono">{d.server}</code>
                    {d.stratum && ` · stratum ${d.stratum}`}
                    {d.offset && ` · ${t('sysSettings.offset')} ${d.offset}`}
                  </span>
                )}
              </>
            ) : (
              <>
                <Tag color="red">{t('sysSettings.timeServiceMissing')}</Tag>
                {canUse && (
                  <Button size="small" type="primary" onClick={() => setInstalling(true)}>
                    {t('sysSettings.installTimeService', { pkg })}
                  </Button>
                )}
              </>
            )}
          </div>
          {d.installed && d.can_configure && (
            <div className="filters" style={{ alignItems: 'flex-end' }}>
              <label className="col" style={{ gap: '0.2rem', flex: 1, minWidth: '18rem' }}>
                {t('sysSettings.ntpServers')}
                <Select
                  mode="tags"
                  size="small"
                  disabled={!canUse || busy !== null}
                  value={servers ?? []}
                  options={options}
                  placeholder={t('sysSettings.ntpServersPlaceholder')}
                  tokenSeparators={[' ', ',']}
                  onChange={(v) => setServers(v.map((s) => s.trim()).filter(Boolean))}
                />
              </label>
              <Button
                size="small"
                disabled={!canUse || !dirty || busy !== null}
                loading={busy === 'servers'}
                onClick={() => post({ servers }, 'servers')}
              >
                {t('common.save')}
              </Button>
              <Button
                size="small"
                type="primary"
                disabled={!canUse || busy !== null}
                loading={busy === 'now'}
                onClick={() => post({ sync_now: true }, 'now')}
              >
                {t('sysSettings.syncNow')}
              </Button>
            </div>
          )}
          {d.installed && !d.can_configure && canUse && (
            <div>
              <Button size="small" type="primary" loading={busy === 'now'} disabled={busy !== null} onClick={() => post({ sync_now: true }, 'now')}>
                {t('sysSettings.syncNow')}
              </Button>
            </div>
          )}
          <p className="small muted" style={{ margin: 0 }}>
            {t('sysSettings.timeSyncNote')}
          </p>
        </div>
      )}
      {installing && (
        <PackageInstallModal
          packageName={pkg}
          wsPath={`/system/apt/packages/${pkg}/install/ws`}
          onClose={() => setInstalling(false)}
          onFinished={handleInstallFinished}
          outcome={installOutcome}
        />
      )}
    </Card>
  )
}
