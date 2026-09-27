import { useMemo, useState } from 'react'
import { AutoComplete, Button, Dropdown, Input, Space, Tag } from 'antd'
import { MoreOutlined, PlusOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { qs, useApi } from '../api'
import type { Me } from '../types'
import { Banner, CodeEditor, ErrorNote, Loading, Modal, formatRelative } from './ui'
import { DataTable } from './DataTable'
import { EditTextModal } from './EditTextModal'
import { confirmAction } from './confirm'
import { useJobLauncher } from './useJobLauncher'

interface Release {
  name: string
  namespace: string
  revision: string
  updated: string
  status: string
  chart: string
  app_version: string
}
interface Repo {
  name: string
  url: string
}
interface HelmStatus {
  installed: boolean
  version?: string
  releases: Release[]
  repos: Repo[]
  error?: string
  setup_version: string
}
interface Revision {
  revision: number
  updated: string
  status: string
  chart: string
  app_version: string
  description: string
}
interface Source {
  repo_name: string
  repo_url: string
  chart: string
  version?: string
}

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

/** «2026-09-10 08:02:11.123456 +0000 UTC» из helm list — в ISO для formatRelative. */
function helmTime(s: string): string {
  const m = /^(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2}:\d{2})/.exec(s)
  return m ? `${m[1]}T${m[2]}Z` : s
}

const STATUS_COLOR: Record<string, string> = { deployed: 'success', failed: 'error', superseded: 'default', uninstalling: 'warning' }

/**
 * Релизы Helm кластера: список (с общим фильтром namespace), история и
 * откат, значения и обновление (правка в окне с диффом), удаление,
 * установка чарта из репозитория. Всё, что меняет кластер, — фоновыми
 * заданиями хоста.
 */
