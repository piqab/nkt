import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Button, Input, Tag, Tooltip, type TableColumnsType } from 'antd'
import { InfoCircleOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { Me, PackageUpdate } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'
import SandboxPackagesCard from '../components/SandboxPackagesCard'
import PackageInstallModal from '../components/PackageInstallModal'
import i18n from '../i18n'
import UpdateModal from '../components/UpdateModal'
import { confirmAction } from '../components/confirm'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'

interface AptSearchResult {
  name: string
  description: string
  installed: boolean
  /** Чем пакет подошёл под запрос — см. classifyAptMatch на стороне Go. */
  match?: 'exact' | 'prefix' | 'name' | 'description'
}

interface AptInstalledPackage {
  name: string
  version: string
  description?: string
}

// How long to wait after the last keystroke before actually asking the
// host to run apt-cache search — that's a real subprocess exec, not a
// cheap client-side filter, so firing one per keystroke would spawn far
// more of them than any search actually needs.
const SEARCH_DEBOUNCE_MS = 400

/**
 * A full apt package manager: search the host's own apt cache for any
 * package by name (not just commonPackages' curated allowlist below),
 * install one, and browse/remove whatever is already installed. The
 * curated quick-install card lives here too rather than on Overview — one
 * place for everything package-related.
 */
/** Общая строка пакета — для поиска и для установленных одна и та же. */
interface PackageRow {
  name: string
  version?: string
  description?: string
  installed?: boolean
  match?: AptSearchResult['match']
}

/**
 * Колонки таблицы пакетов — одни и те же для поиска и для установленных.
 *
 * Две таблицы одного раздела с разной раскладкой и разными кнопками
 * читались как два разных инструмента. Теперь у обеих: галочки для
 * пакетного действия, имя со значком ⓘ (описание по наведению вместо
 * широкого столбца, который растил строки в три этажа), версия, где она
 * известна, и одно действие над строкой иконкой.
 */
function packageColumns(
  t: typeof i18n.t,
  opts: { version: boolean; state: boolean; action?: (p: PackageRow) => ReactNode },
): TableColumnsType<PackageRow> {
  const cols: TableColumnsType<PackageRow> = [
    {
      title: t('packages.colName'),
      key: 'name',
      render: (_, p) => (
        <span className="nowrap">
          <code className="mono">{p.name}</code>
          {p.description && (
            <Tooltip title={p.description}>
              <InfoCircleOutlined
                aria-label={t('packages.colDescription')}
                style={{ marginLeft: '0.35rem', color: 'var(--text-muted)' }}
              />
            </Tooltip>
          )}
        </span>
      ),
    },
  ]
  if (opts.version) {
    cols.push({
      title: t('packages.col.version'),
      key: 'version',
      render: (_, p) => <span className="small muted mono">{p.version || '—'}</span>,
    })
  }
  if (opts.state) {
    cols.push({
      title: t('packages.colState'),
      key: 'state',
      width: '9rem',
      render: (_, p) =>
        p.installed ? <Tag color="green">{t('commonPackages.installed')}</Tag> : <span className="small muted">—</span>,
    })
  }
  if (opts.action) {
    cols.push({ title: '', key: 'actions', align: 'right', className: 'nowrap', render: (_, p) => opts.action!(p) })
  }
  return cols
}

export default function Packages({ me }: { me: Me }) {
  const { t } = useTranslation()
  const canUse = me.is_admin && me.allow_mutations

  // --- OS package updates (apt-get dist-upgrade) ---
  const updates = useApi<{ available: boolean; packages: PackageUpdate[]; reboot_required: boolean }>(
    '/system/apt/updates',
    30_000,
  )
  const pkgUpdates = updates.data?.packages ?? []
  const pkgAvailable = updates.data?.available ?? false
  // Polled independently of whether the update dialog is open — otherwise
  // there's no way to tell, from the button alone, whether "обновить"
  // would reattach to an apt-get already running (started earlier, or by
  // someone else) or start a brand new one; both look identical at first
  // glance (a black terminal with a spinner) once the dialog opens.
  const updateStatus = useApi<{ active: boolean; finished: boolean; succeeded: boolean }>('/updates/status', 5_000)
  const updateActive = updateStatus.data?.active ?? false
  const [updating, setUpdating] = useState(false)
  const [updateOutcome, setUpdateOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)
  const [rescanningAfterUpdate, setRescanningAfterUpdate] = useState(false)

  /**
   * Called the moment the update session's socket closes, i.e. as soon as
   * apt actually exits. /system/apt/updates serves the last inventory
   * scan, so its package list would still show the versions apt just
   * replaced until something rescans the host — only worth doing for a
   * successful run; a failed one leaves the previous state in place and
   * its error on screen instead.
   */
  async function handleUpdateFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>('/updates/status').catch(() => null)
    updateStatus.reload()
    if (fresh?.succeeded) {
      setUpdateOutcome({ ok: true })
      setRescanningAfterUpdate(true)
      try {
        await api('/inventory/refresh', { method: 'POST' })
      } finally {
        setRescanningAfterUpdate(false)
      }
      await updates.reload()
    } else {
      setUpdateOutcome({ ok: false, exitCode: fresh?.exit_code })
    }
  }

  // --- search any package ---
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  useEffect(() => {
    const id = setTimeout(() => setDebouncedQuery(query), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(id)
  }, [query])

  const trimmedQuery = debouncedQuery.trim()
  const searchPath = trimmedQuery.length >= 2 ? `/system/apt/search${qs({ q: trimmedQuery })}` : null
  const search = useApi<{ results: AptSearchResult[]; truncated: boolean }>(searchPath)

  // Several search results can be picked and installed in one apt-get: the
  // package manager resolves the whole selection's dependencies together and
  // takes the dpkg lock once, which installing them one by one cannot.
  const [picked, setPicked] = useState<string[]>([])
  const [batchInstalling, setBatchInstalling] = useState(false)
  const [installOutcome, setInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  async function handleBatchFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>(
      '/system/apt/install/status',
    ).catch(() => null)
    setInstallOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    setPicked([])
    await search.reload()
    await installed.reload()
  }

  // --- everything currently installed ---
  const installed = useApi<{ packages: AptInstalledPackage[] }>('/system/apt/installed', 60_000)
  const [installedQuery, setInstalledQuery] = useState('')
  // Удаление — набором, тем же путём, что установка: одним apt-get,
  // который разрешает зависимости всего набора разом. Одиночное удаление
  // — тот же набор из одного имени.
  const [pickedInstalled, setPickedInstalled] = useState<string[]>([])
  const [removeTargets, setRemoveTargets] = useState<string[] | null>(null)
  const [removeOutcome, setRemoveOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  async function handleRemoveFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>('/system/apt/remove/status').catch(() => null)
    setRemoveOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    setPickedInstalled([])
    await installed.reload()
    await search.reload()
  }

  async function startRemove(names: string[]) {
    if (names.length === 0) return
    const question =
      names.length === 1
        ? t('packages.confirmRemove', { name: names[0] })
        : t('packages.confirmRemoveMany', { count: names.length, names: names.join(', ') })
    if (!(await confirmAction(question))) return
    setRemoveOutcome(null)
    setRemoveTargets(names)
  }

  async function startInstall(names: string[]) {
    if (names.length === 0) return
    setPicked(names)
    setInstallOutcome(null)
    setBatchInstalling(true)
  }

  const installedList = installed.data?.packages ?? []
  const visibleInstalled = useMemo(() => {
    const q = installedQuery.trim().toLowerCase()
    if (!q) return installedList
    return installedList.filter((p) => p.name.toLowerCase().includes(q))
  }, [installedList, installedQuery])

  const searchColumns = packageColumns(t, {
    version: false,
    state: true,
    action: (p) =>
      p.installed ? null : (
        <RowAction action="install" label={t('packages.install')} disabled={!canUse} onClick={() => void startInstall([p.name])} />
      ),
  })
  const installedColumns = packageColumns(t, {
    version: true,
    state: false,
    action: (p) => (
      <RowAction action="delete" label={t('packages.remove')} danger disabled={!canUse} onClick={() => void startRemove([p.name])} />
    ),
  })

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            {t('packages.title')}
            <InfoHint>{t('packages.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      {!canUse && <Banner kind="info">{t('common.mutationsDisabled')}</Banner>}

      {pkgAvailable && (
        <Card title={t('packages.updatesTitle')}>
          {updateActive ? (
            <Button
              type="primary"
              disabled={!canUse}
              onClick={() => {
                setUpdateOutcome(null)
                setUpdating(true)
              }}
            >
              {t('packages.updateRunningOpen')}
            </Button>
          ) : (
            <Button
              type={pkgUpdates.length > 0 ? 'primary' : 'default'}
              disabled={!canUse || pkgUpdates.length === 0}
              onClick={async () => {
                if (!(await confirmAction(t('packages.confirmUpdate', { count: pkgUpdates.length })))) return
                setUpdateOutcome(null)
                setUpdating(true)
              }}
            >
              {pkgUpdates.length > 0 ? t('packages.updateCount', { count: pkgUpdates.length }) : t('packages.noUpdates')}
            </Button>
          )}
          {updates.data?.reboot_required && (
            <div style={{ marginTop: '0.75rem' }}>
              <Banner kind="warn">{t('overview.rebootRequired')}</Banner>
            </div>
          )}
        </Card>
      )}


      <Card title={t('packages.searchTitle')}>
        <Input.Search
          placeholder={t('packages.searchPlaceholder')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          loading={search.loading}
          allowClear
          style={{ maxWidth: '24rem' }}
        />
        <ErrorNote error={search.error} />
        {trimmedQuery.length > 0 && trimmedQuery.length < 2 && (
          <p className="small muted" style={{ marginTop: '0.5rem' }}>
            {t('packages.searchMinLength')}
          </p>
        )}
        {search.data && (
          <>
            {search.data.truncated && (
              <p className="small muted" style={{ marginTop: '0.5rem' }}>
                {t('packages.searchTruncated')}
              </p>
            )}
            {/* Always shown, disabled until something is picked: this is the
                only way to install from a search now, so it has to be visible
                before the first checkbox is ticked. */}
            <div className="row" style={{ gap: '0.5rem', marginTop: '0.5rem', alignItems: 'center' }}>
              <Button
                type="primary"
                disabled={!canUse || picked.length === 0}
                onClick={() => {
                  setInstallOutcome(null)
                  setBatchInstalling(true)
                }}
              >
                {t('packages.installSelected', { count: picked.length })}
              </Button>
              {picked.length > 0 && (
                <>
                  <Button size="small" onClick={() => setPicked([])}>
                    {t('packages.clearSelection')}
                  </Button>
                  <span className="small secondary mono">{picked.join(', ')}</span>
                </>
              )}
            </div>
            {search.data.results.length === 0 ? (
              <p className="small muted" style={{ marginTop: '0.5rem' }}>
                {t('common.noMatch')}
              </p>
            ) : (
              <>
                <p className="small muted" style={{ marginTop: '0.5rem' }}>
                  {t('packages.matchLegend')}
                </p>
                <div className="table-wrap">
                  <DataTable<PackageRow>
                    dataSource={search.data.results}
                    rowKey="name"
                    // Фон строки — граница между группами: сначала пакеты,
                    // чьё имя совпало с запросом, потом те, где он нашёлся
                    // только в описании. Порядок задаёт сервер (rankAptResults).
                    rowClassName={(p) => `pkg-match pkg-match-${p.match ?? 'description'}`}
                    columns={searchColumns}
                    rowSelection={{
                      selectedRowKeys: picked,
                      onChange: (keys) => setPicked(keys as string[]),
                      getCheckboxProps: (p) => ({ disabled: !canUse || p.installed }),
                    }}
                  />
                </div>
              </>
            )}
          </>
        )}
      </Card>

      <SandboxPackagesCard me={me} />

      <Card title={t('packages.installedTitle')}>
        <Input
          placeholder={t('packages.filterPlaceholder')}
          value={installedQuery}
          onChange={(e) => setInstalledQuery(e.target.value)}
          allowClear
          style={{ maxWidth: '20rem', marginBottom: '0.75rem' }}
        />
        <ErrorNote error={installed.error} />
        {!installed.data ? (
          <Loading what={t('packages.installedTitle')} />
        ) : (
          <>
            {/* Та же строка кнопок, что у поиска: выбранные — одним действием. */}
            <div className="row" style={{ gap: '0.5rem', marginBottom: '0.5rem', alignItems: 'center' }}>
              <Button danger disabled={!canUse || pickedInstalled.length === 0} onClick={() => void startRemove(pickedInstalled)}>
                {t('packages.removeSelected', { count: pickedInstalled.length })}
              </Button>
              {pickedInstalled.length > 0 && (
                <>
                  <Button size="small" onClick={() => setPickedInstalled([])}>
                    {t('packages.clearSelection')}
                  </Button>
                  <span className="small secondary mono">{pickedInstalled.join(', ')}</span>
                </>
              )}
            </div>
            <div className="table-wrap">
              <DataTable<PackageRow>
                key={installedQuery}
                dataSource={visibleInstalled}
                columns={installedColumns}
                rowKey="name"
                rowSelection={{
                  selectedRowKeys: pickedInstalled,
                  onChange: (keys) => setPickedInstalled(keys as string[]),
                  getCheckboxProps: () => ({ disabled: !canUse }),
                }}
                pagination={{ defaultPageSize: 20, showSizeChanger: true, pageSizeOptions: [20, 50, 100] }}
              />
            </div>
          </>
        )}
      </Card>

      {batchInstalling && (
        <PackageInstallModal
          packageName={picked.join(', ')}
          wsPath={`/system/apt/install/ws${qs({ pkgs: picked.join(',') })}`}
          onClose={() => setBatchInstalling(false)}
          onFinished={handleBatchFinished}
          outcome={installOutcome}
          action="install"
        />
      )}

      {removeTargets && (
        <PackageInstallModal
          packageName={removeTargets.join(', ')}
          wsPath={`/system/apt/remove/ws${qs({ pkgs: removeTargets.join(',') })}`}
          onClose={() => setRemoveTargets(null)}
          onFinished={handleRemoveFinished}
          outcome={removeOutcome}
          action="remove"
        />
      )}
      {updating && (
        <UpdateModal
          packages={pkgUpdates}
          outcome={updateOutcome}
          rescanning={rescanningAfterUpdate}
          onFinished={handleUpdateFinished}
          onClose={() => {
            setUpdating(false)
            updates.reload()
          }}
        />
      )}
    </>
  )
}
