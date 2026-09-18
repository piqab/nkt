import { useMemo, useState } from 'react'
import { Button, Select, Tag } from 'antd'
import { CodeOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, apiURL, hostScope, readSelectedHost, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, ErrorNote, Loading, formatRelative } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { confirmAction } from '../components/confirm'

/**
 * Вкладка Kubernetes на хосте (internal/k8s): что стоит, узлы кластера и
 * поды — только чтение через kubectl на control plane. Управление
 * кластером (создать, добавить узлы, удалить) — на хабе.
 */

interface K8sStatus {
  installed: boolean
  flavor?: string
  role?: string
  version?: string
  active: boolean
}

interface K8sNode {
  name: string
  roles: string
  ready: boolean
  version: string
  internal_ip: string
  age: string
}

interface PodItem {
  metadata: { name: string; namespace: string; creationTimestamp?: string }
  spec: { nodeName?: string }
  status: {
    phase?: string
    containerStatuses?: { name: string; ready: boolean; restartCount: number; state?: Record<string, unknown> }[]
  }
}

interface PodRow {
  key: string
  namespace: string
  name: string
  node: string
  phase: string
  ready: string
  restarts: number
  created?: string
}

export default function Kubernetes({ me }: { me: Me }) {
  const { t } = useTranslation()
  const status = useApi<{ status: K8sStatus; nodes?: K8sNode[]; nodes_error?: string }>('/k8s', 20_000)
  const st = status.data?.status
  const isServer = !!st?.installed && st.role === 'server' && st.active
  const [namespace, setNamespace] = useState('')
  const pods = useApi<{ items: PodItem[] }>(isServer ? `/k8s/pods${namespace ? `?namespace=${encodeURIComponent(namespace)}` : ''}` : null, 20_000)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  const rows = useMemo<PodRow[]>(
    () =>
      (pods.data?.items ?? []).map((p) => {
        const cs = p.status.containerStatuses ?? []
        return {
          key: p.metadata.namespace + '/' + p.metadata.name,
          namespace: p.metadata.namespace,
          name: p.metadata.name,
          node: p.spec.nodeName ?? '',
          phase: p.status.phase ?? '',
          ready: `${cs.filter((c) => c.ready).length}/${cs.length}`,
          restarts: cs.reduce((n, c) => n + (c.restartCount ?? 0), 0),
          created: p.metadata.creationTimestamp,
        }
      }),
    [pods.data],
  )
  const namespaces = useMemo(() => [...new Set((pods.data?.items ?? []).map((p) => p.metadata.namespace))].sort(), [pods.data])

  if (status.loading && !status.data) return <Loading what="Kubernetes" />
  if (!st?.installed) return <p className="small muted">{t('k8s.notInstalled')}</p>

  async function uninstall() {
    if (!(await confirmAction(t('k8s.confirmUninstall')))) return
    setBusy(true)
    try {
      await api('/k8s/uninstall', { method: 'POST' })
      setNotice(t('k8s.uninstalled'))
      await status.reload()
    } catch (err) {
      setNotice(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="col" style={{ gap: '0.6rem' }}>
      <Card
        title="Kubernetes"
        subtitle={t('k8s.summary', { flavor: st.flavor, role: st.role === 'server' ? t('k8s.roleServer') : t('k8s.roleAgent'), version: st.version || '?' })}
        actions={
          <div className="row" style={{ gap: '0.3rem' }}>
            {isServer && (
              <a href={apiURL('/k8s/kubeconfig')} download="kubeconfig.yaml">
                <Button size="small">kubeconfig</Button>
              </a>
            )}
            {isServer && me.is_admin && (
              <Button
                size="small"
                icon={<CodeOutlined />}
                title={t('clusters.kubectlHint')}
                onClick={() => {
                  // Откреплённый терминал с kubectl на этом узле (?kubectl=1).
                  const params = new URLSearchParams({ kubectl: '1' })
                  if (hostScope.id !== null) params.set('host', String(hostScope.id))
                  const name = readSelectedHost()?.name
                  if (name) params.set('name', name)
                  window.open(`/terminal/popout?${params.toString()}`, `nkt-kubectl-host-${hostScope.id ?? 'local'}`, 'width=980,height=640,resizable=yes')
                }}
              >
                kubectl
              </Button>
            )}
            {me.is_admin && me.allow_mutations && (
              <Button size="small" danger loading={busy} onClick={() => void uninstall()}>
                {t('k8s.uninstall')}
              </Button>
            )}
          </div>
        }
      >
        {notice && <Banner kind="info" onClose={() => setNotice(null)}>{notice}</Banner>}
        <ErrorNote error={status.error} />
        {!st.active && <Banner kind="warn">{t('k8s.inactive')}</Banner>}
        {status.data?.nodes_error && <Banner kind="error">{status.data.nodes_error}</Banner>}
        {isServer && status.data?.nodes && (
          <div className="table-wrap">
            <DataTable<K8sNode>
              dataSource={status.data.nodes}
              rowKey="name"
              size="small"
              pagination={false}
              columns={[
                { title: t('k8s.colNode'), dataIndex: 'name', key: 'name', render: (v: string) => <span className="mono">{v}</span> },
                { title: t('k8s.colRoles'), dataIndex: 'roles', key: 'roles', render: (v: string) => v.split(',').map((r) => <Tag key={r} color={r === 'control-plane' || r === 'master' ? 'blue' : 'default'}>{r}</Tag>) },
                { title: t('k8s.colReady'), dataIndex: 'ready', key: 'ready', render: (v: boolean) => (v ? <Tag color="success">Ready</Tag> : <Tag color="error">NotReady</Tag>) },
                { title: t('k8s.colVersion'), dataIndex: 'version', key: 'version', render: (v: string) => <span className="mono small">{v}</span> },
                { title: 'IP', dataIndex: 'internal_ip', key: 'ip', render: (v: string) => <span className="mono small">{v}</span> },
                { title: t('k8s.colAge'), dataIndex: 'age', key: 'age' },
              ]}
            />
          </div>
        )}
        {!isServer && st.installed && <p className="small muted">{t('k8s.agentOnly')}</p>}
      </Card>

      {isServer && (
        <Card
          title={t('k8s.pods')}
          subtitle={pods.data ? t('k8s.podsCount', { count: rows.length }) : undefined}
          actions={
            <Select
              size="small"
              style={{ minWidth: '12rem' }}
              value={namespace}
              onChange={(v: string) => setNamespace(v)}
              options={[{ value: '', label: t('k8s.allNamespaces') }, ...namespaces.map((n) => ({ value: n, label: n }))]}
            />
          }
        >
          <ErrorNote error={pods.error} />
          <div className="table-wrap">
            <DataTable<PodRow>
              dataSource={rows}
              rowKey="key"
              size="small"
              columns={[
                { title: 'Namespace', dataIndex: 'namespace', key: 'ns' },
                { title: t('k8s.colPod'), dataIndex: 'name', key: 'name', render: (v: string) => <span className="mono small">{v}</span> },
                {
                  title: t('k8s.colPhase'),
                  dataIndex: 'phase',
                  key: 'phase',
                  render: (v: string) => <Tag color={v === 'Running' || v === 'Succeeded' ? 'success' : v === 'Pending' ? 'processing' : 'error'}>{v}</Tag>,
                },
                { title: t('k8s.colReady'), dataIndex: 'ready', key: 'ready' },
                { title: t('k8s.colRestarts'), dataIndex: 'restarts', key: 'restarts' },
                { title: t('k8s.colNode'), dataIndex: 'node', key: 'node', render: (v: string) => <span className="mono small">{v}</span> },
                { title: t('k8s.colAge'), dataIndex: 'created', key: 'age', render: (v?: string) => (v ? formatRelative(v) : '—') },
              ]}
            />
          </div>
        </Card>
      )}
    </div>
  )
}