export function K8sHelm({ me, namespace, namespaces }: { me: Me; namespace: string; namespaces: string[] }) {
  const { t } = useTranslation()
  const res = useApi<HelmStatus>('/k8s/helm', 30_000)
  const job = useJobLauncher(() => void res.reload())
  const [error, setError] = useState<string | null>(null)
  const [dialog, setDialog] = useState<{ type: 'history' | 'upgrade'; rel: Release } | { type: 'install' } | null>(null)
  const canMutate = me.is_admin && me.allow_mutations
  const rows = useMemo(() => (res.data?.releases ?? []).filter((r) => !namespace || r.namespace === namespace), [res.data, namespace])

  async function launch(path: string, body?: unknown) {
    setError(null)
    try {
      await job.start(path, body)
      return true
    } catch (err) {
      setError(errText(err))
      return false
    }
  }

  if (res.loading && !res.data) return <Loading what="Helm" />
  if (res.error) return <ErrorNote error={res.error} />
  const st = res.data
  if (!st) return null

  if (!st.installed) {
    return (
      <div className="col" style={{ gap: '0.5rem' }}>
        <Banner kind="info">{t('k8s.helm.missing', { version: st.setup_version })}</Banner>
        {error && <Banner kind="error">{error}</Banner>}
        {canMutate && (
          <div>
            <Button type="primary" onClick={() => void launch('/k8s/helm/setup')}>
              {t('k8s.helm.setup')}
            </Button>
          </div>
        )}
        {job.modal}
      </div>
    )
  }

  const columns = [
    { title: t('k8s.col.name'), key: 'name', render: (_: unknown, r: Release) => <span className="mono small">{r.name}</span> },
    ...(!namespace ? [{ title: 'Namespace', key: 'ns', render: (_: unknown, r: Release) => <span className="small">{r.namespace}</span> }] : []),
    { title: t('k8s.helm.chart'), key: 'chart', render: (_: unknown, r: Release) => <span className="mono small">{r.chart}</span> },
    { title: t('k8s.helm.appVersion'), key: 'app', render: (_: unknown, r: Release) => <span className="mono small">{r.app_version || '—'}</span> },
    { title: t('k8s.helm.status'), key: 'status', render: (_: unknown, r: Release) => <Tag color={STATUS_COLOR[r.status] ?? 'warning'}>{r.status}</Tag> },
    { title: t('k8s.helm.revision'), key: 'rev', render: (_: unknown, r: Release) => <span className="small">{r.revision}</span> },
    { title: t('k8s.helm.updated'), key: 'updated', render: (_: unknown, r: Release) => <span className="small nowrap">{formatRelative(helmTime(r.updated))}</span> },
    {
      title: '',
      key: 'actions',
      width: 40,
      render: (_: unknown, r: Release) => (
        <Dropdown
          trigger={['click']}
          menu={{
            items: [
              { key: 'history', label: t('k8s.helm.history') },
              ...(canMutate
                ? [
                    { key: 'upgrade', label: t('k8s.helm.upgrade') },
                    { key: 'uninstall', label: t('k8s.helm.uninstall'), danger: true },
                  ]
                : []),
            ],
            onClick: async ({ key }) => {
              if (key === 'uninstall') {
                if (!(await confirmAction(t('k8s.helm.uninstallConfirm', { name: `${r.namespace}/${r.name}` }), { okText: t('k8s.helm.uninstall') }))) return
                void launch('/k8s/helm/uninstall', { namespace: r.namespace, release: r.name })
                return
              }
              setDialog({ type: key as 'history' | 'upgrade', rel: r })
            },
          }}
        >
          <Button size="small" type="text" icon={<MoreOutlined />} aria-label={t('k8s.act.menu')} />
        </Dropdown>
      ),
    },
  ]

  return (
    <div className="col" style={{ gap: '0.5rem' }}>
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <span className="small muted">
          {st.version} · {t('k8s.count', { count: rows.length })}
          {namespace ? ` · ${namespace}` : ''}
        </span>
        {canMutate && (
          <Button size="small" icon={<PlusOutlined />} onClick={() => setDialog({ type: 'install' })}>
            {t('k8s.helm.install')}
          </Button>
        )}
      </div>
      {st.error && <Banner kind="error">{st.error}</Banner>}
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      <div className="table-wrap">
        <DataTable<Release> dataSource={rows} rowKey={(r) => `${r.namespace}/${r.name}`} size="small" columns={columns} />
      </div>
      {job.modal}
      {dialog?.type === 'history' && (
        <HistoryModal rel={dialog.rel} canMutate={canMutate} onClose={() => setDialog(null)} onRollback={(rev) => launch('/k8s/helm/rollback', { namespace: dialog.rel.namespace, release: dialog.rel.name, revision: rev })} />
      )}
      {dialog?.type === 'upgrade' && <InstallModal me={me} repos={st.repos} namespaces={namespaces} release={dialog.rel} onClose={() => setDialog(null)} onLaunch={launch} />}
      {dialog?.type === 'install' && <InstallModal me={me} repos={st.repos} namespaces={namespaces} defaultNamespace={namespace} onClose={() => setDialog(null)} onLaunch={launch} />}
    </div>
  )
}

