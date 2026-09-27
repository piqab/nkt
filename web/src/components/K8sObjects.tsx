import { useEffect, useMemo, useState } from 'react'
import { Button, Input, Segmented, Select, Tabs, Tag, Tooltip } from 'antd'
import { CopyOutlined, PlusOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, ErrorNote, Loading, Modal, formatRelative } from './ui'
import { DataTable } from './DataTable'
import { confirmAction } from './confirm'
import { CreateNamespaceButton, ForwardsBar, K8sRowActions } from './K8sActions'
import { K8sNewObjectModal } from './K8sYAML'
import { AIExplain } from './AIExplain'
import { K8sHelm } from './K8sHelm'

interface Column {
  key: string
  label?: string
}
interface Row {
  name: string
  namespace?: string
  created?: string
  status?: string
  cols: Record<string, string>
}
interface ResourceList {
  kind: string
  namespaced: boolean
  columns: Column[]
  rows: Row[]
}
interface CRD {
  name: string
  group: string
  kind: string
  plural: string
  namespaced: boolean
  version: string
}

const NS_KEY = 'nkt.k8s.namespace'

function loadNamespace(): string {
  try {
    return localStorage.getItem(NS_KEY) ?? ''
  } catch {
    return ''
  }
}

const STATUS_COLOR: Record<string, string> = { ok: 'var(--status-good)', warn: 'var(--status-warning)', error: 'var(--status-critical)' }

/**
 * Объекты кластера: вкладки по видам, в каждой — общий фильтр namespace
 * (один на все вкладки, запоминается) и поиск по имени. Custom Resources —
 * выбором CRD. Значения секретов — по кнопке администратору, с аудитом.
 */
