import { useEffect, useMemo, useState } from 'react'
import { Button, Input, Table, Tag, Tooltip, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'
import CommonPackagesCard from '../components/CommonPackagesCard'
import PackageInstallModal from '../components/PackageInstallModal'

interface AptSearchResult {
  name: string
  description: string
  installed: boolean
}

interface AptInstalledPackage {
  name: string
  version: string
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
export default function Packages({ me }: { me: Me }) {
  const { t } = useTranslation()
  const canUse = me.is_admin && me.allow_mutations

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

  const [installTarget, setInstallTarget] = useState<string | null>(null)
  const [installOutcome, setInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  async function handleInstallFinished() {
    if (!installTarget) return
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>(
      `/system/apt/packages/${installTarget}/install/status`,
    ).catch(() => null)
    setInstallOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    await search.reload()
    await installed.reload()
  }

  // --- everything currently installed ---
  const installed = useApi<{ packages: AptInstalledPackage[] }>('/system/apt/installed', 60_000)
  const [installedQuery, setInstalledQuery] = useState('')
  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  const [removeOutcome, setRemoveOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  async function handleRemoveFinished() {
    if (!removeTarget) return
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>(
      `/system/apt/packages/${removeTarget}/remove/status`,
    ).catch(() => null)
    setRemoveOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    await installed.reload()
    await search.reload()
  }

  const installedList = installed.data?.packages ?? []
  const visibleInstalled = useMemo(() => {
    const q = installedQuery.trim().toLowerCase()
    if (!q) return installedList
    return installedList.filter((p) => p.name.toLowerCase().includes(q))
  }, [installedList, installedQuery])

  const installedColumns: TableColumnsType<AptInstalledPackage> = [
    { title: t('packages.col.name'), dataIndex: 'name', key: 'name', render: (v: string) => <span className="mono">{v}</span> },
    { title: t('packages.col.version'), dataIndex: 'version', key: 'version', render: (v: string) => <span className="small muted">{v}</span> },
    {
      title: '',
      key: 'actions',
      align: 'right',
      render: (_, p) => (
        <Button
          size="small"
          danger
          disabled={!canUse}
          onClick={() => {
            if (!window.confirm(t('packages.confirmRemove', { name: p.name }))) return
            setRemoveOutcome(null)
            setRemoveTarget(p.name)
          }}
        >
          {t('packages.remove')}
        </Button>
      ),
    },
  ]

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

      <CommonPackagesCard canUse={canUse} />

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
            <div className="row" style={{ gap: '0.4rem', marginTop: '0.5rem' }}>
              {search.data.results.length === 0 ? (
                <p className="small muted">{t('common.noMatch')}</p>
              ) : (
                search.data.results.map((p) => (
                  <Tooltip key={p.name} title={p.description || undefined}>
                    <div
                      className="row"
                      style={{
                        alignItems: 'center',
                        gap: '0.4rem',
                        padding: '0.15rem 0.5rem',
                        border: '1px solid var(--border-strong)',
                        borderRadius: 999,
                      }}
                    >
                      <span className="mono">{p.name}</span>
                      {p.installed ? (
                        <Tag color="green" style={{ margin: 0 }}>
                          {t('commonPackages.installed')}
                        </Tag>
                      ) : (
                        <Button
                          size="small"
                          disabled={!canUse}
                          onClick={() => {
                            setInstallOutcome(null)
                            setInstallTarget(p.name)
                          }}
                        >
                          {t('packages.install')}
                        </Button>
                      )}
                    </div>
                  </Tooltip>
                ))
              )}
            </div>
          </>
        )}
      </Card>

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
          <div className="table-wrap">
            <Table<AptInstalledPackage>
              key={installedQuery}
              dataSource={visibleInstalled}
              columns={installedColumns}
              rowKey="name"
              size="small"
              pagination={{ defaultPageSize: 20, showSizeChanger: true, pageSizeOptions: [20, 50, 100] }}
            />
          </div>
        )}
      </Card>

      {installTarget && (
        <PackageInstallModal
          packageName={installTarget}
          wsPath={`/system/apt/packages/${installTarget}/install/ws`}
          onClose={() => setInstallTarget(null)}
          onFinished={handleInstallFinished}
          outcome={installOutcome}
          action="install"
        />
      )}
      {removeTarget && (
        <PackageInstallModal
          packageName={removeTarget}
          wsPath={`/system/apt/packages/${removeTarget}/remove/ws`}
          onClose={() => setRemoveTarget(null)}
          onFinished={handleRemoveFinished}
          outcome={removeOutcome}
          action="remove"
        />
      )}
    </>
  )
}