function HistoryModal({ rel, canMutate, onClose, onRollback }: { rel: Release; canMutate: boolean; onClose: () => void; onRollback: (rev: number) => Promise<boolean> }) {
  const { t } = useTranslation()
  const res = useApi<{ history: Revision[] }>(`/k8s/helm/history${qs({ namespace: rel.namespace, release: rel.name })}`)
  const current = Number(rel.revision)
  const columns = [
    { title: t('k8s.helm.revision'), key: 'rev', render: (_: unknown, r: Revision) => <strong className="small">{r.revision}</strong> },
    { title: t('k8s.helm.updated'), key: 'updated', render: (_: unknown, r: Revision) => <span className="small nowrap">{formatRelative(r.updated)}</span> },
    { title: t('k8s.helm.status'), key: 'status', render: (_: unknown, r: Revision) => <Tag color={STATUS_COLOR[r.status] ?? 'warning'}>{r.status}</Tag> },
    { title: t('k8s.helm.chart'), key: 'chart', render: (_: unknown, r: Revision) => <span className="mono small">{r.chart}</span> },
    { title: t('k8s.helm.description'), key: 'desc', render: (_: unknown, r: Revision) => <span className="small">{r.description}</span> },
    ...(canMutate
      ? [
          {
            title: '',
            key: 'rollback',
            render: (_: unknown, r: Revision) =>
              r.revision !== current && (
                <Button
                  size="small"
                  danger
                  onClick={async () => {
                    if (!(await confirmAction(t('k8s.helm.rollbackConfirm', { name: `${rel.namespace}/${rel.name}`, rev: r.revision }), { okText: t('k8s.helm.rollback') }))) return
                    if (await onRollback(r.revision)) onClose()
                  }}
                >
                  {t('k8s.helm.rollback')}
                </Button>
              ),
          },
        ]
      : []),
  ]
  return (
    <Modal title={t('k8s.helm.historyTitle', { name: `${rel.namespace}/${rel.name}` })} onClose={onClose} width={900}>
      {res.error ? (
        <Banner kind="error">{res.error}</Banner>
      ) : !res.data ? (
        <Loading what="helm history" />
      ) : (
        <DataTable<Revision> dataSource={[...res.data.history].reverse()} rowKey={(r) => String(r.revision)} size="small" columns={columns} />
      )}
    </Modal>
  )
}

/**
 * Установка чарта или обновление релиза: репозиторий (из добавленных или
 * новый, либо oci://), чарт, версия, релиз, namespace и значения —
 * значения правятся в окне с диффом «было → станет».
 */