export function K8sObjects({ me }: { me: Me }) {
  const { t } = useTranslation()
  const [namespace, setNamespaceState] = useState(loadNamespace)
  const setNamespace = (ns: string) => {
    setNamespaceState(ns)
    try {
      localStorage.setItem(NS_KEY, ns)
    } catch {
      // не запомнилось — не страшно
    }
  }
  const nsList = useApi<ResourceList>('/k8s/resources?kind=namespaces', 60_000)
  const namespaces = useMemo(() => (nsList.data?.rows ?? []).map((r) => r.name), [nsList.data])
  // Выбранного namespace больше нет — фильтр сбрасывается на «все».
  useEffect(() => {
    if (namespace && nsList.data && !namespaces.includes(namespace)) setNamespace('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespaces])

  // Новый объект создан — таблицы перечитываются сразу, не ждут опроса.
  const [gen, setGen] = useState(0)
  const [creating, setCreating] = useState(false)
  const table = (kind: string) => <ResourceTable kind={kind} namespace={namespace} namespaces={namespaces} onNamespace={setNamespace} me={me} gen={gen} />
  const group = (kinds: string[]) => <KindGroup kinds={kinds} render={table} />

  return (
    <Card
      title={t('k8s.objects')}
      actions={
        me.is_admin && me.allow_mutations ? (
          <Button size="small" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
            {t('k8s.yaml.new')}
          </Button>
        ) : undefined
      }
    >
      {me.is_admin && <ForwardsBar />}
      {creating && <K8sNewObjectModal namespace={namespace} namespaces={namespaces} me={me} onClose={() => setCreating(false)} onCreated={() => setGen((g) => g + 1)} />}
      <Tabs
        size="small"
        destroyOnHidden
        items={[
          { key: 'workloads', label: t('k8s.tab.workloads'), children: group(['deployments', 'statefulsets', 'daemonsets', 'jobs', 'cronjobs', 'hpa']) },
          { key: 'pods', label: t('k8s.tab.pods'), children: table('pods') },
          { key: 'network', label: t('k8s.tab.network'), children: group(['services', 'ingresses', 'networkpolicies']) },
          { key: 'config', label: t('k8s.tab.config'), children: group(['configmaps', 'secrets']) },
          { key: 'storage', label: t('k8s.tab.storage'), children: group(['pvc', 'pv', 'storageclasses']) },
          { key: 'rbac', label: t('k8s.tab.rbac'), children: group(['serviceaccounts', 'roles', 'rolebindings', 'clusterroles', 'clusterrolebindings']) },
          { key: 'nodes', label: t('k8s.tab.nodes'), children: table('nodes') },
          { key: 'namespaces', label: 'Namespaces', children: table('namespaces') },
          { key: 'events', label: t('k8s.tab.events'), children: table('events') },
          { key: 'crd', label: 'Custom Resources', children: <CustomResources render={table} /> },
          { key: 'helm', label: 'Helm', children: <K8sHelm me={me} namespace={namespace} namespaces={namespaces} onNamespace={setNamespace} /> },
        ]}
      />
    </Card>
  )
}

function KindGroup({ kinds, render }: { kinds: string[]; render: (kind: string) => React.ReactNode }) {
  const { t } = useTranslation()
  const [kind, setKind] = useState(kinds[0])
  return (
    <div className="col" style={{ gap: '0.5rem' }}>
      <Segmented size="small" value={kind} onChange={(v) => setKind(v as string)} options={kinds.map((k) => ({ value: k, label: t(`k8s.kind.${k}`) }))} />
      <div key={kind}>{render(kind)}</div>
    </div>
  )
}

function CustomResources({ render }: { render: (kind: string) => React.ReactNode }) {
  const { t } = useTranslation()
  const crds = useApi<{ crds: CRD[] }>('/k8s/crds')
  const [crd, setCrd] = useState<string>('')
  const list = crds.data?.crds ?? []
  const selected = crd || list[0]?.name || ''
  if (crds.loading && !crds.data) return <Loading what="CRD" />
  if (crds.error) return <ErrorNote error={crds.error} />
  if (list.length === 0) return <p className="small muted">{t('k8s.noCRDs')}</p>
  return (
    <div className="col" style={{ gap: '0.5rem' }}>
      <Select
        showSearch
        size="small"
        value={selected}
        onChange={setCrd}
        style={{ maxWidth: '30rem' }}
        options={list.map((c) => ({ value: c.name, label: `${c.kind} · ${c.group} (${c.version}${c.namespaced ? '' : ', cluster'})` }))}
      />
      <div key={selected}>{render(`cr:${selected}`)}</div>
    </div>
  )
}

function ResourceTable({
  kind,
  namespace,
  namespaces,
  onNamespace,
  me,
  gen,
}: {
  kind: string
  namespace: string
  namespaces: string[]
  onNamespace: (ns: string) => void
  me: Me
  gen: number
}) {
  const { t } = useTranslation()
  const res = useApi<ResourceList>(`/k8s/resources${qs({ kind, namespace: namespace || undefined })}`, 30_000)
  useEffect(() => {
    if (gen > 0) void res.reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- только по новому объекту
  }, [gen])
  const [filter, setFilter] = useState('')
  const [data, setData] = useState<{ title: string; values: Record<string, string>; secret: boolean } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const namespaced = res.data?.namespaced ?? true
  const rows = useMemo(() => {
    const f = filter.trim().toLowerCase()
    return (res.data?.rows ?? []).filter((r) => !f || r.name.toLowerCase().includes(f) || Object.values(r.cols).some((v) => v.toLowerCase().includes(f)))
  }, [res.data, filter])

  async function showData(r: Row) {
    setError(null)
    const secret = kind === 'secrets'
    if (secret && !(await confirmAction(t('k8s.revealConfirm', { name: `${r.namespace}/${r.name}` }), { okText: t('k8s.reveal'), danger: false }))) return
    try {
      const out = secret
        ? await api<{ data: Record<string, string> }>('/k8s/secrets/reveal', { method: 'POST', body: { namespace: r.namespace, name: r.name } })
        : await api<{ data: Record<string, string> }>(`/k8s/configmaps/data${qs({ namespace: r.namespace, name: r.name })}`)
      setData({ title: `${r.namespace}/${r.name}`, values: out.data, secret })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const colLabel = (c: Column) => c.label ?? t(`k8s.col.${c.key}`, { defaultValue: c.key })
  const columns = [
    {
      title: t('k8s.col.name'),
      key: 'name',
      render: (_: unknown, r: Row) => (
        <span className="mono small">
          {r.status && <span style={{ color: STATUS_COLOR[r.status], marginRight: '0.35rem' }}>●</span>}
          {r.name}
        </span>
      ),
    },
    ...(namespaced && !namespace ? [{ title: 'Namespace', key: 'ns', render: (_: unknown, r: Row) => <span className="small">{r.namespace}</span> }] : []),
    ...(res.data?.columns ?? []).map((c) => ({
      title: colLabel(c),
      key: c.key,
      render: (_: unknown, r: Row) => {
        const v = r.cols[c.key] ?? ''
        if (v === 'true' || v === 'false') return <Tag color={v === 'true' ? 'success' : 'default'}>{v}</Tag>
        if (c.key === 'last_seen' || c.key === 'last_schedule') return <span className="small nowrap">{v ? formatRelative(v) : '—'}</span>
        // Предупреждение кластера — с лампочкой ИИ: что значит и что делать.
        if (kind === 'events' && c.key === 'message' && r.cols.type === 'Warning')
          return (
            <span className="row row-nowrap" style={{ gap: '0.3rem', alignItems: 'center' }}>
              <span className="small">{v || '—'}</span>
              <AIExplain ctx={{ kind: 'event', title: `Kubernetes ${r.cols.reason}: ${r.cols.object}`, detail: v, service: 'kubernetes', object: `${r.namespace ?? ''}/${r.cols.object}`, severity: 'medium' }} />
            </span>
          )
        if (c.key === 'pod_selector' && !v) return <span className="small muted">{t('k8s.allPods')}</span>
        return <span className={c.key === 'message' ? 'small' : 'small mono'}>{v || '—'}</span>
      },
    })),
    ...(kind !== 'events' ? [{ title: t('k8s.col.age'), key: 'age', render: (_: unknown, r: Row) => <span className="small nowrap">{r.created ? formatRelative(r.created) : '—'}</span> }] : []),
    ...(kind === 'configmaps' || (kind === 'secrets' && me.is_admin)
      ? [
          {
            title: '',
            key: 'data',
            render: (_: unknown, r: Row) => (
              <Button size="small" type="link" onClick={() => void showData(r)}>
                {kind === 'secrets' ? t('k8s.reveal') : t('k8s.data')}
              </Button>
            ),
          },
        ]
      : []),
    ...(kind !== 'events'
      ? [
          {
            title: '',
            key: 'actions',
            width: 40,
            render: (_: unknown, r: Row) => <K8sRowActions kind={kind} row={r} me={me} onChanged={() => void res.reload()} onError={setError} />,
          },
        ]
      : []),
  ]

  return (
    <div className="col" style={{ gap: '0.5rem' }}>
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <Tooltip title={namespaced ? t('k8s.nsHint') : t('k8s.clusterScoped')}>
          <Select
            size="small"
            showSearch
            style={{ minWidth: '13rem' }}
            value={namespace}
            disabled={!namespaced}
            onChange={onNamespace}
            options={[{ value: '', label: t('k8s.allNamespaces') }, ...namespaces.map((n) => ({ value: n, label: n }))]}
          />
        </Tooltip>
        <Input.Search size="small" allowClear placeholder={t('k8s.search')} value={filter} onChange={(e) => setFilter(e.target.value)} style={{ maxWidth: '18rem' }} />
        <span className="small muted">{res.data ? t('k8s.count', { count: rows.length }) : ''}</span>
        {kind === 'namespaces' && me.is_admin && me.allow_mutations && <CreateNamespaceButton onCreated={() => void res.reload()} onError={setError} />}
      </div>
      <ErrorNote error={res.error} />
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {res.loading && !res.data ? (
        <Loading what={t(`k8s.kind.${kind}`, { defaultValue: kind })} />
      ) : (
        <div className="table-wrap">
          <DataTable<Row> dataSource={rows} rowKey={(r) => `${r.namespace ?? ''}/${r.name}`} size="small" columns={columns} />
        </div>
      )}
      {data && (
        <Modal title={data.title} onClose={() => setData(null)} width={860} sizeKey="k8s-data">
          {data.secret && <Banner kind="warn">{t('k8s.revealedAudit')}</Banner>}
          {Object.keys(data.values).length === 0 ? (
            <p className="small muted">{t('k8s.noData')}</p>
          ) : (
            Object.entries(data.values)
              .sort(([a], [b]) => a.localeCompare(b))
              .map(([k, v]) => (
                <div key={k} style={{ marginBottom: '0.6rem' }}>
                  <div className="row" style={{ gap: '0.3rem', alignItems: 'center' }}>
                    <strong className="mono small">{k}</strong>
                    <Tooltip title={t('k8s.copy')}>
                      <Button size="small" type="text" icon={<CopyOutlined />} onClick={() => void navigator.clipboard?.writeText(v)} />
                    </Tooltip>
                  </div>
                  <pre className={`diff mono small${data.secret ? ' sensitive-area' : ''}`} style={{ whiteSpace: 'pre-wrap', margin: 0, maxHeight: '18rem', overflow: 'auto' }}>
                    {v}
                  </pre>
                </div>
              ))
          )}
        </Modal>
      )}
    </div>
  )
}