function InstallModal({
  me,
  repos,
  namespaces,
  release,
  defaultNamespace,
  onClose,
  onLaunch,
}: {
  me: Me
  repos: Repo[]
  namespaces: string[]
  release?: Release
  defaultNamespace?: string
  onClose: () => void
  onLaunch: (path: string, body: unknown) => Promise<boolean>
}) {
  const { t } = useTranslation()
  const upgrade = !!release
  const cur = useApi<{ values: string; source: Source }>(upgrade ? `/k8s/helm/values${qs({ namespace: release.namespace, release: release.name })}` : null)
  const [form, setForm] = useState<{ repo_name: string; repo_url: string; chart: string; version: string; release: string; namespace: string } | null>(null)
  const [draft, setDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [view, setView] = useState<'all' | 'defaults' | null>(null)

  if (upgrade && !cur.data) {
    return (
      <Modal title={t('k8s.helm.upgradeTitle', { name: `${release.namespace}/${release.name}` })} onClose={onClose}>
        {cur.error ? <Banner kind="error">{cur.error}</Banner> : <Loading what="values" />}
      </Modal>
    )
  }
  const src = cur.data?.source
  const initial = {
    repo_name: src?.repo_name ?? '',
    repo_url: src?.repo_url || repos.find((r) => r.name === src?.repo_name)?.url || '',
    chart: src?.chart ?? '',
    version: '',
    release: release?.name ?? '',
    namespace: release?.namespace ?? (defaultNamespace || 'default'),
  }
  const f = form ?? initial
  const set = (patch: Partial<typeof f>) => setForm({ ...f, ...patch })
  const saved = cur.data?.values ?? ''
  const text = draft ?? saved
  const oci = f.repo_url.startsWith('oci://')

  async function save(): Promise<boolean> {
    setBusy(true)
    const ok = await onLaunch(upgrade ? '/k8s/helm/upgrade' : '/k8s/helm/install', { ...f, values: text })
    setBusy(false)
    return ok
  }

  return (
    <EditTextModal
      title={upgrade ? t('k8s.helm.upgradeTitle', { name: `${release.namespace}/${release.name}` }) : t('k8s.helm.installTitle')}
      saved={saved}
      draft={text}
      onDraft={setDraft}
      busy={busy}
      rows={14}
      blocksEndpoint="/k8s/yaml/blocks"
      onSave={me.is_admin && me.allow_mutations ? save : async () => false}
      onClose={onClose}
      fields={
        <>
          <p className="small muted">{upgrade ? t('k8s.helm.upgradeHint') : t('k8s.helm.installHint')}</p>
          {upgrade && (
            <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap', marginBottom: '0.5rem' }}>
              {saved === '' && <span className="small" style={{ color: 'var(--status-warning)' }}>{t('k8s.helm.noUserValues')}</span>}
              <Button size="small" onClick={() => setView('all')}>
                {t('k8s.helm.allValues')}
              </Button>
              <Button size="small" onClick={() => setView('defaults')}>
                {t('k8s.helm.chartDefaults')}
              </Button>
            </div>
          )}
          {upgrade && view && (
            <ValuesView
              release={release}
              view={view}
              onClose={() => setView(null)}
              onUse={(text) => {
                setDraft(text)
                setView(null)
              }}
            />
          )}
          <div className="row" style={{ gap: '0.5rem', flexWrap: 'wrap', marginBottom: '0.5rem' }}>
            <AutoComplete
              size="small"
              style={{ width: '11rem' }}
              value={f.repo_name}
              placeholder={t('k8s.helm.repoName')}
              disabled={oci}
              options={repos.map((r) => ({ value: r.name, label: `${r.name} — ${r.url}` }))}
              onChange={(v: string) => set({ repo_name: v.trim(), repo_url: repos.find((r) => r.name === v)?.url ?? f.repo_url })}
            />
            <Input size="small" style={{ width: '20rem' }} value={f.repo_url} placeholder={t('k8s.helm.repoURL')} onChange={(e) => set({ repo_url: e.target.value.trim() })} />
            <Input size="small" style={{ width: '10rem' }} value={f.chart} placeholder={t('k8s.helm.chart')} onChange={(e) => set({ chart: e.target.value.trim() })} />
            <Input size="small" style={{ width: '8rem' }} value={f.version} placeholder={t('k8s.helm.versionLatest')} onChange={(e) => set({ version: e.target.value.trim() })} />
          </div>
          <Space wrap style={{ marginBottom: '0.5rem' }}>
            <span className="small">{t('k8s.helm.release')}</span>
            <Input size="small" style={{ width: '12rem' }} value={f.release} disabled={upgrade} onChange={(e) => set({ release: e.target.value.trim() })} />
            <span className="small">Namespace</span>
            {upgrade ? (
              <Input size="small" style={{ width: '12rem' }} value={f.namespace} disabled />
            ) : (
              <AutoComplete size="small" style={{ width: '12rem' }} value={f.namespace} options={namespaces.map((n) => ({ value: n }))} onChange={(v: string) => set({ namespace: v.trim() })} />
            )}
            <span className="small muted">{t('k8s.helm.valuesLabel')}</span>
          </Space>
        </>
      }
    />
  )
}

/** Значения релиза только для чтения: все (с умолчаниями) или
 * values.yaml чарта; «в черновик» — взять за основу правки. */
function ValuesView({ release, view, onClose, onUse }: { release: Release; view: 'all' | 'defaults'; onClose: () => void; onUse: (text: string) => void }) {
  const { t } = useTranslation()
  const res = useApi<{ values: string }>(`/k8s/helm/values${qs({ namespace: release.namespace, release: release.name, view })}`)
  return (
    <Modal title={t(view === 'all' ? 'k8s.helm.allValues' : 'k8s.helm.chartDefaults')} onClose={onClose} width={900} sizeKey="helm-values">
      {res.error ? (
        <Banner kind="error">{res.error}</Banner>
      ) : !res.data ? (
        <Loading what="values" />
      ) : res.data.values === '' ? (
        <p className="small muted">{t('k8s.helm.valuesEmpty')}</p>
      ) : (
        <>
          <CodeEditor value={res.data.values} readOnly rows={22} fill />
          <div style={{ marginTop: '0.5rem' }}>
            <Button type="primary" size="small" onClick={() => onUse(res.data?.values ?? '')}>
              {t('k8s.helm.useAsDraft')}
            </Button>
          </div>
        </>
      )}
    </Modal>
  )
}
